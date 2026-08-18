package main

import (
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/types"
)

// effectivePowerSolve resolves the power-preserving solve setting under the
// CLI/YAML precedence rule (CLI wins, YAML otherwise), and returns the
// effective config that is echoed into the output. A nil result disables the
// solve.
//
//	--power-solve-surfaces A,B,C            -> surfaces = A,B,C, enabled
//	--power-solve alone + YAML surfaces     -> YAML surfaces, enabled
//	--power-solve alone, no YAML surfaces   -> error (nothing to pin)
//	no flag                                 -> YAML power_solve as-is
func effectivePowerSolve(input types.Input, flagPowerSolve bool, flagSurfaces string) *types.PowerSolveConfig {
	var yamlCfg *types.PowerSolveConfig
	if input.Optimization != nil {
		yamlCfg = input.Optimization.PowerSolve
	}

	if flagSurfaces != "" {
		return &types.PowerSolveConfig{Enabled: true, Surfaces: parseCommaIntList(flagSurfaces, "power-solve-surfaces")}
	}

	if flagPowerSolve {
		if yamlCfg != nil && len(yamlCfg.Surfaces) > 0 {
			return &types.PowerSolveConfig{Enabled: true, Surfaces: yamlCfg.Surfaces}
		}
		errOut("Error: --power-solve requires power-solve surfaces (e.g. --power-solve-surfaces 2,5,8 or optimization.power_solve.surfaces)")
	}

	if yamlCfg != nil && yamlCfg.Enabled {
		return yamlCfg
	}
	return nil
}

// applyGlassColor auto-generates a glass-only chromatic optimisation inside
// input: nd/vd variables for every refractive lens element and a per-config
// merit of only longitudinal_color + lateral_color. It is the convenience layer
// for `rayweave optimize --glass-color`, so the user concentrates on the glass
// swap (= colour correction) without handwriting the merit and variable lists.
func applyGlassColor(input *types.Input, gc *glass.Catalog) {
	// Color endpoints for the chromatic merit terms, from each config's own
	// wavelength list (widest separation) with standard g/C defaults as the
	// fallback (matching the glass-optimize demo).
	const (
		defWL1 = 0.0004358 // g
		defWL2 = 0.0006563 // C
	)

	vars := buildGlassColorVariables(input, gc)
	if len(vars) == 0 {
		errOut("Error: --glass-color found no refractive lens elements to optimize")
	}

	if len(input.Configs) == 0 {
		errOut("Error: --glass-color requires at least one config (or chief.fields + surfaces)")
	}

	for ci := range input.Configs {
		cfg := &input.Configs[ci]
		if !cfg.Active {
			continue
		}
		wl1, wl2 := colorEndpoints(cfg.Wavelengths, defWL1, defWL2)
		fields := cfg.Fields
		if len(fields) == 0 && input.Chief != nil {
			for fi, f := range input.Chief.Fields {
				fields = append(fields, types.FieldItem{ID: fi, AngleDeg: f.Angle, Weight: 1.0})
			}
		}
		merit := buildColorMerit(fields, wl1, wl2)
		cfg.Merit = &merit
	}

	// Single-config YAML expresses variables as optimization.variables; a
	// multi-config run uses local_variables. Choose by the number of active
	// configs, matching the normal config-building rule.
	cfgID := ""
	multi := false
	for _, cc := range input.Configs {
		if !cc.Active {
			continue
		}
		if cfgID != "" {
			multi = true
			break
		}
		cfgID = cc.ID
	}

	if multi {
		input.Optimization.LocalVariables = append(input.Optimization.LocalVariables, varsToLocal(vars)...)
	} else {
		input.Optimization.Variables = append(input.Optimization.Variables, vars...)
	}
}

// buildGlassColorVariables returns an nd/vd variable pair per refractive lens
// element (one representative glass surface each), derived from the element
// grouping shared with paraxial.GlassRoles.
func buildGlassColorVariables(input *types.Input, gc *glass.Catalog) []types.OptimizationVariable {
	var out []types.OptimizationVariable
	for i := range input.Configs {
		cfg := &input.Configs[i]
		if !cfg.Active {
			continue
		}
		for _, role := range paraxial.GlassRoles(cfg.Surfaces, gc) {
			if len(role.SurfaceIDs) == 0 {
				continue
			}
			id := role.SurfaceIDs[0]
			base := cfg.ID
			if base == "" {
				base = "config1"
			}
			out = append(out,
				types.OptimizationVariable{
					Name:   "s" + strconv.Itoa(id) + "_nd",
					Target: types.VariableTarget{Type: "surface", Config: base, ID: id, Param: "nd"},
					Min:    1.4, Max: 2.0, Active: true,
				},
				types.OptimizationVariable{
					Name:   "s" + strconv.Itoa(id) + "_vd",
					Target: types.VariableTarget{Type: "surface", Config: base, ID: id, Param: "vd"},
					Min:    20.0, Max: 90.0, Active: true,
				},
			)
		}
	}
	return out
}

func varsToLocal(vars []types.OptimizationVariable) []types.LocalVariableDef {
	out := make([]types.LocalVariableDef, 0, len(vars))
	for _, v := range vars {
		out = append(out, types.LocalVariableDef{
			Name: v.Name, Config: v.Target.Config, Target: v.Target,
			Min: v.Min, Max: v.Max, Active: v.Active,
		})
	}
	return out
}

// colorEndpoints returns the widest-separated pair of distinct wavelengths
// from the config's list (for the F/C or g/C chromatic terms), falling back to
// defWL1/defWL2 when fewer than two distinct values are present.
func colorEndpoints(wls []types.WavelengthItem, defWL1, defWL2 float64) (float64, float64) {
	if len(wls) < 2 {
		return defWL1, defWL2
	}
	lo, hi := wls[0].Value, wls[0].Value
	for _, w := range wls[1:] {
		if w.Value < lo {
			lo = w.Value
		}
		if w.Value > hi {
			hi = w.Value
		}
	}
	if hi-lo < 1e-12 {
		return defWL1, defWL2
	}
	return lo, hi
}

// buildColorMerit builds a merit function of only longitudinal_color (one,
// field-independent) and lateral_color (per off-axis field).
func buildColorMerit(fields []types.FieldItem, wl1, wl2 float64) types.MeritFunction {
	terms := []types.MeritTerm{
		{Kind: optimize.MeritLongitudinalColor, Wavelength: wl1, Wavelength2: wl2, Weight: 1.0},
	}
	for _, f := range fields {
		if f.AngleDeg == 0 {
			continue
		}
		terms = append(terms, types.MeritTerm{
			Kind: optimize.MeritLateralColor, Field: f.ID, Wavelength: wl1, Wavelength2: wl2, Weight: 1.0,
		})
	}
	return types.MeritFunction{Type: "sum", Terms: terms}
}

func parseCommaIntList(s, what string) []int {
	var out []int
	for _, tok := range strings.Split(s, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(tok))
		if err != nil {
			errOut("Error: invalid %s %q", what, tok)
		}
		out = append(out, v)
	}
	return out
}
