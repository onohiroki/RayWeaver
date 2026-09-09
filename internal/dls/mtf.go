package dls

import (
	"math"
)

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