package psf

import (
	"fmt"
	"sync"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/pupil"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

// FrozenPupilGrid builds the polar entrance-pupil grid for one field centred on
// a caller-frozen pupil Z. The ray directions and grid layout replicate the
// optimization grid (dls.traceGridRays) and the chief command's angle-field
// grid: parallel rays at the field angle, laterally offset so the aperture sits
// at pupilZ. Unlike ComputeFieldGrid the dynamic pupil is NOT re-settled.
//
// epdOverride is the virtual entrance-pupil diameter (mm) the optimizer has
// applied for this evaluation; 0 derives the radius from the surfaces (the
// historical behaviour, which for a virtual-pupil system is the paraxial /
// fixed-aperture radius instead of the prescribed pupil).
func FrozenPupilGrid(system types.System, gc *glass.Catalog, fd types.FieldDef,
	refSurface, numRays int, wavelength float64, apertureMargin, pupilZ, epdOverride float64) (*PupilGrid, error) {
	apertureRadius := dls.ApertureRadiusForGrid(system.Surfaces, system.StopSurface, wavelength, gc, apertureMargin, epdOverride)
	if apertureRadius <= 0 {
		return nil, fmt.Errorf("no entrance-pupil radius for the wavefront grid")
	}

	rayDir := raymath.DirectionFromField(fd.Angle, fd.Direction)

	zStart := -100.0
	pupilOffsetX, pupilOffsetY := pupil.GridCentre(rayDir, pupilZ, zStart)

	var vig *types.VignettingDef
	if fd.Vignetting != nil && !fd.Vignetting.IsZero() {
		vig = fd.Vignetting
	}
	samples := pupil.Launch(pupil.LaunchSpec{
		NumRays:        numRays,
		GridType:       types.GridPolar,
		ApertureRadius: apertureRadius,
		RayDir:         rayDir,
		CentreX:        pupilOffsetX,
		CentreY:        pupilOffsetY,
		ZStart:         zStart,
		OPLMode:        pupil.OPLLaunch,
		Vig:            vig,
	})
	grid := make([]types.GridPoint, len(samples))
	for i, s := range samples {
		grid[i] = types.GridPoint{
			PupilX:    s.PupilX,
			PupilY:    s.PupilY,
			Origin:    s.Origin,
			Direction: s.Dir,
		}
	}

	return &PupilGrid{
		GridPoints:    grid,
		ChiefDir:      rayDir,
		EntrancePupil: &types.Pupil{Center: types.Vec3{X: pupilOffsetX, Y: pupilOffsetY, Z: pupilZ}},
	}, nil
}

// DefaultDiffractionMaxGrid caps the auto-enlarged image grid of
// ComputeDiffractionMTF. The auto rule (keep res ≤ Airy/2 over the whole
// window) asks for up to a 1481² grid on a strongly aberrated state, whose
// window is three times the geometric spot; capping keeps the merit term
// affordable while leaving the well-corrected regime — the window is then
// diffraction-sized and the grid stays at GridSize — identical to the
// standalone measurement.
//
// The cap is a pixel-count cap only: the window (and therefore df, the
// frequency spacing 1/(2·half)) is untouched, and in the spot-driven regime
// half = 3·spotRMS so the grid spans 6·half/n cells per RMS radius whatever the
// spot size — the PSF stays amply resolved while the Airy-core rule it replaces
// is irrelevant for a spot tens of Airy disks wide. Measured on a 6-element at
// 10 c/mm (rays 128, RCP+LCP): 230 ms at 512, 60 ms at 256, 18 ms at 128, so
// each halving is a ~4x saving. The cap only binds when the natural grid
// exceeds it, i.e. when half > n/4·Airy: at 128 that is 32·Airy ≈ 0.022 mm,
// where the Gaussian estimate exp(-987·spotRMS²) of MTF(10) is already at or
// below 0.62, and the grid's Nyquist frequency 1/(2·dx) = n/(2·half) is still
// ≫ the 10 c/mm the gate reads (half is the window, so a coarser grid buys
// frequency headroom, not a frequency error). The value the cap coarsens is
// therefore not the value the gate judges; a well-corrected design, whose window
// is diffraction-sized, keeps GridSize and never reaches it.
const DefaultDiffractionMaxGrid = 128

// DiffractionMTFOptions configures ComputeDiffractionMTF. The zero value
// reproduces the `psf` / `focus mtf` defaults for a single frequency on the
// delivered image plane: reference surface = last optical surface, 400 rays,
// image grid 64 (auto-enlarged by the shared rule), RCP polarization.
type DiffractionMTFOptions struct {
	// Wavelength is the evaluation wavelength (mm); 0 uses types.DefaultWavelength.
	Wavelength float64
	// Frequency is the spatial frequency (cycles/mm) at which the MTF is read.
	Frequency float64
	// ReferenceSurface is the wavefront sampling surface; 0 = last optical surface.
	ReferenceSurface int
	// NumRays is the entrance-pupil grid ray count (0 = DefaultNumRays).
	NumRays int
	// GridSize is the image-grid pixel count before auto-enlargement (0 = 64,
	// the psf default).
	GridSize int
	// MaxGrid caps the auto-enlarged image grid (0 = DefaultDiffractionMaxGrid;
	// negative disables the cap).
	MaxGrid int
	// HalfWidth overrides the auto-sized window half-extent (0 = the shared
	// max(4·Airy, 3·spot RMS, 5e-3) rule).
	HalfWidth float64
	// Polarizations lists the input polarization labels whose intensities are
	// averaged incoherently ("RCP+LCP" resolves to two states); nil = RCP.
	Polarizations []string
	// ApertureMargin, EPDOverride, PupilModel and RayDefinition feed the pupil
	// grid builders exactly as in ComputeFieldGrid / FrozenPupilGrid.
	ApertureMargin float64
	EPDOverride    float64
	PupilModel     *types.PupilModelConfig
	RayDefinition  string
	// Workers bounds the trace and the row-parallel Huygens pool (0 = NumCPU).
	Workers int
	// PlaneShift offsets the delivered image plane (0 = the `file` plane).
	PlaneShift float64
}

// DiffractionMTF is one field's diffraction MTF at a single frequency on the
// flat image plane.
type DiffractionMTF struct {
	// Sagittal is the MTF along the image-plane x axis, Tangential along y —
	// the same axes the `psf` / `focus mtf` summaries report.
	Sagittal   float64
	Tangential float64
	// GridSize is the image grid actually used, after auto-enlargement and the
	// MaxGrid cap. HalfWidth is the window half-extent that grid spans.
	GridSize  int
	HalfWidth float64
	// Valid is the number of wavefront samples that reached the reference
	// surface (summed over polarization states).
	Valid int
}

// ComputeDiffractionMTF traces one field's entrance pupil — on the frozen pupil
// Z when frozenPupilZ is non-nil, else through the chief dynamic/virtual pupil —
// propagates the polarized wavefront to the reference surface, integrates it on
// the delivered (flat) image plane with the direct vector Huygens integral and
// reads the MTF at opts.Frequency off that PSF.
//
// It is the in-optimization counterpart of the gate measurement
// (`focus mtf --frequencies F`): the window, the grid auto-enlargement and the
// FFT/interpolated evaluation all go through the same psf functions, so a
// term value and a reported value are the same number for the same state.
// The only deliberate difference is the MaxGrid cap, which cannot bind in the
// well-corrected regime the gate is judged in.
func ComputeDiffractionMTF(system types.System, gc *glass.Catalog, fd types.FieldDef,
	frozenPupilZ *float64, opts DiffractionMTFOptions) (DiffractionMTF, error) {
	var out DiffractionMTF

	wl := opts.Wavelength
	if wl <= 0 {
		wl = types.DefaultWavelength
	}
	if opts.Frequency <= 0 {
		return out, fmt.Errorf("diffraction MTF: no spatial frequency")
	}
	refSurface := opts.ReferenceSurface
	if refSurface <= 0 {
		refSurface = DefaultReferenceSurface(system.Surfaces)
	}
	numRays := opts.NumRays
	if numRays <= 0 {
		numRays = DefaultNumRays
	}
	gridSize := opts.GridSize
	if gridSize <= 0 {
		gridSize = 64
	}
	maxGrid := opts.MaxGrid
	if maxGrid == 0 {
		maxGrid = DefaultDiffractionMaxGrid
	}
	apertureMargin := opts.ApertureMargin
	if apertureMargin <= 0 {
		apertureMargin = 1.0
	}

	var fg *PupilGrid
	var err error
	if frozenPupilZ != nil {
		fg, err = FrozenPupilGrid(system, gc, fd, refSurface, numRays, wl, apertureMargin, *frozenPupilZ, opts.EPDOverride)
	} else {
		fg, err = ComputeFieldGrid(system, gc, fd, refSurface, numRays, wl, types.GridPolar, opts.PupilModel, opts.RayDefinition)
	}
	if err != nil {
		return out, err
	}
	if fg == nil || len(fg.GridPoints) == 0 {
		return out, fmt.Errorf("diffraction MTF: empty entrance-pupil grid")
	}

	engine := ray.NewEngine(gc, nil)
	planeZ := imagePlaneZ(system.Surfaces) + opts.PlaneShift
	nImage := ImageSpaceIndex(system.Surfaces, refSurface, wl, gc)

	// Trace every requested polarization state concurrently on the shared
	// grid, exactly like psf.computeCombined does for RCP+LCP.
	pols := resolvePolStates(opts.Polarizations)
	samples := make([][]WavefrontSample, len(pols))
	stats := make([]WavefrontStats, len(pols))
	var wg sync.WaitGroup
	for i := range pols {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			samples[i], stats[i] = TraceWavefront(system, engine, fg, fd, refSurface, wl, pols[i].jones, opts.Workers)
		}(i)
	}
	wg.Wait()
	for i := range samples {
		out.Valid += stats[i].Valid
		if len(samples[i]) < 3 {
			return out, fmt.Errorf("diffraction MTF: only %d valid grid rays", stats[i].Valid)
		}
	}

	// One shared image grid, derived exactly as psf.Compute derives it for the
	// field (first state), then optionally capped in pixel count only.
	cx, cy, _ := ImagePlaneSpot(samples[0], planeZ)
	center := types.Vec3{X: cx, Y: cy, Z: planeZ}
	spec := DefaultImageGrid(samples[0], center, nImage, wl, planeZ, cx, cy, opts.HalfWidth, gridSize)
	spec = capImageGrid(spec, maxGrid)
	out.GridSize = spec.NX
	out.HalfWidth = 0.5 * float64(spec.NX) * spec.DX

	pairs := make([]fieldPair, len(samples))
	for i := range samples {
		pairs[i] = fieldPair{samples: samples[i], center: center, actual: NewFieldGrid(spec)}
	}
	computePairs(pairs, planeZ, nImage, wl, spec, opts.Workers)

	intensity := make([]float64, spec.NX*spec.NY)
	for _, pr := range pairs {
		for i, v := range pr.actual.Intensity {
			intensity[i] += v
		}
	}

	summary := ComputeMTF(intensity, spec, &types.PSFMTFConfig{Frequencies: []float64{opts.Frequency}})
	if summary == nil {
		return out, fmt.Errorf("diffraction MTF: OTF evaluation failed")
	}
	if len(summary.Sagittal.Evaluated) == 0 || len(summary.Tangential.Evaluated) == 0 {
		return out, fmt.Errorf("diffraction MTF: no evaluated point at %.6g cycles/mm", opts.Frequency)
	}
	out.Sagittal = summary.Sagittal.Evaluated[0].MTF
	out.Tangential = summary.Tangential.Evaluated[0].MTF
	return out, nil
}

// capImageGrid limits the image grid to max pixels while keeping the window:
// the pixel size grows to span/max and the origin is left alone, so the grid
// stays where DefaultImageGrid put it and only the sampling coarsens (never
// truncates). A grid at or below max, or a max below the smallest grid psf
// ever builds, is left untouched.
func capImageGrid(spec ImageGridSpec, max int) ImageGridSpec {
	if max < 16 || spec.NX <= max || spec.DX <= 0 {
		return spec
	}
	span := float64(spec.NX) * spec.DX
	spec.NX, spec.NY = max, max
	spec.DX = span / float64(max)
	spec.DY = spec.DX
	return spec
}
