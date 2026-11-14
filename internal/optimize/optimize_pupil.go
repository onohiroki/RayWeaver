package optimize

import (
	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// resolvePupilZ returns the pupil Z used to centre grid traces for a config:
// the explicit stop surface Z when one is set, otherwise the caller-provided
// dynamic pupil Z. The stop surface is never inferred.
func resolvePupilZ(surfaces []types.Surface, stopSurface int, pupilZ float64) float64 {
	if stopSurface > 0 {
		sc := append([]types.Surface{}, surfaces...)
		surface.Precompute(sc)
		for _, s := range sc {
			if s.ID == stopSurface {
				return s.PhysicalZ
			}
		}
	}
	return pupilZ
}

// seedPupilResolved seeds config.pupilResolved at construction, before the
// first UpdatePupils. An explicit stop and a virtual entrance pupil model are
// structural guarantees — the virtual check comes first because such a pupil's
// axial_position may legitimately be exactly 0 mm, which is the whole point of
// this flag. What remains is chief's dynamic-pupil probe, which reports both
// "failed" and "resolved onto 0 mm" as a zero result; the failure keeps the
// historical no-pupil launch, and the other is corrected the moment the first
// iteration resolves a pupil.
func seedPupilResolved(stopSurface int, pupilZ float64, pupilModel *types.PupilModelConfig) bool {
	if pupilModel != nil && pupilModel.Mode == "virtual_entrance_pupil" {
		return true
	}
	return stopSurface > 0 || pupilZ != 0
}

// UpdatePupils re-derives each config's dynamic entrance pupil at the current
// variable state x and stores it as the grid centring for the rest of the DLS
// iteration (the solver calls it once per iteration). The pupil therefore
// follows the lens during optimisation — the aperture position moves — while
// staying frozen within one iteration so the base-point and Jacobian residual
// evaluations share the same grid centring.
func (o *Optimizer) UpdatePupils(x []float64) {
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)
	o.updateGlassRoles(configSurfaces, gc)
	pol := types.NewCircularJones(true)

	for ci := range o.configs {
		cfg := &o.configs[ci]
		if cfg.refSurface <= 0 || len(cfg.fieldDefs) == 0 {
			continue
		}
		surfaces := configSurfaces[cfg.id]

		// Virtual entrance pupil mode: use the model's axial_position/diameter
		// (with the pupil-model variables applied) directly, skip the
		// chief-ray dynamic-pupil iteration.
		if cfg.pupilModel != nil && cfg.pupilModel.Mode == "virtual_entrance_pupil" {
			p := pupils[cfg.id]
			if cfg.pupilZs == nil {
				cfg.pupilZs = make(map[float64]float64)
			}
			for _, fd := range cfg.fieldDefs {
				cfg.pupilZs[fd.Angle] = p.z
			}
			// The mode, not the value, says a pupil is in use: a virtual
			// entrance pupil at Z=0 is resolved like any other position.
			cfg.pupilZ = p.z
			cfg.pupilResolved = true
			continue
		}

		wl := effectiveReferenceWavelength(cfg.referenceWavelength)
		// An explicit stop defines the pupil as its paraxial image, and every
		// field shares that plane. Using the per-field dynamic crossing instead
		// moved the launch centre for off-axis fields, so the optimiser's grid
		// disagreed with chief / zoom on the same design (a wide field read
		// ~0.23 there and 0.90 in zoom). Keep the stop's paraxial pupil for all
		// fields; a stop-free system keeps the dynamic per-field pupil.
		if cfg.stopSurface > 0 {
			if z, ok := chief.StopEntrancePupilZ(surfaces, cfg.stopSurface, wl, gc); ok {
				if cfg.pupilZs == nil {
					cfg.pupilZs = make(map[float64]float64)
				}
				for _, fd := range cfg.fieldDefs {
					cfg.pupilZs[fd.Angle] = z
				}
				cfg.pupilZ = z
				cfg.pupilResolved = true
				continue
			}
		}

		results := chief.DetermineChiefRaysGridMode(
			types.System{Surfaces: surfaces, StopSurface: cfg.stopSurface},
			cfg.fieldDefs, cfg.refSurface, o.numRays, gc, pol,
			wl, false, types.GridPolar, nil, nil, nil, nil, 0, 0,
			cfg.rayDefinition,
		)
		for i, r := range results {
			if r.EntrancePupil == nil {
				continue
			}
			if cfg.pupilZs == nil {
				cfg.pupilZs = make(map[float64]float64)
			}
			if i < len(cfg.fieldDefs) {
				cfg.pupilZs[cfg.fieldDefs[i].Angle] = r.EntrancePupil.Center.Z
			}
			if i == 0 {
				cfg.pupilZ = r.EntrancePupil.Center.Z
				// The probe reported a pupil: resolved even when it sits at
				// exactly 0 mm (a value must never decide presence).
				cfg.pupilResolved = true
			}
		}
	}
}

// appliedPupil carries the per-evaluation virtual-entrance-pupil values.
// Pupil-model variables are applied to a per-call copy (applyVariables stays
// pure, so parallel Jacobian columns never race on shared state); the grid
// traces read these values instead of the frozen Optimizer state so the
// pupil position/diameter follow the variables with consistent derivatives.
type appliedPupil struct {
	// active reports whether the virtual entrance pupil is in use for this
	// evaluation. It is derived from the model's mode, never from z: 0 mm is
	// a perfectly valid pupil position (and the -20..+50 sweep crosses it),
	// so z must never be used as an "unset" sentinel.
	active bool
	z      float64 // entrance-pupil Z (virtual mode); valid only when active
	dia    float64 // entrance-pupil diameter EPD override; 0 = standard paraxial sizing
}

// pupilFromModel extracts the per-call pupil values from a model config.
func pupilFromModel(m *types.PupilModelConfig) appliedPupil {
	var p appliedPupil
	if m != nil && m.Mode == "virtual_entrance_pupil" {
		p.active = true
		p.z = m.AxialPosition
		if m.Diameter > 0 {
			p.dia = m.Diameter
		}
	}
	return p
}

// gridCentring resolves the entrance-pupil Z for one grid trace: the per-call
// virtual-pupil position when active (it follows the axial_position variable
// exactly — no dynamic-pupil noise — so derivatives stay consistent), else the
// frozen per-iteration dynamic pupil (per-field override when resolved).
func (o *Optimizer) gridCentring(cfg *config, p appliedPupil, angle float64) float64 {
	if p.active {
		return p.z
	}
	z := cfg.pupilZ
	if cfg.pupilZs != nil {
		if v, ok := cfg.pupilZs[angle]; ok {
			z = v
		}
	}
	return z
}

// FinalPupilModels returns the per-config virtual entrance pupil after
// applying x: a copy of each config's pupil model with the pupil-model
// variables' values applied. Entries exist only for configs that carry a
// pupil model, so callers can write the optimised pupil back into the
// pipeline output.
func (o *Optimizer) FinalPupilModels(x []float64) map[string]types.PupilModelConfig {
	_, _, pupils := o.applyVariables(x)
	out := make(map[string]types.PupilModelConfig, len(pupils))
	for id, p := range pupils {
		cfg := o.findConfig(id)
		if cfg == nil || cfg.pupilModel == nil {
			// No virtual entrance pupil in use for this config: writing a
			// default (all-zero) model back would break a subsequent trace.
			continue
		}
		m := *cfg.pupilModel
		if p.active {
			m.AxialPosition = p.z
		}
		if p.dia > 0 {
			m.Diameter = p.dia
		}
		out[id] = m
	}
	return out
}
