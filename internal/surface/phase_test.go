package surface

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestRadialPhase(t *testing.T) {
	// φ(r) = c₂·r² with c₂ = 1e4, normR = 1
	coeffs := []float64{1e4}
	r := 5.0
	phi := RadialPhase(r, coeffs, 1)
	want := 1e4 * 25.0 // 250000
	if math.Abs(phi-want) > 1e-6 {
		t.Errorf("RadialPhase(5) = %g, want %g", phi, want)
	}
}

func TestRadialPhaseGradient(t *testing.T) {
	// φ(r) = c₂·r²  →  dφ/dr = 2·c₂·r
	coeffs := []float64{1e4}
	r := 5.0
	dphi := RadialPhaseGradient(r, coeffs, 1)
	want := 2 * 1e4 * 5.0 // 100000
	if math.Abs(dphi-want) > 1e-6 {
		t.Errorf("RadialPhaseGradient(5) = %g, want %g", dphi, want)
	}
}

func TestRadialPhaseGradientAtZero(t *testing.T) {
	coeffs := []float64{1e4, -1e2}
	dphi := RadialPhaseGradient(0, coeffs, 1)
	if dphi != 0 {
		t.Errorf("RadialPhaseGradient(0) = %g, want 0", dphi)
	}
}

func TestRadialPhaseGradientNumerical(t *testing.T) {
	// Verify gradient against numerical differentiation.
	coeffs := []float64{1e4, -1e2, 5e-3}
	normR := 10.0
	r := 7.5
	dphi := RadialPhaseGradient(r, coeffs, normR)
	dr := 1e-7
	dphiNum := (RadialPhase(r+dr, coeffs, normR) - RadialPhase(r-dr, coeffs, normR)) / (2 * dr)
	if math.Abs(dphi-dphiNum) > 1e-4 {
		t.Errorf("RadialPhaseGradient(7.5) = %g, numerical = %g", dphi, dphiNum)
	}
}

func TestDiffractionEfficiencyAtDesignWavelength(t *testing.T) {
	// At λ = λ₀ and normal incidence, η should be 1 for order 1.
	lambda := 0.58756
	lambda0 := 0.58756
	dPhiDr := 1000.0
	eta := DiffractionEfficiency(lambda, lambda0, 0, dPhiDr, 1)
	if math.Abs(eta-1.0) > 1e-10 {
		t.Errorf("efficiency at design λ = %g, want 1.0", eta)
	}
}

func TestDiffractionEfficiencyOffDesign(t *testing.T) {
	// At a different wavelength, efficiency should be < 1.
	lambda := 0.650 // red
	lambda0 := 0.58756
	dPhiDr := 1000.0
	eta := DiffractionEfficiency(lambda, lambda0, 0, dPhiDr, 1)
	if eta >= 1 || eta <= 0 {
		t.Errorf("efficiency off-design = %g, want (0,1)", eta)
	}
}

func TestPhaseDeflectionConverging(t *testing.T) {
	// For a converging kinoform (dφ/dr > 0), the deflected ray should
	// point inward (toward the optical axis).
	dIn := types.Vec3{X: 0.1, Y: 0, Z: 1}
	r := 10.0
	dPhiDr := 1000.0 // positive → converging
	lambda := 0.00058756
	dOut := PhaseDeflection(dIn, r, dPhiDr, lambda, 1)
	if dOut.X >= dIn.X {
		t.Errorf("converging deflection should reduce X: got %g, in %g", dOut.X, dIn.X)
	}
}

func TestPhaseOPL(t *testing.T) {
	coeffs := []float64{1e4}
	r := 5.0
	normR := 1.0
	order := 1
	opl := PhaseOPL(r, coeffs, normR, order)
	phi := RadialPhase(r, coeffs, normR)
	want := phi / (2 * math.Pi)
	if math.Abs(opl-want) > 1e-12 {
		t.Errorf("PhaseOPL = %g, want %g", opl, want)
	}
}

func TestOPDPhaseRoundtrip(t *testing.T) {
	lambda0 := 0.00058756 // mm
	cOPD := -0.0001       // mm
	rad := OPDToPhase(cOPD, lambda0)
	back := PhaseToOPD(rad, lambda0)
	if math.Abs(back-cOPD) > 1e-15 {
		t.Errorf("OPD→phase→OPD roundtrip: %g → %g → %g", cOPD, rad, back)
	}
}
