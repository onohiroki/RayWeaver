package dls

import (
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

// Default class parameters for adaptive damping. These provide sensible
// starting points: curvature and asphere get stronger damping (high
// sensitivity, nonlinear), thickness gets weaker damping (low sensitivity,
// often stalls with uniform damping).
var defaultClassConfig = map[string]types.DampingClassConfig{
	"curvature": {sensitivityPower(1.20), multiplierPtr(1.50)},
	"thickness": {sensitivityPower(0.75), multiplierPtr(0.50)},
	"diameter":  {sensitivityPower(1.00), multiplierPtr(1.00)},
	"nd":        {sensitivityPower(1.00), multiplierPtr(1.00)},
	"vd":        {sensitivityPower(1.00), multiplierPtr(1.00)},
	"conic":     {sensitivityPower(1.20), multiplierPtr(2.00)},
	"asphere":   {sensitivityPower(1.30), multiplierPtr(3.00)},
	"shared":    {sensitivityPower(1.00), multiplierPtr(1.00)},
}

func sensitivityPower(v float64) *float64 { return &v }
func multiplierPtr(v float64) *float64     { return &v }

// AdaptiveDampingState holds the per-iteration state for adaptive damping.
type AdaptiveDampingState struct {
	emaH         []float64 // EMA-smoothed Hessian diagonal per variable
	localFactor  []float64 // history-based local damping multiplier per variable
	diagonal     []float64 // last built diagonal (for logging)
	lastRatio    []float64 // last sensitivity ratio per variable (for logging)
	lastRef      float64   // last geometric mean reference (for logging)
}

// NewAdaptiveDampingState allocates state for nVars variables.
func NewAdaptiveDampingState(nVars int) *AdaptiveDampingState {
	s := &AdaptiveDampingState{
		emaH:        make([]float64, nVars),
		localFactor: make([]float64, nVars),
		diagonal:    make([]float64, nVars),
		lastRatio:   make([]float64, nVars),
	}
	for j := range s.localFactor {
		s.localFactor[j] = 1.0
	}
	return s
}

// dampingClass maps a VariableInfo.Param string to a damping class name.
func dampingClass(param string) string {
	switch param {
	case "curvature":
		return "curvature"
	case "thickness":
		return "thickness"
	case "diameter":
		return "diameter"
	case "nd":
		return "nd"
	case "vd":
		return "vd"
	case "conic":
		return "conic"
	case "a4", "a6", "a8", "a10", "a12",
		"coefficient_0", "coefficient_1", "coefficient_2",
		"coefficient_3", "coefficient_4":
		return "asphere"
	default:
		return "shared"
	}
}

// resolveClassConfig returns the effective (sensitivityPower, multiplier) for a
// variable, checking per-variable overrides first, then class config, then
// built-in defaults.
func resolveClassConfig(vars []VariableInfo, idx int, cfg types.AdaptiveDampingConfig) (power, mult float64) {
	name := vars[idx].Name
	class := dampingClass(vars[idx].Param)

	// Per-variable override (highest priority).
	if vc, ok := cfg.Variables[name]; ok {
		power = 1.0
		mult = 1.0
		if vc.SensitivityPower != nil {
			power = *vc.SensitivityPower
		}
		if vc.Multiplier != nil {
			mult = *vc.Multiplier
		}
		return power, mult
	}

	// Class-level config.
	power = 1.0
	mult = 1.0
	if cc, ok := cfg.Classes[class]; ok {
		if cc.SensitivityPower != nil {
			power = *cc.SensitivityPower
		}
		if cc.Multiplier != nil {
			mult = *cc.Multiplier
		}
		return power, mult
	}

	// Built-in defaults.
	if def, ok := defaultClassConfig[class]; ok {
		if def.SensitivityPower != nil {
			power = *def.SensitivityPower
		}
		if def.Multiplier != nil {
			mult = *def.Multiplier
		}
	}
	return power, mult
}

// BuildDiagonal constructs the per-variable damping diagonal D from the
// Hessian diagonal H_jj, variable info, and config. The returned slice has
// length nVars and each element is positive.
func (s *AdaptiveDampingState) BuildDiagonal(
	hDiag []float64,
	vars []VariableInfo,
	cfg types.AdaptiveDampingConfig,
) []float64 {
	n := len(hDiag)

	// Resolve config defaults.
	hFloor := cfg.SensitivityFloor
	if hFloor <= 0 {
		hFloor = 1e-12
	}
	ratioMin := cfg.RatioMin
	if ratioMin <= 0 {
		ratioMin = 1e-3
	}
	ratioMax := cfg.RatioMax
	if ratioMax <= 0 {
		ratioMax = 1e3
	}
	dMin := cfg.DampingMin
	if dMin <= 0 {
		dMin = 1e-4
	}
	dMax := cfg.DampingMax
	if dMax <= 0 {
		dMax = 1e4
	}
	ema := cfg.SensitivityEMA
	if ema < 0 {
		ema = 0
	}
	if ema > 1 {
		ema = 1
	}

	// Step 1: EMA smoothing of sensitivity.
	for j := 0; j < n; j++ {
		h := hDiag[j]
		if h < hFloor {
			h = hFloor
		}
		if s.emaH[j] == 0 {
			s.emaH[j] = h
		} else {
			s.emaH[j] = ema*s.emaH[j] + (1-ema)*h
		}
	}

	// Step 2: Geometric mean reference.
	logSum := 0.0
	for j := 0; j < n; j++ {
		logSum += math.Log(s.emaH[j])
	}
	hRef := math.Exp(logSum / float64(n))
	s.lastRef = hRef

	// Step 3: Sensitivity ratio and diagonal construction.
	for j := 0; j < n; j++ {
		q := s.emaH[j] / hRef
		if q < ratioMin {
			q = ratioMin
		}
		if q > ratioMax {
			q = ratioMax
		}
		s.lastRatio[j] = q

		power, mult := resolveClassConfig(vars, j, cfg)

		d := math.Pow(q, power) * mult * s.localFactor[j]
		if d < dMin {
			d = dMin
		}
		if d > dMax {
			d = dMax
		}
		s.diagonal[j] = d
	}

	return s.diagonal
}

// OnRejected updates localFactor for variables that contributed significantly
// to a rejected step. The contribution is measured by |g_j * delta_j| normalised
// by the total.
func (s *AdaptiveDampingState) OnRejected(
	gradient, step []float64,
	vars []VariableInfo,
	cfg types.AdaptiveDampingConfig,
) {
	n := len(gradient)
	if n == 0 {
		return
	}

	boost := cfg.RejectBoost
	if boost <= 0 {
		boost = 2.0
	}
	threshold := cfg.ContributionThreshold
	if threshold <= 0 {
		threshold = 0.10
	}
	dMax := cfg.DampingMax
	if dMax <= 0 {
		dMax = 1e4
	}

	// Compute total contribution.
	total := 0.0
	for j := 0; j < n; j++ {
		total += math.Abs(gradient[j] * step[j])
	}
	eps := 1e-30

	for j := 0; j < n; j++ {
		contrib := math.Abs(gradient[j]*step[j]) / (total + eps)
		if contrib >= threshold {
			s.localFactor[j] *= boost
			if s.localFactor[j] > dMax {
				s.localFactor[j] = dMax
			}
		}
	}
}

// OnAccepted relaxes localFactor for variables that took a meaningful step.
func (s *AdaptiveDampingState) OnAccepted(
	step []float64,
	vars []VariableInfo,
	cfg types.AdaptiveDampingConfig,
) {
	n := len(step)
	if n == 0 {
		return
	}

	relax := cfg.AcceptRelax
	if relax <= 0 {
		relax = 0.85
	}

	for j := 0; j < n; j++ {
		if math.Abs(step[j]) > 1e-10 {
			s.localFactor[j] *= relax
			if s.localFactor[j] < 1.0 {
				s.localFactor[j] = 1.0
			}
		}
	}
}
