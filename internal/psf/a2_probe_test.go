package psf

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/mesh"
	"github.com/hiroki/rayweaver/internal/types"
)

// makeCurvedSamples builds a polar pupil footprint on z = sag*(x^2+y^2),
// returning both the 3D positions and the true local area element
// dA_t = sqrt(1 + (dz/dx)^2 + (dz/dy)^2) * (annular ring area of the polar cell).
func makeCurvedSamples(rings, angles int, apR, sag float64) ([]types.Vec3, []mesh.Point, []float64) {
	step := apR / float64(rings)
	var vecs []types.Vec3
	var pts2 []mesh.Point
	var trueA []float64
	for i := 0; i < rings; i++ {
		rin := step * float64(i)
		rout := step * float64(i + 1)
		// annular area per cell
		cellArea := math.Pi * (rout*rout - rin*rin) / float64(angles)
		rc := (rin + rout) / 2
		for j := 0; j < angles; j++ {
			th := 2 * math.Pi * float64(j) / float64(angles)
			x, y := rc*math.Cos(th), rc*math.Sin(th)
			z := sag * (x*x + y*y)
			vecs = append(vecs, types.Vec3{X: x, Y: y, Z: z})
			pts2 = append(pts2, mesh.Point{X: x, Y: y})
			// true local area element on the curved surface
			d := math.Sqrt(1 + (2*sag*x)*(2*sag*x) + (2*sag*y)*(2*sag*y))
			trueA = append(trueA, cellArea*d)
		}
	}
	return vecs, pts2, trueA
}

// TestA2LocalAreaWeights checks the per-vertex Delaunay area weights against the
// analytic local area element on a curved reference surface. A strong mismatch
// in *distribution* (even if the total matches) would corrupt the Huygens sum.
func TestA2LocalAreaWeights(t *testing.T) {
	apR := 10.0
	for _, sag := range []float64{0, 0.05, 0.2} {
		for _, ringAng := range [][2]int{{20, 36}, {8, 12}} {
			rings, angles := ringAng[0], ringAng[1]
			vecs, pts2, trueA := makeCurvedSamples(rings, angles, apR, sag)
			tris := mesh.Triangulate(pts2)
			areas := mesh.VertexAreas(vecs, tris)
			// Compare each vertex's area to its analytic cell area.
			var maxRel float64
			var sumSq float64
			n := 0
			for i := range areas {
				if trueA[i] <= 0 {
					continue
				}
				rel := math.Abs(areas[i]-trueA[i]) / trueA[i]
				if rel > maxRel {
					maxRel = rel
				}
				sumSq += rel * rel
				n++
			}
			t.Logf("sag=%.2f rings=%d angles=%d: max|rel|area=%.1f%% rms=%.1f%%",
				sag, rings, angles, 100*maxRel, 100*math.Sqrt(sumSq/float64(n)))
		}
	}
}
