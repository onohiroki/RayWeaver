package chief

import (
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

// SummarizeWavelengths attaches the resolved set's labels and weights to a
// field's per-wavelength statistics and derives the two polychromatic
// summaries:
//
//   - the white spot — the merged ray cloud about its common centroid, so the
//     RMS includes the inter-wavelength (lateral colour) spread that each
//     per-wavelength RMS about its own centroid cannot see;
//   - the lateral colour — the chief-ray height spread relative to the
//     reference wavelength.
//
// A wavelength without a matching sample keeps weight 1; an empty set uses
// equal weights. The stats slice is updated in place.
func SummarizeWavelengths(stats []types.WavelengthStats, samples []types.WavelengthSample, reference float64) (*types.SpotStats, *types.LateralColorStats) {
	if len(stats) == 0 {
		return nil, nil
	}
	weights := make([]float64, len(stats))
	for i := range stats {
		if len(samples) == 0 {
			weights[i] = 1
			continue
		}
		// A stat that is not in the resolved set is not part of the white
		// combination (the design table's weight <= 0 excludes it), even
		// though chief still reports its per-wavelength row.
		weights[i] = 0
		for _, s := range samples {
			if math.Abs(s.Value-stats[i].Value) <= 1e-12 {
				stats[i].Label = s.Label
				stats[i].Weight = s.Weight
				weights[i] = s.Weight
				break
			}
		}
	}

	var wSum, cxSum, cySum float64
	for i := range stats {
		w := weights[i]
		wSum += w
		cxSum += w * stats[i].SpotStats.Centroid.X
		cySum += w * stats[i].SpotStats.Centroid.Y
	}
	if wSum <= 0 {
		// Every declared line was excluded (or the weights were unusable):
		// fall back to equal weights so the diagnostic is still produced.
		for i := range weights {
			weights[i] = 1
		}
		wSum, cxSum, cySum = 0, 0, 0
		for i := range stats {
			wSum += 1
			cxSum += stats[i].SpotStats.Centroid.X
			cySum += stats[i].SpotStats.Centroid.Y
		}
		if wSum <= 0 {
			return nil, nil
		}
	}
	cx, cy := cxSum/wSum, cySum/wSum

	var vx, vy float64
	white := &types.SpotStats{Centroid: types.Vec3{X: cx, Y: cy}}
	first := true
	for i := range stats {
		w := weights[i]
		if w <= 0 {
			continue
		}
		ss := stats[i].SpotStats
		dx, dy := ss.Centroid.X-cx, ss.Centroid.Y-cy
		vx += w * (ss.RMS_X*ss.RMS_X + dx*dx)
		vy += w * (ss.RMS_Y*ss.RMS_Y + dy*dy)
		white.TotalRays += ss.TotalRays
		white.TracedRays += ss.TracedRays
		white.MissedRays += ss.MissedRays
		if first {
			white.MinX, white.MaxX = ss.MinX, ss.MaxX
			white.MinY, white.MaxY = ss.MinY, ss.MaxY
			first = false
			continue
		}
		if ss.MinX < white.MinX {
			white.MinX = ss.MinX
		}
		if ss.MaxX > white.MaxX {
			white.MaxX = ss.MaxX
		}
		if ss.MinY < white.MinY {
			white.MinY = ss.MinY
		}
		if ss.MaxY > white.MaxY {
			white.MaxY = ss.MaxY
		}
	}
	white.RMS_X = math.Sqrt(vx / wSum)
	white.RMS_Y = math.Sqrt(vy / wSum)
	white.RMS_R = math.Hypot(white.RMS_X, white.RMS_Y)

	refHeight := stats[0].ChiefHeight
	for i := range stats {
		if reference > 0 && math.Abs(stats[i].Value-reference) <= 1e-12 {
			refHeight = stats[i].ChiefHeight
			break
		}
	}
	lateral := &types.LateralColorStats{Reference: reference}
	var lcSum float64
	for i := range stats {
		if weights[i] <= 0 {
			continue
		}
		d := stats[i].ChiefHeight - refHeight
		if math.Abs(d) > lateral.MaxMM {
			lateral.MaxMM = math.Abs(d)
		}
		lcSum += weights[i] * d * d
	}
	lateral.RMSMM = math.Sqrt(lcSum / wSum)
	if reference <= 0 {
		lateral.Reference = stats[0].Value
	}
	return white, lateral
}
