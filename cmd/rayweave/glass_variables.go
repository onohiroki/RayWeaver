package main

import (
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/glass"
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
		cfg := &types.PowerSolveConfig{Enabled: true, Surfaces: parseCommaIntList(flagSurfaces, "power-solve-surfaces")}
		if yamlCfg != nil {
			cfg.ColorScale = yamlCfg.ColorScale
		}
		return cfg
	}

	if flagPowerSolve {
		if yamlCfg != nil && len(yamlCfg.Surfaces) > 0 {
			return &types.PowerSolveConfig{Enabled: true, Surfaces: yamlCfg.Surfaces, ColorScale: yamlCfg.ColorScale}
		}
		errOut("Error: --power-solve requires power-solve surfaces (e.g. --power-solve-surfaces 2,5,8 or optimization.power_solve.surfaces)")
	}

	if yamlCfg != nil && yamlCfg.Enabled {
		return yamlCfg
	}
	return nil
}

// hasGlassVariable reports whether any active optimization variable targets a
// glass dispersion (nd/vd), across every variable container (single-config
// optimization.variables, and multi-config shared/local variables). The
// power-preserving glass phase only has degrees of freedom to move when such a
// variable exists; without one the phase would lock every variable and run a
// no-op DLS, so escape skips it entirely.
func hasGlassVariable(input *types.Input) bool {
	for _, v := range input.Optimization.Variables {
		if v.Active && (v.Target.Param == "nd" || v.Target.Param == "vd") {
			return true
		}
	}
	for _, v := range input.Optimization.LocalVariables {
		if v.Active && (v.Target.Param == "nd" || v.Target.Param == "vd") {
			return true
		}
	}
	for _, s := range input.Optimization.SharedVariables {
		if !s.Active {
			continue
		}
		for _, b := range s.Bindings {
			if b.Param == "nd" || b.Param == "vd" {
				return true
			}
		}
	}
	return false
}

// applyGlassVariables auto-generates nd/vd optimization variables for every
// refractive lens element (one representative surface each). It is the
// convenience layer for `rayweave optimize --glass-variables` /
// `rayweave escape --glass-variables`, so the user can make the glasses
// optimizable without handwriting the variable list.
//
// It deliberately does NOT touch the merit: the escape glass phase derives its
// objective from the config's own terms (colour scaled by
// power_solve.color_scale plus a cheap geometric guardrail), so an explicit
// merit is always preserved instead of being silently replaced.
func applyGlassVariables(input *types.Input, gc *glass.Catalog) {
	if len(input.Configs) == 0 {
		errOut("Error: --glass-variables requires at least one config (or chief.fields + surfaces)")
	}
	vars := buildGlassVariables(input, gc)
	if len(vars) == 0 {
		errOut("Error: --glass-variables found no refractive lens elements to optimize")
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

// buildGlassVariables returns an nd/vd variable pair per refractive lens
// element (one representative glass surface each), derived from the element
// grouping shared with paraxial.GlassRoles.
func buildGlassVariables(input *types.Input, gc *glass.Catalog) []types.OptimizationVariable {
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
