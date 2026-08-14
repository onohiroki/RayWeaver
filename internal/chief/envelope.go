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
// ray extent (max |X|,|Y|) per surface, keyed by surface ID. The dynamic-pupil
// grid points already fill the effective aperture, so the envelope reflects
// the true bundle. Rays failing for other reasons (geometry miss, fixed-surface
// clip, glass path) are skipped, so fixed apertures still bound the envelope.
func BeamEnvelope(results []Result, engine *ray.Engine, surfaces []types.Surface, path []int, wavelength float64, pol types.JonesVector) map[int]float64 {
	type job struct{ origin, direction types.Vec3 }
	var jobs []job
	for _, r := range results {
		for _, gp := range r.GridPoints {
			jobs = append(jobs, job{origin: gp.Origin, direction: gp.Direction})
		}
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
				tr := engine.TraceRay(ray, surfaces)
				if tr.Error != "" {
					continue
				}
				for _, sr := range tr.Surfaces {
					e := math.Abs(sr.Position.Y)
					if ax := math.Abs(sr.Position.X); ax > e {
						e = ax
					}
					if e > local[sr.SurfaceID] {
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
