package raymath

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestRefractNormalIncidence(t *testing.T) {
	d := types.Vec3{X: 0, Y: 0, Z: 1}
	n := types.Vec3{X: 0, Y: 0, Z: -1}
	got, ok := Refract(d, n, 1.0, 1.5)
	if !ok {
		t.Fatal("Refract returned false for normal incidence")
	}
	if math.Abs(got.X) > 1e-12 || math.Abs(got.Y) > 1e-12 || math.Abs(got.Z-1) > 1e-12 {
		t.Errorf("Normal incidence: got %v, want (0,0,1)", got)
	}
}

func TestRefractSnellLaw(t *testing.T) {
	sinT1 := 0.5
	d := types.Vec3{X: 0, Y: sinT1, Z: -math.Cos(math.Asin(sinT1))}
	n := types.Vec3{X: 0, Y: 0, Z: -1}
	got, ok := Refract(d.Normalize(), n, 1.0, 1.5)
	if !ok {
		t.Fatal("Refract returned false")
	}
	wantSin := sinT1 / 1.5
	gotSin := math.Sqrt(got.X*got.X + got.Y*got.Y)
	if math.Abs(gotSin-wantSin) > 1e-10 {
		t.Errorf("Snell: sin(theta2) = %v, want %v", gotSin, wantSin)
	}
}

func TestRefractTIR(t *testing.T) {
	sinT1 := math.Sin(math.Pi / 4)
	d := types.Vec3{X: 0, Y: sinT1, Z: -math.Cos(math.Pi / 4)}
	n := types.Vec3{X: 0, Y: 0, Z: -1}
	_, ok := Refract(d.Normalize(), n, 1.5, 1.0)
	if ok {
		t.Error("Expected TIR (false) for incidence above critical angle")
	}
}

func TestReflect(t *testing.T) {
	sin45 := math.Sin(math.Pi / 4)
	d := types.Vec3{X: 0, Y: sin45, Z: -sin45}
	n := types.Vec3{X: 0, Y: 0, Z: -1}
	got := Reflect(d.Normalize(), n)
	if math.Abs(got.Y-sin45) > 1e-12 || math.Abs(got.Z-sin45) > 1e-12 {
		t.Errorf("Reflect = %v, want (0, sin45, sin45)", got)
	}
	if got.Z <= 0 {
		t.Error("Reflected ray should have Z > 0")
	}
}

func TestFresnelNormalIncidence(t *testing.T) {
	rs, rp, ts, tp := FresnelAmplitude(1.0, 1.5, 1.0, 1.0)
	rWant := (1.0 - 1.5) / (1.0 + 1.5)
	if math.Abs(rs-rWant) > 1e-12 {
		t.Errorf("Fresnel rs = %v, want %v", rs, rWant)
	}
	if math.Abs(rp-rWant) > 1e-12 {
		t.Errorf("Fresnel rp = %v, want %v", rp, rWant)
	}
	_ = ts
	_ = tp
}

func TestFresnelBrewster(t *testing.T) {
	// At Brewster angle, rp = 0
	// tan(theta_B) = n2/n1
	// For n1=1.5, n2=1.0 entering from denser medium, theta_B = arctan(1/1.5) = 33.69°
	thetaB := math.Atan(1.0 / 1.5)
	ct1 := math.Cos(thetaB)
	st1 := math.Sin(thetaB)
	ct2 := math.Cos(math.Asin(1.5 * st1 / 1.0))
	_, rp, _, _ := FresnelAmplitude(1.5, 1.0, ct1, ct2)
	if math.Abs(rp) > 1e-10 {
		t.Errorf("rp at Brewster = %v, want 0", rp)
	}
}

func TestIntersectSphereOnAxis(t *testing.T) {
	origin := types.Vec3{X: 0, Y: 0, Z: -10}
	dir := types.Vec3{X: 0, Y: 0, Z: 1}
	// Sphere radius 100, center at (0,0,100)
	// Vertex at z=0. Ray from z=-10 to z=0 is 10 units.
	tHit, ok := IntersectSphere(origin, dir, 100)
	if !ok || math.Abs(tHit-10) > 1e-10 {
		t.Errorf("IntersectSphere: t = %v, want 10", tHit)
	}
}

func TestIntersectSphereMiss(t *testing.T) {
	origin := types.Vec3{X: 200, Y: 0, Z: -10}
	dir := types.Vec3{X: 0, Y: 0, Z: 1}
	_, ok := IntersectSphere(origin, dir, 100)
	if ok {
		t.Error("Expected miss for ray far from sphere")
	}
}

func TestIntersectSphereTangent(t *testing.T) {
	origin := types.Vec3{X: 100.1, Y: 0, Z: 0}
	dir := types.Vec3{X: 0, Y: 0, Z: 1}
	// Ray at x=100.1, outside sphere radius 100 centered at (0,0,100), should miss
	_, ok := IntersectSphere(origin, dir, 100)
	if ok {
		t.Error("Expected miss for ray outside sphere")
	}
}

func TestIntersectSphereCoincidentConvex(t *testing.T) {
	// Ray is already on a zero-thickness (coincident) surface at the vertex of
	// a convex sphere: t1 = 0, t2 = 2R. The vertex hit must be accepted so the
	// trace continues through coincident surfaces.
	tHit, ok := IntersectSphere(types.Vec3{}, types.Vec3{Z: 1}, 100)
	if !ok || math.Abs(tHit) > 1e-9 {
		t.Errorf("IntersectSphere at convex vertex: t = %v, ok = %v, want t ≈ 0", tHit, ok)
	}
}

func TestIntersectSphereCoincidentConcave(t *testing.T) {
	// Same but for a concave surface (radius < 0): the vertex is the t2 ≈ 0
	// intersection.
	tHit, ok := IntersectSphere(types.Vec3{}, types.Vec3{Z: 1}, -100)
	if !ok || math.Abs(tHit) > 1e-9 {
		t.Errorf("IntersectSphere at concave vertex: t = %v, ok = %v, want t ≈ 0", tHit, ok)
	}
}

func TestSphereNormal(t *testing.T) {
	// Sphere center at (0,0,R). Point at (100,0,0) on sphere R=100.
	// Normal should point from center to surface: (1,0,-1) / sqrt(2) = (0.707,0,-0.707)
	p := types.Vec3{X: 100, Y: 0, Z: 0}
	n := SphereNormal(p, 100)
	if math.Abs(n.X-0.70710678) > 1e-6 || math.Abs(n.Z+0.70710678) > 1e-6 {
		t.Errorf("SphereNormal at (R,0,0) = %v, want approx (0.707,0,-0.707)", n)
	}
}

func TestSphereNormalAtVertex(t *testing.T) {
	// At vertex (0,0,0) on sphere R=100, normal should be (0,0,-1)
	p := types.Vec3{X: 0, Y: 0, Z: 0}
	n := SphereNormal(p, 100)
	want := types.Vec3{X: 0, Y: 0, Z: -1}
	if math.Abs(n.X-want.X) > 1e-6 || math.Abs(n.Y-want.Y) > 1e-6 || math.Abs(n.Z-want.Z) > 1e-6 {
		t.Errorf("SphereNormal at vertex = %v, want %v", n, want)
	}
}

func TestIntersectAsphereOnAxis(t *testing.T) {
	sag := func(h float64) float64 {
		return h * h / (100 * (1 + math.Sqrt(1-h*h/(100*100))))
	}
	origin := types.Vec3{X: 0, Y: 0, Z: -10}
	dir := types.Vec3{X: 0, Y: 0, Z: 1}
	tHit, ok := IntersectAsphere(origin, dir, sag, 100, 20, 1e-12)
	if !ok || math.Abs(tHit-10) > 1e-6 {
		t.Errorf("IntersectAsphere on-axis: t = %v, want 10", tHit)
	}
}

func TestIntersectAsphereOffAxisMatchesSphere(t *testing.T) {
	// A zero-coefficient asphere is a pure sphere; its intersection must match
	// the analytic sphere intersection even for an off-axis ray whose root lies
	// far along the ray (the sphere seed must converge).
	R := 100.0
	sag := func(h float64) float64 {
		return h * h / (R * (1 + math.Sqrt(1-h*h/(R*R))))
	}
	// 12° ray offset so it passes through the sphere's clear aperture.
	dir := DirectionFromAngle(12)
	zStart := -200.0
	offset := -(10.0 - zStart) * math.Tan(DegToRad(12)) // pupil offset for pupilZ=10
	origin := types.Vec3{X: offset, Y: 0, Z: zStart}
	tSphere, ok := IntersectSphere(origin, dir, R)
	if !ok {
		t.Fatal("sphere intersection failed for reference")
	}
	tAsp, ok := IntersectAsphere(origin, dir, sag, R, 30, 1e-9)
	if !ok {
		t.Fatalf("IntersectAsphere off-axis: miss, want t≈%v", tSphere)
	}
	if math.Abs(tAsp-tSphere) > 1e-4 {
		t.Errorf("IntersectAsphere off-axis: t = %v, sphere = %v", tAsp, tSphere)
	}
}

func TestPolynomialAsphereSagSpherical(t *testing.T) {
	h := 10.0
	R := 100.0
	z := PolynomialAsphereSag(h, R, 0, nil)
	// Spherical sag: z = h^2 / (R * (1 + sqrt(1 - h^2/R^2)))
	want := h * h / (R * (1 + math.Sqrt(1-h*h/(R*R))))
	if math.Abs(z-want) > 1e-12 {
		t.Errorf("Spherical sag at h=10: %v, want %v", z, want)
	}
}

func TestZernikeAsphereSag(t *testing.T) {
	h := 10.0
	R := 100.0
	coeffs := []float64{0, 0, 0, 0.5} // Z4 defocus term
	z := ZernikeAsphereSag(h, R, 0, coeffs, 50)
	// Should include spherical base + Zernike term
	if z == 0 {
		t.Error("Zernike sag should be non-zero")
	}
}

func TestComputeDecenterTransform(t *testing.T) {
	steps := []types.DecenterStep{
		{Shift: types.Vec3{X: 1, Y: 2, Z: 0}},
	}
	m := ComputeDecenterTransform(steps)
	p := m.MultiplyPoint(types.Vec3{X: 0, Y: 0, Z: 0})
	want := types.Vec3{X: 1, Y: 2, Z: 0}
	if math.Abs(p.X-want.X) > 1e-12 || math.Abs(p.Y-want.Y) > 1e-12 {
		t.Errorf("Decenter shift = %v, want %v", p, want)
	}
}

func TestDetermineInteraction(t *testing.T) {
	if got := DetermineInteraction(0, 1, 2); got != types.Transmit {
		t.Error("0->1->2 should be Transmit")
	}
	if got := DetermineInteraction(2, 1, 0); got != types.Transmit {
		t.Error("2->1->0 should be Transmit (reverse path)")
	}
	if got := DetermineInteraction(0, 1, 0); got != types.Reflect {
		t.Error("0->1->0 should be Reflect")
	}
}

func TestComputeParaxialCurvature(t *testing.T) {
	sag := func(h float64) float64 {
		return h * h / (2 * 100)
	}
	cv := ComputeParaxialCurvature(sag)
	want := 1.0 / 100.0
	if math.Abs(cv-want) > 1e-6 {
		t.Errorf("Paraxial curvature = %v, want %v", cv, want)
	}
}

func TestSphereParaxialRadius(t *testing.T) {
	r := SphereParaxialRadius(100, nil)
	if r != 100 {
		t.Errorf("SphereParaxialRadius = %v, want 100", r)
	}
	r2 := SphereParaxialRadius(100, []float64{1e-5})
	if math.Abs(r2-100) < 1e-6 {
		t.Error("Expected different paraxial radius with aspheric coefficient")
	}
}

func TestIntersectSpherePlaneBehind(t *testing.T) {
	// A flat surface (radius 0) is crossed even at negative t (the ray is
	// already behind it); this must stay accepted (the original behaviour).
	tHit, ok := IntersectSphere(types.Vec3{Z: 5}, types.Vec3{Z: 1}, 0)
	if !ok || math.Abs(tHit+5) > 1e-9 {
		t.Errorf("IntersectSphere plane behind: t = %v, ok = %v, want -5", tHit, ok)
	}
}

func TestIntersectSphereBoth(t *testing.T) {
	// A ray on the axis of a sphere returns both roots (vertex and far side).
	t1, t2, ok := IntersectSphereBoth(types.Vec3{Z: -100}, types.Vec3{Z: 1}, 100)
	if !ok {
		t.Fatal("IntersectSphereBoth failed")
	}
	if math.Abs(t1-100) > 1e-6 || math.Abs(t2-300) > 1e-6 {
		t.Errorf("both roots: t1=%v t2=%v, want 100/300", t1, t2)
	}
}

func TestIntersectSpherePlane(t *testing.T) {
	origin := types.Vec3{X: 0, Y: 0, Z: -10}
	dir := types.Vec3{X: 0, Y: 0, Z: 1}
	tHit, ok := IntersectSphere(origin, dir, 0)
	if !ok || math.Abs(tHit-10) > 1e-10 {
		t.Errorf("Plane intersection: t = %v, want 10", tHit)
	}
}

func TestAsphereNormalOnAxis(t *testing.T) {
	sag := func(h float64) float64 {
		return h * h / (2 * 100)
	}
	n := AsphereNormal(types.Vec3{X: 0, Y: 0, Z: 0}, sag)
	want := types.Vec3{X: 0, Y: 0, Z: 1}
	if math.Abs(n.X-want.X) > 1e-6 || math.Abs(n.Y-want.Y) > 1e-6 || math.Abs(n.Z-want.Z) > 1e-6 {
		t.Errorf("AsphereNormal on-axis = %v, want %v", n, want)
	}
}

func TestDegToRadRadToDeg(t *testing.T) {
	if math.Abs(DegToRad(180)-math.Pi) > 1e-12 {
		t.Errorf("DegToRad(180) = %v, want %v", DegToRad(180), math.Pi)
	}
	if math.Abs(RadToDeg(math.Pi)-180) > 1e-12 {
		t.Errorf("RadToDeg(pi) = %v, want 180", RadToDeg(math.Pi))
	}
	if math.Abs(RadToDeg(DegToRad(37.5))-37.5) > 1e-12 {
		t.Error("RadToDeg(DegToRad(x)) != x")
	}
}

func TestDirectionFromAngle(t *testing.T) {
	dir := DirectionFromAngle(0)
	if math.Abs(dir.X) > 1e-12 || math.Abs(dir.Y) > 1e-12 || math.Abs(dir.Z-1) > 1e-12 {
		t.Errorf("DirectionFromAngle(0) = %v, want (0,0,1)", dir)
	}
	dir = DirectionFromAngle(90)
	if math.Abs(dir.X) > 1e-12 || math.Abs(dir.Y-1) > 1e-12 || math.Abs(dir.Z) > 1e-12 {
		t.Errorf("DirectionFromAngle(90) = %v, want (0,1,0)", dir)
	}
	mag := dir.X*dir.X + dir.Y*dir.Y + dir.Z*dir.Z
	if math.Abs(mag-1) > 1e-12 {
		t.Errorf("DirectionFromAngle not normalized: |v|^2 = %v", mag)
	}
}

func TestSolveLinear(t *testing.T) {
	a := [][]float64{{2, 1}, {1, 3}}
	b := []float64{3, 5}
	if !SolveLinear(a, b) {
		t.Fatal("SolveLinear returned false for nonsingular system")
	}
	if math.Abs(b[0]-0.8) > 1e-10 || math.Abs(b[1]-1.4) > 1e-10 {
		t.Errorf("SolveLinear = %v, want [0.8 1.4]", b)
	}
}

func TestSolveLinearSingular(t *testing.T) {
	a := [][]float64{{1, 2}, {2, 4}}
	b := []float64{1, 2}
	if SolveLinear(a, b) {
		t.Error("SolveLinear should report false for a singular matrix")
	}
}

func TestProjectOntoWavefront(t *testing.T) {
	// dir along +Z: projection is identity in x/y, plane through c.
	dir := types.Vec3{X: 0, Y: 0, Z: 1}
	c := types.Vec3{X: 1, Y: 2, Z: 5}
	for _, p := range []types.Vec3{
		{X: 0, Y: 0, Z: -100},
		{X: 3, Y: -4, Z: 0},
	} {
		got := ProjectOntoWavefront(p, c, dir)
		// (got - c)·dir == 0
		if math.Abs((got.X-c.X)*dir.X+(got.Y-c.Y)*dir.Y+(got.Z-c.Z)*dir.Z) > 1e-12 {
			t.Errorf("projected point not on wavefront: got %v c %v", got, c)
		}
		// x,y unchanged when dir=(0,0,1)
		if got.X != p.X || got.Y != p.Y {
			t.Errorf("x/y should be unchanged: got %v want %v", got, p)
		}
		// moving origin along dir by (c-p)·dir preserves the ray line: the
		// point p + dir*((c-p)·dir) is on the same line as p.
		delta := c.Subtract(p).Dot(dir)
		want := types.Vec3{X: p.X, Y: p.Y, Z: p.Z + delta}
		if got.Z != want.Z {
			t.Errorf("z = %v, want %v", got.Z, want.Z)
		}
	}

	// 45° in the YZ plane: origin shift equals (c-p)·dir along dir; the
	// projected origin still lies on the ray through p in direction dir.
	rad := math.Pi / 4
	dir = types.Vec3{X: 0, Y: math.Sin(rad), Z: math.Cos(rad)}
	c = types.Vec3{X: 0, Y: 0, Z: 0}
	p := types.Vec3{X: 0, Y: 0, Z: -100}
	got := ProjectOntoWavefront(p, c, dir)
	// On the wavefront: (got - c)·dir == 0.
	if math.Abs(got.Dot(dir)) > 1e-12 {
		t.Errorf("45° projection not on wavefront: %v", got)
	}
	// On the ray line through p along dir: got - p is parallel to dir.
	diff := got.Subtract(p)
	par := diff.Cross(dir)
	if par.Length() > 1e-12 {
		t.Errorf("projected origin not on the ray line: diff %v not parallel to dir", diff)
	}
}

// TestProjectOntoWavefront90Deg verifies the projection stays finite at 90°
// incidence (dir.Z = 0), where a tanθ formulation would diverge.
func TestProjectOntoWavefront90Deg(t *testing.T) {
	dir := types.Vec3{X: 0, Y: 1, Z: 0} // 90° field: horizontal ray
	c := types.Vec3{X: 0, Y: 0, Z: 10}
	p := types.Vec3{X: 2, Y: 3, Z: -100}
	got := ProjectOntoWavefront(p, c, dir)
	if math.IsNaN(got.X) || math.IsNaN(got.Y) || math.IsNaN(got.Z) || math.IsInf(got.Z, 0) {
		t.Fatalf("90° projection produced non-finite result: %v", got)
	}
	// On the wavefront plane through c.
	if math.Abs(got.Subtract(c).Dot(dir)) > 1e-12 {
		t.Errorf("90° projection not on wavefront: %v", got)
	}
	// Shift is along dir: the difference is parallel to dir.
	diff := got.Subtract(p)
	if math.Abs(diff.X) > 1e-12 || math.Abs(diff.Z) > 1e-12 {
		t.Errorf("90° projection should only shift along Y: diff %v", diff)
	}
}

func TestWavefrontGridCenter(t *testing.T) {
	zStart := -100.0
	// On-axis: dir = +Z, centre = (0,0,zStart).
	c := types.Vec3{X: 0, Y: 0, Z: 50}
	dir := types.Vec3{X: 0, Y: 0, Z: 1}
	got := WavefrontGridCenter(c, dir, zStart)
	if math.Abs(got.X) > 1e-12 || math.Abs(got.Y) > 1e-12 || math.Abs(got.Z-zStart) > 1e-12 {
		t.Errorf("on-axis centre = %v, want (0,0,%v)", got, zStart)
	}
	// 30° in YZ: centre offset = -(50-(-100))·tan(30°) = -86.6 in Y.
	rad := math.Pi / 6
	dir = types.Vec3{X: 0, Y: math.Sin(rad), Z: math.Cos(rad)}
	got = WavefrontGridCenter(c, dir, zStart)
	wantY := -(c.Z - zStart) * math.Sin(rad) / math.Cos(rad)
	if math.Abs(got.Y-wantY) > 1e-9 || math.Abs(got.X) > 1e-12 || math.Abs(got.Z-zStart) > 1e-12 {
		t.Errorf("30° centre = %v, want (0,%v,%v)", got, wantY, zStart)
	}
	// Ray from centre in direction dir crosses c: centre + t·dir == c at
	// t = (c.Z - zStart)/dir.Z.
	tc := (c.Z - zStart) / dir.Z
	rayAt := got.Add(dir.Scale(tc))
	if rayAt.Subtract(c).Length() > 1e-9 {
		t.Errorf("centre ray does not cross c: %v vs %v", rayAt, c)
	}
	// 90°: dir.Z = 0 → fall back to c.
	dir90 := types.Vec3{X: 0, Y: 1, Z: 0}
	if g := WavefrontGridCenter(c, dir90, zStart); g != c {
		t.Errorf("90° centre = %v, want c %v", g, c)
	}
	// 90° stays finite.
	if g := WavefrontGridCenter(c, dir90, zStart); math.IsNaN(g.X) || math.IsInf(g.Z, 0) {
		t.Errorf("90° centre non-finite: %v", g)
	}
}
