package dls

import (
	"math"
	"sort"
)

// WhiteSpotRMS combines per-wavelength spot grids into one polychromatic
// spot. Each group is normalised by its own total flux and combined with its
// wavelength weight, so the result is the RMS of the merged ray cloud about the
// common flux-weighted centroid:
//
//	RMS² = Σ_i w_i (S_i + T_i·|c_i − c|²) / Σ_i w_i·T_i
//
// The second term is the inter-wavelength (lateral colour) contribution that a
// per-wavelength RMS about its own centroid cannot see. Weights shorter than
// groups default to 1. Returns ok=false when no group carries a usable ray.
func WhiteSpotRMS(groups [][]IPoint, weights []float64) (rms float64, ok bool) {
	type groupStat struct {
		cx, cy, total, ss float64
		weight            float64
	}
	stats := make([]groupStat, 0, len(groups))
	for gi, points := range groups {
		cx, cy, total := weightedCentroid(points)
		if total <= 0 {
			continue
		}
		var ss float64
		for _, p := range points {
			if !p.OK {
				continue
			}
			w := pointWeight(p)
			dx, dy := p.X-cx, p.Y-cy
			ss += w * (dx*dx + dy*dy)
		}
		w := 1.0
		if gi < len(weights) {
			w = weights[gi]
		}
		if w <= 0 {
			continue
		}
		stats = append(stats, groupStat{cx: cx, cy: cy, total: total, ss: ss, weight: w})
	}
	if len(stats) == 0 {
		return 0, false
	}
	var wSum, cxSum, cySum float64
	for _, s := range stats {
		wt := s.weight * s.total
		wSum += wt
		cxSum += wt * s.cx
		cySum += wt * s.cy
	}
	if wSum <= 0 {
		return 0, false
	}
	cx, cy := cxSum/wSum, cySum/wSum
	var sum float64
	for _, s := range stats {
		dx, dy := s.cx-cx, s.cy-cy
		sum += s.weight * (s.ss + s.total*(dx*dx+dy*dy))
	}
	return math.Sqrt(sum / wSum), true
}

// WhiteSpotEERadius is WhiteSpotRMS's encircled-energy counterpart: the radius
// about the common flux-weighted centroid enclosing the requested fraction of
// the wavelength- and flux-weighted ray energy.
func WhiteSpotEERadius(groups [][]IPoint, weights []float64, fraction float64) (float64, bool) {
	if fraction <= 0 || fraction > 1 {
		fraction = 0.8
	}
	type weightedPoint struct {
		x, y, w float64
		index   int
	}
	var pts []weightedPoint
	var cxSum, cySum, wSum float64
	index := 0
	for gi, points := range groups {
		wl := 1.0
		if gi < len(weights) {
			wl = weights[gi]
		}
		if wl <= 0 {
			continue
		}
		for _, p := range points {
			if !p.OK {
				continue
			}
			w := wl * pointWeight(p)
			if w <= 0 {
				continue
			}
			pts = append(pts, weightedPoint{x: p.X, y: p.Y, w: w, index: index})
			cxSum += p.X * w
			cySum += p.Y * w
			wSum += w
			index++
		}
	}
	if len(pts) < 2 || wSum <= 0 {
		return 0, false
	}
	cx, cy := cxSum/wSum, cySum/wSum
	type radius struct {
		r     float64
		w     float64
		index int
	}
	radii := make([]radius, len(pts))
	for i, p := range pts {
		dx, dy := p.x-cx, p.y-cy
		radii[i] = radius{r: math.Hypot(dx, dy), w: p.w, index: p.index}
	}
	// Stable order with an index tiebreak so equal radii cannot reorder the
	// cumulative sum between runs or worker counts.
	sort.Slice(radii, func(i, j int) bool {
		if radii[i].r != radii[j].r {
			return radii[i].r < radii[j].r
		}
		return radii[i].index < radii[j].index
	})
	target := fraction * wSum
	var accum float64
	for _, r := range radii {
		accum += r.w
		if accum >= target {
			return r.r, true
		}
	}
	return radii[len(radii)-1].r, true
}
