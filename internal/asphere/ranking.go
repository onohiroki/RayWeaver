package asphere

import (
	"math"
	"sort"

	"github.com/hiroki/rayweaver/internal/types"
)

// ScoreOptions carries the cross-surface quantities needed to normalise a
// surface's composite score.
type ScoreOptions struct {
	MaxCurvature float64 // max |curvature| over all candidates (for mfg penalty)
	MaxContrast  float64 // max |n2-n1| over all candidates (for sensitivity)
	StopZ        float64 // stop surface physical Z
	HasStop      bool
	MaxEvenOrder int
	// MeasuredH is the traced relative merit improvement of the surface's
	// scaled asphere (Phase 3). When HasMeasuredH is set it replaces the
	// analytic index-contrast sensitivity proxy.
	MeasuredH    float64
	HasMeasuredH bool
}

// ScoreSurface computes one candidate surface's composite score from its cell
// statistics:
//
//	S_s = w_com·E^common + w_uni·E^unique + w_fit·F + w_sens·H
//	      - w_conf·C - w_mfg·M - w_unstable·U
//
// E^common / E^unique are the shared/unique OPD energies normalised by the
// total cell OPD energy on the surface; F is the rotationally-symmetric fit
// quality (R²); H the sensitivity (traced relative merit improvement when
// available, else the index-contrast proxy); C the weighted inter-field
// variance; M the manufacturing penalty; U the stop-proximity instability.
func ScoreSurface(cells []types.AsphereCellStat, surf types.Surface, n1, n2 float64, weights types.AsphereScoreWeights, opts ScoreOptions) types.AsphereSurfaceScore {
	score := types.AsphereSurfaceScore{SurfaceID: surf.ID}

	var total, common, unique, normSum, confNormW float64
	var sharedCells []types.AsphereCellStat
	for _, c := range cells {
		w := c.Weight
		if w <= 0 {
			continue
		}
		total += w * (c.CommonOPD*c.CommonOPD + c.Conflict)
		if len(c.OccupiedFields) >= 2 {
			common += w * c.CommonOPD * c.CommonOPD
			sharedCells = append(sharedCells, c)
			eps := 1e-12
			normSum += w * c.Conflict / (c.Conflict + c.CommonOPD*c.CommonOPD + eps)
			confNormW += w
		} else {
			unique += w * c.UniqueResidual
		}
	}

	meanR := 0.0
	var rW float64
	for _, c := range cells {
		meanR += c.Weight * c.MeanR
		rW += c.Weight
	}
	if rW > 0 {
		meanR /= rW
	}

	if total > 0 {
		score.Coverage = (common + unique) / total
		score.CommonEnergy = common / total
		score.UniqueEnergy = unique / total
		// Normalised inter-field conflict: the per-cell conflict relative to
		// the cell's own OPD magnitude, weighted across shared cells (0..1).
		if confNormW > 0 {
			score.Conflict = normSum / confNormW
		}
	}

	// Fit quality: R² of fitting the shared-cell common OPD to a radial
	// rotationally-symmetric asphere basis.
	if len(sharedCells) >= 2 {
		_, score.FitQuality = fitRadial(sharedCells, maxOrder(opts.MaxEvenOrder))
	}

	// Sensitivity H: the traced relative merit improvement of the scaled
	// asphere when Phase 3 measured it (the calibrated improvement when
	// calibration ran), otherwise the analytic index-contrast proxy scaled by
	// the fraction of OPD energy the asphere can address. The measured value is
	// floored at 0 so an overshooting probe (a scale that makes the merit
	// worse) can never feed a negative penalty into the score and demote a
	// genuinely aspherizable surface below an unfit one.
	sens := 0.0
	if opts.HasMeasuredH {
		sens = opts.MeasuredH
		if sens < 0 {
			sens = 0
		}
	} else if opts.MaxContrast > 0 {
		contrast := math.Abs(n2 - n1)
		sens = (contrast / opts.MaxContrast) * score.Coverage
	}

	// Manufacturing penalty: base curvature magnitude and beam radius.
	mfg := 0.0
	if opts.MaxCurvature > 0 {
		mfg += 0.6 * math.Min(1, math.Abs(surf.Curvature)/opts.MaxCurvature)
	}
	mfg += 0.4 * math.Min(1, meanR/50.0)
	score.ManufacturingPenalty = mfg

	// Instability penalty: proximity to the stop surface.
	unstable := 0.0
	if opts.HasStop {
		dz := math.Abs(surf.PhysicalZ - opts.StopZ)
		unstable = math.Exp(-dz / 20.0)
	}
	score.SensitivityPenalty = sens

	score.Score = weights.Common*score.CommonEnergy +
		weights.Unique*score.UniqueEnergy +
		weights.Fit*score.FitQuality +
		weights.Sensitivity*sens -
		weights.Conflict*score.Conflict -
		weights.Manufacturing*mfg -
		weights.Unstable*unstable

	return score
}

// RankSurfaces computes and sorts the composite score of every candidate
// surface. index maps surface ID to the (n1, n2) refractive indices before/after.
// measuredH maps surface ID to the Phase-3 traced relative merit improvement
// (1 - asphere/base) used as the sensitivity term; surfaces absent from the map
// fall back to the analytic index-contrast proxy.
func RankSurfaces(surfaces []types.Surface, cellsBySurf map[int][]types.AsphereCellStat, index map[int][2]float64, weights types.AsphereScoreWeights, maxEvenOrder int, stopZ float64, hasStop bool, measuredH map[int]float64) []types.AsphereSurfaceScore {
	maxCurvature := 0.0
	maxContrast := 0.0
	for _, s := range surfaces {
		if c := math.Abs(s.Curvature); c > maxCurvature {
			maxCurvature = c
		}
		if pair, ok := index[s.ID]; ok {
			if c := math.Abs(pair[0] - pair[1]); c > maxContrast {
				maxContrast = c
			}
		}
	}
	opts := ScoreOptions{
		MaxCurvature: maxCurvature,
		MaxContrast:  maxContrast,
		StopZ:        stopZ,
		HasStop:      hasStop,
		MaxEvenOrder: maxEvenOrder,
	}

	out := make([]types.AsphereSurfaceScore, 0, len(surfaces))
	for _, s := range surfaces {
		cells := cellsBySurf[s.ID]
		if len(cells) == 0 {
			continue
		}
		var n1, n2 float64
		if pair, ok := index[s.ID]; ok {
			n1, n2 = pair[0], pair[1]
		}
		if h, ok := measuredH[s.ID]; ok {
			opts.MeasuredH = h
			opts.HasMeasuredH = true
		} else {
			opts.MeasuredH = 0
			opts.HasMeasuredH = false
		}
		out = append(out, ScoreSurface(cells, s, n1, n2, weights, opts))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].SurfaceID < out[j].SurfaceID
	})
	return out
}

// maxOrder returns the largest basis index (number of terms beyond conic) for
// the asphere polynomial from a max even order: 8 → A4..A8, 10 → A4..A10,
// 12 → A4..A12.
func maxOrder(maxEvenOrder int) int {
	switch {
	case maxEvenOrder >= 12:
		return 5 // A4..A12 (r^4..r^12)
	case maxEvenOrder >= 10:
		return 4
	case maxEvenOrder >= 8:
		return 3
	default:
		return 2
	}
}
