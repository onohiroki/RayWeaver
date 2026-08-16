package pupil

import (
	"runtime"
	"sync"

	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

// OPLMode selects how the launch-geometry OPL tilt of a parallel angle-field
// bundle is removed. Both modes are mathematically equivalent (the projected
// origin makes the per-ray dot exactly zero); they differ only in how the ray
// positions are kept: moving the origin along the ray leaves the ray line
// unchanged but perturbs the traced OPLTotal by ~1e-15 floating-point noise,
// so the scalar form is preferred where the ray positions feed a merit grid.
type OPLMode int

const (
	// OPLLaunch moves each sample origin onto the wavefront plane through the
	// grid centre along RayDir; the recorded OPLTotal then carries no launch
	// tilt and no OPL delta is applied.
	OPLLaunch OPLMode = iota
	// OPLScalar keeps each sample origin on the zStart plane and subtracts
	// (wavefrontC - origin)·RayDir from OPLTotal, keeping the traced ray
	// positions bit-identical to a plain launch.
	OPLScalar
)

// LaunchSpec is everything needed to distribute and launch one parallel bundle
// of pupil rays: the grid pattern, its radius, the ray direction, the grid
// centre on the launch plane, an optional vignetting clip, the OPL
// normalization mode and the skip flags forwarded to the tracer. The grid type
// and ray direction are supplied by the caller exactly as the analogous
// chief/DLS/wavefront/asphere paths already build them, so a bundle stays
// consistent across every consumer of this package.
type LaunchSpec struct {
	NumRays           int
	GridType          types.GridType
	RotationOffset    float64
	ApertureRadius    float64
	RayDir            types.Vec3
	CentreX           float64 // grid centre on the zStart plane (pupil offset)
	CentreY           float64
	ZStart            float64
	Vig               *types.VignettingDef // nil = no vignetting clip
	OPLMode           OPLMode
	SkipApertureCheck bool
	SkipGlassPath     bool
	// HeightOrigin, when non-nil, launches a finite-conjugate bundle from the
	// object point: every sample origin is HeightOrigin and its direction
	// points from there through the grid sample. No OPL delta is applied.
	HeightOrigin *types.Vec3
}

// Sample is one pupil ray: the absolute launch offset, the launch state, the
// OPL delta applied for the wavefront-plane normalization and — once traced —
// the per-surface results plus OPL/intensity aggregates.
type Sample struct {
	PupilX, PupilY float64 // relative grid offsets (unit aperture cell coords × radius; centre applied to Origin)
	Area           float64 // pupil-cell area weight
	Origin         types.Vec3
	Dir            types.Vec3
	OPLDelta       float64 // subtracted from OPLTotal to null the launch tilt
	// Skip flags forwarded to the tracer.
	SkipApertureCheck  bool
	SkipGlassPathCheck bool
	// Filled by Trace.
	OK        bool
	Err       string
	ErrorCode string // trace error code (empty when OK)
	OPL       float64 // OPLTotal - OPLDelta
	Intensity float64 // (IntensityS + IntensityP) / 2 at the last surface
	Surfaces  []types.SurfaceResult
}

// GridCentre returns the grid centre on the zStart plane whose ray in the
// given direction passes through the entrance-pupil centre (0, 0, pupilZ).
// Vector-based (no tanθ), degrading to the wavefront plane through the pupil
// at grazing incidence instead of diverging.
func GridCentre(rayDir types.Vec3, pupilZ, zStart float64) (x, y float64) {
	gc := raymath.WavefrontGridCenter(types.Vec3{Z: pupilZ}, rayDir, zStart)
	return gc.X, gc.Y
}

// Launch distributes the pupil grid and builds the per-ray launch states,
// applying the vignetting clip and the OPL normalization selection. It does
// not trace anything. The returned samples are in a deterministic order.
func Launch(spec LaunchSpec) []Sample {
	pts := raymath.PupilGrid(spec.NumRays, spec.ApertureRadius, spec.GridType, spec.RotationOffset)
	wavefrontC := types.Vec3{X: spec.CentreX, Y: spec.CentreY, Z: spec.ZStart}

	var out []Sample
	for _, p := range pts {
		if spec.Vig != nil && !spec.Vig.Contains(p.X, p.Y, spec.ApertureRadius) {
			continue
		}
		px := spec.CentreX + p.X
		py := spec.CentreY + p.Y

		s := Sample{
			PupilX:               p.X,
			PupilY:               p.Y,
			Area:                 p.Area,
			SkipApertureCheck:    spec.SkipApertureCheck,
			SkipGlassPathCheck:   spec.SkipGlassPath,
		}
		if spec.HeightOrigin != nil {
			s.Origin = *spec.HeightOrigin
			s.Dir = types.Vec3{
				X: px - spec.HeightOrigin.X,
				Y: py - spec.HeightOrigin.Y,
				Z: spec.ZStart - spec.HeightOrigin.Z,
			}.Normalize()
		} else {
			switch spec.OPLMode {
			case OPLScalar:
				origin := types.Vec3{X: px, Y: py, Z: spec.ZStart}
				s.Origin = origin
				s.Dir = spec.RayDir
				s.OPLDelta = wavefrontC.Subtract(origin).Dot(spec.RayDir)
			default: // OPLLaunch
				s.Origin = raymath.ProjectOntoWavefront(
					types.Vec3{X: px, Y: py, Z: spec.ZStart}, wavefrontC, spec.RayDir)
				s.Dir = spec.RayDir
			}
		}
		out = append(out, s)
	}
	return out
}

// Trace traces every sample in parallel over `workers` goroutines, writing the
// per-surface results, OPL (with the sample's launch-tilt delta removed) and
// the last-surface intensity back into the slice by index, so the outcome is
// deterministic regardless of worker count.
func Trace(engine *ray.Engine, path []int, surfaces []types.Surface,
	samples []Sample, wavelength float64, pol types.JonesVector, workers int) {
	if workers < 1 {
		workers = runtime.NumCPU()
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range samples {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			s := &samples[i]
			r := types.Ray{
				Wavelength:         wavelength,
				Initial:            types.RayState{Origin: s.Origin, Direction: s.Dir},
				Path:               path,
				Jones:              pol,
				SkipApertureCheck:  s.SkipApertureCheck,
				SkipGlassPathCheck: s.SkipGlassPathCheck,
			}
			res := engine.TraceRay(r, surfaces)
			if res.Error != "" {
				s.Err = res.Error
				s.ErrorCode = res.ErrorCode
				return
			}
			s.OK = true
			s.Surfaces = res.Surfaces
			if len(res.Surfaces) > 0 {
				last := res.Surfaces[len(res.Surfaces)-1]
				s.Intensity = (last.IntensityS + last.IntensityP) / 2
			}
			s.OPL = res.OPLTotal - s.OPLDelta
		}(i)
	}
	wg.Wait()
}