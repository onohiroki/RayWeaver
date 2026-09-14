package surface

import (
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

// RadialPhase evaluates the rotationally symmetric phase function:
//
//	φ(r) = Σ coeffs[i] * (r / normR)^(2i+2)   [rad]
//
// If normR ≤ 0 it defaults to 1 (no normalisation).
func RadialPhase(r float64, coeffs []float64, normR float64) float64 {
	if len(coeffs) == 0 {
		return 0
	}
	if normR <= 0 {
		normR = 1
	}
	rho := r / normR
	var phi float64
	rhoPow := rho * rho // ρ²
	for _, c := range coeffs {
		phi += c * rhoPow
		rhoPow *= rho * rho // next even power
	}
	return phi
}

// RadialPhaseGradient evaluates dφ/dr of the rotationally symmetric phase:
//
//	dφ/dr = Σ coeffs[i] * (2i+2) * r^(2i+1) / normR^(2i+2)
//
// Returns 0 at r = 0 (symmetry).
func RadialPhaseGradient(r float64, coeffs []float64, normR float64) float64 {
	if len(coeffs) == 0 || r == 0 {
		return 0
	}
	if normR <= 0 {
		normR = 1
	}
	rho := r / normR
	var dphi float64
	rhoPow := rho // ρ^1
	power := 2.0  // exponent coefficient (2i+2) starts at 2
	for _, c := range coeffs {
		dphi += c * power * rhoPow
		rhoPow *= rho * rho // next odd power of ρ
		power += 2
	}
	return dphi / normR
}

// DiffractionEfficiency computes the scalar diffraction efficiency for
// order m of a kinoform/blazed grating:
//
//	η = sinc²( m · (1 − λ₀/λ) )
//
// At the design wavelength (λ = λ₀) this gives η = 1 for m = 1.
// The formula is independent of the local phase gradient and incidence
// angle, capturing the dominant chromatic rolloff of a kinoform.
func DiffractionEfficiency(lambda, lambda0, thetaInc, dPhiDr float64, order int) float64 {
	if lambda <= 0 || lambda0 <= 0 {
		return 1
	}
	x := float64(order) * (1 - lambda0/lambda)
	if math.Abs(x) < 1e-12 {
		return 1
	}
	sinc := math.Sin(math.Pi*x) / (math.Pi*x)
	return sinc * sinc
}

// PhaseDeflection applies the grating equation to compute the outgoing ray
// direction after diffraction at a kinoform surface.  The phase gradient is
// radial, so only the transverse (x,y) components of the direction change:
//
//	d_out = d_in − (λ / 2π) · (dφ/dr) · t̂
//
// where t̂ = (d_in.x, d_in.y, 0) / r is the radial unit vector in the
// surface plane.
func PhaseDeflection(dIn types.Vec3, r, dPhiDr, lambda float64, order int) types.Vec3 {
	if r == 0 || dPhiDr == 0 || lambda == 0 {
		return dIn
	}
	factor := -lambda * float64(order) * dPhiDr / (2 * math.Pi)
	return types.Vec3{
		X: dIn.X + factor*dIn.X/r,
		Y: dIn.Y + factor*dIn.Y/r,
		Z: dIn.Z,
	}
}

// PhaseOPL returns the optical-path-length contribution of a diffractive
// surface for a given diffraction order:
//
//	OPL = m · φ(r) / (2π)
func PhaseOPL(r float64, coeffs []float64, normR float64, order int) float64 {
	phi := RadialPhase(r, coeffs, normR)
	return float64(order) * phi / (2 * math.Pi)
}

// OPDToPhase converts a CODE V HCO coefficient (OPD in mm at λ₀) to radians:
//
//	rad = OPD_mm × 2π / λ₀_mm
func OPDToPhase(cOPD, lambda0MM float64) float64 {
	if lambda0MM <= 0 {
		return cOPD
	}
	return cOPD * 2 * math.Pi / lambda0MM
}

// PhaseToOPD converts radians to a CODE V HCO coefficient (OPD in mm):
//
//	OPD_mm = rad × λ₀_mm / 2π
func PhaseToOPD(cRad, lambda0MM float64) float64 {
	if lambda0MM <= 0 {
		return cRad
	}
	return cRad * lambda0MM / (2 * math.Pi)
}
