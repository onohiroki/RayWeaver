package constraint

import (
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

// RegionActiveState holds the region-active state for one constraint: the
// active/inactive flag, the Lagrange multiplier, and the hysteresis thresholds.
// Equality constraints are always active (the flag is forced true and never
// toggled).
type RegionActiveState struct {
	// Operand is the original constraint definition (read-only during solve).
	Operand types.ConstraintOperand
	// Active is whether this constraint is currently in the active set.
	// Equality constraints are always true. For inequality constraints the
	// value is toggled by UpdateActiveSet based on hysteresis thresholds.
	Active bool
	// Lambda is the Lagrange multiplier for this constraint (>= 0).
	// Updated only for active constraints.
	Lambda float64
	// Violation is the most recently measured violation (for monitoring/logging).
	Violation float64
}

// RegionActiveDefaults returns the default region-active thresholds.
type RegionActiveDefaults struct {
	EpsActivate   float64
	EpsDeactivate float64
	LambdaStep    float64
	MaxLambda     float64
}

// DefaultRegionActive returns the default threshold values.
func DefaultRegionActive() RegionActiveDefaults {
	return RegionActiveDefaults{
		EpsActivate:   1e-3,
		EpsDeactivate: 1e-4,
		LambdaStep:    1.0,
		MaxLambda:     1e6,
	}
}

// EffectiveDefaults merges user-supplied RegionActiveConfig with defaults.
func EffectiveDefaults(cfg *types.RegionActiveConfig) RegionActiveDefaults {
	d := DefaultRegionActive()
	if cfg == nil {
		return d
	}
	if cfg.EpsActivate > 0 {
		d.EpsActivate = cfg.EpsActivate
	}
	if cfg.EpsDeactivate > 0 {
		d.EpsDeactivate = cfg.EpsDeactivate
	}
	if cfg.LambdaStep > 0 {
		d.LambdaStep = cfg.LambdaStep
	}
	if cfg.MaxLambda > 0 {
		d.MaxLambda = cfg.MaxLambda
	}
	// Enforce eps_activate > eps_deactivate (hysteresis invariant).
	if d.EpsActivate <= d.EpsDeactivate {
		d.EpsActivate = d.EpsDeactivate * 10
	}
	return d
}

// BuildRegionActiveStates constructs the region-active state slice from the
// constraint list. Equality constraints start active (and never toggle).
func BuildRegionActiveStates(constraints []types.ConstraintOperand) []RegionActiveState {
	states := make([]RegionActiveState, len(constraints))
	for i, c := range constraints {
		states[i] = RegionActiveState{
			Operand: c,
			Active:  c.Kind == types.ConstraintEquality, // equality = always active
			Lambda:  0,
		}
	}
	return states
}

// UpdateActiveSet applies the hysteresis-based active-set update and the
// Lagrange multiplier update for each constraint. The caller must provide the
// measured violation for each constraint (via constraint.Evaluate +
// constraint.ComputeError). For equality constraints the active flag is always
// true. For inequality constraints the update rules are:
//
//   - Activate when: NOT active AND violation > epsActivate
//   - Deactivate when: active AND violation < epsDeactivate AND |lambda| small
//
// The Lagrange multiplier is updated for active constraints only:
//
//	lambda <- max(0, lambda + lambdaStep * violation)
//
// The multiplier is capped at maxLambda.
func UpdateActiveSet(states []RegionActiveState, violations []float64, defaults RegionActiveDefaults) {
	if len(states) != len(violations) {
		return
	}
	for i := range states {
		s := &states[i]
		v := violations[i]
		s.Violation = v

		// Equality constraints are always active; never toggle.
		if s.Operand.Kind == types.ConstraintEquality {
			s.Active = true
			// Still update multiplier for equality (if applicable).
			s.Lambda = math.Max(0, s.Lambda+defaults.LambdaStep*v)
			capLambda(s, defaults.MaxLambda)
			continue
		}

		// Hysteresis-based active-set toggle for inequality constraints.
		if !s.Active && v > defaults.EpsActivate {
			s.Active = true
			s.Lambda = 0 // initialise multiplier on activation
		} else if s.Active && v < defaults.EpsDeactivate && math.Abs(s.Lambda) < 1e-6 {
			s.Active = false
			s.Lambda = 0
		}

		// Lagrange multiplier update (active constraints only).
		if s.Active {
			s.Lambda = math.Max(0, s.Lambda+defaults.LambdaStep*v)
			capLambda(s, defaults.MaxLambda)
		}
	}
}

// capLambda clamps the Lagrange multiplier to [0, maxLambda].
func capLambda(s *RegionActiveState, maxLambda float64) {
	if maxLambda > 0 && s.Lambda > maxLambda {
		s.Lambda = maxLambda
	}
}

// ActiveIndices returns the indices of constraints that are currently active.
// Used by the DLS solver to filter the augmented system.
func ActiveIndices(states []RegionActiveState) []int {
	var indices []int
	for i, s := range states {
		if s.Active {
			indices = append(indices, i)
		}
	}
	return indices
}

// LagrangeMultipliers returns the current Lagrange multipliers (length == len(states)).
func LagrangeMultipliers(states []RegionActiveState) []float64 {
	lambdas := make([]float64, len(states))
	for i, s := range states {
		lambdas[i] = s.Lambda
	}
	return lambdas
}

// SetLambdas writes back Lagrange multipliers from the solver into the state.
func SetLambdas(states []RegionActiveState, lambdas []float64) {
	for i := range states {
		if i < len(lambdas) {
			states[i].Lambda = lambdas[i]
		}
	}
}

// ConstraintViolations computes the raw violation (before weighting) for each
// constraint. This is the value passed to UpdateActiveSet.
func ConstraintViolations(constraints []types.ConstraintOperand, surfaces []types.Surface, gc interface{}, numRays int, apertureMargin float64, stopSurface int, pupilZ float64, fieldAngleFn func(types.ConstraintOperand) float64) []float64 {
	violations := make([]float64, len(constraints))
	for i, c := range constraints {
		if !c.Active {
			violations[i] = 0
			continue
		}
		angle := fieldAngleFn(c)
		// The gc parameter is interface{} for import-cycle avoidance; callers
		// must pass a concrete *glass.Catalog via the Optimizer wrapper.
		// Here we only record the violation from ComputeError.
		_ = angle
		_ = surfaces
		_ = numRays
		_ = apertureMargin
		_ = stopSurface
		_ = pupilZ
		// Placeholder: actual violation is computed by the Optimizer and
		// passed directly to this package. See Optimize.UpdateActiveSet.
	}
	return violations
}
