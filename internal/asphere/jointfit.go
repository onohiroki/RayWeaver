// Raw-ray analysis of the aspherisable common OPD. Instead of binning the ray
// hits into polar (ring×sector) cells centred on the surface's optical axis —
// which for decentred off-axis footprints collapses the beam into a few outer
// bands and mixes its azimuthal content — the joint fit treats every ray as a
// sample point at its exact axial radius r = √(x²+y²) on the surface. One even
// radial polynomial z(r) is fitted to ALL fields'/wavelengths' rays at once, so
// the field-common component and its R² are measured without binning loss.
// The residual e = OPD − Ō(r) is then decomposed in the per-field beam frame
// (tangential axis along the field azimuth, sagittal perpendicular) into the
// parts a rotationally-symmetric asphere cannot correct: the sagittal-antisymmetric
// residual (asym, new score term) and the inter-field conflict / single-field
// unique residual (shared footprints).
package asphere

import (
	"math"

	"github.com/hiroki/rayweaver/internal/raymath"
)

// effWeight returns a ray's effective fit weight: field weight scaled by the
// pupil-cell area when one is recorded (0/absent → 1), so outer polar-grid
// annuli are not over-represented in the joint fit and diagnostics.
func effWeight(h RayHit) float64 {
	w := h.Weight
	if w <= 0 {
		w = 1
	}
	if h.Area > 0 {
		w *= h.Area
	}
	return w
}

// raySample is one valid ray of a (field, wavelength) grid at a surface.
type raySample struct {
	r, opd, w float64
	field     int
	pos       [2]float64
}

// collectSurfaceRays gathers every valid ray that crossed surfaceID, with its
// axial radius, OPD, effective weight, field id and footprint position.
func collectSurfaceRays(footprints []FieldFootprintData, surfaceID int) ([]raySample, float64) {
	var rays []raySample
	rMax := 0.0
	for _, fd := range footprints {
		for _, h := range fd.RayHits {
			if !h.OK {
				continue
			}
			sh, ok := h.Hits[surfaceID]
			if !ok {
				continue
			}
			r := math.Hypot(sh.Position.X, sh.Position.Y)
			rays = append(rays, raySample{
				r:     r,
				opd:   h.OPD,
				w:     effWeight(h),
				field: fd.FieldID,
				pos:   [2]float64{sh.Position.X, sh.Position.Y},
			})
			if r > rMax {
				rMax = r
			}
		}
	}
	return rays, rMax
}

// JointFit is the raw-ray joint radial fit of one surface's common OPD.
type JointFit struct {
	Coef       []float64 // even polynomial coefficients in OPD units, [ρ², ρ⁴, ...]
	FitQuality float64   // R² of the r⁴+ fit on the r²-removed residual
	CommonE    float64   // Σ w·Ō² / Σ w·OPD²  (the L² projection fraction)
	Total      float64   // Σ w·OPD²
	ResidualE  float64   // Σ w·e²  (total − common)
	RMax       float64
	Rays       int
}

// JointRadialFit fits the common even radial polynomial z(r) to every valid
// ray across all (field, wavelength) footprints on surfaceID at once. nTerms is
// the number of beyond-conic terms (4 → A4..A10, 5 → A4..A12); the basis always
// carries the leading ρ² defocus term. The design matrix, ridge and order
// penalty match the old cell-based fitRadial so the only change is the row
// source: exact rays instead of cell means.
func JointRadialFit(footprints []FieldFootprintData, surfaceID, nTerms int) *JointFit {
	if nTerms < 1 {
		nTerms = 1
	}
	rays, rMax := collectSurfaceRays(footprints, surfaceID)
	if len(rays) == 0 || rMax <= 0 {
		return &JointFit{Rays: len(rays)}
	}

	ncols := nTerms + 1
	rows := make([][]float64, len(rays))
	ys := make([]float64, len(rays))
	ws := make([]float64, len(rays))
	for i, rp := range rays {
		rho := rp.r / rMax
		row := make([]float64, ncols)
		row[0] = rho * rho
		for p := 0; p < nTerms; p++ {
			row[p+1] = math.Pow(rho, float64(4+2*p))
		}
		rows[i] = row
		ys[i] = rp.opd
		ws[i] = rp.w
	}
	orderPenalty := make([]float64, ncols)
	for j := range orderPenalty {
		orderPenalty[j] = 1 + float64(j)*0.5
	}
	coef, ok := solveRidge(rows, ys, ws, 0.05, orderPenalty)
	if !ok {
		return &JointFit{Rays: len(rays)}
	}

	out := &JointFit{Coef: coef, RMax: rMax, Rays: len(rays)}

	// L² split: total = common + residual.
	var total, common, resid float64
	residArr := make([]float64, len(rays))
	for i, rp := range rays {
		rho := rp.r / rMax
		pred := predictRadial(rho, coef)
		w := rp.w
		total += w * rp.opd * rp.opd
		common += w * pred * pred
		e := rp.opd - pred
		residArr[i] = e
		resid += w * e * e
	}
	if total > 0 {
		out.CommonE = common / total
	}
	out.Total = total
	out.ResidualE = resid

	// Fit quality: R² of the r⁴+ fit on the r²-removed residual (defocus split).
	var mean, wsum float64
	for _, rp := range rays {
		rho := rp.r / rMax
		d := rp.opd - coef[0]*rho*rho
		mean += rp.w * d
		wsum += rp.w
	}
	if wsum > 0 {
		mean /= wsum
	}
	var ssTot, ssRes float64
	for _, rp := range rays {
		rho := rp.r / rMax
		d := rp.opd - coef[0]*rho*rho
		pred := 0.0
		for p := 1; p < ncols; p++ {
			pred += coef[p] * math.Pow(rho, float64(4+2*(p-1)))
		}
		ssTot += rp.w * (d - mean) * (d - mean)
		ssRes += rp.w * (d - pred) * (d - pred)
	}
	if ssTot > 1e-30 {
		out.FitQuality = 1 - ssRes/ssTot
	}
	return out
}

// predictRadial evaluates the joint polynomial at a normalised radius ρ.
func predictRadial(rho float64, coef []float64) float64 {
	v := coef[0] * rho * rho
	for p := 1; p < len(coef); p++ {
		v += coef[p] * math.Pow(rho, float64(4+2*(p-1)))
	}
	return v
}

// fieldFootprintFrame returns the field's footprint centroid (cx, cy) on the
// surface and the tangential/sagittal unit axes (tangential = field azimuth).
func fieldFootprintFrame(fd FieldFootprintData, surfaceID int) (cx, cy, tx, ty float64) {
	dx, dy := raymath.FieldAzimuth(fd.Direction)
	var wsum float64
	for _, h := range fd.RayHits {
		if !h.OK {
			continue
		}
		sh, ok := h.Hits[surfaceID]
		if !ok {
			continue
		}
		w := effWeight(h)
		cx += w * sh.Position.X
		cy += w * sh.Position.Y
		wsum += w
	}
	if wsum > 0 {
		cx /= wsum
		cy /= wsum
	}
	return cx, cy, dx, dy
}

// BeamFrameAsym measures the sagittal-antisymmetric residual a rotationally
// symmetric asphere cannot correct: per field the residual e = OPD − Ō(r) is
// binned by the tangential coordinate (in the field's beam frame, so a y-field's
// bin is the tangential scan) and the +s / −s sagittal half-bin mean difference
// is squared. Returns the energy fraction of the surface's total OPD energy.
// coef/rMax come from JointRadialFit; bins with fewer than minRays per half are
// ignored (no signal). total is the surface's Σ w·OPD².
func BeamFrameAsym(footprints []FieldFootprintData, surfaceID int, coef []float64, rMax float64, tBins, minRays int, total float64) float64 {
	if tBins < 1 || total <= 0 || len(coef) == 0 || rMax <= 0 {
		return 0
	}
	var asym float64
	for _, fd := range footprints {
		cx, cy, tx, ty := fieldFootprintFrame(fd, surfaceID)
		type tbin struct {
			plusW, minusW     float64
			plusSum, minusSum float64
			plusN, minusN     int
		}
		bins := make(map[int]*tbin, tBins)
		tMin, tMax := math.Inf(1), math.Inf(-1)
		for _, h := range fd.RayHits {
			if !h.OK {
				continue
			}
			sh, ok := h.Hits[surfaceID]
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
		}
		if math.IsInf(tMax, 0) || tMax <= tMin {
			continue
		}
		for _, h := range fd.RayHits {
			if !h.OK {
				continue
			}
			sh, ok := h.Hits[surfaceID]
			if !ok {
				continue
			}
			px, py := sh.Position.X-cx, sh.Position.Y-cy
			t := px*tx + py*ty
			s := px*(-ty) + py*tx
			rho := math.Hypot(sh.Position.X, sh.Position.Y) / rMax
			e := h.OPD - predictRadial(rho, coef)
			w := effWeight(h)
			bi := int((t - tMin) / (tMax - tMin) * float64(tBins))
			if bi >= tBins {
				bi = tBins - 1
			}
			if bi < 0 {
				bi = 0
			}
			b := bins[bi]
			if b == nil {
				b = &tbin{}
				bins[bi] = b
			}
			if s >= 0 {
				b.plusW += w
				b.plusSum += w * e
				b.plusN++
			} else {
				b.minusW += w
				b.minusSum += w * e
				b.minusN++
			}
		}
		for _, b := range bins {
			if b.plusN < minRays || b.minusN < minRays || b.plusW <= 0 || b.minusW <= 0 {
				continue
			}
			d := b.plusSum/b.plusW - b.minusSum/b.minusW
			asym += (b.plusW + b.minusW) * d * d
		}
	}
	if total <= 0 {
		return 0
	}
	return asym / total
}

// SharedConflictUnique decomposes the joint-fit residual into the inter-field
// conflict (fields disagreeing at shared footprint regions) and the unique
// residual (rays in regions covered by a single field). A field's footprint
// region is its weighted centroid ± k·RMS radius; a ray is shared when it falls
// inside another field's region. Returns (conflictFrac, uniqueFrac) of the
// surface's total OPD energy.
func SharedConflictUnique(footprints []FieldFootprintData, surfaceID int, coef []float64, rMax, k, total float64) (conflictFrac, uniqueFrac float64) {
	if len(footprints) == 0 || total <= 0 || len(coef) == 0 || rMax <= 0 {
		return 0, 0
	}
	// Per-field ray lists with positions.
	rayList := make([][]raySample, len(footprints))
	radii := make(map[int]float64, len(footprints))
	centroids := make(map[int][2]float64, len(footprints))
	for gi, fd := range footprints {
		cx, cy, _, _ := fieldFootprintFrame(fd, surfaceID)
		centroids[fd.FieldID] = [2]float64{cx, cy}
		for _, h := range fd.RayHits {
			if !h.OK {
				continue
			}
			sh, ok := h.Hits[surfaceID]
			if !ok {
				continue
			}
			r := math.Hypot(sh.Position.X, sh.Position.Y)
			rayList[gi] = append(rayList[gi], raySample{r: r, opd: h.OPD, w: effWeight(h), pos: [2]float64{sh.Position.X, sh.Position.Y}})
		}
		var s2, wsum float64
		for _, s := range rayList[gi] {
			dx := s.pos[0] - cx
			dy := s.pos[1] - cy
			s2 += s.w * (dx*dx + dy*dy)
			wsum += s.w
		}
		if wsum > 0 {
			radii[fd.FieldID] = math.Sqrt(s2 / wsum)
		}
	}
	// Per-ray shared flag: in any other field's region.
	shared := make([][]bool, len(rayList))
	for gi := range footprints {
		shared[gi] = make([]bool, len(rayList[gi]))
		for ri, s := range rayList[gi] {
			for gj, gfd := range footprints {
				if gj == gi {
					continue
				}
				c := centroids[gfd.FieldID]
				a := radii[gfd.FieldID]
				if a <= 0 {
					continue
				}
				dx := s.pos[0] - c[0]
				dy := s.pos[1] - c[1]
				if math.Hypot(dx, dy) <= k*a {
					shared[gi][ri] = true
					break
				}
			}
		}
	}
	// Conflict: per-field mean residual over its shared rays.
	type fm struct {
		mu, wsum float64
		has      bool
	}
	fieldMu := make(map[int]*fm, len(footprints))
	for gi, fd := range footprints {
		f := fieldMu[fd.FieldID]
		if f == nil {
			f = &fm{}
			fieldMu[fd.FieldID] = f
		}
		for ri, s := range rayList[gi] {
			if !shared[gi][ri] {
				continue
			}
			rho := s.r / rMax
			e := s.opd - predictRadial(rho, coef)
			f.mu += s.w * e
			f.wsum += s.w
			f.has = true
		}
	}
	var muBar, wTotal float64
	for _, f := range fieldMu {
		if !f.has || f.wsum <= 0 {
			continue
		}
		f.mu /= f.wsum
		muBar += f.wsum * f.mu
		wTotal += f.wsum
	}
	if wTotal > 0 {
		muBar /= wTotal
	}
	var conflictE float64
	for _, f := range fieldMu {
		if !f.has {
			continue
		}
		d := f.mu - muBar
		conflictE += f.wsum * d * d
	}
	// Unique: residual energy of non-shared rays.
	var uniqueE float64
	for gi := range footprints {
		for ri, s := range rayList[gi] {
			if shared[gi][ri] {
				continue
			}
			rho := s.r / rMax
			e := s.opd - predictRadial(rho, coef)
			uniqueE += s.w * e * e
		}
	}
	return conflictE / total, uniqueE / total
}

// FieldLowOrder is one field's beam-frame low-order residual as seen from the
// surface: the weighted fit of the joint-fit residual e = OPD − Ō(r) to
// [1, u², v²] in the field's beam frame (u = sagittal, v = tangential about the
// footprint centroid). Defocus = (a+b)/2, Astig = (b−a)/2.
type FieldLowOrder struct {
	FieldID        int
	CX, CY         float64 // footprint centroid on the surface
	Y0             float64 // centroid axial radius (field-height handle)
	Defocus, Astig float64
	Rays           int
}

// FieldPortrait aggregates the per-field beam-frame low orders: the field-curvature
// (defocus) and astigmatism that a single rotationally-symmetric asphere z(r)
// would have to reproduce at each field's footprint offset Y0. The consistency
// measures test whether they do follow one z(r): R² of Astig vs Y0² and of
// Defocus vs Y0² across fields. High R² means the fields' residuals scale
// smoothly with field height and are tractable by (or attributable to) the
// asphere rather than being inter-field conflict.
type FieldPortrait struct {
	Fields    []FieldLowOrder
	AstigR2   float64 // R² of Astig = c·Y0² across fields
	DefocusR2 float64 // R² of Defocus = c·Y0² across fields
}

// FieldLowOrderPortrait builds the beam-frame low-order portrait of the
// joint-fit residual for every field on surfaceID. coef/rMax come from
// JointRadialFit. Fields with fewer than minRays valid rays are omitted.
func FieldLowOrderPortrait(footprints []FieldFootprintData, surfaceID int, coef []float64, rMax float64, minRays int) FieldPortrait {
	var out FieldPortrait
	if len(coef) == 0 || rMax <= 0 {
		return out
	}
	byField := make(map[int][]FieldFootprintData)
	for _, fd := range footprints {
		byField[fd.FieldID] = append(byField[fd.FieldID], fd)
	}
	for fieldID, fds := range byField {
		var cx, cy, wsum float64
		var tx, ty float64
		for _, fd := range fds {
			if tx == 0 && ty == 0 {
				_, _, tx, ty = fieldFootprintFrame(fd, surfaceID)
			}
			for _, h := range fd.RayHits {
				if !h.OK {
					continue
				}
				sh, ok := h.Hits[surfaceID]
				if !ok {
					continue
				}
				w := effWeight(h)
				cx += w * sh.Position.X
				cy += w * sh.Position.Y
				wsum += w
			}
		}
		if wsum <= 0 {
			continue
		}
		cx /= wsum
		cy /= wsum
		var rows [][]float64
		var b []float64
		var hits []RayHit
		n := 0
		for _, fd := range fds {
			for _, h := range fd.RayHits {
				if !h.OK {
					continue
				}
				sh, ok := h.Hits[surfaceID]
				if !ok {
					continue
				}
				px, py := sh.Position.X-cx, sh.Position.Y-cy
				u := px*(-ty) + py*tx // sagittal
				v := px*tx + py*ty    // tangential
				rho := math.Hypot(sh.Position.X, sh.Position.Y) / rMax
				e := h.OPD - predictRadial(rho, coef)
				rows = append(rows, []float64{1, u * u, v * v})
				b = append(b, e)
				wh := h
				wh.Weight = effWeight(h)
				hits = append(hits, wh)
				n++
			}
		}
		if n < minRays {
			continue
		}
		g, ok := solveWeightedLS(rows, b, hits)
		if !ok {
			continue
		}
		d := (g[1] + g[2]) / 2
		ast := (g[2] - g[1]) / 2
		out.Fields = append(out.Fields, FieldLowOrder{
			FieldID: fieldID, CX: cx, CY: cy, Y0: math.Hypot(cx, cy),
			Defocus: d, Astig: ast, Rays: n,
		})
	}
	if len(out.Fields) < 2 {
		return out
	}
	// Consistency: linear regression through the origin of value = c·Y0².
	out.AstigR2 = fitThroughOriginR2(y0Sq(out.Fields), astigs(out.Fields))
	out.DefocusR2 = fitThroughOriginR2(y0Sq(out.Fields), defocuses(out.Fields))
	return out
}

func y0Sq(fs []FieldLowOrder) []float64 {
	o := make([]float64, len(fs))
	for i, f := range fs {
		o[i] = f.Y0 * f.Y0
	}
	return o
}
func astigs(fs []FieldLowOrder) []float64 {
	o := make([]float64, len(fs))
	for i, f := range fs {
		o[i] = f.Astig
	}
	return o
}
func defocuses(fs []FieldLowOrder) []float64 {
	o := make([]float64, len(fs))
	for i, f := range fs {
		o[i] = f.Defocus
	}
	return o
}

// fitThroughOriginR2 returns R² of the least-squares fit y = c·x through the
// origin (how well y follows a single linear-in-x trend).
func fitThroughOriginR2(x, y []float64) float64 {
	n := len(x)
	if n < 2 {
		return math.NaN()
	}
	var sxx, sxy float64
	for i := range x {
		sxx += x[i] * x[i]
		sxy += x[i] * y[i]
	}
	if sxx <= 0 {
		return math.NaN()
	}
	c := sxy / sxx
	var ssRes, ssTot float64
	for i := range x {
		ssRes += (y[i] - c*x[i]) * (y[i] - c*x[i])
	}
	m := 0.0
	for _, v := range y {
		m += v
	}
	m /= float64(n)
	for i := range y {
		ssTot += (y[i] - m) * (y[i] - m)
	}
	if ssTot <= 1e-30 {
		return math.NaN()
	}
	return 1 - ssRes/ssTot
}
