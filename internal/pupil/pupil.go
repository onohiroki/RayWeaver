package pupil

import (
	"runtime"
	"sync"
	"sync/atomic"

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
	NumRings          int // 0 = derive from NumRays via ResolvePolarDims
	NumSpokes         int // 0 = derive from NumRays or NumRings
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
	ErrorCode string  // trace error code (empty when OK)
	OPL       float64 // OPLTotal - OPLDelta
	Intensity float64 // (IntensityS + IntensityP) / 2 at the last surface
	Surfaces  []types.SurfaceResult
	// Slab, when non-nil, is the shared backing array from which Surfaces was
	// sliced (set by Trace when the slab allocator is active). The GC collects
	// the slab once all samples and any downstream references are freed.
	Slab []types.SurfaceResult
}

// surfaceResultSlabs reuses the backing arrays for the per-ray surface results
// produced by Trace. Merit evaluation traces many grids and immediately
// reduces the detailed per-surface results to scalar IPoints; allocating a
// fresh nRays×nSurfaces slab for every field/wavelength/Jacobian point caused
// the dominant allocation churn in escape profiles. Call Release after the
// caller has copied the values it needs out of Samples.
var surfaceResultSlabs sync.Pool

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
	pts := raymath.PupilGrid(spec.NumRays, spec.ApertureRadius, spec.GridType, spec.RotationOffset, spec.NumRings, spec.NumSpokes)
	wavefrontC := types.Vec3{X: spec.CentreX, Y: spec.CentreY, Z: spec.ZStart}

	var out []Sample
	for _, p := range pts {
		if spec.Vig != nil && !spec.Vig.Contains(p.X, p.Y, spec.ApertureRadius) {
			continue
		}
		px := spec.CentreX + p.X
		py := spec.CentreY + p.Y

		s := Sample{
			PupilX:             p.X,
			PupilY:             p.Y,
			Area:               p.Area,
			SkipApertureCheck:  spec.SkipApertureCheck,
			SkipGlassPathCheck: spec.SkipGlassPath,
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

// indexByMaterial precomputes the refractive index of every distinct surface
// material at wavelength. The returned map is read-only during the parallel
// trace, so concurrent readers need no lock. Returns nil when the engine has no
// glass catalog, in which case TraceRay falls back to a per-call lookup.
func indexByMaterial(engine *ray.Engine, surfaces []types.Surface, wavelength float64) map[types.Material]float64 {
	if engine == nil || engine.Glass == nil || len(surfaces) == 0 {
		return nil
	}
	m := make(map[types.Material]float64, len(surfaces))
	for i := range surfaces {
		mat := surfaces[i].Material
		if _, ok := m[mat]; ok {
			continue
		}
		n, err := engine.Glass.RefractiveIndex(mat, wavelength)
		if err != nil {
			continue
		}
		m[mat] = n
	}
	return m
}

// Trace traces every sample in parallel over `workers` goroutines, writing the
// per-surface results, OPL (with the sample's launch-tilt delta removed) and
// the last-surface intensity back into the slice by index, so the outcome is
// deterministic regardless of worker count.
func Trace(engine *ray.Engine, path []int, surfaces []types.Surface,
	samples []Sample, wavelength float64, pol types.JonesVector, workers int) {
	n := len(samples)
	if n == 0 {
		return
	}
	if workers < 1 {
		workers = runtime.NumCPU()
	}
	if workers > n {
		workers = n
	}

	// Resolve each distinct surface material's refractive index once, then share
	// the read-only map with every ray so TraceRay skips the catalog's locked
	// index cache on the per-surface, per-ray hot path.
	idxByMat := indexByMaterial(engine, surfaces, wavelength)

	// Pre-allocate a single slab for all per-surface results and hand out
	// sub-slices, one per ray.  This eliminates the per-ray allocation that
	// previously dominated the heap profile (1.17 TB over a full escape run).
	pathLen := len(path)
	slab := acquireSurfaceResultSlab(n * pathLen)
	for i := range samples {
		samples[i].Surfaces = slab[i*pathLen : i*pathLen : (i+1)*pathLen]
	}
	samples[0].Slab = slab // keep the backing array alive through the samples

	// traceOne writes only its own slot, so the result is independent of the
	// worker schedule (deterministic for any worker count).
	traceOne := func(i int) {
		s := &samples[i]
		r := types.Ray{
			Wavelength:         wavelength,
			Initial:            types.RayState{Origin: s.Origin, Direction: s.Dir},
			Path:               path,
			Jones:              pol,
			IndexByMaterial:    idxByMat,
			SkipApertureCheck:  s.SkipApertureCheck,
			SkipGlassPathCheck: s.SkipGlassPathCheck,
		}
		s.Surfaces, s.ErrorCode = engine.TraceRayInto(r, surfaces, false, s.Surfaces)

		if s.ErrorCode != "" {
			switch s.ErrorCode {
			case string(ray.ErrMissedSurface):
				s.Err = "ray missed surface"
			case string(ray.ErrApertureStop):
				s.Err = "ray missed surface (aperture stop)"
			case string(ray.ErrTIR):
				s.Err = "total internal reflection"
			case string(ray.ErrGlassPathShort):
				s.Err = "ray missed surface (glass path too short)"
			case string(ray.ErrGlassPathLong):
				s.Err = "ray missed surface (glass path too long)"
			default:
				s.Err = s.ErrorCode
			}
			return
		}
		last := s.Surfaces[len(s.Surfaces)-1]
		s.OK = true
		s.Intensity = (last.IntensityS + last.IntensityP) / 2
		s.OPL = last.OPL - s.OPLDelta
	}

	if workers <= 1 {
		for i := 0; i < n; i++ {
			traceOne(i)
		}
		return
	}

	// Fixed worker pool: a constant number of goroutines pull ray indices from a
	// shared counter, instead of spawning one goroutine per ray. The per-ray
	// goroutine spawn made the runtime scheduler's work-stealing/spinning a
	// dominant profile cost; the pool keeps the goroutine count constant.
	var next int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&next, 1)) - 1
				if i >= n {
					return
				}
				traceOne(i)
			}
		}()
	}
	wg.Wait()
}

// Release returns Trace's shared per-surface result slab to the pool and drops
// the Samples' references to it. It must only be called after all reads of
// Sample.Surfaces/Slab are complete. Scalar values (OK, OPL, Intensity, etc.)
// remain untouched. Samples that had to grow an individual slice for an error
// result are detached as well and become collectible normally.
func Release(samples []Sample) {
	if len(samples) == 0 {
		return
	}
	slab := samples[0].Slab
	for i := range samples {
		samples[i].Surfaces = nil
		samples[i].Slab = nil
	}
	if len(slab) == 0 {
		return
	}
	// SurfaceResult has optional pointer fields when detail tracing is enabled.
	// Clear the entire capacity before pooling so a future detail trace or a
	// shorter path cannot keep stale objects alive.
	clear(slab[:cap(slab)])
	surfaceResultSlabs.Put(slab[:0])
}

func acquireSurfaceResultSlab(n int) []types.SurfaceResult {
	if n <= 0 {
		return nil
	}
	if v := surfaceResultSlabs.Get(); v != nil {
		slab := v.([]types.SurfaceResult)
		if cap(slab) >= n {
			return slab[:n]
		}
	}
	return make([]types.SurfaceResult, n)
}
