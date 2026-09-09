package dls

import (
	"math"
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
//   MTF_u(ν) = |Σ w_i exp(-j 2π ν (u·r_i))| / Σ w_i
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

// ComputeWavefrontShift computes the wavefront-shift merit value.
//
// It maximizes MTF at a specific frequency by minimizing the wavefront
// difference between pupil-shifted ray pairs. The merit is:
//
//   MF = Σ w_i (OPL_i - OPL_j)²
//
// where j is the nearest grid point to (PupilX_i - Δu_x, PupilY_i - Δu_y),
// Δu = ν·λ·2 is the normalized pupil shift, and w_i = Area_i × Intensity_i.
//
// Direction controls the shift axis:
//   - "sag": shift along X axis → (Δu, 0)
//   - "tan": shift along Y axis → (0, Δu)
//
// Returns the merit value (lower = better MTF). Returns 0 if no valid pairs found.
func ComputeWavefrontShift(points []IPoint, freqLPmm, wavelength, apertureRadius float64, direction string) float64 {
	if freqLPmm <= 0 || wavelength <= 0 || apertureRadius <= 0 {
		return 0
	}

	// Normalized pupil shift: Δu = (ν·λ·D) / R = ν·λ·2
	shiftNorm := freqLPmm * wavelength * 2.0

	var shiftX, shiftY float64
	switch direction {
	case "tan":
		shiftY = shiftNorm
	default: // "sag" or any other direction
		shiftX = shiftNorm
	}

	// Build spatial index: discretized (PupilX, PupilY) → index
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

	var merit float64
	for _, e := range entries {
		p := points[e.idx]
		w := p.Area * p.Intensity
		if w <= 0 {
			continue
		}

		// Shifted position in pupil coordinates
		sx := p.PupilX - shiftX
		sy := p.PupilY - shiftY

		// Nearest-neighbor lookup
		gx := int(math.Round(sx * gridScale))
		gy := int(math.Round(sy * gridScale))
		jdx, ok := cellMap[cellKey{gx, gy}]
		if !ok {
			continue
		}
		q := points[jdx]
		if !q.OK {
			continue
		}

		dOPL := p.OPL - q.OPL
		merit += w * dOPL * dOPL
	}

	return merit
}

// pupilPairRef represents one of the 9 reference points on the unit pupil.
type pupilPairRef struct {
	px, py float64
}

var pupilPairRefs = []pupilPairRef{
	{0, 0},                     // center
	{-1, 0}, {1, 0},           // horizontal axis
	{0, -1}, {0, 1},           // vertical axis
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
//   MF = Σ (OPL_i - OPL_j)² / λ²
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