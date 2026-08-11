package mesh

import "math"

// Point is a 2D point used for the reference-surface triangulation.
type Point struct {
	X, Y float64
}

// Triangle is a triangle in the triangulation, given as indices into the
// input point slice (or the super-triangle vertices during construction).
type Triangle struct {
	A, B, C int
}

// pt is a normalised working point.
type pt struct{ x, y float64 }

// orient returns twice the signed area of the triangle (a, b, c). Positive
// means counter-clockwise.
func orient(a, b, c pt) float64 {
	return (b.x-a.x)*(c.y-a.y) - (b.y-a.y)*(c.x-a.x)
}

// inCircle reports whether p lies inside the circumcircle of the triangle
// (a, b, c). The result is orientation-aware so the triangle need not be CCW.
func inCircle(a, b, c, p pt) bool {
	ax, ay := a.x-p.x, a.y-p.y
	bx, by := b.x-p.x, b.y-p.y
	cx, cy := c.x-p.x, c.y-p.y
	det := (ax*ax+ay*ay)*(bx*cy-by*cx) -
		(bx*bx+by*by)*(ax*cy-ay*cx) +
		(cx*cx+cy*cy)*(ax*by-ay*bx)
	const eps = 1e-12
	if orient(a, b, c) > 0 {
		return det > eps
	}
	return det < -eps
}

// Triangulate returns the Delaunay triangulation of points using the
// Bowyer–Watson algorithm. The returned triangles are counter-clockwise and
// reference only real point indices (super-triangle vertices are removed).
// Coincident points are skipped (they receive no triangles).
func Triangulate(points []Point) []Triangle {
	n := len(points)
	if n < 3 {
		return nil
	}

	// Normalise to the unit box centred on the origin so the circumcircle
	// determinant stays well conditioned. Delaunay connectivity is invariant
	// under similarity transforms.
	midx, midy := 0.0, 0.0
	for _, p := range points {
		midx += p.X
		midy += p.Y
	}
	midx /= float64(n)
	midy /= float64(n)
	scale := 0.0
	for _, p := range points {
		if dx := math.Abs(p.X - midx); dx > scale {
			scale = dx
		}
		if dy := math.Abs(p.Y - midy); dy > scale {
			scale = dy
		}
	}
	if scale == 0 {
		scale = 1
	}

	q := make([]pt, n)
	for i, p := range points {
		q[i] = pt{(p.X - midx) / scale, (p.Y - midy) / scale}
	}

	// Super triangle containing the unit box [-1,1]².
	sup := []pt{{-4, -2}, {4, -2}, {0, 6}}
	supN := n

	vertex := func(i int) pt {
		if i < n {
			return q[i]
		}
		return sup[i-n]
	}

	// Track already-inserted coordinate buckets to skip coincident points.
	seen := make(map[pt]int, n)
	tris := []Triangle{{supN, supN + 1, supN + 2}}

	for i := 0; i < n; i++ {
		if _, dup := seen[q[i]]; dup {
			continue
		}
		seen[q[i]] = i
		p := q[i]

		// Gather triangles whose circumcircle contains p and remove them.
		var rest []Triangle
		cavity := make([][2]int, 0, 8)
		for _, t := range tris {
			if inCircle(vertex(t.A), vertex(t.B), vertex(t.C), p) {
				cavity = append(cavity, [2]int{t.A, t.B}, [2]int{t.B, t.C}, [2]int{t.C, t.A})
			} else {
				rest = append(rest, t)
			}
		}
		tris = rest

		// Boundary edges of the cavity appear once; internal edges twice.
		counts := make(map[[2]int]int, len(cavity))
		for _, e := range cavity {
			u, v := e[0], e[1]
			if u > v {
				u, v = v, u
			}
			counts[[2]int{u, v}]++
		}
		for e, c := range counts {
			if c != 1 {
				continue
			}
			// New triangle (u, v, i) kept counter-clockwise.
			if orient(vertex(e[0]), vertex(e[1]), p) < 0 {
				tris = append(tris, Triangle{e[1], e[0], i})
			} else {
				tris = append(tris, Triangle{e[0], e[1], i})
			}
		}
	}

	// Drop triangles that touch a super-triangle vertex.
	out := tris[:0]
	for _, t := range tris {
		if t.A < n && t.B < n && t.C < n {
			out = append(out, t)
		}
	}
	return out
}
