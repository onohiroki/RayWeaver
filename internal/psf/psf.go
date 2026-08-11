package psf

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/types"
)

// Options configures a PSF computation.
type Options struct {
	ReferenceSurface int
	NumRays          int
	GridType         types.GridType
	GridSize         int    // image-plane pixels per side (0 = auto)
	HalfWidth        float64 // evaluation half-width mm (0 = auto)
	Polarizations    []string
}

// Result is one computed PSF with its analysis summary.
type Result struct {
	FieldIndex     int
	FieldAngle     float64
	Wavelength     float64
	Polarization   string
	Grid           *FieldGrid
	IdealPeak      float64
	Strehl         float64
	FWHMX          float64
	FWHMY          float64
	CentroidX      float64
	CentroidY      float64
	PeakValue      float64
	PeakX          float64
	PeakY          float64
	Encircled50    float64
	AiryRadius     float64
	ImageNA        float64
	SpotRMS        float64
	RMSOPD         float64 // wavefront error RMS relative to the focus reference (mm)
	PVOPD          float64 // wavefront error peak-to-valley (mm)
	Stats          WavefrontStats
}

// polState is one resolved coherent input state.
type polState struct {
	label    string
	jones    types.JonesVector
	combined bool // intensity-averaged with the following sibling (RCP+LCP)
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
			out = append(out,
				polState{label: string(types.PolRCP), jones: types.NewCircularJones(true), combined: true},
				polState{label: string(types.PolLCP), jones: types.NewCircularJones(false), combined: true})
		default: // RCP
			out = append(out, polState{label: string(types.PolRCP), jones: types.NewCircularJones(true)})
		}
	}
	return out
}

// Compute runs the full PSF pipeline: for every (field, wavelength,
// polarization) it samples the entrance pupil, traces the polarized wavefront
// to the reference surface, and integrates it onto the flat image plane via
// the direct vector Huygens integral.
func Compute(system types.System, gc *glass.Catalog, fields []types.FieldDef,
	wavelengths []float64, opts Options) ([]Result, error) {
	engine := ray.NewEngine(gc, nil)
	if opts.ReferenceSurface <= 0 {
		opts.ReferenceSurface = DefaultReferenceSurface(system.Surfaces)
	}
	if opts.NumRays <= 0 {
		opts.NumRays = 400
	}
	if opts.GridSize <= 0 {
		opts.GridSize = 64
	}
	if opts.GridType == "" {
		opts.GridType = types.GridPolar
	}
	if len(wavelengths) == 0 {
		wavelengths = []float64{types.DefaultWavelength}
	}
	pols := resolvePolStates(opts.Polarizations)
	planeZ := imagePlaneZ(system.Surfaces)

	var results []Result
	for fi, fd := range fields {
		for _, wl := range wavelengths {
			pg, err := ComputeFieldGrid(system, gc, fd, opts.ReferenceSurface, opts.NumRays, wl, opts.GridType)
			if err != nil || len(pg.GridPoints) == 0 {
				continue
			}
			nImage := imageSpaceIndex(system.Surfaces, opts.ReferenceSurface, wl, gc)
			fieldAngle := angleFromDir(pg.ChiefDir)

			for pi := 0; pi < len(pols); pi++ {
				st := pols[pi]
				if !st.combined {
					r := computeOne(engine, system, pg, fd, opts, planeZ, nImage, wl, fieldAngle, fi, st.label, st.jones)
					if r != nil {
						results = append(results, *r)
					}
					continue
				}
				// Combined RCP+LCP: average the sibling intensities.
				if pi+1 >= len(pols) || !pols[pi+1].combined {
					continue
				}
				st2 := pols[pi+1]
				r := computeCombined(engine, system, pg, fd, opts, planeZ, nImage, wl, fieldAngle, fi, st.jones, st2.jones)
				if r != nil {
					results = append(results, *r)
				}
				pi++
			}
		}
	}
	return results, nil
}

// computeOne computes and analyses the PSF for a single coherent state.
func computeOne(engine *ray.Engine, system types.System, pg *PupilGrid, fd types.FieldDef,
	opts Options, planeZ, nImage, wl, fieldAngle float64, fi int, label string, pol types.JonesVector) *Result {
	samples, stats := TraceWavefront(system, engine, pg, fd, opts.ReferenceSurface, wl, pol)
	if len(samples) < 3 {
		return nil
	}
	cx, cy, _ := ImagePlaneSpot(samples, planeZ)
	center := types.Vec3{X: cx, Y: cy, Z: planeZ}
	spec := DefaultImageGrid(samples, center, nImage, wl, planeZ, cx, cy, opts.HalfWidth, opts.GridSize)
	grid := ComputeField(samples, center, planeZ, nImage, wl, spec, false)
	ideal := ComputeField(samples, center, planeZ, nImage, wl, spec, true)
	return finishResult(grid, ideal, samples, center, nImage, wl, planeZ, fieldAngle, fi, label, stats)
}

// computeCombined computes the PSF for two coherent states (RCP and LCP) and
// averages their intensities incoherently.
func computeCombined(engine *ray.Engine, system types.System, pg *PupilGrid, fd types.FieldDef,
	opts Options, planeZ, nImage, wl, fieldAngle float64, fi int, pol1, pol2 types.JonesVector) *Result {
	s1, stats1 := TraceWavefront(system, engine, pg, fd, opts.ReferenceSurface, wl, pol1)
	s2, stats2 := TraceWavefront(system, engine, pg, fd, opts.ReferenceSurface, wl, pol2)
	if len(s1) < 3 || len(s2) < 3 {
		return nil
	}
	cx, cy, _ := ImagePlaneSpot(s1, planeZ)
	center := types.Vec3{X: cx, Y: cy, Z: planeZ}
	spec := DefaultImageGrid(s1, center, nImage, wl, planeZ, cx, cy, opts.HalfWidth, opts.GridSize)
	g1 := ComputeField(s1, center, planeZ, nImage, wl, spec, false)
	g2 := ComputeField(s2, center, planeZ, nImage, wl, spec, false)
	ideal1 := ComputeField(s1, center, planeZ, nImage, wl, spec, true)
	ideal2 := ComputeField(s2, center, planeZ, nImage, wl, spec, true)

	// Incoherent sum of intensities.
	comb := NewFieldGrid(spec)
	idealComb := NewFieldGrid(spec)
	for i := range comb.Intensity {
		comb.Intensity[i] = g1.Intensity[i] + g2.Intensity[i]
		idealComb.Intensity[i] = ideal1.Intensity[i] + ideal2.Intensity[i]
	}
	stats := stats1
	stats.Total += stats2.Total
	stats.Valid += stats2.Valid
	stats.Missed += stats2.Missed
	return finishResult(comb, idealComb, s1, center, nImage, wl, planeZ, fieldAngle, fi,
		string(types.PolRCPLCP), stats)
}

// finishResult analyses the (raw) intensity grid and fills the summary.
func finishResult(grid, ideal *FieldGrid, samples []WavefrontSample,
	center types.Vec3, nImage, wl, planeZ, fieldAngle float64, fi int, label string, stats WavefrontStats) *Result {
	idealPeak, _, _ := ideal.Peak()
	actualPeak, _, _ := grid.Peak()
	strehl := 0.0
	if idealPeak > 0 {
		strehl = actualPeak / idealPeak
	}
	na := ComputeImageNA(samples, center, nImage)
	_, _, spotRMS := ImagePlaneSpot(samples, planeZ)
	rmsOPD, pvOPD := wavefrontOPD(samples, center, nImage)

	grid.Normalize()
	peakVal, peakX, peakY := grid.Peak()
	cx, cy := grid.Centroid()
	fx, fy := grid.FWHM()
	ee50 := grid.RadiusForEnergy(cx, cy, 0.5)

	return &Result{
		FieldIndex:    fi,
		FieldAngle:    fieldAngle,
		Wavelength:    wl,
		Polarization:  label,
		Grid:          grid,
		IdealPeak:     idealPeak,
		Strehl:        strehl,
		FWHMX:         fx,
		FWHMY:         fy,
		CentroidX:     cx,
		CentroidY:     cy,
		PeakValue:     peakVal,
		PeakX:         peakX,
		PeakY:         peakY,
		Encircled50:    ee50,
		AiryRadius:     AiryRadius(wl, na),
		ImageNA:        na,
		SpotRMS:        spotRMS,
		RMSOPD:         rmsOPD,
		PVOPD:          pvOPD,
		Stats:          stats,
	}
}

// wavefrontOPD computes the OPD of each sample relative to the perfect
// converging sphere to the given focus: OPD_j = OPL_j + n·|q_j - focus|,
// referenced to its mean. Returns RMS and peak-to-valley.
func wavefrontOPD(samples []WavefrontSample, focus types.Vec3, nImage float64) (rms, pv float64) {
	opds := make([]float64, len(samples))
	for i, s := range samples {
		opds[i] = s.OPL + nImage*s.Position.Subtract(focus).Length()
	}
	mean := 0.0
	for _, o := range opds {
		mean += o
	}
	if len(opds) == 0 {
		return 0, 0
	}
	mean /= float64(len(opds))
	minV, maxV := math.Inf(1), math.Inf(-1)
	sumSq := 0.0
	for _, o := range opds {
		d := o - mean
		sumSq += d * d
		if d < minV {
			minV = d
		}
		if d > maxV {
			maxV = d
		}
	}
	rms = math.Sqrt(sumSq / float64(len(opds)))
	pv = maxV - minV
	return rms, pv
}

// angleFromDir returns the field angle (degrees) of a direction relative to
// the optical axis.
func angleFromDir(d types.Vec3) float64 {
	perp := math.Hypot(d.X, d.Y)
	return math.Atan2(perp, d.Z) * 180 / math.Pi
}
