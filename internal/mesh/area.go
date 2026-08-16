package mesh

import (
	"github.com/hiroki/rayweaver/internal/types"
)

// triArea3D returns the area of the triangle formed by the three 3D points.
func triArea3D(p0, p1, p2 types.Vec3) float64 {
	u := p1.Subtract(p0)
	v := p2.Subtract(p0)
	return 0.5 * u.Cross(v).Length()
}

// VertexAreas returns, for each input point, the area weight obtained by
// splitting each triangle's area equally among its three vertices. Triangles
// are measured in 3D (the true reference-surface area element), even though
// the triangulation connectivity was computed in a 2D projection.
func VertexAreas(points []types.Vec3, tris []Triangle) []float64 {
	areas := make([]float64, len(points))
	for _, t := range tris {
		if t.A >= len(points) || t.B >= len(points) || t.C >= len(points) {
			continue
		}
		a := triArea3D(points[t.A], points[t.B], points[t.C]) / 3.0
		areas[t.A] += a
		areas[t.B] += a
		areas[t.C] += a
	}
	return areas
}
