package dls

import (
	"math"

	"github.com/hiroki/rayweaver/internal/mesh"
)

// Wavefront-shift merit kinds.
// These minimize the squared wavefront difference between pupil-shifted ray pairs.
// The shift Δ = ν·λ·2 depends on frequency; lower merit = higher MTF.
const (
	MeritWavefrontShiftSag = "wavefront_shift_sag"
	MeritWavefrontShiftTan = "wavefront_shift_tan"
)

// Wavefront-pair phase merit kind.
// This minimizes squared phase differences at 9 fixed pupil reference points.
// Lower merit = better wavefront quality across S/T/D directions.
const MeritWavefrontPairPhase = "wavefront_pair_phase"

// ComputeGeometricMTF computes the geometric MTF at a given spatial frequency
// from a set of image-plane ray intercepts (IPoint).
//
// The MTF is computed via direct complex summation (no FFT, no PSF binning):
//
//	MTF_u(ν) = |Σ w_i exp(-j 2π ν (u·r_i))| / Σ w_i
//
// where r_i = (X - X̄, Y - Ȳ) are coordinates relative to the flux-weighted
// centroid, and w_i = Area_i × Intensity_i are the pupil-cell area weights
// times the transmitted intensity.
//
// Sagittal direction: u = (1, 0)  → depends on X spread
// Tangential direction: u = (0, 1) → depends on Y spread
//
// Returns (sagittal, tangential) MTF values in [0, 1].
// Returns (0, 0) if no valid points or sum of weights is zero.
func ComputeGeometricMTF(points []IPoint, freqLPmm float64) (sagittal, tangential float64) {
	if freqLPmm <= 0 {
		return 0, 0
	}

	cx, cy, _ := Centroid(points)
	if cx == 0 && cy == 0 {
		// Centroid returns (0,0) when count == 0; check if any valid points exist
		hasValid := false
		for _, p := range points {
			if p.OK {
				hasValid = true
				break
			}
		}
		if !hasValid {
			return 0, 0
		}
	}

	var sumW, csX, snX, csY, snY float64
	twoPiFreq := 2.0 * math.Pi * freqLPmm

	for _, p := range points {
		if !p.OK {
			continue
		}
		w := p.Area * p.Intensity
		if w <= 0 {
			continue
		}
		sumW += w

		dx := p.X - cx
		dy := p.Y - cy

		ax := twoPiFreq * dx
		ay := twoPiFreq * dy

		csX += w * math.Cos(ax)
		snX += w * math.Sin(ax)
		csY += w * math.Cos(ay)
		snY += w * math.Sin(ay)
	}

	if sumW == 0 {
		return 0, 0
	}

	invSumW := 1.0 / sumW
	sagittal = math.Hypot(csX, snX) * invSumW
	tangential = math.Hypot(csY, snY) * invSumW

	// Clamp to [0, 1] for numerical safety
	if sagittal > 1 {
		sagittal = 1
	}
	if tangential > 1 {
		tangential = 1
	}

	return sagittal, tangential
}

// minShiftPairs is the smallest number of pupil pairs that makes the
// wavefront-shift estimate meaningful. Below it the estimate is dominated by
// rounding and by the handful of pairs nearest the pupil rim, so the kind
// reports no value rather than a number the solver would chase.
const minShiftPairs = 4

// ComputeWavefrontShift returns a merit proportional to the loss of optical
// transfer at a specified spatial frequency, read straight off the pupil
// wavefront. The incoherent OTF is the autocorrelation of the pupil function, so
// the path difference between two pupil samples one shear apart carries the whole
// frequency content; for a small aberration,
//
//	|OTF(ν)| ≈ 1 − ½·Var(2π·ΔW/λ)
//
// and the weighted variance of ΔW is the statistic to drive down:
//
//	MF = Σ w_i (ΔW_i − ⟨ΔW⟩)² / Σ w_i,   w_i = Area_i × Intensity_i
//	ΔW_i = W(PupilX_i, PupilY_i) − W(PupilX_i − s, PupilY_i)   [sag]
//	     = W(PupilX_i, PupilY_i) − W(PupilX_i, PupilY_i − s)   [tan]
//	s = ν · λ · shearScale
//
// Direction "sag" shifts along the image-plane X axis, "tan" along Y. Lower is
// better, as for every other wavefront term.
//
// The reference sphere. W is the OPL with a quadratic removed first — a constant,
// a tilt and a radial quadratic, the same set wavefront_sphere_rms removes. That
// is not cosmetic: the focusing of a converging bundle is itself a quadratic in
// the pupil, its sheared difference is therefore *linear* in the pupil, and a
// statistic that did not remove it would measure where the focus is rather than
// how well the wavefront is formed — an ideal lens would score a large residual
// and the solver would walk the image plane instead of fixing the wavefront. It
// also removes the tilt (a shifted PSF has the same MTF magnitude, so the tilt
// must not be charged) and the piston (which the difference cancels anyway).
//
// What that leaves is the same aberration set wavefront_sphere_rms keeps:
// astigmatism, spherical aberration and coma, all frequency-weighted by s. Field
// curvature is the exception: a defocus term is exactly one of the removed
// quadratics, and that is not a limitation of the estimator but of what an OTF at
// a best-focus plane can see — the delivered-plane gate is measured at the plane
// the back-focus solve picks, so the residual defocus there is small by
// construction, and the per-field defocus that curvature is made of is carried by
// the wavefront_defocus terms (one per field, also cheap).
//
// The shear scale. The displacement a spatial frequency corresponds to is
// s = ν·λ·z on the pupil plane, z the pupil-to-image path scale; in
// entrance-pupil coordinates that is ν·λ·R_ent/NA, and for an infinite conjugate
// the working F-number collapses it to ν·λ·EFL. shearScale is that length in mm,
// so `frequency` is a genuine spatial frequency in lp/mm. It used to be the
// dimensionless constant 2, subtracted from the millimetre-scale PupilX: on a
// stop-free f/3.9 system that under-shot the requested displacement by ~24x, and
// the pairing was an exact 0.001 mm key match, which no traced grid satisfies —
// so on a polar grid the kind found no partner at all and reported 0 for every
// state.
//
// The displaced point is interpolated on a Delaunay triangulation of the pupil
// samples. A pair whose displaced point has left the sampled pupil has no pupil
// overlap at that displacement and is dropped — a physical statement, not a
// numerical one. At the cutoff every pair drops and the value falls to zero, so
// the kind is meaningful only for ν below the cutoff (see docs/contrast.md).
//
// Returns 0 when there is no usable sample, no positive weight, or fewer than
// minShiftPairs formed pairs.
func ComputeWavefrontShift(points []IPoint, freqLPmm, wavelength, shearScale float64, direction string) float64 {
	if freqLPmm <= 0 || wavelength <= 0 || shearScale <= 0 {
		return 0
	}
	shift := freqLPmm * wavelength * shearScale
	if shift <= 0 {
		return 0
	}

	var shiftX, shiftY float64
	switch direction {
	case "tan":
		shiftY = shift
	default: // "sag" or any other direction
		shiftX = shift
	}

	sites := make([]mesh.Point, 0, len(points))
	resid := make([]float64, 0, len(points))
	weights := make([]float64, 0, len(points))
	for _, p := range points {
		if !p.OK {
			continue
		}
		sites = append(sites, mesh.Point{X: p.PupilX, Y: p.PupilY})
		resid = append(resid, p.OPL)
		weights = append(weights, p.Area*p.Intensity)
	}
	if len(sites) < minShiftPairs {
		return 0
	}
	removeReferenceSphere(sites, resid, weights)
	interp := newPupilInterpolation(sites, resid)

	var sumW, sumWD, sumWD2 float64
	pairs := 0
	for k := range sites {
		w := weights[k]
		if w <= 0 {
			continue
		}
		other, ok := interp.at(sites[k].X-shiftX, sites[k].Y-shiftY)
		if !ok {
			continue
		}
		d := resid[k] - other
		sumW += w
		sumWD += w * d
		sumWD2 += w * d * d
		pairs++
	}
	if pairs < minShiftPairs || sumW <= 0 {
		return 0
	}
	inv := 1 / sumW
	mean := sumWD * inv
	variance := sumWD2*inv - mean*mean
	if variance < 0 {
		// Only reachable through rounding: a variance is non-negative.
		return 0
	}
	return variance
}

// removeReferenceSphere subtracts from resid, in place, the weighted least-squares
// fit of a constant, a tilt and a radial quadratic over the pupil — the reference
// sphere that carries the converging bundle and the image displacement, i.e. the
// same three terms wavefront_sphere_rms removes. Astigmatism and everything above
// it survive, which is what the frequency weighting is then applied to.
func removeReferenceSphere(sites []mesh.Point, resid, weights []float64) {
	// Normal equations of Σ w (r − c0 − c1·x − c2·y − c3·(x²+y²))².
	var m [4][5]float64
	for k, s := range sites {
		w := weights[k]
		if w <= 0 {
			continue
		}
		b := [4]float64{1, s.X, s.Y, s.X*s.X + s.Y*s.Y}
		for a := 0; a < 4; a++ {
			m[a][4] += w * b[a] * resid[k]
			for c := 0; c < 4; c++ {
				m[a][c] += w * b[a] * b[c]
			}
		}
	}
	coef, ok := solve4(m)
	if !ok {
		return
	}
	for k, s := range sites {
		resid[k] -= coef[0] + coef[1]*s.X + coef[2]*s.Y + coef[3]*(s.X*s.X+s.Y*s.Y)
	}
}

// solve4 solves a 4x5 augmented system by Gaussian elimination with partial
// pivoting. ok is false when the normal matrix is singular, in which case the
// caller keeps the un-referenced OPL rather than a wrong reference.
func solve4(m [4][5]float64) ([4]float64, bool) {
	var out [4]float64
	for col := 0; col < 4; col++ {
		piv, best := col, math.Abs(m[col][col])
		for r := col + 1; r < 4; r++ {
			if v := math.Abs(m[r][col]); v > best {
				piv, best = r, v
			}
		}
		if best == 0 || best < 1e-300 {
			return out, false
		}
		m[col], m[piv] = m[piv], m[col]
		for r := 0; r < 4; r++ {
			if r == col {
				continue
			}
			f := m[r][col] / m[col][col]
			for c := col; c < 5; c++ {
				m[r][c] -= f * m[col][c]
			}
		}
	}
	for i := 0; i < 4; i++ {
		out[i] = m[i][4] / m[i][i]
	}
	return out, true
}

// pupilInterpolation locates a point inside a Delaunay triangulation of the
// pupil samples and interpolates one scalar per sample (the OPL) barycentrically.
// The vertex-to-triangle index makes the location O(1) per query, which is what
// keeps the shift kind affordable: it queries one point per sample, per field, on
// every merit evaluation — i.e. once per Jacobian column of every DLS iteration.
type pupilInterpolation struct {
	sites  []mesh.Point
	values []float64
	tris   []mesh.Triangle
	byVert [][]int32
}

func newPupilInterpolation(sites []mesh.Point, values []float64) *pupilInterpolation {
	tris := mesh.Triangulate(sites)
	byVert := make([][]int32, len(sites))
	for ti, t := range tris {
		byVert[t.A] = append(byVert[t.A], int32(ti))
		byVert[t.B] = append(byVert[t.B], int32(ti))
		byVert[t.C] = append(byVert[t.C], int32(ti))
	}
	return &pupilInterpolation{sites: sites, values: values, tris: tris, byVert: byVert}
}

// at returns the interpolated scalar at (x, y). The search is complete rather
// than heuristic: for a Delaunay triangulation the site nearest to a point is
// always a vertex of the triangle containing it, so scanning that vertex's
// incident triangles finds the containing triangle whenever the point is inside
// the sampled region, and reports a miss when it is outside.
func (p *pupilInterpolation) at(x, y float64) (float64, bool) {
	if len(p.tris) == 0 {
		return 0, false
	}
	near := p.nearest(x, y)
	if near < 0 {
		return 0, false
	}
	for _, ti := range p.byVert[near] {
		t := p.tris[ti]
		a, b, c := p.sites[t.A], p.sites[t.B], p.sites[t.C]
		d := (b.Y-c.Y)*(a.X-c.X) + (c.X-b.X)*(a.Y-c.Y)
		if d == 0 {
			continue
		}
		l1 := ((b.Y-c.Y)*(x-c.X) + (c.X-b.X)*(y-c.Y)) / d
		l2 := ((c.Y-a.Y)*(x-c.X) + (a.X-c.X)*(y-c.Y)) / d
		l3 := 1 - l1 - l2
		const eps = -1e-9
		if l1 < eps || l2 < eps || l3 < eps {
			continue
		}
		return l1*p.values[t.A] + l2*p.values[t.B] + l3*p.values[t.C], true
	}
	return 0, false
}

// nearest returns the index of the site closest to (x, y).
func (p *pupilInterpolation) nearest(x, y float64) int {
	best, bestD := -1, math.Inf(1)
	for i, s := range p.sites {
		dx, dy := s.X-x, s.Y-y
		if d := dx*dx + dy*dy; d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// pupilPairRef represents one of the 9 reference points on the unit pupil.
type pupilPairRef struct {
	px, py float64
}

var pupilPairRefs = []pupilPairRef{
	{0, 0},          // center
	{-1, 0}, {1, 0}, // horizontal axis
	{0, -1}, {0, 1}, // vertical axis
	{-0.7071067811865475, -0.7071067811865475}, // 45° diagonal
	{-0.7071067811865475, 0.7071067811865475},
	{0.7071067811865475, -0.7071067811865475},
	{0.7071067811865475, 0.7071067811865475},
}

// pupilPairDef defines a symmetric pair of reference indices and its direction.
type pupilPairDef struct {
	i, j   int
	weight float64
}

var pupilPairDefs = []pupilPairDef{
	// S direction: horizontal symmetric pair
	{1, 2, 1.0},
	// T direction: vertical symmetric pair
	{3, 4, 1.0},
	// D direction: two diagonal pairs
	{5, 8, 0.5},
	{6, 7, 0.5},
}

// ComputeWavefrontPairPhase computes the wavefront-pair phase difference merit.
//
// This evaluates the wavefront structure at 9 fixed pupil reference points
// (center + 4 axial + 4 diagonal on the unit circle) and computes the
// weighted sum of squared phase differences between symmetric pairs.
//
//	MF = Σ (OPL_i - OPL_j)² / λ²
//
// The pairs are:
//   - S direction: (-r, 0) ↔ (r, 0)
//   - T direction: (0, -r) ↔ (0, r)
//   - D direction: two diagonal pairs at ±r/√2
//
// Returns the merit value (lower = better wavefront). Returns 0 if no valid pairs found.
func ComputeWavefrontPairPhase(points []IPoint, apertureRadius float64, wavelength float64) float64 {
	if apertureRadius <= 0 || wavelength <= 0 {
		return 0
	}

	// Build spatial index for nearest-neighbor lookup
	type entry struct {
		px, py float64
		idx    int
	}
	var entries []entry
	for i, p := range points {
		if !p.OK {
			continue
		}
		entries = append(entries, entry{p.PupilX, p.PupilY, i})
	}
	if len(entries) == 0 {
		return 0
	}

	const gridScale = 1000.0
	type cellKey struct{ gx, gy int }
	cellMap := make(map[cellKey]int, len(entries))
	for i, e := range entries {
		cellMap[cellKey{int(math.Round(e.px * gridScale)), int(math.Round(e.py * gridScale))}] = i
	}

	// Find nearest valid index for a reference point
	findNearest := func(rx, ry float64) (int, bool) {
		gx := int(math.Round(rx * gridScale))
		gy := int(math.Round(ry * gridScale))
		if jdx, ok := cellMap[cellKey{gx, gy}]; ok && points[jdx].OK {
			return jdx, true
		}
		return 0, false
	}

	invLambda := 1.0 / wavelength
	var merit float64

	for _, pair := range pupilPairDefs {
		r1 := pupilPairRefs[pair.i]
		r2 := pupilPairRefs[pair.j]
		phys1x := r1.px * apertureRadius
		phys1y := r1.py * apertureRadius
		phys2x := r2.px * apertureRadius
		phys2y := r2.py * apertureRadius

		idx1, ok1 := findNearest(phys1x, phys1y)
		idx2, ok2 := findNearest(phys2x, phys2y)
		if !ok1 || !ok2 {
			continue
		}

		dOPL := points[idx1].OPL - points[idx2].OPL
		dPhi := dOPL * invLambda
		merit += pair.weight * dPhi * dPhi
	}

	return merit
}
