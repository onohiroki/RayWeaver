package wavefront

import (
	"fmt"
	"runtime"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/pupil"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

// FitFieldParaboloid computes the least-squares quadratic (paraboloid) fit of
// the OPD on the reference surface for one (field, wavelength), referencing the
// OPD to the best-focus point exactly like the full wavefront analysis. It
// returns the paraboloid coefficients each optimizer wavefront merit term reads.
//
// When frozenPupilZ is non-nil the entrance-pupil grid is centred on that
// (per-iteration) pupil Z with the same polar layout the chief command uses,
// but without re-settling the dynamic pupil, so the DLS base point and its
// Jacobian perturbations share one pupil (the optimizer passes its frozen
// cfg.pupilZ). When nil the chief dynamic pupil is settled as usual.
func FitFieldParaboloid(system types.System, gc *glass.Catalog, fd types.FieldDef,
	refSurface, numRays int, wavelength float64, apertureMargin float64, frozenPupilZ *float64) (Paraboloid, error) {
	an, err := analyzeField(system, gc, fd, refSurface, numRays, wavelength, apertureMargin, frozenPupilZ)
	if err != nil {
		return Paraboloid{}, err
	}
	if an.Paraboloid.Constant == 0 && an.Paraboloid.X2 == 0 && an.Paraboloid.Y2 == 0 &&
		an.Paraboloid.XY == 0 && an.Paraboloid.X == 0 && an.Paraboloid.Y == 0 {
		return Paraboloid{}, fmt.Errorf("paraboloid fit returned a zero model")
	}
	return an.Paraboloid, nil
}

// FitFieldSphereRMS computes the reference-sphere residual RMS (and PV) of the
// OPD on the reference surface for one (field, wavelength), referencing the
// OPD to the best-focus point exactly like the full wavefront analysis and
// psf --best-focus. The reference sphere removes piston + tilt + defocus only,
// so astigmatism is retained in the residual — this is the exact quantity psf
// reports as rms_opd and the direct Strehl determinant, so minimizing it drives
// the psf-reported Strehl directly. The grid, pupil and fallback machinery are
// shared with FitFieldParaboloid.
func FitFieldSphereRMS(system types.System, gc *glass.Catalog, fd types.FieldDef,
	refSurface, numRays int, wavelength float64, apertureMargin float64, frozenPupilZ *float64) (rms, pv float64, err error) {
	an, err := analyzeField(system, gc, fd, refSurface, numRays, wavelength, apertureMargin, frozenPupilZ)
	if err != nil {
		return 0, 0, err
	}
	return an.Statistics.RMS, an.Statistics.PV, nil
}

// analyzeField traces the wavefront for one (field, wavelength) on the
// reference surface (frozen or dynamic pupil) and runs the full analysis,
// returning the paraboloid, reference-sphere and Strehl statistics. It is the
// shared machinery behind FitFieldParaboloid and FitFieldSphereRMS.
func analyzeField(system types.System, gc *glass.Catalog, fd types.FieldDef,
	refSurface, numRays int, wavelength float64, apertureMargin float64, frozenPupilZ *float64) (fieldAnalysis, error) {
	if refSurface <= 0 {
		refSurface = psf.DefaultReferenceSurface(system.Surfaces)
	}
	if numRays <= 0 {
		numRays = 400
	}
	if apertureMargin <= 0 {
		apertureMargin = 1.0
	}

	engine := ray.NewEngine(gc, nil)
	pol := types.NewCircularJones(true)

	var fg *psf.PupilGrid
	if frozenPupilZ != nil {
		var err error
		fg, err = frozenPupilGrid(system, gc, fd, refSurface, numRays, wavelength, apertureMargin, *frozenPupilZ)
		if err != nil {
			return fieldAnalysis{}, err
		}
	} else {
		pg, err := psf.ComputeFieldGrid(system, gc, fd, refSurface, numRays, wavelength, types.GridPolar)
		if err != nil {
			return fieldAnalysis{}, err
		}
		fg = pg
	}

	global, stats := psf.TraceWavefront(system, engine, fg, fd, refSurface, wavelength, pol, runtime.NumCPU())
	if stats.Valid < 6 {
		return fieldAnalysis{}, fmt.Errorf("only %d valid grid rays", stats.Valid)
	}

	return analyzeSamples(global, system.Surfaces, refSurface, wavelength, gc, 0)
}

// frozenPupilGrid builds the polar entrance-pupil grid for one field centred on
// a caller-frozen pupil Z. The ray directions and grid layout replicate the
// optimization grid (dls.traceGridRays) and the chief command's angle-field
// grid: parallel rays at the field angle, laterally offset so the aperture sits
// at pupilZ. Unlike psf.ComputeFieldGrid the dynamic pupil is NOT re-settled.
func frozenPupilGrid(system types.System, gc *glass.Catalog, fd types.FieldDef,
	refSurface, numRays int, wavelength float64, apertureMargin, pupilZ float64) (*psf.PupilGrid, error) {
	apertureRadius := dls.ApertureRadiusForGrid(system.Surfaces, system.StopSurface, wavelength, gc, apertureMargin)
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

	return &psf.PupilGrid{
		GridPoints:    grid,
		ChiefDir:      rayDir,
		EntrancePupil: &types.Pupil{Center: types.Vec3{X: pupilOffsetX, Y: pupilOffsetY, Z: pupilZ}},
	}, nil
}
