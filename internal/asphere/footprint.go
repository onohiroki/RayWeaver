package asphere

import (
	"math"
	"runtime"
	"sync"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// Field is one object-space field used by the asphere analysis.
type Field struct {
	ID        int
	Angle     float64 // object field angle (degrees)
	Weight    float64
	Direction []float64 // [dx, dy] azimuth of the field (normalised)
}

// SurfaceHit is a ray's intersection with one surface: the intersection point
// in global coordinates and the emergent direction after the interaction.
type SurfaceHit struct {
	Position  types.Vec3
	Direction types.Vec3
}

// RayHit is one traced ray's data: the image OPL (optical path length to the
// image plane), the pupil-plane grid coordinate, and the per-surface
// intersections along the path.
type RayHit struct {
	OPL    float64
	OPD    float64 // wavefront error referenced to the field mean (post-preprocessing)
	Weight float64
	PupilX float64
	PupilY float64
	Hits   map[int]SurfaceHit
	OK     bool
}

// FieldFootprintData is all ray hits for one (field, wavelength) pair.
type FieldFootprintData struct {
	FieldID    int
	Wavelength float64
	Weight     float64
	RayHits    []RayHit
}

type pupilPoint struct {
	X, Y float64
}

// GenerateFootprints traces a polar pupil grid for each (field, wavelength)
// and returns per-ray image OPL plus per-surface intersection data. The grid
// radius and centring follow the same conventions as the chief/optimize grid
// traces (dls.ApertureRadiusForGrid plus a per-field pupil offset), so the
// beam fills the entrance pupil for every field. pupilZs gives each field's
// entrance-pupil Z (the aperture position) used to centre its grid.
func GenerateFootprints(surfaces []types.Surface, fields []Field, wavelengths []float64, pupilSamples int, gc *glass.Catalog, pupilZs []float64) []FieldFootprintData {
	if len(wavelengths) == 0 {
		wavelengths = []float64{types.DefaultWavelength}
	}
	engine := ray.NewEngine(gc, nil)
	path := dls.BuildPath(surfaces)
	zStart := -100.0

	var out []FieldFootprintData
	for fi, f := range fields {
		dir := rayDirection(f)
		pupilZ := 0.0
		if fi < len(pupilZs) {
			pupilZ = pupilZs[fi]
		}
		for _, wl := range wavelengths {
			fd := FieldFootprintData{FieldID: f.ID, Wavelength: wl, Weight: f.Weight}

			radius := dls.ApertureRadiusForGrid(surfaces, 0, wl, gc, 1.0)
			if radius <= 0 {
				radius = surface.MinApertureRadius(surfaces)
			}
			if radius <= 0 {
				continue
			}

			grid := polarGrid(pupilSamples, radius)
			offsetX, offsetY := gridOffset(f, pupilZ, zStart)
			for i := range grid {
				grid[i].X += offsetX
				grid[i].Y += offsetY
			}

			fd.RayHits = make([]RayHit, len(grid))
			parallelFor(len(grid), func(i int) {
				pt := grid[i]
				origin := types.Vec3{X: pt.X, Y: pt.Y, Z: zStart}
				r := types.Ray{
					Wavelength: wl,
					Initial:    types.RayState{Origin: origin, Direction: dir},
					Path:       path,
					Jones:      types.NewCircularJones(true),
				}
				res := engine.TraceRay(r, surfaces)
				hit := RayHit{Weight: f.Weight, PupilX: pt.X, PupilY: pt.Y}
				if res.Error != "" || len(res.Surfaces) == 0 {
					fd.RayHits[i] = hit
					return
				}
				hit.OPL = res.OPLTotal
				hit.OK = true
				hit.Hits = make(map[int]SurfaceHit, len(res.Surfaces))
				for _, sr := range res.Surfaces {
					if sr.SurfaceID <= 0 {
						continue
					}
					hit.Hits[sr.SurfaceID] = SurfaceHit{Position: sr.Position, Direction: sr.Direction}
				}
				fd.RayHits[i] = hit
			})

			out = append(out, fd)
		}
	}
	return out
}

// rayDirection returns the object-space ray direction for a field: an angle in
// the XY plane, rotated by the field azimuth Direction.
func rayDirection(f Field) types.Vec3 {
	rad := raymath.DegToRad(f.Angle)
	sinT := math.Sin(rad)
	cosT := math.Cos(rad)
	dx, dy := 0.0, 1.0
	if len(f.Direction) >= 2 {
		norm := math.Hypot(f.Direction[0], f.Direction[1])
		if norm > 0 {
			dx = f.Direction[0] / norm
			dy = f.Direction[1] / norm
		}
	}
	return types.Vec3{X: sinT * dx, Y: sinT * dy, Z: cosT}.Normalize()
}

// gridOffset shifts the pupil grid so the field's chief ray crosses the
// optical axis at the entrance-pupil plane z=pupilZ, mirroring the chief/optimize
// grid centring.
func gridOffset(f Field, pupilZ float64, zStart float64) (float64, float64) {
	rad := raymath.DegToRad(f.Angle)
	tanComp := math.Tan(rad)
	dx, dy := 0.0, 1.0
	if len(f.Direction) >= 2 {
		norm := math.Hypot(f.Direction[0], f.Direction[1])
		if norm > 0 {
			dx = f.Direction[0] / norm
			dy = f.Direction[1] / norm
		}
	}
	off := -(pupilZ - zStart) * tanComp
	return off * dx, off * dy
}

// polarGrid generates a polar pupil grid: `samples` rings, linearly spaced in
// radius, each with `samples` angular sectors.
func polarGrid(samples int, radius float64) []pupilPoint {
	var pts []pupilPoint
	n := samples
	if n < 2 {
		n = 2
	}
	for i := 0; i < n; i++ {
		r := (float64(i) + 0.5) / float64(n) * radius
		for j := 0; j < n; j++ {
			theta := 2 * math.Pi * (float64(j) + 0.5) / float64(n)
			pts = append(pts, pupilPoint{X: r * math.Cos(theta), Y: r * math.Sin(theta)})
		}
	}
	return pts
}

// parallelFor runs work over [0, n) with GOMAXPROCS workers.
func parallelFor(n int, work func(i int)) {
	if n <= 0 {
		return
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > n {
		workers = n
	}
	ch := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				work(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		ch <- i
	}
	close(ch)
	wg.Wait()
}
