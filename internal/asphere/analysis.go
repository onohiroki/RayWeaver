package asphere

import (
	"math"
	"sort"

	"github.com/hiroki/rayweaver/internal/types"
)

// cellHit is one ray's contribution to a polar cell on one surface.
type cellHit struct {
	FieldID int
	X, Y    float64
	R       float64
	Theta   float64
	OPD     float64
	Weight  float64
}

// cellData is the aggregation of all ray hits in one polar cell on one surface.
type cellData struct {
	SurfaceID int
	Ring      int
	Sector    int
	MeanR     float64
	Hits      []cellHit
}

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
// partial pivoting.
func solveLinear(a [][]float64, b []float64) ([]float64, bool) {
	n := len(a)
	if n == 0 {
		return nil, false
	}
	// augmented
	aug := make([][]float64, n)
	for i := 0; i < n; i++ {
		aug[i] = make([]float64, n+1)
		copy(aug[i], a[i])
		aug[i][n] = b[i]
	}
	for col := 0; col < n; col++ {
		pivot := col
		maxv := math.Abs(aug[col][col])
		for r := col + 1; r < n; r++ {
			if v := math.Abs(aug[r][col]); v > maxv {
				maxv = v
				pivot = r
			}
		}
		if maxv < 1e-300 {
			return nil, false
		}
		aug[col], aug[pivot] = aug[pivot], aug[col]
		for r := col + 1; r < n; r++ {
			f := aug[r][col] / aug[col][col]
			for c := col; c <= n; c++ {
				aug[r][c] -= f * aug[col][c]
			}
		}
	}
	x := make([]float64, n)
	for r := n - 1; r >= 0; r-- {
		s := aug[r][n]
		for c := r + 1; c < n; c++ {
			s -= aug[r][c] * x[c]
		}
		x[r] = s / aug[r][r]
	}
	return x, true
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

// BuildCellGrid bins every ray hit on the given surface into polar cells
// (rings × sectors), centred on the surface's optical axis. The ring grid
// spans [0, maxR] where maxR is the maximum radial extent of all hits.
func BuildCellGrid(footprints []FieldFootprintData, surfaceID, rings, angles int) []cellData {
	if rings < 1 {
		rings = 1
	}
	if angles < 1 {
		angles = 1
	}

	var all []cellHit
	maxR := 0.0
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
			theta := math.Atan2(sh.Position.Y, sh.Position.X)
			if theta < 0 {
				theta += 2 * math.Pi
			}
			all = append(all, cellHit{
				FieldID: fd.FieldID,
				X:       sh.Position.X,
				Y:       sh.Position.Y,
				R:       r,
				Theta:   theta,
				OPD:     h.OPD,
				Weight:  h.Weight,
			})
			if r > maxR {
				maxR = r
			}
		}
	}
	if maxR <= 0 || len(all) == 0 {
		return nil
	}

	cells := make(map[[2]int]*cellData)
	for _, c := range all {
		ring := int(c.R / maxR * float64(rings))
		if ring >= rings {
			ring = rings - 1
		}
		sector := int(c.Theta / (2 * math.Pi) * float64(angles))
		if sector >= angles {
			sector = angles - 1
		}
		key := [2]int{ring, sector}
		cd, ok := cells[key]
		if !ok {
			cd = &cellData{SurfaceID: surfaceID, Ring: ring, Sector: sector}
			cells[key] = cd
		}
		cd.Hits = append(cd.Hits, c)
	}

	out := make([]cellData, 0, len(cells))
	for _, cd := range cells {
		var sum float64
		for _, h := range cd.Hits {
			sum += h.R
		}
		cd.MeanR = sum / float64(len(cd.Hits))
		out = append(out, *cd)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ring != out[j].Ring {
			return out[i].Ring < out[j].Ring
		}
		return out[i].Sector < out[j].Sector
	})
	return out
}

// ComputeCellStats aggregates each cell's hits into the shared statistics: the
// occupied field set, the field-common OPD, the inter-field conflict, the
// unique residual for single-field cells, the azimuthal variance, the radial
// gradient and the occupancy weight. Cells with fewer than minRays total hits
// are dropped. totalWeight is the surface's total ray weight (for occupancy).
func ComputeCellStats(cells []cellData, minRays int, totalWeight float64) []types.AsphereCellStat {
	muByCell := make(map[[2]int]float64, len(cells))
	meanRByCell := make(map[[2]int]float64, len(cells))

	var out []types.AsphereCellStat
	for _, cd := range cells {
		if len(cd.Hits) < minRays || totalWeight <= 0 {
			continue
		}

		// Group hits by field.
		type fieldAgg struct {
			opdSum float64
			count  int
			w      float64
		}
		byField := make(map[int]*fieldAgg)
		var cellW float64
		for _, h := range cd.Hits {
			fa := byField[h.FieldID]
			if fa == nil {
				fa = &fieldAgg{w: h.Weight}
				byField[h.FieldID] = fa
			}
			fa.opdSum += h.OPD
			fa.count++
			cellW += h.Weight
		}

		// Field-common OPD and conflict.
		var mu, wSum float64
		var occupied []int
		for fid, fa := range byField {
			if fa.count < minRays {
				continue
			}
			occupied = append(occupied, fid)
			w := fa.w
			if w <= 0 {
				w = 1
			}
			mu += w * (fa.opdSum / float64(fa.count))
			wSum += w
		}
		if len(occupied) == 0 || wSum <= 0 {
			continue
		}
		mu /= wSum

		var conflict float64
		for _, fid := range occupied {
			fa := byField[fid]
			w := fa.w
			if w <= 0 {
				w = 1
			}
			om := fa.opdSum/float64(fa.count) - mu
			conflict += w * om * om
		}
		conflict /= wSum

		// Azimuthal variance: weighted variance of OPD across all hits.
		var azVar float64
		{
			var m, ws float64
			for _, h := range cd.Hits {
				w := h.Weight
				if w <= 0 {
					w = 1
				}
				m += w * h.OPD
				ws += w
			}
			if ws > 0 {
				m /= ws
				for _, h := range cd.Hits {
					w := h.Weight
					if w <= 0 {
						w = 1
					}
					d := h.OPD - m
					azVar += w * d * d
				}
				azVar /= ws
			}
		}

		cs := types.AsphereCellStat{
			SurfaceID:       cd.SurfaceID,
			Ring:            cd.Ring,
			Sector:          cd.Sector,
			MeanR:           cd.MeanR,
			OccupiedFields:  occupied,
			CommonOPD:       mu,
			Conflict:        conflict,
			AzimuthVariance: azVar,
			Weight:          cellW / totalWeight,
		}
		if len(occupied) == 1 {
			fa := byField[occupied[0]]
			om := fa.opdSum / float64(fa.count)
			cs.UniqueResidual = om * om
		}

		muByCell[[2]int{cd.Ring, cd.Sector}] = mu
		meanRByCell[[2]int{cd.Ring, cd.Sector}] = cd.MeanR
		out = append(out, cs)
	}

	// Radial gradient: |∂mu/∂r| between adjacent rings at the same sector.
	for i := range out {
		cs := &out[i]
		key := [2]int{cs.Ring + 1, cs.Sector}
		muNext, ok1 := muByCell[key]
		rNext, ok2 := meanRByCell[key]
		if ok1 && ok2 && rNext > cs.MeanR {
			cs.RadialGradient = math.Abs(muNext-cs.CommonOPD) / (rNext - cs.MeanR)
		}
	}
	return out
}
