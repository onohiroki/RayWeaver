package wavefront

import (
	"math"

	"github.com/hiroki/rayweaver/internal/mesh"
)

// Grid is a regular grid specification for a wavefront map.
type Grid struct {
	NX, NY int
	X0, Y0 float64
	DX, DY float64
}

// InterpolateResidual builds a square regular grid (n per side) over the pupil
// bounding box of the samples (padded by 5%) and barycentrically interpolates
// the per-sample residual — OPL minus the best-fit sphere — onto it via the
// Delaunay triangulation. Grid points outside the sampled hull are NaN. The
// grid rows are y-major (row j, column i at index j·NX + i).
func InterpolateResidual(data []SampleData, n int) (Grid, []float64) {
	if n <= 0 {
		n = 64
	}
	if len(data) > 0 && n > len(data) {
		n = len(data)
	}
	if len(data) == 0 {
		return Grid{NX: n, NY: n}, make([]float64, n*n)
	}

	px := make([]float64, len(data))
	py := make([]float64, len(data))
	v := make([]float64, len(data))
	for i, s := range data {
		px[i], py[i], v[i] = s.X, s.Y, s.SphereResidual
	}
	minX, maxX, minY, maxY := bounds(px, py)
	padX := (maxX - minX) * 0.05
	padY := (maxY - minY) * 0.05
	if padX <= 0 {
		padX = 1
	}
	if padY <= 0 {
		padY = 1
	}
	x0, x1 := minX-padX, maxX+padX
	y0, y1 := minY-padY, maxY+padY
	dx := (x1 - x0) / float64(n)
	dy := (y1 - y0) / float64(n)
	it := newInterp2D(px, py, v)
	g := Grid{NX: n, NY: n, X0: x0, Y0: y0, DX: dx, DY: dy}
	vals := make([]float64, n*n)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			qx := x0 + (float64(i)+0.5)*dx
			qy := y0 + (float64(j)+0.5)*dy
			if wv, ok := it.eval(qx, qy); ok {
				vals[j*n+i] = wv
			} else {
				vals[j*n+i] = math.NaN()
			}
		}
	}
	return g, vals
}

// interp2D is a barycentric interpolant over a Delaunay triangulation of
// scattered (x, y) samples. Query points inside the convex hull are
// interpolated; outside points report false.
type interp2D struct {
	tris []mesh.Triangle
	x    []float64
	y    []float64
	v    []float64
}

// newInterp2D builds the interpolant from the sample coordinates and values.
func newInterp2D(px, py, v []float64) *interp2D {
	pts := make([]mesh.Point, len(px))
	for i := range px {
		pts[i] = mesh.Point{X: px[i], Y: py[i]}
	}
	return &interp2D{tris: mesh.Triangulate(pts), x: px, y: py, v: v}
}

// eval interpolates the value array at (qx, qy). It reports false when the
// point lies outside the triangulated hull.
func (it *interp2D) eval(qx, qy float64) (float64, bool) {
	for _, t := range it.tris {
		// Barycentric coordinates of (qx, qy) in triangle (a, b, c).
		ax, ay := it.x[t.A], it.y[t.A]
		bx, by := it.x[t.B], it.y[t.B]
		cx, cy := it.x[t.C], it.y[t.C]
		d := (by-cy)*(ax-cx) + (cx-bx)*(ay-cy)
		if d == 0 {
			continue
		}
		w1 := ((by-cy)*(qx-cx) + (cx-bx)*(qy-cy)) / d
		w2 := ((cy-ay)*(qx-cx) + (ax-cx)*(qy-cy)) / d
		w3 := 1 - w1 - w2
		const eps = -1e-9
		if w1 < eps || w2 < eps || w3 < eps {
			continue
		}
		return w1*it.v[t.A] + w2*it.v[t.B] + w3*it.v[t.C], true
	}
	return 0, false
}

func bounds(x, y []float64) (minX, maxX, minY, maxY float64) {
	minX, maxX = math.Inf(1), math.Inf(-1)
	minY, maxY = math.Inf(1), math.Inf(-1)
	for i := range x {
		if x[i] < minX {
			minX = x[i]
		}
		if x[i] > maxX {
			maxX = x[i]
		}
		if y[i] < minY {
			minY = y[i]
		}
		if y[i] > maxY {
			maxY = y[i]
		}
	}
	return
}
