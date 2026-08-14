package dls

import (
	"math"
	"sort"
)

// pointWeight returns the physical weight of a grid ray for the weighted spot
// statistics: the pupil-cell area (the entrance-pupil flux the ray represents)
// times the transmitted mean intensity (Fresnel/TMM reflection losses). When
// either is unset it degrades gracefully — area alone, then equal weight — so
// grids traced without the new fields behave exactly like the legacy RMS.
func pointWeight(p IPoint) float64 {
	if p.Area > 0 && p.Intensity > 0 {
		return p.Area * p.Intensity
	}
	if p.Area > 0 {
		return p.Area
	}
	return 1
}

// weightedCentroid returns the flux-weighted centroid of the accepted rays and
// the total weight. A grid without area/intensity data falls back to the
// unweighted centroid (weight 1 per ray), matching Centroid.
func weightedCentroid(points []IPoint) (cx, cy, total float64) {
	for _, p := range points {
		if !p.OK {
			continue
		}
		w := pointWeight(p)
		cx += p.X * w
		cy += p.Y * w
		total += w
	}
	if total == 0 {
		return 0, 0, 0
	}
	return cx / total, cy / total, total
}

// ComputeSpotWeightedRMS returns the flux-weighted spot RMS radius about the
// flux-weighted centroid. Weighting by pupil-cell area and transmitted
// intensity makes the metric reflect the energy that actually reaches the
// image: vignetted off-axis pupils (asymmetric, intensity-reduced) are measured
// correctly instead of being weighted like a full, unvignetted pupil. This is
// the DLS counterpart of the chief path's intensity-weighted centroid, extended
// to the RMS itself.
func ComputeSpotWeightedRMS(points []IPoint) float64 {
	cx, cy, total := weightedCentroid(points)
	if total == 0 {
		return 1e6
	}

	var sum float64
	for _, p := range points {
		if !p.OK {
			continue
		}
		w := pointWeight(p)
		dx := p.X - cx
		dy := p.Y - cy
		sum += w * (dx*dx + dy*dy)
	}
	return math.Sqrt(sum / total)
}

// ComputeSpotAxisRMS decomposes each ray's deviation from the flux-weighted
// centroid into the tangential direction (tx, ty — the field azimuth in the
// image plane, unit vector) and the perpendicular sagittal direction, and
// returns the flux-weighted RMS of each component. A comatic off-axis spot
// flares tangentially, so RMS_T exceeds RMS_S; astigmatism separates the two
// even at the circle of least confusion. The two values (or their maximum) let
// the merit function attack the dominant axis directly.
func ComputeSpotAxisRMS(points []IPoint, tx, ty float64) (rmsT, rmsS float64) {
	cx, cy, total := weightedCentroid(points)
	if total == 0 {
		return 1e6, 1e6
	}

	norm := math.Hypot(tx, ty)
	if norm == 0 {
		tx, ty = 0, 1
	} else {
		tx /= norm
		ty /= norm
	}
	sx, sy := -ty, tx

	var sumT, sumS float64
	for _, p := range points {
		if !p.OK {
			continue
		}
		w := pointWeight(p)
		dx := p.X - cx
		dy := p.Y - cy
		t := dx*tx + dy*ty
		s := dx*sx + dy*sy
		sumT += w * t * t
		sumS += w * s * s
	}
	return math.Sqrt(sumT / total), math.Sqrt(sumS / total)
}

// ComputeSpotEERadius returns the radius about the flux-weighted centroid that
// encloses the given fraction of the total ray weight (the encircled-energy
// radius, e.g. EE50/EE80). Unlike RMS, it is insensitive to a sparse comatic
// tail, so it measures how much energy is concentrated near the core — the
// metric that correlates with MTF for off-axis fields. fraction is clamped to
// (0, 1]; a non-positive fraction defaults to 0.8 (EE80).
func ComputeSpotEERadius(points []IPoint, fraction float64) float64 {
	if fraction <= 0 {
		fraction = 0.8
	}
	if fraction > 1 {
		fraction = 1
	}

	cx, cy, total := weightedCentroid(points)
	if total == 0 {
		return 1e6
	}

	type distWeight struct {
		d float64
		w float64
	}
	rays := make([]distWeight, 0, len(points))
	for _, p := range points {
		if !p.OK {
			continue
		}
		w := pointWeight(p)
		if w <= 0 {
			continue
		}
		dx := p.X - cx
		dy := p.Y - cy
		rays = append(rays, distWeight{d: math.Hypot(dx, dy), w: w})
	}
	if len(rays) == 0 {
		return 1e6
	}

	sort.Slice(rays, func(i, j int) bool { return rays[i].d < rays[j].d })

	target := fraction * total
	var acc float64
	for _, r := range rays {
		acc += r.w
		if acc >= target {
			return r.d
		}
	}
	return rays[len(rays)-1].d
}
