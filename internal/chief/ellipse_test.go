package chief

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

// survivingGrid builds a grid of pupil points (relative to the grid centre,
// plus the centre offset) in which every point listed in pts reached the
// reference surface and the leading points did not — the shape of a heavily
// vignetted bundle.
func survivingGrid(pts []xy, centreX, centreY float64) []types.GridPoint {
	one := 1.0
	grid := make([]types.GridPoint, 0, len(pts)+3)
	for i := 0; i < 3; i++ {
		// dead rays: no image, so the fit must ignore them
		grid = append(grid, types.GridPoint{PupilX: centreX + 0.9, PupilY: centreY - 0.9})
	}
	for _, p := range pts {
		grid = append(grid, types.GridPoint{
			PupilX: centreX + p.x, PupilY: centreY + p.y,
			ImageX: &one, ImageY: &one,
		})
	}
	return grid
}

// TestFitEffectiveVignettingRoundTrip fits a known, off-centre and rotated
// ellipse from its sampled survivors and checks the reported VignettingDef:
// it round-trips (every survivor passes Contains — the fitted ellipse is
// inflated by effectiveVignettingSafety for exactly that), it recovers the
// generating centre/axes/angle, and it is never smaller than the beam it was
// fitted from.
func TestFitEffectiveVignettingRoundTrip(t *testing.T) {
	const (
		R                = 8.0
		centreX, centreY = -0.7, 1.9 // grid centre on the launch plane
	)
	want := types.VignettingDef{
		DecenterX:    0.35,
		DecenterY:    -0.2,
		CompressionX: 1 - 0.6,
		CompressionY: 1 - 0.3,
		Tangent:      math.Tan(35 * math.Pi / 180),
	}
	theta := math.Atan(want.Tangent)
	a := (1 - want.CompressionX) * R
	b := (1 - want.CompressionY) * R

	// Boundary and interior samples of the generating ellipse: the survivors
	// of a bundle clipped down to it.
	var pts []xy
	for ring := 1; ring <= 5; ring++ {
		rho := float64(ring) / 5
		for i := 0; i < 60; i++ {
			th := float64(i) / 60 * 2 * math.Pi
			u, v := a*rho*math.Cos(th), b*rho*math.Sin(th)
			pts = append(pts, xy{
				x: want.DecenterX*R + u*math.Cos(theta) - v*math.Sin(theta),
				y: want.DecenterY*R + u*math.Sin(theta) + v*math.Cos(theta),
			})
		}
	}
	got := fitEffectiveVignetting(survivingGrid(pts, centreX, centreY), centreX, centreY, R)
	if got == nil {
		t.Fatal("fitEffectiveVignetting returned nil for a well populated ellipse")
	}

	for _, p := range pts {
		if !got.Contains(p.x, p.y, R) {
			t.Fatalf("survivor (%.6f, %.6f) falls outside the reported effective vignetting", p.x, p.y)
		}
	}

	if math.Abs(got.DecenterX*R-want.DecenterX*R) > 1e-6 ||
		math.Abs(got.DecenterY*R-want.DecenterY*R) > 1e-6 {
		t.Errorf("centre = (%.6f, %.6f), want (%.6f, %.6f)",
			got.DecenterX*R, got.DecenterY*R, want.DecenterX*R, want.DecenterY*R)
	}

	// The fit may report the axes swapped (angle shifted by 90°); compare the
	// shape with that ambiguity resolved, allowing the 0.1% safety inflation
	// and a small orientation tolerance.
	ga, gb := (1-got.CompressionX)*R, (1-got.CompressionY)*R
	lo, hi := math.Min(ga, gb), math.Max(ga, gb)
	if lo < 0.98*b || hi > 1.02*a {
		t.Errorf("semi-axes = (%.6f, %.6f), want (%.6f, %.6f) within 2%%", ga, gb, a, b)
	}
	if area := math.Pi * ga * gb; area < math.Pi*a*b {
		t.Errorf("fitted ellipse area %.6f is smaller than the beam area %.6f", area, math.Pi*a*b)
	}
	gPhi := math.Atan(got.Tangent)
	// distance between the two axis directions, modulo pi/2 (axis swap)
	d := math.Mod(gPhi-theta, math.Pi/2)
	if d < 0 {
		d += math.Pi / 2
	}
	if d > math.Pi/4 {
		d = math.Pi/2 - d
	}
	if tol := 1.0 * math.Pi / 180; d > tol {
		t.Errorf("axis angle = %.4f deg, want %.4f deg within %.1f deg (mod 90)",
			gPhi*180/math.Pi, theta*180/math.Pi, tol*180/math.Pi)
	}
	// The reported angle is canonicalised so Tangent never runs away at the
	// +/-90 deg fold (atan of 1e16 round-trips, but it reads as a bug).
	if gPhi > math.Pi/4 || gPhi < -math.Pi/4 {
		t.Errorf("axis angle %.4f deg is outside the canonical [-45, 45] deg range", gPhi*180/math.Pi)
	}
}

// TestFitEffectiveVignettingFallbacks covers the estimates chief refuses: too
// few survivors, a degenerate (zero-area) survivor set and a pupil that is not
// positive. Each must hand the caller back to the circular radius probe.
func TestFitEffectiveVignettingFallbacks(t *testing.T) {
	const R = 8.0
	one := 1.0
	var few []types.GridPoint
	for i := 0; i < minEffectiveVignettingRays-1; i++ {
		few = append(few, types.GridPoint{PupilX: float64(i), PupilY: 1, ImageX: &one, ImageY: &one})
	}
	if eff := fitEffectiveVignetting(few, 0, 0, R); eff != nil {
		t.Errorf("fewer than %d survivors produced %v, want nil", minEffectiveVignettingRays, eff)
	}

	var same []types.GridPoint
	for i := 0; i < 16; i++ {
		same = append(same, types.GridPoint{PupilX: 2, PupilY: 3, ImageX: &one, ImageY: &one})
	}
	if eff := fitEffectiveVignetting(same, 0, 0, R); eff != nil {
		t.Errorf("a single point produced %v, want nil (zero-area ellipse)", eff)
	}

	pts := []xy{{x: 1, y: 0}, {x: -1, y: 0}, {x: 0, y: 1}, {x: 0, y: -1},
		{x: 0.7, y: 0.7}, {x: -0.7, y: -0.7}, {x: 0.7, y: -0.7}, {x: -0.7, y: 0.7}}
	if eff := fitEffectiveVignetting(survivingGrid(pts, 0, 0), 0, 0, 0); eff != nil {
		t.Errorf("a non-positive aperture radius produced %v, want nil", eff)
	}
}

// TestEffectiveVignettingForClippedField runs chief end to end on a field the
// document vignetted down to a small, off-centre pupil ellipse: the result
// must carry the estimated effective vignetting, every ray of the emitted
// (full-aperture) grid that survived must lie inside it, and the spot
// statistics must have been re-laid over that ellipse — more rays traced than
// the clipped full grid kept. A field that is not clipped reports nothing.
func TestEffectiveVignettingForClippedField(t *testing.T) {
	sys, gc := passThroughTripletSystem()
	sys.StopSurface = 2
	const (
		wl      = 0.00058756
		numRays = 256
	)
	vig := &types.VignettingDef{
		DecenterX:    0.25,
		DecenterY:    -0.15,
		CompressionX: 0.65, // (1−c)^2 ≈ 12% of the pupil area: below the 25%
		CompressionY: 0.65, // survival threshold, so the clipped path runs
		Tangent:      math.Tan(20 * math.Pi / 180),
	}
	fields := []types.FieldDef{
		{Angle: 10.0, Direction: []float64{0, 1}, Vignetting: vig},
		{Angle: 4.0, Direction: []float64{0, 1}},
	}
	res := DetermineChiefRaysGridMode(sys, fields, 3, numRays, gc, types.NewCircularJones(true),
		wl, false, types.GridPolar, nil, nil, nil, nil, 0, 0, "")
	if len(res) != 2 {
		t.Fatalf("%d results, want 2", len(res))
	}

	clipped, plain := res[0], res[1]
	if plain.EffectiveVignetting != nil {
		t.Errorf("an unclipped field reported effective vignetting %v, want nil", plain.EffectiveVignetting)
	}
	if clipped.EffectiveVignetting == nil {
		t.Fatal("a field vignetted below the survival threshold reported no effective vignetting")
	}
	eff := clipped.EffectiveVignetting

	full := 0
	for i := range clipped.GridPoints {
		if clipped.GridPoints[i].ImageX != nil {
			full++
		}
	}
	if full == 0 {
		t.Fatal("no ray of the full-aperture grid survived")
	}

	// Every survivor must sit inside the reported ellipse — the invariant that
	// makes it safe to reuse as a vignetting specification. The grid centre is
	// recomputed exactly as computeChiefRayAngleGrid does.
	cr := clipped.ChiefRay
	gcpt := raymath.WavefrontGridCenter(
		types.Vec3{Z: clipped.EntrancePupil.Center.Z}, cr.Initial.Direction, cr.Initial.Origin.Z)
	inside := 0
	for i := range clipped.GridPoints {
		gp := clipped.GridPoints[i]
		if gp.ImageX == nil {
			continue
		}
		if eff.Contains(gp.PupilX-gcpt.X, gp.PupilY-gcpt.Y, clipped.EntrancePupil.Radius) {
			inside++
		}
	}
	if inside != full {
		t.Errorf("%d of %d surviving rays fall outside the reported effective vignetting", full-inside, full)
	}

	// The statistics grid is the remapped one: it carries the whole grid's
	// sample count concentrated in the effective pupil, not the ~12% of rays
	// the clipped full grid kept.
	if clipped.SpotStats == nil {
		t.Fatal("clipped field produced no spot statistics")
	}
	if clipped.SpotStats.TracedRays <= full {
		t.Errorf("statistics grid traced %d rays, want more than the clipped full grid's %d "+
			"(the effective-vignetting remap was not applied)",
			clipped.SpotStats.TracedRays, full)
	}
	if got, wantArea := (1-eff.CompressionX)*(1-eff.CompressionY), (1-vig.CompressionX)*(1-vig.CompressionY); got > wantArea*1.25 {
		t.Errorf("fitted pupil area fraction %.4f is more than 25%% larger than the document's %.4f",
			got, wantArea)
	}
}
