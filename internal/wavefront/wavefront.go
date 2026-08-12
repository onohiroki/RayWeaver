package wavefront

import (
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// Options configures a wavefront analysis.
type Options struct {
	ReferenceSurface int
	NumRays          int
	// Workers bounds the per-field task parallelism (0 = runtime.NumCPU()).
	Workers int
	// ZernikeMaxOrder is the highest Fringe index to fit (default 15).
	ZernikeMaxOrder int
	// Polarizations lists the input polarization labels (RCP default).
	Polarizations []string
	// BestFocus, when non-nil, enables the weighted best-focus shift.
	BestFocus *FocusConfig
}

// SampleData is one sampled wavefront point on the reference surface (global
// lateral coordinates). Residual is OPL minus the removed low-order model
// (best-fit sphere + paraboloid astigmatism); SphereResidual is OPL minus the
// best-fit sphere alone. All in mm.
type SampleData struct {
	X, Y           float64
	OPL            float64
	Residual       float64
	SphereResidual float64
}

// Statistics summarizes the wavefront residual after best-fit-sphere removal.
type Statistics struct {
	// RMS and PV are in mm of OPL.
	RMS, PV float64
	// Strehl is the exact peak-ratio Strehl of the residual wavefront:
	// |⟨e^{i(2π/λ)W}⟩|², the pupil-area-weighted coherent average over the
	// samples. Meaningful beyond the Marechal limit (σ ≲ 0.2λ).
	Strehl float64
}

// Samples counts the pupil grid rays by outcome.
type Samples struct {
	Total, Valid, Missed int
}

// FieldResult is the wavefront analysis of one (field, wavelength,
// polarization): the always-computed paraboloid, the best-fit sphere seeded
// from it, the stabilized Zernike decomposition of the residual, and summary
// statistics. Data holds the raw per-sample values for map outputs.
type FieldResult struct {
	FieldIndex   int
	FieldAngle   float64
	Wavelength   float64
	Polarization string
	Paraboloid   Paraboloid
	Sphere       Sphere
	Zernike      Zernike
	Statistics   Statistics
	Samples      Samples
	Data         []SampleData
}

// BestFocusOut is the weighted best-image-plane shift.
type BestFocusOut struct {
	WeightType     string
	WeightedFocusZ float64 // weighted sphere-center axial distance from the reference surface (mm)
	PerField       []FocusField
	ImagePlaneZ    float64 // current reference-surface → image-plane distance (mm)
	ShiftMM        float64 // WeightedFocusZ - ImagePlaneZ (mm)
}

// Result is the complete wavefront analysis of one config.
type Result struct {
	Fields    []FieldResult
	BestFocus *BestFocusOut
}

// polState is one requested input polarization label plus the Jones vector
// used for the trace. The wavefront OPD is polarization-independent, so one
// trace serves all requested states.
type polState struct {
	label string
	jones types.JonesVector
}

func resolvePolStates(labels []string) []polState {
	if len(labels) == 0 {
		return []polState{{label: string(types.PolRCP), jones: types.NewCircularJones(true)}}
	}
	var out []polState
	for _, l := range labels {
		switch types.PolarizationLabel(l) {
		case types.PolLCP:
			out = append(out, polState{label: string(types.PolLCP), jones: types.NewCircularJones(false)})
		case types.PolX:
			out = append(out, polState{label: string(types.PolX), jones: types.NewLinearJones(0)})
		case types.PolY:
			out = append(out, polState{label: string(types.PolY), jones: types.NewLinearJones(90)})
		case types.PolRCPLCP:
			// The OPD is the same for both handednesses; label the joined state.
			out = append(out, polState{label: string(types.PolRCPLCP), jones: types.NewCircularJones(true)})
		default: // RCP
			out = append(out, polState{label: string(types.PolRCP), jones: types.NewCircularJones(true)})
		}
	}
	return out
}

// Compute runs the full wavefront analysis for every (field, wavelength,
// polarization): sample the entrance pupil, trace the polarized wavefront to
// the reference surface, fit the paraboloid (always) and the best-fit sphere
// seeded from it, decompose the residual into Fringe-Zernike terms, and — when
// opts.BestFocus is set — combine the per-field sphere-center distances into a
// weighted image-plane shift.
//
// Tasks (field × wavelength) run in parallel goroutines bounded by
// opts.Workers; each task's ray tracing uses a bounded share of the CPU so the
// total goroutine count stays proportional to the machine.
func Compute(system types.System, gc *glass.Catalog, fields []types.FieldDef, wavelengths []float64, opts Options) (*Result, error) {
	engine := ray.NewEngine(gc, nil)
	if opts.ReferenceSurface <= 0 {
		opts.ReferenceSurface = psf.DefaultReferenceSurface(system.Surfaces)
	}
	if opts.NumRays <= 0 {
		opts.NumRays = 400
	}
	if opts.Workers <= 0 {
		opts.Workers = runtime.NumCPU()
	}
	if opts.ZernikeMaxOrder <= 0 {
		opts.ZernikeMaxOrder = 15
	}
	if len(wavelengths) == 0 {
		wavelengths = []float64{types.DefaultWavelength}
	}
	pols := resolvePolStates(opts.Polarizations)
	if len(pols) == 0 {
		pols = resolvePolStates(nil)
	}

	// Reference-surface local frame: transform every field's samples into it
	// so the sphere fit sees the true 3D geometry (the surface sag is not
	// mistaken for wavefront curvature). The image-plane distance is measured
	// in the same frame.
	surface.Precompute(system.Surfaces)
	refIdx := -1
	for i, s := range system.Surfaces {
		if s.ID == opts.ReferenceSurface {
			refIdx = i
			break
		}
	}
	if refIdx < 0 || refIdx >= len(system.Surfaces)-1 {
		return nil, fmt.Errorf("reference surface %d not found before the image plane", opts.ReferenceSurface)
	}
	refG2L := system.Surfaces[refIdx].GlobalToLocal
	imgVertex := system.Surfaces[len(system.Surfaces)-1].LocalToGlobal.MultiplyPoint(types.Vec3{})
	planeZ := imgVertex.Z            // global image-plane Z
	imagePlaneZ := refG2L.MultiplyPoint(imgVertex).Z // reference-surface-local image distance

	type task struct {
		fi  int
		fd  types.FieldDef
		wl  float64
		pol polState
	}
	var tasks []task
	for fi, fd := range fields {
		for _, wl := range wavelengths {
			for _, p := range pols {
				tasks = append(tasks, task{fi, fd, wl, p})
			}
		}
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no (field, wavelength, polarization) tasks")
	}

	results := make([]FieldResult, len(tasks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, opts.Workers)
	var mu sync.Mutex
	var firstErr error
	rayWorkers := runtime.NumCPU() / opts.Workers
	if rayWorkers < 1 {
		rayWorkers = 1
	}

	for i, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, t task) {
			defer wg.Done()
			defer func() { <-sem }()
			fr, err := computeField(engine, system, gc, t.fd, opts.ReferenceSurface, opts.NumRays, opts.ZernikeMaxOrder, t.wl, t.pol, rayWorkers, refG2L, planeZ)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("field %d @ %.6f nm: %v", t.fi, t.wl*1e6, err)
				}
				mu.Unlock()
				return
			}
			fr.FieldIndex = t.fi
			fr.FieldAngle = fieldAngle(fr, t.fd)
			fr.Wavelength = t.wl
			fr.Polarization = t.pol.label
			mu.Lock()
			results[idx] = fr
			mu.Unlock()
		}(i, t)
	}
	wg.Wait()
	close(sem)
	if firstErr != nil {
		return nil, firstErr
	}

	// Deterministic order: field index, then wavelength, then polarization.
	sort.SliceStable(results, func(a, b int) bool {
		if results[a].FieldIndex != results[b].FieldIndex {
			return results[a].FieldIndex < results[b].FieldIndex
		}
		if results[a].Wavelength != results[b].Wavelength {
			return results[a].Wavelength < results[b].Wavelength
		}
		return results[a].Polarization < results[b].Polarization
	})

	out := &Result{Fields: results}
	if opts.BestFocus != nil {
		bf, err := computeBestFocus(results, imagePlaneZ, *opts.BestFocus)
		if err != nil {
			return nil, err
		}
		out.BestFocus = bf
	}
	return out, nil
}

// computeField performs the single-field analysis pipeline for one
// (field, wavelength) using the given polarization state. The wavefront
// samples are transformed into the reference-surface local frame, and the OPD
// is referenced to the current image point (where the beam lands on the image
// plane) so off-axis fields are analysed correctly.
func computeField(engine *ray.Engine, system types.System, gc *glass.Catalog, fd types.FieldDef,
	refSurface, numRays, zernikeOrder int, wl float64, p polState, rayWorkers int, refG2L types.Mat4, planeZ float64) (FieldResult, error) {
	var fr FieldResult

	pg, err := psf.ComputeFieldGrid(system, gc, fd, refSurface, numRays, wl, types.GridPolar)
	if err != nil {
		return fr, err
	}
	global, stats := psf.TraceWavefront(system, engine, pg, fd, refSurface, wl, p.jones, rayWorkers)
	fr.Samples = Samples{Total: stats.Total, Valid: stats.Valid, Missed: stats.Missed}
	if stats.Valid < 6 {
		return fr, fmt.Errorf("only %d valid grid rays", stats.Valid)
	}

	nImage := imageSpaceIndex(system.Surfaces, refSurface, wl, gc)

	// Image-plane normal in the reference-surface local frame.
	dir := refG2L.MultiplyPoint(types.Vec3{Z: 1})
	dirLen := dir.Length()
	if dirLen > 0 {
		dir = dir.Scale(1 / dirLen)
	} else {
		dir = types.Vec3{Z: 1}
	}

	// Best focus: geometric spot RMS minimization along the image-plane normal
	// (global frame, robust against angle-field OPL artifacts).
	sph, err := FitSphereShift(global, planeZ)
	if err != nil {
		return fr, err
	}
	Fbest := sph.Center() // global best-focus point
	FbestLocal := refG2L.MultiplyPoint(Fbest)

	samples := toLocalFrame(global, refG2L)

	// OPD samples referenced to the best-focus point: OPD = OPL + n·|P - Fbest|.
	// Using the spot-RMS best focus (the same reference psf --best-focus
	// evaluates at) keeps the wavefront rms/pv/strehl consistent with the PSF's
	// best-focus rms_opd/pv_opd and Strehl.
	opdSamples := make([]psf.WavefrontSample, len(samples))
	for i, s := range samples {
		opdSamples[i] = s
		opdSamples[i].OPL = s.OPL + nImage*lineDist(s.Position, FbestLocal, dir, 0)
	}

	pab, err := FitParaboloid(opdSamples)
	if err != nil {
		return fr, err
	}

	// Stabilized Zernike on the paraboloid residual of the OPD (terms 1-6
	// removed: the paraboloid accounts for piston/tilt/defocus/astigmatism).
	residual := make([]float64, len(opdSamples))
	for i, s := range opdSamples {
		residual[i] = s.OPL - pab.Eval(s.Position.X, s.Position.Y)
	}
	zen, err := FitZernike(opdSamples, residual, zernikeOrder)
	if err != nil {
		return fr, err
	}

	// Statistics at best focus: the wavefront error relative to the reference
	// sphere (piston + tilt + defocus removed; astigmatism retained), the
	// standard wavefront-aberration definition (matches PSF's rms_opd).
	refSph, err := FitReferenceSphere(opdSamples)
	if err != nil {
		return fr, err
	}
	sphereRes := make([]float64, len(opdSamples))
	for i, s := range opdSamples {
		sphereRes[i] = s.OPL - refSph.Eval(s.Position.X, s.Position.Y)
	}
	rms := refSph.RMSResidual
	pv := refSph.PV
	strehl := exactStrehl(opdSamples, sphereRes, wl)

	fr.Paraboloid = pab
	fr.Sphere = Sphere{
		CenterX:     FbestLocal.X,
		CenterY:     FbestLocal.Y,
		CenterZ:     FbestLocal.Z,
		Radius:      FbestLocal.Z,
		ShiftMM:     sph.ShiftMM,
		SpotRMS:     sph.SpotRMS,
		RMSResidual: rms,
	}
	fr.Zernike = zen
	fr.Statistics = Statistics{RMS: rms, PV: pv, Strehl: strehl}
	fr.Data = make([]SampleData, 0, len(samples))
	for i, s := range samples {
		fr.Data = append(fr.Data, SampleData{
			X:              s.Position.X,
			Y:              s.Position.Y,
			OPL:            s.OPL,
			Residual:       residual[i],
			SphereResidual: sphereRes[i],
		})
	}
	return fr, nil
}

// toLocalFrame copies the samples with their positions transformed into the
// given inverse (global→local) transform.
func toLocalFrame(samples []psf.WavefrontSample, g2l types.Mat4) []psf.WavefrontSample {
	out := make([]psf.WavefrontSample, len(samples))
	for i, s := range samples {
		out[i] = s
		out[i].Position = g2l.MultiplyPoint(s.Position)
	}
	return out
}

// computeBestFocus combines the per-field best-fit-sphere focus distances. For
// each field the first (design) wavelength's result is used. The weighted
// focus distance minus the current reference-surface → image-plane distance is
// the image-plane shift to apply.
func computeBestFocus(results []FieldResult, imagePlaneZ float64, cfg FocusConfig) (*BestFocusOut, error) {
	// First result per field index (results are sorted by field, wavelength).
	var focusZ []float64
	var fieldIdx []int
	seen := make(map[int]bool)
	for _, r := range results {
		if seen[r.FieldIndex] {
			continue
		}
		seen[r.FieldIndex] = true
		focusZ = append(focusZ, r.Sphere.CenterZ)
		fieldIdx = append(fieldIdx, r.FieldIndex)
	}
	fr, err := ComputeBestFocus(focusZ, fieldIdx, cfg)
	if err != nil {
		return nil, err
	}
	return &BestFocusOut{
		WeightType:     fr.WeightType,
		WeightedFocusZ: fr.WeightedFocusZ,
		PerField:       fr.PerField,
		ImagePlaneZ:    imagePlaneZ,
		ShiftMM:        fr.WeightedFocusZ - imagePlaneZ,
	}, nil
}

// fieldAngle returns the chief-ray field angle (degrees) of a field result.
func fieldAngle(fr FieldResult, fd types.FieldDef) float64 {
	return fd.Angle
}

// exactStrehl is the peak-ratio Strehl of the residual wavefront at the
// reference-sphere focus: |⟨e^{i(2π/λ)W}⟩|² with the pupil-area-weighted
// coherent average over the samples. Unlike the Marechal approximation
// exp(-(2πσ/λ)²) — valid only for σ ≲ 0.2λ, beyond which it collapses towards
// 0 — the exact average never exceeds 1 and stays meaningful for highly
// aberrated fields, matching psf's peak-ratio Strehl at best focus.
func exactStrehl(samples []psf.WavefrontSample, residual []float64, wl float64) float64 {
	if len(samples) == 0 || len(samples) != len(residual) {
		return 0
	}
	k := 2 * math.Pi / wl
	var sumA, re, im float64
	for i, s := range samples {
		w := k * residual[i]
		sumA += s.Area
		re += s.Area * math.Cos(w)
		im += s.Area * math.Sin(w)
	}
	if sumA <= 0 {
		return 0
	}
	return (re*re + im*im) / (sumA * sumA)
}

// imageSpaceIndex returns the refractive index of the medium immediately
// following the reference surface at the given wavelength.
func imageSpaceIndex(surfaces []types.Surface, refSurfaceID int, wavelength float64, gc *glass.Catalog) float64 {
	idx := dls.SurfaceIndex(surfaces, refSurfaceID)
	if idx < 0 || idx >= len(surfaces) {
		return 1
	}
	n, _ := gc.RefractiveIndex(surfaces[idx].Material, wavelength)
	if n <= 0 {
		return 1
	}
	return n
}
