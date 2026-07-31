package paraxial

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/types"
)

func TestComputeSeidelAxis(t *testing.T) {
	sys, gc := singletSystem()
	res := ComputeSeidel(sys.Surfaces, 0, 0.00058756, gc)

	if res.Coma != 0 || res.Astigmatism != 0 || res.Distortion != 0 {
		t.Errorf("off-axis coefficients must vanish on axis: %+v", res)
	}
	if res.Spherical <= 0 {
		t.Errorf("spherical should be positive for a converging singlet, got %v", res.Spherical)
	}
}

func TestComputeSeidelDistortionOddInField(t *testing.T) {
	sys, gc := singletSystem()
	pos := ComputeSeidel(sys.Surfaces, 10, 0.00058756, gc)
	neg := ComputeSeidel(sys.Surfaces, -10, 0.00058756, gc)

	if pos.Distortion == 0 {
		t.Error("expected non-zero distortion for an off-axis field")
	}
	if math.Abs(pos.Distortion+neg.Distortion) > 1e-12*math.Abs(pos.Distortion) {
		t.Errorf("S5 must be odd in field: S5(+10)=%v S5(-10)=%v", pos.Distortion, neg.Distortion)
	}
}

// TestComputeSeidelDistortionRegression pins the S5 value for the singlet
// (c=0.01 / N-BK7 / 10mm / c=-0.01) at a 10 degree field.
func TestComputeSeidelDistortionRegression(t *testing.T) {
	sys, gc := singletSystem()
	res := ComputeSeidel(sys.Surfaces, 10, 0.00058756, gc)
	want := -0.0007097250783
	if math.Abs(res.Distortion-want) > 1e-10 {
		t.Errorf("S5 = %v, want %v", res.Distortion, want)
	}
}

// TestSeidelDistortionSignMatchesGeometricDistortion verifies the S5 sign
// agrees with the actual (real-ray) geometric distortion for the same chief
// ray convention: a pupil located at the first surface.
func TestSeidelDistortionSignMatchesGeometricDistortion(t *testing.T) {
	sys, gc := singletSystem()
	const thetaDeg = 10.0
	thetaRad := thetaDeg * math.Pi / 180.0

	seidel := ComputeSeidel(sys.Surfaces, thetaDeg, 0.00058756, gc)

	engine := ray.NewEngine(gc, nil)
	path := []int{0, 1, 2}
	dir := types.Vec3{X: 0, Y: math.Sin(thetaRad), Z: math.Cos(thetaRad)}.Normalize()
	origin := types.Vec3{Z: -0.01}
	r := types.Ray{
		Wavelength: 0.00058756,
		Initial:    types.RayState{Origin: origin, Direction: dir},
		Path:       path,
		Jones:      types.NewCircularJones(true),
	}
	result := engine.TraceRay(r, sys.Surfaces)
	if result.Error != "" {
		t.Fatalf("trace error: %s", result.Error)
	}
	last := result.Surfaces[len(result.Surfaces)-1]
	yReal := last.Position.Y

	nIndex := resolveIndices(sys.Surfaces, 0.00058756, gc)
	chiefVerts := traceChiefForward(sys.Surfaces, nIndex, math.Tan(thetaRad))
	yParax := chiefVerts[len(chiefVerts)-1].Y

	distPct := 100.0 * (yReal - yParax) / yParax
	if math.Signbit(distPct) != math.Signbit(seidel.Distortion) {
		t.Errorf("S5 sign (%v) disagrees with geometric distortion (%.3f%%): yReal=%v yParax=%v",
			seidel.Distortion, distPct, yReal, yParax)
	}
}
