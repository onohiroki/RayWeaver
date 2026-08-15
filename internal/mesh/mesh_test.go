package mesh

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestTriangulateSquare(t *testing.T) {
	// Four corners of a unit square: two triangles.
	pts := []Point{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	tris := Triangulate(pts)
	if len(tris) != 2 {
		t.Fatalf("square should triangulate to 2 triangles, got %d", len(tris))
	}
	total := 0.0
	for _, tr := range tris {
		a, b, c := pts[tr.A], pts[tr.B], pts[tr.C]
		total += 0.5 * math.Abs((b.X-a.X)*(c.Y-a.Y)-(b.Y-a.Y)*(c.X-a.X))
	}
	if math.Abs(total-1.0) > 1e-9 {
		t.Errorf("square area = %v, want 1.0", total)
	}
}

func TestTriangulatePentagon(t *testing.T) {
	// Regular pentagon (convex): should produce n-2 = 3 triangles.
	pts := make([]Point, 5)
	for i := 0; i < 5; i++ {
		a := 2 * math.Pi * float64(i) / 5
		pts[i] = Point{math.Cos(a), math.Sin(a)}
	}
	tris := Triangulate(pts)
	if len(tris) != 3 {
		t.Fatalf("pentagon should triangulate to 3 triangles, got %d", len(tris))
	}
}

func TestTriangulatePolarGridArea(t *testing.T) {
	// A polar grid with n rings of n angles approximates a circle of radius 1.
	// The triangulated area should approach the disk area (π) from below.
	rings, angles := 12, 16
	var pts []Point
	for i := 0; i < rings; i++ {
		r := (float64(i) + 0.5) / float64(rings)
		for j := 0; j < angles; j++ {
			a := 2 * math.Pi * float64(j) / float64(angles)
			pts = append(pts, Point{r * math.Cos(a), r * math.Sin(a)})
		}
	}
	tris := Triangulate(pts)
	total := 0.0
	for _, tr := range tris {
		a, b, c := pts[tr.A], pts[tr.B], pts[tr.C]
		total += 0.5 * math.Abs((b.X-a.X)*(c.Y-a.Y)-(b.Y-a.Y)*(c.X-a.X))
	}
	frac := total / math.Pi
	if frac < 0.80 || frac > 1.05 {
		t.Errorf("polar grid area fraction = %.3f (area %v), want within [0.8, 1.05]", frac, total)
	}
}

func TestVertexAreasSum(t *testing.T) {
	// Vertex area weights of a square (unit area) must sum to the area.
	pts2 := []Point{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	tris := Triangulate(pts2)
	vecs := []types.Vec3{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}, {X: 0, Y: 1}}
	areas := VertexAreas(vecs, tris)
	sum := 0.0
	for _, a := range areas {
		sum += a
	}
	if math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("vertex areas sum = %v, want 1.0", sum)
	}
}

func TestTriangulateCollinear(t *testing.T) {
	// Degenerate collinear input must not panic and must give zero area.
	pts := []Point{{0, 0}, {1, 0}, {2, 0}}
	tris := Triangulate(pts)
	for _, tr := range tris {
		a, b, c := pts[tr.A], pts[tr.B], pts[tr.C]
		area := 0.5 * math.Abs((b.X-a.X)*(c.Y-a.Y)-(b.Y-a.Y)*(c.X-a.X))
		if area > 1e-9 {
			t.Errorf("collinear input produced non-zero area triangle %v", tr)
		}
	}
}

func TestTriangulateDeterministic(t *testing.T) {
	// The Bowyer-Watson rebuild must not depend on Go map iteration order
	// (randomized), so repeated runs must return the identical triangle list.
	// A polar grid with a centre point gives cocircular / near-cocircular
	// point sets that would previously pick an arbitrary triangulation per run.
	var pts []Point
	pts = append(pts, Point{0, 0})
	for i := 0; i < 8; i++ {
		a := 2 * math.Pi * float64(i) / 8
		pts = append(pts, Point{0.5 * math.Cos(a), 0.5 * math.Sin(a)})
	}
	for i := 0; i < 12; i++ {
		a := 2 * math.Pi * float64(i) / 12
		pts = append(pts, Point{1.0 * math.Cos(a), 1.0 * math.Sin(a)})
	}
	first := Triangulate(pts)
	for run := 0; run < 20; run++ {
		next := Triangulate(pts)
		if len(next) != len(first) {
			t.Fatalf("run %d: triangle count %d != first %d", run, len(next), len(first))
		}
		for i := range first {
			if next[i] != first[i] {
				t.Errorf("run %d: triangle %d = %v, want %v", run, i, next[i], first[i])
				break
			}
		}
	}
}

