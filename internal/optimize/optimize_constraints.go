package optimize

import (
	"math"

	"github.com/hiroki/rayweaver/internal/constraint"
	"github.com/hiroki/rayweaver/internal/types"
)

// buildRegionActiveStates initialises the region-active state from the
// configuration. Called once from newOptimizer when region_active is enabled.
func (o *Optimizer) buildRegionActiveStates(raCfg *types.RegionActiveConfig) {
	o.regionActiveCfg = raCfg
	o.regionActive = make([]*regionActiveState, len(o.configs))
	for ci := range o.configs {
		cfg := &o.configs[ci]
		states := constraint.BuildRegionActiveStates(cfg.constraints)
		o.regionActive[ci] = &regionActiveState{
			configID: cfg.id,
			states:   states,
		}
	}
}

// UpdateRegionActiveSet implements dls.RegionActiveUpdater. It evaluates all
// constraints at the current x and applies the hysteresis-based active-set
// update and Lagrange multiplier update. Called once per DLS iteration at the
// current x, so the active set is frozen for the rest of the iteration.
func (o *Optimizer) UpdateRegionActiveSet(x []float64) {
	if o.regionActiveCfg == nil || len(o.regionActive) == 0 {
		return
	}
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)
	defaults := constraint.EffectiveDefaults(o.regionActiveCfg)

	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		ras := o.regionActive[ci]
		if ras == nil {
			continue
		}

		// Compute violations for each constraint.
		violations := make([]float64, len(cfg.constraints))
		p := pupils[cfg.id]
		for i, c := range cfg.constraints {
			if !c.Active {
				violations[i] = 0
				continue
			}
			angle := o.constraintFieldAngle(cfg, c, surfaces, gc)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.stopSurface, o.gridCentring(cfg, p, angle), p.dia)
			err := constraint.ComputeError(c.Kind, value, c)
			violations[i] = err // raw violation (before weighting)
		}

		// Update active set with hysteresis and Lagrange multipliers.
		constraint.UpdateActiveSet(ras.states, violations, defaults)
	}
}

// ActiveConstraintIndices implements dls.ActiveConstraintIndices. It returns
// the flattened indices of constraints that are currently in the active set.
func (o *Optimizer) ActiveConstraintIndices() []int {
	if o.regionActiveCfg == nil || len(o.regionActive) == 0 {
		return nil // nil = all constraints active (legacy)
	}
	var indices []int
	offset := 0
	for ci := range o.configs {
		cfg := &o.configs[ci]
		ras := o.regionActive[ci]
		if ras == nil {
			// No region-active state for this config: all constraints active.
			for i := range cfg.constraints {
				if cfg.constraints[i].Active {
					indices = append(indices, offset+i)
				}
			}
			offset += len(cfg.constraints)
			continue
		}
		for i, s := range ras.states {
			if s.Active {
				indices = append(indices, offset+i)
			}
		}
		offset += len(cfg.constraints)
	}
	return indices
}

// ConstraintMultipliers implements dls.ConstraintMultipliers. It returns the
// flattened Lagrange multipliers for all constraints (active and inactive).
func (o *Optimizer) ConstraintMultipliers() []float64 {
	if o.regionActiveCfg == nil || len(o.regionActive) == 0 {
		return nil
	}
	var lambdas []float64
	for ci := range o.configs {
		ras := o.regionActive[ci]
		if ras == nil {
			cfg := &o.configs[ci]
			for range cfg.constraints {
				lambdas = append(lambdas, 0)
			}
			continue
		}
		lambdas = append(lambdas, constraint.LagrangeMultipliers(ras.states)...)
	}
	return lambdas
}

// SetConstraintMultipliers implements dls.ConstraintMultipliers setter. It
// writes back the Lagrange multipliers from the solver into the region-active
// state. Called after every accepted DLS step.
func (o *Optimizer) SetConstraintMultipliers(lambdas []float64) {
	if o.regionActiveCfg == nil || len(o.regionActive) == 0 {
		return
	}
	offset := 0
	for ci := range o.configs {
		ras := o.regionActive[ci]
		if ras == nil {
			cfg := &o.configs[ci]
			offset += len(cfg.constraints)
			continue
		}
		n := len(ras.states)
		if offset+n <= len(lambdas) {
			constraint.SetLambdas(ras.states, lambdas[offset:offset+n])
		}
		offset += n
	}
}

func (o *Optimizer) ComputeConstraints(x []float64) []float64 {
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var allC []float64
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		p := pupils[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, nil, p)

		for _, c := range cfg.constraints {
			if !c.Active {
				allC = append(allC, 0)
				continue
			}
			angle := o.constraintFieldAngle(cfg, c, surfaces, gc)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.stopSurface, o.gridCentring(cfg, p, angle), p.dia)
			err := constraint.ComputeError(c.Kind, value, c)
			w := c.Weight
			if w <= 0 {
				w = 1.0
			}
			allC = append(allC, math.Sqrt(w)*err)
		}
	}
	return allC
}

// HasConstraints reports whether any config has active constraints. When false,
// ComputeConstraints can be skipped (it would return an empty slice anyway).
func (o *Optimizer) HasConstraints() bool {
	for ci := range o.configs {
		for _, c := range o.configs[ci].constraints {
			if c.Active {
				return true
			}
		}
	}
	return false
}

// ConstraintViolation reports an active constraint whose weighted residual
// magnitude exceeds a tolerance at the final state.
type ConstraintViolation struct {
	ID       string
	Config   string
	Kind     string
	Measure  string
	Residual float64
}

// FinalConstraintViolations evaluates the active constraints at x and returns
// those whose weighted residual magnitude exceeds tol.
func (o *Optimizer) FinalConstraintViolations(x []float64, tol float64) []ConstraintViolation {
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var out []ConstraintViolation
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		p := pupils[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, nil, p)

		for _, c := range cfg.constraints {
			if !c.Active {
				continue
			}
			angle := o.constraintFieldAngle(cfg, c, surfaces, gc)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.stopSurface, o.gridCentring(cfg, p, angle), p.dia)
			err := constraint.ComputeError(c.Kind, value, c)
			w := c.Weight
			if w <= 0 {
				w = 1.0
			}
			residual := math.Sqrt(w) * err
			if math.Abs(residual) > tol {
				out = append(out, ConstraintViolation{
					ID:       c.ID,
					Config:   cfg.id,
					Kind:     string(c.Kind),
					Measure:  string(c.Measure),
					Residual: residual,
				})
			}
		}
	}
	return out
}

// FeasibilityConstraintViolations reports the active constraints violated
// beyond the escape feasibility tolerance at x. Unlike FinalConstraintViolations
// (absolute weighted residual, used by the optimize command's final report),
// feasibility uses a scale-free rule: a constraint is violated when its
// unweighted error exceeds 1% of its natural scale (target for equality,
// bound for inequality/band). This is what escape feasibility applies — the
// recorded merit does not include constraints, so feasibility is the only
// place they are enforced. The weight is deliberately not involved: it is a
// DLS priority knob, not a requirement knob.
func (o *Optimizer) FeasibilityConstraintViolations(x []float64) []ConstraintViolation {
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var out []ConstraintViolation
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		p := pupils[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, nil, p)

		for _, c := range cfg.constraints {
			if !c.Active {
				continue
			}
			angle := o.constraintFieldAngle(cfg, c, surfaces, gc)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.stopSurface, o.gridCentring(cfg, p, angle), p.dia)
			err := constraint.ComputeError(c.Kind, value, c)
			scale := constraint.Scale(c)
			if scale <= 0 {
				scale = 1
			}
			if math.Abs(err)/scale > 0.01 {
				out = append(out, ConstraintViolation{
					ID:       c.ID,
					Config:   cfg.id,
					Kind:     string(c.Kind),
					Measure:  string(c.Measure),
					Residual: err,
				})
			}
		}
	}
	return out
}

// FinalConstraintViolations it reports all constraints, not just the violated
// ones, so callers (e.g. the optimize command) can record the actual value the
// constraint reached (for example the final vignetting factor).
func (o *Optimizer) FinalConstraintMeasurements(x []float64) []types.ConstraintMeasurement {
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var out []types.ConstraintMeasurement
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		p := pupils[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, nil, p)

		for _, c := range cfg.constraints {
			if !c.Active {
				continue
			}
			angle := o.constraintFieldAngle(cfg, c, surfaces, gc)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.stopSurface, o.gridCentring(cfg, p, angle), p.dia)
			err := constraint.ComputeError(c.Kind, value, c)
			w := c.Weight
			if w <= 0 {
				w = 1.0
			}
			out = append(out, types.ConstraintMeasurement{
				ID:       c.ID,
				Config:   cfg.id,
				Kind:     string(c.Kind),
				Measure:  string(c.Measure),
				Field:    c.Field,
				Value:    value,
				Residual: math.Sqrt(w) * err,
			})
		}
	}
	return out
}
