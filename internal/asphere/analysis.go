package asphere

import (
	"math"
	"sort"

	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

// PreprocessOPD converts each ray's image OPL into an OPD referenced to the
// field's mean OPL, and optionally removes the fitted tilt plane (and defocus
// paraboloid) in pupil coordinates. Piston removal is implicit in the mean
// reference.
func PreprocessOPD(footprints []FieldFootprintData, removeTilt, removeDefocus bool) {
	for i := range footprints {
		fd := &footprints[i]
		var sum float64
		var count int
		for _, h := range fd.RayHits {
			if h.OK {
				sum += h.OPL
				count++
			}
		}
		if count == 0 {
			continue
		}
		ref := sum / float64(count)
		for j := range fd.RayHits {
			fd.RayHits[j].OPD = fd.RayHits[j].OPL - ref
		}
		if removeTilt || removeDefocus {
			removeFittedTerms(fd.RayHits, removeTilt, removeDefocus)
		}
	}
}

// removeFittedTerms removes the least-squares best-fit plane (and optionally a
// defocus paraboloid) from each valid ray's OPD, using the pupil coordinates.
// Basis columns are [1, X, Y, X²+Y²]; the piston column is fitted for a stable
// normal equation but only the tilt/defocus terms are subtracted (piston was
// already removed via the mean reference).
func removeFittedTerms(hits []RayHit, removeTilt, removeDefocus bool) {
	ncols := 1
	if removeTilt {
		ncols += 2
	}
	if removeDefocus {
		ncols++
	}

	var valid []RayHit
	var cx, cy float64
	var n float64
	for _, h := range hits {
		if h.OK {
			valid = append(valid, h)
			cx += h.PupilX
			cy += h.PupilY
			n++
		}
	}
	if len(valid) < ncols {
		return
	}
	cx /= n
	cy /= n

	rows := make([][]float64, len(valid))
	b := make([]float64, len(valid))
	for i, h := range valid {
		px := h.PupilX - cx
		py := h.PupilY - cy
		row := make([]float64, ncols)
		row[0] = 1
		col := 1
		if removeTilt {
			row[col] = px
			row[col+1] = py
			col += 2
		}
		if removeDefocus {
			row[col] = px*px + py*py
		}
		rows[i] = row
		b[i] = h.OPD
	}

	coeffs, ok := solveWeightedLS(rows, b, valid)
	if !ok {
		return
	}

	var tiltX, tiltY, defocus float64
	if removeTilt {
		tiltX = coeffs[1]
		tiltY = coeffs[2]
	}
	if removeDefocus {
		defocus = coeffs[ncols-1]
	}
	for i := range hits {
		if !hits[i].OK {
			continue
		}
		px := hits[i].PupilX - cx
		py := hits[i].PupilY - cy
		if removeTilt {
			hits[i].OPD -= tiltX*px + tiltY*py
		}
		if removeDefocus {
			hits[i].OPD -= defocus * (px*px + py*py)
		}
	}
}

// solveWeightedLS solves the weighted least-squares normal equations
// (AᵀWA)c = AᵀWb with weights taken from the hits' Weight field.
func solveWeightedLS(rows [][]float64, b []float64, hits []RayHit) ([]float64, bool) {
	n := len(rows)
	if n == 0 {
		return nil, false
	}
	ncols := len(rows[0])
	// ATA
	ata := make([][]float64, ncols)
	atb := make([]float64, ncols)
	for i := 0; i < ncols; i++ {
		ata[i] = make([]float64, ncols)
	}
	for k := 0; k < n; k++ {
		w := hits[k].Weight
		if w <= 0 {
			w = 1
		}
		for i := 0; i < ncols; i++ {
			for j := 0; j < ncols; j++ {
				ata[i][j] += w * rows[k][i] * rows[k][j]
			}
			atb[i] += w * rows[k][i] * b[k]
		}
	}
	c, ok := solveLinear(ata, atb)
	return c, ok
}

// solveLinear solves the square system Ax = b by Gaussian elimination with
// partial pivoting, without mutating the inputs.
func solveLinear(a [][]float64, b []float64) ([]float64, bool) {
	if len(a) == 0 {
		return nil, false
	}
	return raymath.SolveLinearCopy(a, b)
}

// solveRidge solves the weighted least-squares problem with Tikhonov ridge
// regularization. The design columns are scaled to unit norm so the ridge
// penalty is well conditioned; each basis column j is damped by orderPenalty[j]
// (higher orders are damped harder to suppress oscillatory overfitting).
// Order-dependent scaling ensures the reported physical coefficients do not
// blow up when a smooth low-order target sag is fitted with a high-order basis.
func solveRidge(rows [][]float64, b []float64, wts []float64, lambda float64, orderPenalty []float64) ([]float64, bool) {
	n := len(rows)
	if n == 0 {
		return nil, false
	}
	m := len(rows[0])

	// Column norms for scaling.
	scale := make([]float64, m)
	for j := 0; j < m; j++ {
		s := 0.0
		for i := 0; i < n; i++ {
			s += wts[i] * rows[i][j] * rows[i][j]
		}
		if s > 0 {
			scale[j] = math.Sqrt(s)
		} else {
			scale[j] = 1
		}
	}

	ata := make([][]float64, m)
	atb := make([]float64, m)
	for j := 0; j < m; j++ {
		ata[j] = make([]float64, m)
	}
	for i := 0; i < n; i++ {
		w := wts[i]
		if w <= 0 {
			w = 1
		}
		for j := 0; j < m; j++ {
			aj := rows[i][j] / scale[j]
			for k := 0; k < m; k++ {
				ata[j][k] += w * aj * rows[i][k] / scale[k]
			}
			atb[j] += w * aj * b[i]
		}
	}
	for j := 0; j < m; j++ {
		pen := 1.0
		if j < len(orderPenalty) {
			pen = orderPenalty[j]
		}
		ata[j][j] += lambda * pen * pen
	}

	c, ok := solveLinear(ata, atb)
	if !ok {
		return nil, false
	}
	for j := range c {
		c[j] /= scale[j]
	}
	return c, true
}

func BuildOPDProfiles(footprints []FieldFootprintData, surfaceIDs []int, rings int) []types.AsphereOPDProfile {
	var out []types.AsphereOPDProfile
	for _, sid := range surfaceIDs {
		profile := types.AsphereOPDProfile{SurfaceID: sid}
		buildSurfaceOPDProfile(&profile, footprints, rings)
		buildBeamOPDProfile(&profile, footprints, rings)
		if len(profile.Fields) > 0 {
			out = append(out, profile)
		}
	}
	return out
}

// buildBeamOPDProfile emits the field-local profile used by the demo: raw
// preprocessed OPD versus tangential position, split into +s and -s half beams.
// Footprints carry one entry per (field, wavelength); their hits are pooled per
// field and binned once on a shared tangential grid (weight-mean over
// wavelengths), so each field emits ONE sweep whose t values increase
// monotonically — downstream gnuplot curves stay connected without wrap-around
// segments between per-wavelength runs.
func buildBeamOPDProfile(profile *types.AsphereOPDProfile, footprints []FieldFootprintData, bins int) {
	if bins < 1 {
		bins = 1
	}
	type sample struct {
		t, opd, w float64
		plus      bool
	}
	type fieldAcc struct {
		fieldID   int
		direction []float64
		hits      []RayHit
	}
	byField := make(map[int]*fieldAcc)
	var order []*fieldAcc // first-seen field order, preserved in the output
	for _, fd := range footprints {
		fa := byField[fd.FieldID]
		if fa == nil {
			fa = &fieldAcc{fieldID: fd.FieldID, direction: fd.Direction}
			byField[fd.FieldID] = fa
			order = append(order, fa)
		}
		fa.hits = append(fa.hits, fd.RayHits...)
	}
	type agg struct {
		wp, wm, sp, sm float64
		np, nm         int
	}
	for _, fa := range order {
		cx, cy, tx, ty := fieldFootprintFrame(FieldFootprintData{
			FieldID:   fa.fieldID,
			Direction: fa.direction,
			RayHits:   fa.hits,
		}, profile.SurfaceID)
		var samples []sample
		tMin, tMax := math.Inf(1), math.Inf(-1)
		for _, h := range fa.hits {
			if !h.OK {
				continue
			}
			sh, ok := h.Hits[profile.SurfaceID]
			if !ok {
				continue
			}
			px, py := sh.Position.X-cx, sh.Position.Y-cy
			t := px*tx + py*ty
			if t < tMin {
				tMin = t
			}
			if t > tMax {
				tMax = t
			}
			samples = append(samples, sample{t: t, opd: h.OPD, w: effWeight(h), plus: px*(-ty)+py*tx >= 0})
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i].t < samples[j].t })
		if len(samples) == 0 || !(tMax > tMin) {
			continue
		}
		aggs := make([]agg, bins)
		for _, s := range samples {
			bi := int((s.t - tMin) / (tMax - tMin) * float64(bins))
			if bi >= bins {
				bi = bins - 1
			}
			if bi < 0 {
				bi = 0
			}
			if s.plus {
				aggs[bi].wp += s.w
				aggs[bi].sp += s.w * s.opd
				aggs[bi].np++
			} else {
				aggs[bi].wm += s.w
				aggs[bi].sm += s.w * s.opd
				aggs[bi].nm++
			}
		}
		field := types.AsphereOPDField{FieldID: fa.fieldID}
		for bi, a := range aggs {
			if a.np < 1 || a.nm < 1 || a.wp <= 0 || a.wm <= 0 {
				continue
			}
			t := tMin + (float64(bi)+0.5)/float64(bins)*(tMax-tMin)
			field.TRadius = append(field.TRadius, t)
			field.OPDPlus = append(field.OPDPlus, a.sp/a.wp)
			field.OPDMinus = append(field.OPDMinus, a.sm/a.wm)
		}
		if len(field.TRadius) > 0 {
			profile.Fields = append(profile.Fields, field)
		}
	}
}

// buildSurfaceOPDProfile fills profile for one surface from the footprint
// data, binning every ray by its footprint ring and field.
func buildSurfaceOPDProfile(profile *types.AsphereOPDProfile, footprints []FieldFootprintData, rings int) {
	if rings < 1 {
		rings = 1
	}
	type ringAgg struct {
		rSum float64
		wSum float64
		opdW float64 // sum of weight*OPD
	}
	// maxR over all valid hits on this surface.
	maxR := 0.0
	for _, fd := range footprints {
		for _, h := range fd.RayHits {
			if !h.OK {
				continue
			}
			sh, ok := h.Hits[profile.SurfaceID]
			if !ok {
				continue
			}
			if r := math.Hypot(sh.Position.X, sh.Position.Y); r > maxR {
				maxR = r
			}
		}
	}
	if maxR <= 0 {
		return
	}
	profile.MaxR = maxR

	// Accumulate per (field, ring).
	type fieldKey struct {
		field int
		ring  int
	}
	agg := make(map[fieldKey]*ringAgg)
	fieldOrder := make(map[int]int)
	var order []int
	for _, fd := range footprints {
		if _, ok := fieldOrder[fd.FieldID]; !ok {
			fieldOrder[fd.FieldID] = len(order)
			order = append(order, fd.FieldID)
		}
		for _, h := range fd.RayHits {
			if !h.OK {
				continue
			}
			sh, ok := h.Hits[profile.SurfaceID]
			if !ok {
				continue
			}
			r := math.Hypot(sh.Position.X, sh.Position.Y)
			ring := int(r / maxR * float64(rings))
			if ring >= rings {
				ring = rings - 1
			}
			key := fieldKey{fd.FieldID, ring}
			ra := agg[key]
			if ra == nil {
				ra = &ringAgg{}
				agg[key] = ra
			}
			w := h.Weight
			if w <= 0 {
				w = 1
			}
			ra.rSum += w * r
			ra.wSum += w
			ra.opdW += w * h.OPD
		}
	}

	// Order the ring radii ascending so the profile curves are continuous.
	for _, fid := range order {
		field := types.AsphereOPDField{FieldID: fid}
		type ringPoint struct {
			ring int
			r    float64
			opd  float64
		}
		var pts []ringPoint
		for key, ra := range agg {
			if key.field != fid {
				continue
			}
			if ra.wSum <= 0 {
				continue
			}
			pts = append(pts, ringPoint{ring: key.ring, r: ra.rSum / ra.wSum, opd: ra.opdW / ra.wSum})
		}
		if len(pts) == 0 {
			continue
		}
		sort.Slice(pts, func(i, j int) bool { return pts[i].ring < pts[j].ring })
		for _, p := range pts {
			field.RingRadius = append(field.RingRadius, p.r)
			field.OPD = append(field.OPD, p.opd)
		}
		profile.Fields = append(profile.Fields, field)
	}
}
