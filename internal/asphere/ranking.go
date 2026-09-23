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

// SurfaceMetrics contains the unbinned measurements used by the production
// ranking path. The legacy cell statistics remain available for comparison
// tests, but are no longer used to rank candidates.
type SurfaceMetrics struct {
	Joint            *JointFit
	Asym             float64
	Conflict         float64
	Unique           float64
	MeanR            float64
	FieldConsistency float64
	AstigY0R2        float64
	DefocusY0R2      float64
}

// ScoreSurfaceMetrics computes a candidate score from exact ray metrics.
func ScoreSurfaceMetrics(metrics SurfaceMetrics, surf types.Surface, n1, n2 float64, weights types.AsphereScoreWeights, opts ScoreOptions) types.AsphereSurfaceScore {
	score := types.AsphereSurfaceScore{SurfaceID: surf.ID}
	if metrics.Joint != nil {
		score.CommonEnergy = metrics.Joint.CommonE
		score.FitQuality = metrics.Joint.FitQuality
	}
	score.Conflict = clamp01(metrics.Conflict)
	score.UniqueEnergy = clamp01(metrics.Unique)
	score.Coverage = clamp01(score.CommonEnergy + score.UniqueEnergy)
	score.AsymResidual = clamp01(metrics.Asym)
	score.FieldConsistency = clamp01(metrics.FieldConsistency)
	score.AstigY0R2 = metrics.AstigY0R2
	score.DefocusY0R2 = metrics.DefocusY0R2

	sens := 0.0
	if opts.HasMeasuredH {
		sens = math.Max(0, opts.MeasuredH)
	} else if opts.MaxContrast > 0 {
		sens = math.Abs(n2-n1) / opts.MaxContrast * score.Coverage
	}
	score.SensitivityPenalty = sens
	mfg := 0.0
	if opts.MaxCurvature > 0 {
		mfg += 0.6 * math.Min(1, math.Abs(surf.Curvature)/opts.MaxCurvature)
	}
	mfg += 0.4 * math.Min(1, metrics.MeanR/50.0)
	score.ManufacturingPenalty = mfg
	unstable := 0.0
	if opts.HasStop {
		unstable = math.Exp(-math.Abs(surf.PhysicalZ-opts.StopZ) / 20.0)
	}
	// A smooth field law is less damaging than an incoherent asymmetry. The
	// conflict metric is kept independent because it already measures shared
	// footprint disagreement.
	asymPenalty := score.AsymResidual * (1 - score.FieldConsistency)
	score.Score = weights.Common*score.CommonEnergy +
		weights.Unique*score.UniqueEnergy + weights.Fit*score.FitQuality +
		weights.Sensitivity*sens - weights.Conflict*score.Conflict -
		weights.Asym*asymPenalty - weights.Manufacturing*mfg -
		weights.Unstable*unstable
	return score
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// RankSurfaceMetrics ranks exact-ray measurements.
func RankSurfaceMetrics(surfaces []types.Surface, metrics map[int]SurfaceMetrics, index map[int][2]float64, weights types.AsphereScoreWeights, maxEvenOrder int, stopZ float64, hasStop bool, measuredH map[int]float64) []types.AsphereSurfaceScore {
	maxCurvature, maxContrast := 0.0, 0.0
	for _, s := range surfaces {
		maxCurvature = math.Max(maxCurvature, math.Abs(s.Curvature))
		if p, ok := index[s.ID]; ok {
			maxContrast = math.Max(maxContrast, math.Abs(p[0]-p[1]))
		}
	}
	out := make([]types.AsphereSurfaceScore, 0, len(metrics))
	for _, s := range surfaces {
		m, ok := metrics[s.ID]
		if !ok || m.Joint == nil || m.Joint.Rays == 0 {
			continue
		}
		opts := ScoreOptions{MaxCurvature: maxCurvature, MaxContrast: maxContrast, StopZ: stopZ, HasStop: hasStop, MaxEvenOrder: maxEvenOrder}
		if h, ok := measuredH[s.ID]; ok {
			opts.MeasuredH, opts.HasMeasuredH = h, true
		}
		n1, n2 := 1.0, 1.0
		if p, ok := index[s.ID]; ok {
			n1, n2 = p[0], p[1]
		}
		out = append(out, ScoreSurfaceMetrics(m, s, n1, n2, weights, opts))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].SurfaceID < out[j].SurfaceID
	})
	return out
}

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
