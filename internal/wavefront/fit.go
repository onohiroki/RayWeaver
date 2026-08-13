package wavefront

import (
	"fmt"
	"math"
	"runtime"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/psf"
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
			return Paraboloid{}, err
		}
	} else {
		pg, err := psf.ComputeFieldGrid(system, gc, fd, refSurface, numRays, wavelength, types.GridPolar)
		if err != nil {
			return Paraboloid{}, err
		}
		fg = pg
	}

	global, stats := psf.TraceWavefront(system, engine, fg, fd, refSurface, wavelength, pol, runtime.NumCPU())
	if stats.Valid < 6 {
		return Paraboloid{}, fmt.Errorf("only %d valid grid rays", stats.Valid)
	}

	an, err := analyzeSamples(global, system.Surfaces, refSurface, wavelength, gc, 0)
	if err != nil {
		return Paraboloid{}, err
	}
	if an.Paraboloid.Constant == 0 && an.Paraboloid.X2 == 0 && an.Paraboloid.Y2 == 0 &&
		an.Paraboloid.XY == 0 && an.Paraboloid.X == 0 && an.Paraboloid.Y == 0 {
		return Paraboloid{}, fmt.Errorf("paraboloid fit returned a zero model")
	}
	return an.Paraboloid, nil
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

	thetaRad := raymath.DegToRad(fd.Angle)
	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)

	dx, dy := 0.0, 1.0
	if len(fd.Direction) >= 2 {
		norm := math.Hypot(fd.Direction[0], fd.Direction[1])
		if norm > 0 {
			dx = fd.Direction[0] / norm
			dy = fd.Direction[1] / norm
		}
	}
	rayDir := types.Vec3{X: sinT * dx, Y: sinT * dy, Z: cosT}.Normalize()

	zStart := -100.0
	pupilOffsetX, pupilOffsetY := 0.0, 0.0
	tanComponent := math.Sqrt(rayDir.X*rayDir.X + rayDir.Y*rayDir.Y)
	if rayDir.Z > 1e-12 && tanComponent > 1e-12 {
		tanComponent /= rayDir.Z
		pupilOffsetX = -(pupilZ - zStart) * tanComponent * dx
		pupilOffsetY = -(pupilZ - zStart) * tanComponent * dy
	}

	samples := chief.GenerateGridPoints(numRays, apertureRadius, types.GridPolar)
	grid := make([]types.GridPoint, len(samples))
	wavefrontC := types.Vec3{X: pupilOffsetX, Y: pupilOffsetY, Z: zStart}
	for i, p := range samples {
		grid[i] = types.GridPoint{
			PupilX: p.X,
			PupilY: p.Y,
			Origin: raymath.ProjectOntoWavefront(
				types.Vec3{X: p.X + pupilOffsetX, Y: p.Y + pupilOffsetY, Z: zStart},
				wavefrontC, rayDir),
			Direction: rayDir,
		}
	}

	return &psf.PupilGrid{
		GridPoints:    grid,
		ChiefDir:      rayDir,
		EntrancePupil: &types.Pupil{Center: types.Vec3{X: pupilOffsetX, Y: pupilOffsetY, Z: pupilZ}},
	}, nil
}
