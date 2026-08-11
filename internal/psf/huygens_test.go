package psf

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/mesh"
	"github.com/hiroki/rayweaver/internal/types"
)

const testWavelength = 0.00058756 // 587.56 nm in mm

// makePerfectSphereSamples builds wavefront samples on a flat circular pupil
// of radius a at z=0 that form a perfect converging sphere to the focus
// (0, 0, f): OPL_j = -|q_j - focus| so the phase at the focus is constant.
func makePerfectSphereSamples(a, f float64, rings, angles int) []WavefrontSample {
	focus := types.Vec3{Z: f}
	var pts2 []mesh.Point
	var vecs []types.Vec3
	var samples []WavefrontSample
	for i := 0; i < rings; i++ {
		r := (float64(i) + 0.5) / float64(rings) * a
		for j := 0; j < angles; j++ {
			th := 2 * math.Pi * float64(j) / float64(angles)
			x, y := r*math.Cos(th), r*math.Sin(th)
			q := types.Vec3{X: x, Y: y, Z: 0}
			dir := types.Vec3{X: -x, Y: -y, Z: f}.Normalize()
			samples = append(samples, WavefrontSample{
				Position:  q,
				Direction: dir,
				OPL:       -q.Subtract(focus).Length(),
				Field:     types.Vec3C{X: complex(1, 0), Y: complex(0, 0)},
			})
			pts2 = append(pts2, mesh.Point{X: x, Y: y})
			vecs = append(vecs, q)
		}
	}
	tris := mesh.Triangulate(pts2)
	areas := mesh.VertexAreas(vecs, tris)
	for i := range samples {
		samples[i].Area = areas[i]
	}
	return samples
}

// radialProfile returns the intensity along the row through the peak pixel,
// indexed by distance from the peak.
func radialProfile(g *FieldGrid) (xs, ys []float64) {
	_, px, _ := g.Peak()
	pi, pj := 0, 0
	best := -1.0
	for j := 0; j < g.Spec.NY; j++ {
		for i := 0; i < g.Spec.NX; i++ {
			if v := g.Intensity[j*g.Spec.NX+i]; v > best {
				best = v
				pi, pj = i, j
			}
		}
	}
	for i := pi; i < g.Spec.NX; i++ {
		x := g.Spec.X0 + (float64(i)+0.5)*g.Spec.DX
		xs = append(xs, math.Abs(x-px))
		ys = append(ys, g.Intensity[pj*g.Spec.NX+i])
	}
	return xs, ys
}

func firstDarkRing(g *FieldGrid) float64 {
	xs, ys := radialProfile(g)
	// First local minimum after the peak along the row.
	for i := 1; i < len(ys)-1; i++ {
		if ys[i] < ys[i-1] && ys[i] < ys[i+1] {
			return xs[i]
		}
	}
	return 0
}

func firstSidelobeRatio(g *FieldGrid) float64 {
	xs, ys := radialProfile(g)
	peak, _, _ := g.Peak()
	// Find the first local max after the first dark ring.
	var minIdx = -1
	for i := 1; i < len(ys)-1; i++ {
		if ys[i] < ys[i-1] && ys[i] < ys[i+1] {
			minIdx = i
			break
		}
	}
	if minIdx < 0 {
		return 0
	}
	for i := minIdx + 1; i < len(ys)-1; i++ {
		if ys[i] > ys[i-1] && ys[i] > ys[i+1] {
			return ys[i] / peak
		}
	}
	_ = xs
	return 0
}

func TestHuygensAiryPattern(t *testing.T) {
	a, f := 10.0, 100.0
	na := a / math.Hypot(a, f)
	airy := 0.61 * testWavelength / na

	samples := makePerfectSphereSamples(a, f, 24, 40)
	focus := types.Vec3{Z: f}
	spec := ImageGridSpec{
		NX: 160, NY: 160,
		X0: -4 * airy, Y0: -4 * airy,
		DX: 8 * airy / 160, DY: 8 * airy / 160,
	}
	grid := ComputeField(samples, focus, f, 1.0, testWavelength, spec, false)

	// Peak should be at the centre of the grid (within one pixel).
	peak, px, py := grid.Peak()
	_ = peak
	_ = py
	if math.Abs(px) > spec.DX || math.Abs(py) > spec.DY {
		t.Errorf("peak at (%.4g, %.4g), want near centre", px, py)
	}

	// First dark ring radius ≈ 0.61 λ/NA (within 10 %).
	ring := firstDarkRing(grid)
	if ring == 0 {
		t.Fatal("no first dark ring found")
	}
	if rel := math.Abs(ring-airy) / airy; rel > 0.10 {
		t.Errorf("first dark ring = %.5g mm, want ≈ %.5g (rel %.2f%%)", ring, airy, 100*rel)
	}

	// First sidelobe ≈ 0.0175 of the peak (within a factor of 2.5 — sensitive
	// to sampling and the finite integration window).
	sl := firstSidelobeRatio(grid)
	if sl == 0 || math.Abs(sl-0.0175)/0.0175 > 1.5 {
		t.Errorf("first sidelobe ratio = %.5g, want ≈ 0.0175", sl)
	}
}

func TestHuygensAiryWavelengthScaling(t *testing.T) {
	a, f := 10.0, 100.0
	na := a / math.Hypot(a, f)
	airy := 0.61 * testWavelength / na
	twoAiry := 0.61 * 2 * testWavelength / na

	samples := makePerfectSphereSamples(a, f, 24, 40)
	focus := types.Vec3{Z: f}

	run := func(lambda float64, half float64, n int) *FieldGrid {
		spec := ImageGridSpec{
			NX: n, NY: n,
			X0: -half, Y0: -half,
			DX: 2 * half / float64(n), DY: 2 * half / float64(n),
		}
		return ComputeField(samples, focus, f, 1.0, lambda, spec, false)
	}

	g1 := run(testWavelength, 4*airy, 160)
	g2 := run(2*testWavelength, 4*twoAiry, 160)

	r1 := firstDarkRing(g1)
	r2 := firstDarkRing(g2)
	if r1 == 0 || r2 == 0 {
		t.Fatal("no dark ring found")
	}
	ratio := r2 / r1
	if math.Abs(ratio-2.0) > 0.20 {
		t.Errorf("wavelength 2x should double the Airy radius, got ratio %.3f", ratio)
	}
}

func TestHuygensStrehlPerfect(t *testing.T) {
	a, f := 10.0, 100.0
	na := a / math.Hypot(a, f)
	airy := 0.61 * testWavelength / na

	samples := makePerfectSphereSamples(a, f, 24, 40)
	focus := types.Vec3{Z: f}
	spec := ImageGridSpec{
		NX: 128, NY: 128,
		X0: -4 * airy, Y0: -4 * airy,
		DX: 8 * airy / 128, DY: 8 * airy / 128,
	}
	actual := ComputeField(samples, focus, f, 1.0, testWavelength, spec, false)
	ideal := ComputeField(samples, focus, f, 1.0, testWavelength, spec, true)

	ap, _, _ := actual.Peak()
	ip, _, _ := ideal.Peak()
	if ip <= 0 {
		t.Fatal("ideal peak zero")
	}
	strehl := ap / ip
	if math.Abs(strehl-1.0) > 0.05 {
		t.Errorf("perfect sphere Strehl = %.4f, want ≈ 1.0", strehl)
	}
}

func TestEncircledEnergyAiry(t *testing.T) {
	a, f := 10.0, 100.0
	na := a / math.Hypot(a, f)
	airy := 0.61 * testWavelength / na
	// Airy encircled energy at the first dark ring is ~0.84.
	samples := makePerfectSphereSamples(a, f, 24, 40)
	focus := types.Vec3{Z: f}
	spec := ImageGridSpec{
		NX: 200, NY: 200,
		X0: -5 * airy, Y0: -5 * airy,
		DX: 10 * airy / 200, DY: 10 * airy / 200,
	}
	grid := ComputeField(samples, focus, f, 1.0, testWavelength, spec, false)
	grid.Normalize()
	ee := grid.EncircledEnergy(focus.X, focus.Y, airy)
	if math.Abs(ee-0.84) > 0.08 {
		t.Errorf("encircled energy at Airy radius = %.3f, want ≈ 0.84", ee)
	}
}
