package wavefront

import (
	"fmt"
)

// FocusConfig selects how per-field focus distances are combined.
type FocusConfig struct {
	// WeightType is "uniform" (default) or "custom".
	WeightType string
	// CustomWeights is required when WeightType == "custom".
	CustomWeights []float64
}

// FocusField is one field's contribution to the best-focus shift.
type FocusField struct {
	FieldIndex int
	Weight     float64
	FocusZ     float64 // sphere center axial distance from the reference surface (mm)
}

// FocusResult is the weighted combination of the per-field focus distances.
type FocusResult struct {
	WeightType      string
	WeightedFocusZ  float64 // Σ w·f / Σ w (mm)
	PerField        []FocusField
}

// ComputeBestFocus weights the per-field best-fit-sphere focus distances
// (sphere center axial distance from the reference surface) and returns the
// weighted average. Weight types: "uniform" (equal weights) or "custom" (the
// caller-provided per-field weights, in field order). The weighted focus
// distance is the target image-plane distance; the caller subtracts the
// current image-plane distance to obtain the shift.
func ComputeBestFocus(focusZ []float64, fieldIndices []int, cfg FocusConfig) (FocusResult, error) {
	if len(focusZ) != len(fieldIndices) {
		return FocusResult{}, fmt.Errorf("focus: %d focus distances for %d fields", len(focusZ), len(fieldIndices))
	}
	if len(focusZ) == 0 {
		return FocusResult{}, fmt.Errorf("focus: no field results")
	}

	wt := cfg.WeightType
	if wt == "" {
		wt = "uniform"
	}
	weights := make([]float64, len(focusZ))
	switch wt {
	case "uniform":
		for i := range weights {
			weights[i] = 1
		}
	case "custom":
		if len(cfg.CustomWeights) != len(focusZ) {
			return FocusResult{}, fmt.Errorf("focus: %d custom weights for %d fields", len(cfg.CustomWeights), len(focusZ))
		}
		copy(weights, cfg.CustomWeights)
	default:
		return FocusResult{}, fmt.Errorf("focus: unknown weight type %q (uniform | custom)", wt)
	}

	wSum, fSum := 0.0, 0.0
	fields := make([]FocusField, len(focusZ))
	for i, f := range focusZ {
		w := weights[i]
		wSum += w
		fSum += w * f
		fields[i] = FocusField{FieldIndex: fieldIndices[i], Weight: w, FocusZ: f}
	}
	if wSum <= 0 {
		return FocusResult{}, fmt.Errorf("focus: non-positive total weight")
	}
	return FocusResult{
		WeightType:     wt,
		WeightedFocusZ: fSum / wSum,
		PerField:       fields,
	}, nil
}
