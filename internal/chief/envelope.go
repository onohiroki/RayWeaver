package chief

import (
	"math"
	"runtime"
	"sync"

	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/types"
)

// BeamEnvelope traces every grid point of the given chief results through all
// surfaces with auto-aperture checks disabled and returns the maximum radial
// beam extent per surface, keyed by surface ID: the footprint of the prescribed
// bundle at that surface, which is what an aperture has to cover.
//
// Three properties make it usable as a sizing target:
//
//   - The height is the SURFACE-LOCAL radius, the quantity the tracer clips on
//     (`h > Diameter/2`). A global max(|X|,|Y|) is a bounding-box measure that
//     under-reports an off-axis or tilted footprint by up to a factor √2, so
//     apertures sized from it stayed too small and kept clipping the very field
//     they were sized for.
//   - Auto-aperture checks are disabled, so the measurement is not circular:
//     an aperture that is currently too small does not shrink the bundle it is
//     going to be sized from.
//   - A ray that fails later still contributes the surfaces it did reach. The
//     old code dropped the whole ray on the first failure anywhere in the
//     system, so one undersized aperture downstream starved the envelope of
//     every surface before it (39/74 rays still clipped at one surface after
//     five `chief --clear-aperture` passes).
//
// Fixed apertures (the stop, `auto_aperture: false` surfaces) stay closed, so
// they still bound the bundle.
func BeamEnvelopeMax(results []Result, engine *ray.Engine, surfaces []types.Surface, path []int, wavelengths []float64, pol types.JonesVector) map[int]float64 {
	if len(wavelengths) == 0 {
		return nil
	}
	out := make(map[int]float64)
	for _, wl := range wavelengths {
		for id, extent := range BeamEnvelope(results, engine, surfaces, path, wl, pol) {
			if extent > out[id] {
				out[id] = extent
			}
		}
	}
	return out
}

func BeamEnvelope(results []Result, engine *ray.Engine, surfaces []types.Surface, path []int, wavelength float64, pol types.JonesVector) map[int]float64 {
	type job struct{ origin, direction types.Vec3 }
	var jobs []job
	for _, r := range results {
		for _, gp := range r.GridPoints {
			jobs = append(jobs, job{origin: gp.Origin, direction: gp.Direction})
		}
	}

	byID := make(map[int]types.Surface, len(surfaces))
	for _, s := range surfaces {
		byID[s.ID] = s
	}

	workers := runtime.GOMAXPROCS(0)
	if workers > len(jobs) {
		workers = len(jobs)
	}
	if workers < 1 {
		workers = 1
	}
	perWorkerMax := make([]map[int]float64, workers)
	for w := 0; w < workers; w++ {
		perWorkerMax[w] = make(map[int]float64)
	}
	var wg sync.WaitGroup
	jobCh := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			local := perWorkerMax[w]
			for i := range jobCh {
				gp := jobs[i]
				ray := types.Ray{
					Wavelength:            wavelength,
					Initial:               types.RayState{Origin: gp.origin, Direction: gp.direction},
					Path:                  path,
					Jones:                 pol,
					SkipAutoApertureCheck: true,
				}
				// The reached prefix counts even when the ray fails later: the
				// heights it did reach are real measurements of this bundle.
				tr := engine.TraceRay(ray, surfaces, false)
				for _, sr := range tr.Surfaces {
					s, ok := byID[sr.SurfaceID]
					if !ok {
						continue
					}
					p := s.GlobalToLocal.MultiplyPoint(sr.Position)
					if e := math.Hypot(p.X, p.Y); e > local[sr.SurfaceID] {
						local[sr.SurfaceID] = e
					}
				}
			}
		}(w)
	}
	for i := range jobs {
		jobCh <- i
	}
	close(jobCh)
	wg.Wait()

	env := make(map[int]float64)
	for w := 0; w < workers; w++ {
		for id, e := range perWorkerMax[w] {
			if e > env[id] {
				env[id] = e
			}
		}
	}
	return env
}
