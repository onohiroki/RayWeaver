package optimize

import (
	"fmt"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// Variable is one optimisation variable. It binds one component of the
// variable vector to either:
//   - a single surface parameter of one config (Config, SurfaceID, Param), or
//   - many surface parameters across configs via scale/offset bindings
//     (IsShared + Bindings).
type Variable struct {
	Name         string
	SurfaceID    int // primary surface (for display, snap, etc.)
	GlassName    string
	Param        string
	Min          float64
	Max          float64
	IsShared     bool
	Bindings     []types.SharedVariableBinding
	Config       string
	SurfaceSet   []int   // all surfaces to apply to (for attr/hull); nil → []int{SurfaceID}
	CurrentValue float64 // snapshot of current value for sensitivity tracking
}

// LinkedVariable is a dependent variable whose value is computed from
// another variable during applyVariables. It does not occupy the
// optimisation vector.
type LinkedVariable struct {
	Name        string
	SurfaceID   int
	Param       string
	Config      string
	SourceIndex int // index into the combined independent+linked variable lookup
	Scale       float64
	Offset      float64
}

type VariableState struct {
	Name      string
	Config    string
	SurfaceID int
	GlassName string
	Param     string
	Before    float64
	After     float64
}

// variableSurfaceSet returns the SurfaceSet of the variable at index i, falling
// back to the primary surface so a variable without an element grouping still
// yields a non-empty list.
func (o *Optimizer) variableSurfaceSet(i int) []int {
	if i < 0 || i >= len(o.variables) {
		return nil
	}
	v := o.variables[i]
	if len(v.SurfaceSet) > 0 {
		return v.SurfaceSet
	}
	if v.SurfaceID != 0 {
		return []int{v.SurfaceID}
	}
	return nil
}

// variableBounds returns the bounds variable i should be clamped against:
// the original Min/Max while the glass phase's Min==Max locks are in effect
// (pinnedMin/pinnedMax), the live Min/Max otherwise.
func (o *Optimizer) variableBounds(i int) (lo, hi float64) {
	if o.pinnedMin != nil && len(o.pinnedMin) == len(o.variables) {
		return o.pinnedMin[i], o.pinnedMax[i]
	}
	return o.variables[i].Min, o.variables[i].Max
}

// clampVar keeps v inside [lo, hi].
func clampVar(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (o *Optimizer) unlockVariables() {
	if o.pinnedMin == nil {
		return
	}
	for i := range o.variables {
		if o.pinnedMin[i] == o.pinnedMax[i] && isGlassParam(o.variables[i].Param) {
			continue
		}
		o.variables[i].Min = o.pinnedMin[i]
		o.variables[i].Max = o.pinnedMax[i]
	}
	o.pinnedMin = nil
	o.pinnedMax = nil
}

func (o *Optimizer) Variables() []dls.VariableInfo {
	result := make([]dls.VariableInfo, len(o.variables))
	for i, v := range o.variables {
		var param string
		if v.IsShared {
			if len(v.Bindings) > 0 {
				param = v.Bindings[0].Param
			}
		} else {
			param = v.Param
		}
		result[i] = dls.VariableInfo{
			Name:      v.Name,
			SurfaceID: v.SurfaceID,
			GlassName: v.GlassName,
			Param:     param,
			Min:       v.Min,
			Max:       v.Max,
		}
	}
	return result
}

// applyVariables maps the variable vector x onto the surfaces of every config
// and returns the updated surfaces, a nil glass catalog (the nd/vd targets are
// written as inline model glass on the surfaces themselves, so no override
// catalog is needed), and the per-config virtual-entrance-pupil values with the
// pupil-model variables applied. It is pure: no Optimizer state is mutated, so
// the DLS Jacobian loop can be parallelised.
func (o *Optimizer) applyVariables(x []float64) (map[string][]types.Surface, *glass.Catalog, map[string]appliedPupil) {
	configSurfaces := make(map[string][]types.Surface, len(o.configs))
	for ci := range o.configs {
		cfg := &o.configs[ci]
		s := make([]types.Surface, len(cfg.surfaces))
		for j, src := range cfg.surfaces {
			cp := src
			if src.Coefficients != nil {
				cp.Coefficients = append([]float64(nil), src.Coefficients...)
			}
			if src.Decenter != nil {
				cp.Decenter = append([]types.DecenterStep(nil), src.Decenter...)
			}
			s[j] = cp
		}
		configSurfaces[cfg.id] = s
	}

	pupils := make(map[string]appliedPupil, len(o.configs))
	for ci := range o.configs {
		pupils[o.configs[ci].id] = pupilFromModel(o.configs[ci].pupilModel)
	}

	for vi, v := range o.variables {
		val := x[vi]

		if v.IsShared {
			for _, b := range v.Bindings {
				surfaces, ok := configSurfaces[b.Config]
				if !ok {
					continue
				}
				idx := surfaceIndex(surfaces, b.ID)
				if idx < 0 {
					continue
				}
				scale := b.Scale
				if scale == 0 {
					scale = 1.0
				}
				bound := scale*val + b.Offset
				switch b.Param {
				case "nd", "vd":
					// Inline model glass: the startup pass converted the target
					// keyed surface, so set the material directly. Only the bound
					// surface changes — a sibling sharing the catalogue key keeps
					// its own glass.
					m := &surfaces[idx].Material
					m.Key = ""
					if b.Param == "nd" {
						m.ND = bound
					} else {
						m.VD = bound
					}
				default:
					SetSurfaceParam(&surfaces[idx], b.Param, bound)
				}
			}
			continue
		}

		// Virtual-entrance-pupil variables target the config's pupil model,
		// not a surface (their SurfaceID is unset); apply to the per-call
		// copy before the surface-index lookup.
		switch v.Param {
		case "pupil_model_axial_position":
			if p, ok := pupils[v.Config]; ok {
				// The variable exists to drive the virtual entrance pupil, so
				// mark the per-call pupil active as well: presence must not be
				// inferred from z. A config whose model is absent still gets
				// its grid centred on the variable's value, exactly as before
				// whenever that value was non-zero.
				p.active = true
				p.z = val
				pupils[v.Config] = p
			}
			continue
		case "pupil_model_diameter":
			if p, ok := pupils[v.Config]; ok {
				if val > 0 {
					p.dia = val
				}
				pupils[v.Config] = p
			}
			continue
		}

		surfaces, ok := configSurfaces[v.Config]
		if !ok {
			continue
		}
		idx := surfaceIndex(surfaces, v.SurfaceID)
		if idx < 0 {
			continue
		}

		if ai, ok := AsphereCoefIndex(v.Param); ok {
			for len(surfaces[idx].Coefficients) <= ai {
				surfaces[idx].Coefficients = append(surfaces[idx].Coefficients, 0)
			}
			surfaces[idx].Coefficients[ai] = val
			continue
		}
		switch v.Param {
		case "curvature":
			surfaces[idx].Curvature = val
		case "conic":
			surfaces[idx].Conic = val
		case "thickness":
			surfaces[idx].Thickness = val
		case "diameter":
			surfaces[idx].Diameter = val
		case "radius":
			if val == 0 {
				surfaces[idx].Curvature = 0
			} else {
				surfaces[idx].Curvature = 1.0 / val
			}
		case "nd", "vd":
			// Set the targeted surface as an inline model glass (bindings only):
			// the catalogue key is dropped so the model values win, and a sibling
			// surface sharing the key is left untouched.
			m := &surfaces[idx].Material
			m.Key = ""
			if v.Param == "nd" {
				m.ND = val
			} else {
				m.VD = val
			}
		}
	}

	// Apply linked variables: dependent variables computed from independent
	// (or other linked) variables via scale*source+offset. Linked variables
	// are processed in definition order so that a link can reference another
	// link that appears earlier.
	linkValues := make(map[string]float64, len(o.linkedVariables))
	for _, lk := range o.linkedVariables {
		if lk.SourceIndex < 0 {
			continue
		}
		var sourceVal float64
		if lk.SourceIndex < len(x) {
			sourceVal = x[lk.SourceIndex]
		} else {
			// Source is another linked variable — look up its computed value.
			// Find the source name from varNameIndex.
			for name, idx := range o.varNameIndex {
				if idx == lk.SourceIndex {
					sourceVal = linkValues[name]
					break
				}
			}
		}
		val := lk.Scale*sourceVal + lk.Offset
		linkValues[lk.Name] = val

		// Pupil-model linked variables.
		switch lk.Param {
		case "pupil_model_axial_position":
			if p, ok := pupils[lk.Config]; ok {
				// Same contract as the direct variable: mark the per-call
				// pupil active so z=0 stays a usable position.
				p.active = true
				p.z = val
				pupils[lk.Config] = p
			}
			continue
		case "pupil_model_diameter":
			if p, ok := pupils[lk.Config]; ok {
				if val > 0 {
					p.dia = val
				}
				pupils[lk.Config] = p
			}
			continue
		}

		surfaces, ok := configSurfaces[lk.Config]
		if !ok {
			continue
		}
		idx := surfaceIndex(surfaces, lk.SurfaceID)
		if idx < 0 {
			continue
		}

		if ai, ok := AsphereCoefIndex(lk.Param); ok {
			for len(surfaces[idx].Coefficients) <= ai {
				surfaces[idx].Coefficients = append(surfaces[idx].Coefficients, 0)
			}
			surfaces[idx].Coefficients[ai] = val
			continue
		}
		switch lk.Param {
		case "curvature":
			surfaces[idx].Curvature = val
		case "conic":
			surfaces[idx].Conic = val
		case "thickness":
			surfaces[idx].Thickness = val
		case "diameter":
			surfaces[idx].Diameter = val
		case "radius":
			if val == 0 {
				surfaces[idx].Curvature = 0
			} else {
				surfaces[idx].Curvature = 1.0 / val
			}
		case "nd":
			m := &surfaces[idx].Material
			m.Key = ""
			m.ND = val
		case "vd":
			m := &surfaces[idx].Material
			m.Key = ""
			m.VD = val
		}
	}

	// Power variables drive their element's dependent solve surface to the
	// variable's thin-lens power (paraxial.SolveElementPower). They run after
	// every curvature and glass variable so the solve reads final front
	// curvatures and glass indices, and independent of powerSolveEnabled: a
	// power variable is an ordinary variable in the main phases (so a design
	// can build element power from a zero-power start), and the escape glass
	// phase preserves it by locking the variable (Min==Max), which keeps
	// SolveElementPower reproducing the same curvature every evaluation.
	gc := o.gc
	o.applyPowerVariables(x, configSurfaces, gc)

	// Apply the power-preserving solve after the variables and in-flight glass
	// overrides are in place but before Precompute, so the solved curvature
	// feeds both ParaxialRadius (Precompute) and every downstream consumer.
	// The solve keeps each pinned element's thin-lens power at its initial
	// value, so the pure glass chromatic optimisation cannot drift the layout.
	o.applyPowerSolve(configSurfaces, gc)

	// Size the auto_aperture diameters before the back-focus solve so the solve
	// sees the same beam envelope the merit grid will trace. The evaluators'
	// restoreDiameters + sizeAutoApertures pair then reproduces identical
	// diameters, so only the solve's input (the image plane) changes. Without
	// this the solved plane is the best focus of the stored-diameter bundle and
	// the saved/evaluated plane drifts from the merit's (observed ~0.46 mm on
	// the broad campaign).
	if o.backFocusSolve != nil {
		o.sizeAperturesForBackFocus(configSurfaces, pupils, gc)
	}

	// Apply the back-focus solve after the power-preserving solve so the
	// target surface thickness reflects the final paraxial/spot/wavefront focus.
	// The pupils map carries this evaluation's pupil-model variables, so the
	// solve focuses the same bundle the merit grid just traced.
	o.applyBackFocusSolve(configSurfaces, pupils, gc)

	for ci := range o.configs {
		cfg := &o.configs[ci]
		surface.Precompute(configSurfaces[cfg.id])
	}

	return configSurfaces, nil, pupils
}

// applyPowerVariables applies every "power" variable: the element containing
// the target (solve) surface is re-solved to the variable's thin-lens power
// via paraxial.SolveElementPower, recomputing the dependent surface's
// curvature from the already-applied front curvatures and glass indices.
// Called from applyVariables (pure). Shared variables scale/offset each
// binding like the other params; local variables apply the value directly.
// Surfaces that cannot host a solve (mirrors, non-element surfaces) are
// silently skipped by SolveElementPower, so the variable simply has no
// surface effect there.
func (o *Optimizer) applyPowerVariables(x []float64, configSurfaces map[string][]types.Surface, gc *glass.Catalog) {
	for vi := range o.variables {
		v := &o.variables[vi]
		if v.Param != "power" {
			continue
		}
		val := x[vi]
		if v.IsShared {
			for _, b := range v.Bindings {
				surfaces, ok := configSurfaces[b.Config]
				if !ok {
					continue
				}
				scale := b.Scale
				if scale == 0 {
					scale = 1.0
				}
				paraxial.SolveElementPower(surfaces, gc, b.ID, scale*val+b.Offset)
			}
			continue
		}
		surfaces, ok := configSurfaces[v.Config]
		if !ok {
			continue
		}
		paraxial.SolveElementPower(surfaces, gc, v.SurfaceID, val)
	}
}

// SetSurfaceParam sets a single surface parameter by name.
func SetSurfaceParam(s *types.Surface, param string, val float64) {
	if ai, ok := AsphereCoefIndex(param); ok {
		for len(s.Coefficients) <= ai {
			s.Coefficients = append(s.Coefficients, 0)
		}
		s.Coefficients[ai] = val
		return
	}
	switch param {
	case "curvature":
		s.Curvature = val
	case "conic":
		s.Conic = val
	case "thickness":
		s.Thickness = val
	case "diameter":
		s.Diameter = val
	case "radius":
		if val == 0 {
			s.Curvature = 0
		} else {
			s.Curvature = 1.0 / val
		}
	}
}

func getSurfaceParam(s types.Surface, param string) float64 {
	if ai, ok := AsphereCoefIndex(param); ok {
		if ai < len(s.Coefficients) {
			return s.Coefficients[ai]
		}
		return 0
	}
	switch param {
	case "curvature":
		return s.Curvature
	case "conic":
		return s.Conic
	case "thickness":
		return s.Thickness
	case "diameter":
		return s.Diameter
	case "radius":
		return s.Radius()
	}
	return 0
}

func (o *Optimizer) buildVariableStates(x []float64) []VariableState {
	states := make([]VariableState, len(o.variables))
	for i, v := range o.variables {
		st := VariableState{
			Name:   v.Name,
			Config: v.Config,
			After:  x[i],
		}

		if v.IsShared {
			if len(v.Bindings) > 0 {
				st.SurfaceID = v.Bindings[0].ID
				st.Param = v.Bindings[0].Param
			}
			for _, cfg := range o.configs {
				for _, b := range v.Bindings {
					if b.Config == cfg.id {
						if idx := surfaceIndex(cfg.surfaces, b.ID); idx >= 0 {
							st.Before = getSurfaceParam(cfg.surfaces[idx], b.Param)
						}
						break
					}
				}
				if st.Before != 0 {
					break
				}
			}
		} else {
			st.SurfaceID = v.SurfaceID
			st.Param = v.Param
			st.GlassName = v.GlassName
			if v.Param == "pupil_model_axial_position" || v.Param == "pupil_model_diameter" {
				if cfg := findConfigByID(o.configs, v.Config); cfg != nil && cfg.pupilModel != nil {
					if v.Param == "pupil_model_diameter" {
						st.Before = cfg.pupilModel.Diameter
					} else {
						st.Before = cfg.pupilModel.AxialPosition
					}
				}
			} else if cfg := findConfigByID(o.configs, v.Config); cfg != nil {
				if idx := surfaceIndex(cfg.surfaces, v.SurfaceID); idx >= 0 {
					if v.Param == "power" {
						st.Before = paraxial.ElementPowerCurvature(cfg.surfaces, o.gc, v.SurfaceID)
					} else if v.Param == "nd" || v.Param == "vd" {
						m := cfg.surfaces[idx].Material
						switch {
						case m.HasModel() && !m.HasKey():
							if v.Param == "nd" {
								st.Before = m.ND
							} else {
								st.Before = m.VD
							}
						case m.HasKey() && o.gc != nil:
							if g, ok := o.gc.Lookup(m.Key); ok {
								if v.Param == "nd" {
									st.Before = g.ND
								} else {
									st.Before = g.VD
								}
							}
						}
					} else {
						st.Before = getSurfaceParam(cfg.surfaces[idx], v.Param)
					}
				}
			}
		}

		states[i] = st
	}
	return states
}

func surfaceIndex(surfaces []types.Surface, id int) int {
	for i, s := range surfaces {
		if s.ID == id {
			return i
		}
	}
	return -1
}

func cfgSurfKey(configID string, surfID int) string {
	return configID + ":" + fmt.Sprint(surfID)
}
