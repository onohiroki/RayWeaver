package psf

import (
	"fmt"
	"runtime"
	"sort"
	"sync"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/mesh"
	"github.com/hiroki/rayweaver/internal/polarization"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/types"
)

// WavefrontSample is one ray's coherent contribution to the reference
// wavefront used by the Huygens integration.
type WavefrontSample struct {
	Position  types.Vec3   // global position on the reference surface
	Direction types.Vec3   // emergent direction (unit)
	OPL       float64      // optical path length object → reference surface
	Field     types.Vec3C  // global complex electric field at the surface
	Area      float64      // reference-surface area element (mm²)
	Intensity float64      // |E|² at the surface
	launchX   float64      // entrance-pupil launch X (deterministic sort key)
	launchY   float64      // entrance-pupil launch Y (deterministic sort key)
}

// PupilGrid is the per-field entrance-pupil sampling shared across
// polarizations: the ray launch states and the chief ray.
type PupilGrid struct {
	GridPoints    []types.GridPoint
	ChiefRay      types.Ray
	ChiefDir      types.Vec3
	EntrancePupil *types.Pupil
}

// WavefrontStats counts the pupil grid rays by outcome.
type WavefrontStats struct {
	Total  int
	Valid  int
	Missed int
}

// ComputeFieldGrid builds the entrance-pupil grid for one field using the
// chief command's pupil logic (dynamic pupil, explicit stop, image-height
// search). GridPoints carry the ray launch Origin/Direction and a non-nil
// ImageX/ImageY for rays that reached the reference surface.
func ComputeFieldGrid(system types.System, gc *glass.Catalog, fd types.FieldDef,
	refSurface, numRays int, wavelength float64, gridType types.GridType) (*PupilGrid, error) {
	if gridType == "" {
		gridType = types.GridPolar
	}
	// The grid itself is polarization-independent; use a reference RCP.
	pol := types.NewCircularJones(true)
	results := chief.DetermineChiefRaysGrid(system, []types.FieldDef{fd}, refSurface,
		numRays, gc, pol, wavelength, false, gridType, nil, nil, nil)
	if len(results) == 0 {
		return nil, fmt.Errorf("chief returned no grid for field")
	}
	r := results[0]
	pts := make([]types.GridPoint, 0, len(r.GridPoints))
	for _, gp := range r.GridPoints {
		if gp.ImageX == nil {
			continue
		}
		pts = append(pts, gp)
	}
	chiefDir := r.ChiefRay.Initial.Direction.Normalize()
	return &PupilGrid{
		GridPoints:    pts,
		ChiefRay:      r.ChiefRay,
		ChiefDir:      chiefDir,
		EntrancePupil: r.EntrancePupil,
	}, nil
}

// TraceWavefront traces the field grid through the system with full Jones
// tracking and records a wavefront sample at the reference surface for every
// ray that reaches it. The samples carry Delaunay-derived area weights.
// workers bounds the per-ray tracing parallelism (0 = runtime.NumCPU()).
func TraceWavefront(system types.System, engine *ray.Engine, fg *PupilGrid,
	fd types.FieldDef, refSurface int, wavelength float64, pol types.JonesVector, workers int) ([]WavefrontSample, WavefrontStats) {
	path := dls.BuildPath(system.Surfaces)
	if len(fd.Path) > 0 {
		path = append([]int{}, fd.Path...)
		if path[0] != 0 {
			path = append([]int{0}, path...)
		}
	}

	// Input field in the transverse frame of the chief ray so the Jones
	// vector has a well-defined meaning for off-axis fields.
	u, v := polarization.TransverseFrame(fg.ChiefDir)
	field0 := polarization.TransverseField(u, v, pol)

	stats := WavefrontStats{Total: len(fg.GridPoints)}
	samples := make([]WavefrontSample, 0, len(fg.GridPoints))

	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers < 1 {
		workers = 1
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for _, gp := range fg.GridPoints {
		wg.Add(1)
		sem <- struct{}{}
		go func(gp types.GridPoint) {
			defer wg.Done()
			defer func() { <-sem }()

			r := types.Ray{
				Wavelength:   wavelength,
				Initial:      types.RayState{Origin: gp.Origin, Direction: gp.Direction},
				Path:         path,
				Jones:        pol,
				InitialField: &field0,
			}
			tr := engine.TraceRay(r, system.Surfaces)
			var sr *types.SurfaceResult
			if tr.Error == "" {
				for i := range tr.Surfaces {
					if tr.Surfaces[i].SurfaceID == refSurface {
						sr = &tr.Surfaces[i]
						break
					}
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if sr == nil {
				stats.Missed++
				return
			}
			stats.Valid++
			samples = append(samples, WavefrontSample{
				Position:  sr.Position,
				Direction: sr.Direction,
				OPL:       sr.OPL,
				Field:     sr.Field,
				Intensity: sr.Field.AbsSq(),
				launchX:   gp.Origin.X,
				launchY:   gp.Origin.Y,
			})
		}(gp)
	}
	wg.Wait()
	close(sem)

	// The parallel trace appends samples in completion order; sort by the
	// intrinsic entrance-pupil launch coordinates so the Huygens summation
	// order (and therefore the floating-point result) is independent of the
	// worker count and of chief's internal grid completion order. The index
	// tiebreaker keeps the order deterministic even when two samples share the
	// same launch coordinates (Go's sort.Slice is not stable).
	sort.Slice(samples, func(a, b int) bool {
		if samples[a].launchX != samples[b].launchX {
			return samples[a].launchX < samples[b].launchX
		}
		if samples[a].launchY != samples[b].launchY {
			return samples[a].launchY < samples[b].launchY
		}
		return a < b
	})

	// Area weights via Delaunay triangulation of the reference-surface hits.
	if len(samples) >= 3 {
		pts2 := make([]mesh.Point, len(samples))
		vecs := make([]types.Vec3, len(samples))
		for i, s := range samples {
			pts2[i] = mesh.Point{X: s.Position.X, Y: s.Position.Y}
			vecs[i] = s.Position
		}
		tris := mesh.Triangulate(pts2)
		areas := mesh.VertexAreas(vecs, tris)
		for i := range samples {
			samples[i].Area = areas[i]
		}
	}
	return samples, stats
}

// DefaultReferenceSurface returns the last optical surface ID (the surface
// immediately before the image plane), used as the default wavefront sampling
// surface. Falls back to the largest surface ID when there is only one.
func DefaultReferenceSurface(surfaces []types.Surface) int {
	if len(surfaces) == 0 {
		return 0
	}
	// Surface IDs in system order; the image plane is conventionally last.
	if len(surfaces) >= 2 {
		return surfaces[len(surfaces)-2].ID
	}
	return surfaces[len(surfaces)-1].ID
}
