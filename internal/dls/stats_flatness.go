package dls

import "math"

// spotDecompMinPoints is the minimum number of accepted rays the linear spot
// fit needs: three unknowns per image component (two slopes plus the
// intercept) plus a safety margin, so a nearly-empty grid is rejected rather
// than fitted to noise.
const spotDecompMinPoints = 4

// spotDecompCondTol is the smallest admissible eigenvalue ratio of the
// weighted pupil second-moment matrix. Below it the surviving pupil samples
// are effectively collinear (a sliver left by heavy vignetting), the normal
// equations are singular to working precision and the fitted slopes carry no
// information — the caller then reports the degenerate penalty.
const spotDecompCondTol = 1e-6

// SpotDecomp is the first-order (linear) decomposition of the geometric spot
// displacement field measured on one image plane:
//
//	d(rho) = A*rho + t
//
// where rho = (PupilX, PupilY) is the ray's entrance-pupil coordinate in mm
// (relative to the grid centre) and d is its image-plane intersection relative
// to the flux-weighted centroid. The fit is the flux-weighted (pupil-cell area
// x transmitted intensity, see pointWeight) least-squares solution of
//
//	[A11 A12]   [rho_x]   [t_x]
//	[A21 A22] * [rho_y] + [t_y]
//
// split into the physical first-order aberrations:
//
//	Defocus = trace(A)/2                              radial (isotropic) part
//	Astig   = hypot((A11-A22)/2, (A12+A21)/2)         traceless symmetric part
//
// both in mm of image displacement per mm of entrance-pupil coordinate. The
// intercept is estimated and removed (the fit runs on centred data), so a
// chief-ray offset is piston/tilt of the spot and leaks into neither magnitude;
// the antisymmetric part of A is image rotation and is excluded from both, so
// only the two physically meaningful focus terms survive.
//
// The merit layer converts these to an equivalent longitudinal focus shift
// with the focus scale B = R_marginal/NA' (mm): a plane displaced by dz moves
// a ray entering at rho by dz*rho*NA'/R, hence dz = Defocus*B and the two
// astigmatic line foci sit at +/- Astig*B (2*Astig*B apart). Both are then
// plain mm of image-plane displacement, comparable with the field-curvature
// gate and with the wavefront family's image-plane-referenced DefocusMM.
//
// OK reports whether the fit is meaningful. Residual carries the weighted RMS
// displacement the linear model does not explain (higher-order spot content),
// which the merit ignores but the diagnostics report.
type SpotDecomp struct {
	// A11, A12, A21, A22 is the displacement matrix A (mm/mm).
	A11, A12, A21, A22 float64
	// Defocus is trace(A)/2 (mm/mm).
	Defocus float64
	// Astig is the traceless symmetric magnitude (mm/mm).
	Astig float64
	// Rotation is the antisymmetric magnitude (mm/mm), reported for
	// completeness; no merit kind consumes it.
	Rotation float64
	// Residual is the flux-weighted RMS displacement unexplained by the fit (mm).
	Residual float64
	// Count is the number of accepted rays, Total their total weight.
	Count int
	Total float64
	// OK is false when the fit is degenerate (too few rays, no weight, or an
	// effectively one-dimensional pupil); every other field is then zero.
	OK bool
}

// ComputeSpotDecomp fits the linear displacement field of the accepted grid
// rays (see SpotDecomp) and returns its defocus/astigmatism decomposition.
// Rays whose OK flag is unset (vignetted, failed intersection) are skipped.
func ComputeSpotDecomp(points []IPoint) SpotDecomp {
	var n int
	var wsum, mpX, mpY, mX, mY float64
	for _, p := range points {
		if !p.OK {
			continue
		}
		w := pointWeight(p)
		n++
		wsum += w
		mpX += w * p.PupilX
		mpY += w * p.PupilY
		mX += w * p.X
		mY += w * p.Y
	}
	out := SpotDecomp{Count: n, Total: wsum}
	if n < spotDecompMinPoints || wsum <= 0 {
		return out
	}
	mpX /= wsum
	mpY /= wsum
	mX /= wsum
	mY /= wsum

	// Weighted central moments: S = cov(rho) (2x2, pupil sampling) and
	// C = cov(d, rho) (2x2, displacement against pupil coordinate).
	var sXX, sXY, sYY, cXX, cXY, cYX, cYY float64
	for _, p := range points {
		if !p.OK {
			continue
		}
		w := pointWeight(p)
		rx, ry := p.PupilX-mpX, p.PupilY-mpY
		ex, ey := p.X-mX, p.Y-mY
		sXX += w * rx * rx
		sXY += w * rx * ry
		sYY += w * ry * ry
		cXX += w * ex * rx
		cXY += w * ex * ry
		cYX += w * ey * rx
		cYY += w * ey * ry
	}
	sXX /= wsum
	sXY /= wsum
	sYY /= wsum
	cXX /= wsum
	cXY /= wsum
	cYX /= wsum
	cYY /= wsum

	// Conditioning of the pupil sampling: eigenvalues of S.
	mid := (sXX + sYY) / 2
	rad := math.Hypot((sXX-sYY)/2, sXY)
	lMax := mid + rad
	lMin := mid - rad
	if lMax <= 0 || lMin <= 0 || lMin < spotDecompCondTol*lMax {
		return out
	}
	det := sXX*sYY - sXY*sXY
	if det <= 0 {
		return out
	}

	// A = C * S^-1, row by row: [a b] = [cx cy] * adj(S) / det.
	out.A11 = (cXX*sYY - cXY*sXY) / det
	out.A12 = (cXY*sXX - cXX*sXY) / det
	out.A21 = (cYX*sYY - cYY*sXY) / det
	out.A22 = (cYY*sXX - cYX*sXY) / det

	out.Defocus = (out.A11 + out.A22) / 2
	// Symmetric traceless part (astigmatism) and antisymmetric part (image
	// rotation, reported with the sign of a counter-clockwise turn).
	symX := (out.A11 - out.A22) / 2
	symY := (out.A12 + out.A21) / 2
	out.Astig = math.Hypot(symX, symY)
	out.Rotation = (out.A21 - out.A12) / 2

	var res float64
	for _, p := range points {
		if !p.OK {
			continue
		}
		w := pointWeight(p)
		rx, ry := p.PupilX-mpX, p.PupilY-mpY
		ex, ey := p.X-mX, p.Y-mY
		fx := ex - (out.A11*rx + out.A12*ry)
		fy := ey - (out.A21*rx + out.A22*ry)
		res += w * (fx*fx + fy*fy)
	}
	out.Residual = math.Sqrt(res / wsum)
	out.OK = true
	return out
}
