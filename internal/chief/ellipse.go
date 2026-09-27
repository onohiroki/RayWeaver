package chief

import (
	"math"
	"sort"

	"github.com/hiroki/rayweaver/internal/types"
)

// Effective-vignetting estimate (chief's heavily clipped path).
//
// When too few rays of a field's full-aperture pupil grid reach the reference
// surface the beam has been cut down to an effective pupil that is neither
// centred on the nominal one nor circular. Instead of shrinking the probe
// radius concentrically, chief fits the min-area ellipse that still contains
// every surviving ray, reports it as a VignettingDef (`chief_rays[].effective_
// vignetting` — the same convention as fields[].vignetting, so it round-trips
// through the clip the grid sampler already understands) and re-lays the spot
// statistics / centroid grid over it, so those cover the rays that actually
// pass instead of a concentric disc that is half dead area.
//
// The fit is the smallest ellipse, about the surviving bundle's centroid, that
// contains the measured survivors: the orientation is scanned over the convex
// hull's edge directions plus the principal axis and a uniform sweep, and for
// each candidate the exact smallest axis ratio is found by a ternary search on
// the convex objective a²·t = max_i(t·u_i² + v_i²/t) (t = b/a in the rotated
// frame). Containment holds for every candidate by construction — the axes are
// the extremes of the projections — and the result is inflated by
// effectiveVignettingSafety so every fitted point still passes
// VignettingDef.Contains after the round trip through the compressed-axis +
// tangent convention.

const (
	// minEffectiveVignettingRays is the smallest survivor set the ellipse is
	// fitted from; below it the estimate describes the sampling rather than
	// the beam and the circular radius probe takes over.
	minEffectiveVignettingRays = 8
	// effectiveVignettingSafety inflates the fitted semi-axes by 0.1%: the
	// slack that keeps every measured survivor inside the reported ellipse
	// after the round trip (fit rounding, the tangent/atan axis angle, and
	// Contains itself).
	effectiveVignettingSafety = 1.001
	// ellipseAspectLo/Hi bound the fitted axis ratio b/a, so a nearly
	// collinear survivor set still yields a thin but usable ellipse instead of
	// an unbounded one.
	ellipseAspectLo = 1e-3
	ellipseAspectHi = 1e3
	// ellipseOrientationSteps / ellipseMaxEdgeCandidates bound the orientation
	// scan: the hull contributes at most ellipseMaxEdgeCandidates edge
	// directions, the rest is a uniform sweep; ellipseTernaryIterations bounds
	// the per-orientation axis-ratio search (the objective is convex in log t,
	// so 32 steps over a 13.8-wide log range are far more than enough).
	ellipseOrientationSteps  = 24
	ellipseMaxEdgeCandidates = 64
	ellipseTernaryIterations = 32
)

type xy struct{ x, y float64 }

// fitEffectiveVignetting estimates the effective vignetted pupil of a traced
// pupil grid: the min-area ellipse over the surviving rays' pupil coordinates
// (relative to the grid centre, the frame fields[].vignetting is applied in),
// returned as a VignettingDef against apertureRadius. It returns nil when the
// estimate is not trustworthy — fewer than minEffectiveVignettingRays
// survivors, a degenerate (zero-area) fit, or an aperture radius that is not
// positive.
func fitEffectiveVignetting(grid []types.GridPoint, centreX, centreY, apertureRadius float64) *types.VignettingDef {
	if apertureRadius <= 0 {
		return nil
	}
	pts := make([]xy, 0, len(grid))
	var sum xy
	for i := range grid {
		if grid[i].ImageX == nil || grid[i].ImageY == nil {
			continue
		}
		p := xy{x: grid[i].PupilX - centreX, y: grid[i].PupilY - centreY}
		pts = append(pts, p)
		sum.x += p.x
		sum.y += p.y
	}
	if len(pts) < minEffectiveVignettingRays {
		return nil
	}
	c := xy{x: sum.x / float64(len(pts)), y: sum.y / float64(len(pts))}

	ux := make([]float64, len(pts))
	vy := make([]float64, len(pts))
	bestArea := math.Inf(1)
	bestPhi, bestT := 0.0, 1.0
	for _, phi := range ellipseOrientations(pts, c) {
		ct, st := math.Cos(phi), math.Sin(phi)
		for i := range pts {
			dx, dy := pts[i].x-c.x, pts[i].y-c.y
			ux[i] = dx*ct + dy*st
			vy[i] = -dx*st + dy*ct
		}
		t, area := minAspectAxes(ux, vy)
		if area < bestArea {
			bestArea, bestPhi, bestT = area, phi, t
		}
	}
	if !(bestArea > 0) || math.IsInf(bestArea, 1) {
		return nil
	}

	// Final axes about the winning orientation and aspect ratio, taken over
	// every survivor (the scan already used all of them; recomputing keeps the
	// last step independent of the scan's scratch buffers).
	ct, st := math.Cos(bestPhi), math.Sin(bestPhi)
	maxQ := 0.0
	for i := range pts {
		dx, dy := pts[i].x-c.x, pts[i].y-c.y
		u := dx*ct + dy*st
		v := -dx*st + dy*ct
		if q := u*u + (v*v)/(bestT*bestT); q > maxQ {
			maxQ = q
		}
	}
	a := math.Sqrt(maxQ) * effectiveVignettingSafety
	b := a * bestT
	if !(a > 0) || !(b > 0) {
		return nil
	}

	// Canonical orientation: phi and phi+pi describe the same ellipse, and so
	// do (a, b, phi) and (b, a, phi+/-pi/2). Fold into [-pi/2, pi/2), then pick
	// the representative with |phi| <= 45 deg so Tangent stays within [-1, 1]
	// — atan recovers the angle either way, but a reported tangent of 1e16 at
	// the fold edge reads as a bug and loses precision on the round trip.
	for bestPhi >= math.Pi/2 {
		bestPhi -= math.Pi
	}
	for bestPhi < -math.Pi/2 {
		bestPhi += math.Pi
	}
	if bestPhi > math.Pi/4 {
		bestPhi -= math.Pi / 2
		a, b = b, a
	} else if bestPhi < -math.Pi/4 {
		bestPhi += math.Pi / 2
		a, b = b, a
	}
	return &types.VignettingDef{
		DecenterX:    c.x / apertureRadius,
		DecenterY:    c.y / apertureRadius,
		CompressionX: 1 - a/apertureRadius,
		CompressionY: 1 - b/apertureRadius,
		Tangent:      math.Tan(bestPhi),
	}
}

// minAspectAxes returns the axis ratio t = b/a in [ellipseAspectLo,
// ellipseAspectHi] minimising the enclosing area (pi * max_i(t*u_i^2 +
// v_i^2/t)) for the already rotated coordinates, together with that area.
// The objective is convex in log t, so a bounded ternary search is exact for
// the precision we need.
func minAspectAxes(ux, vy []float64) (t, area float64) {
	lo := math.Log(ellipseAspectLo)
	hi := math.Log(ellipseAspectHi)
	objective := func(s float64) float64 {
		e := math.Exp(s)
		m := 0.0
		for i := range ux {
			if q := e*ux[i]*ux[i] + vy[i]*vy[i]/e; q > m {
				m = q
			}
		}
		return m
	}
	for i := 0; i < ellipseTernaryIterations; i++ {
		m1 := lo + (hi-lo)*0.382
		m2 := lo + (hi-lo)*0.618
		if objective(m1) < objective(m2) {
			hi = m2
		} else {
			lo = m1
		}
	}
	s := 0.5 * (lo + hi)
	t = math.Exp(s)
	// a^2 * t = objective(s) and the ellipse area is pi * a * b = pi * a^2 * t.
	return t, math.Pi * objective(s)
}

// ellipseOrientations is the candidate scan: the survivors' principal axis,
// the convex-hull edge directions (subsampled to ellipseMaxEdgeCandidates —
// the min-area bounding rectangle's optimum always sits on one) and a uniform
// sweep as a safety net for the degenerate hulls.
func ellipseOrientations(pts []xy, c xy) []float64 {
	var cxx, cxy, cyy float64
	for i := range pts {
		dx, dy := pts[i].x-c.x, pts[i].y-c.y
		cxx += dx * dx
		cxy += dx * dy
		cyy += dy * dy
	}
	angs := make([]float64, 0, ellipseMaxEdgeCandidates+ellipseOrientationSteps+1)
	angs = append(angs, 0.5*math.Atan2(2*cxy, cxx-cyy))

	if h := convexHull(pts); len(h) >= 3 {
		edges := make([]float64, 0, len(h))
		for i := 0; i < len(h); i++ {
			j := (i + 1) % len(h)
			dx, dy := h[j].x-h[i].x, h[j].y-h[i].y
			if dx == 0 && dy == 0 {
				continue
			}
			edges = append(edges, math.Atan2(dy, dx))
		}
		step := 1
		if len(edges) > ellipseMaxEdgeCandidates {
			step = (len(edges) + ellipseMaxEdgeCandidates - 1) / ellipseMaxEdgeCandidates
		}
		for i := 0; i < len(edges); i += step {
			angs = append(angs, edges[i])
		}
	}
	for k := 0; k < ellipseOrientationSteps; k++ {
		angs = append(angs, float64(k)*math.Pi/float64(ellipseOrientationSteps))
	}
	return angs
}

// convexHull is Andrew's monotone chain over the (deduplicated) points,
// returned counter-clockwise. It is deterministic: the sort has an x-then-y
// tiebreak and equal points are dropped, so cocircular/repeated samples can
// never flip the hull (and with it the fitted orientation).
func convexHull(pts []xy) []xy {
	if len(pts) < 3 {
		return pts
	}
	s := append([]xy(nil), pts...)
	sort.Slice(s, func(i, j int) bool {
		if s[i].x != s[j].x {
			return s[i].x < s[j].x
		}
		return s[i].y < s[j].y
	})
	uniq := make([]xy, 0, len(s))
	for i := range s {
		if len(uniq) > 0 && s[i] == uniq[len(uniq)-1] {
			continue
		}
		uniq = append(uniq, s[i])
	}
	if len(uniq) < 3 {
		return uniq
	}
	cross := func(o, a, b xy) float64 {
		return (a.x-o.x)*(b.y-o.y) - (a.y-o.y)*(b.x-o.x)
	}
	build := func(seq []xy) []xy {
		res := make([]xy, 0, len(seq))
		for _, p := range seq {
			for len(res) >= 2 && cross(res[len(res)-2], res[len(res)-1], p) <= 0 {
				res = res[:len(res)-1]
			}
			res = append(res, p)
		}
		return res
	}
	lower := build(uniq)
	upper := build(reversed(uniq))
	// Drop each chain's closing vertex before concatenating (both repeat the
	// hull's extremal points).
	res := make([]xy, 0, len(lower)+len(upper)-2)
	res = append(res, lower[:len(lower)-1]...)
	res = append(res, upper[:len(upper)-1]...)
	return res
}

func reversed(in []xy) []xy {
	out := make([]xy, len(in))
	for i := range in {
		out[len(in)-1-i] = in[i]
	}
	return out
}
