package main

import (
	"fmt"
	"os"
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
		os.Exit(1)
	}

	// A multi-config (zoom) run keeps one shared nd/vd pair per physical element,
	// bound to that element's surface in every active config, so the glass is a
	// single degree of freedom shared across zoom positions rather than one per
	// position. A single-config run keeps the optimization.variables form.
	if activeConfigCount(input) > 1 {
		shared := buildSharedGlassVariables(input, gc)
		if len(shared) == 0 {
			errOut("Error: --glass-variables found no refractive lens elements to optimize")
			os.Exit(1)
		}
		input.Optimization.SharedVariables = append(input.Optimization.SharedVariables, shared...)
		return
	}

	vars := buildGlassVariables(input, gc)
	if len(vars) == 0 {
		errOut("Error: --glass-variables found no refractive lens elements to optimize")
		os.Exit(1)
	}
	input.Optimization.Variables = append(input.Optimization.Variables, vars...)
}

// activeConfigCount returns the number of active configs.
func activeConfigCount(input *types.Input) int {
	n := 0
	for i := range input.Configs {
		if input.Configs[i].Active {
			n++
		}
	}
	return n
}

// buildSharedGlassVariables returns one shared nd/vd variable pair per physical
// lens element (a representative surface ID), bound to that surface in every
// active config that carries glass there. Every active config contributes its
// elements, not just the first: a zoom whose positions number their surfaces
// differently, or give a position an extra element, otherwise left that element
// out of the optimisation without a word. An element that is missing (or air)
// in some active config binds only the configs where it exists — the glass
// cannot be shared with a surface that is not there — and that partial binding
// is reported on stderr.
func buildSharedGlassVariables(input *types.Input, gc *glass.Catalog) []types.SharedVariable {
	var active []int
	for i := range input.Configs {
		if input.Configs[i].Active {
			active = append(active, i)
		}
	}
	if len(active) == 0 {
		return nil
	}

	// Union of the element representative IDs over every active config, in
	// first-seen order, so a shared layout keeps the natural element order.
	var ids []int
	seen := make(map[int]bool)
	for _, ci := range active {
		for _, role := range paraxial.GlassRoles(input.Configs[ci].Surfaces, gc) {
			if len(role.SurfaceIDs) == 0 {
				continue
			}
			if id := role.SurfaceIDs[0]; !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}

	var out []types.SharedVariable
	for _, id := range ids {
		var bindings []types.SharedVariableBinding
		bound := make(map[string][]string) // param -> binding configs
		var missing []string
		for _, ci := range active {
			cfg := &input.Configs[ci]
			if !surfaceHasGlassID(cfg.Surfaces, id) {
				missing = append(missing, cfg.ID)
				continue
			}
			for _, param := range []string{"nd", "vd"} {
				bindings = append(bindings, types.SharedVariableBinding{Config: cfg.ID, ID: id, Param: param})
				bound[param] = append(bound[param], cfg.ID)
			}
		}
		if len(bound["nd"]) == 0 {
			continue
		}
		if len(missing) > 0 {
			errOut("Warning: glass element on surface %d is not glass in %s, so its shared nd/vd variable binds only %s",
				id, strings.Join(missing, ", "), strings.Join(bound["nd"], ", "))
		}
		out = append(out,
			types.SharedVariable{Name: fmt.Sprintf("s%d_nd", id), Min: 1.4, Max: 2.0, Active: true, Bindings: keepParam(bindings, "nd")},
			types.SharedVariable{Name: fmt.Sprintf("s%d_vd", id), Min: 20.0, Max: 90.0, Active: true, Bindings: keepParam(bindings, "vd")},
		)
	}
	return out
}

// keepParam returns the bindings whose param matches p.
func keepParam(in []types.SharedVariableBinding, p string) []types.SharedVariableBinding {
	var out []types.SharedVariableBinding
	for _, b := range in {
		if b.Param == p {
			out = append(out, b)
		}
	}
	return out
}

// surfaceHasGlassID reports whether the surface list contains a refractive
// surface with the given ID.
func surfaceHasGlassID(surfaces []types.Surface, id int) bool {
	for i := range surfaces {
		if surfaces[i].ID == id {
			return !surfaces[i].Material.IsAir()
		}
	}
	return false
}

// validateSharedGlassVariables rejects a shared nd/vd variable whose bindings
// mix glass and non-glass params, mix nd with vd, or start from different
// glasses — cases where "one value for every binding" is ill-defined.
func validateSharedGlassVariables(input *types.Input, gc *glass.Catalog) error {
	if input.Optimization == nil {
		return nil
	}
	for _, sv := range input.Optimization.SharedVariables {
		if !sv.Active || len(sv.Bindings) == 0 {
			continue
		}
		glassParam := ""
		var sig, sigWhere string
		for _, b := range sv.Bindings {
			isGlass := b.Param == "nd" || b.Param == "vd"
			if !isGlass {
				if glassParam != "" {
					return fmt.Errorf("shared variable %q mixes glass (nd/vd) and non-glass bindings", sv.Name)
				}
				continue
			}
			if glassParam == "" {
				glassParam = b.Param
			} else if b.Param != glassParam {
				return fmt.Errorf("shared variable %q mixes nd and vd bindings", sv.Name)
			}
			cfg := findInputConfig(input, b.Config)
			if cfg == nil {
				continue
			}
			s := initialGlassSignature(cfg, b.ID, gc)
			where := fmt.Sprintf("%s/s%d=%s", b.Config, b.ID, s)
			if sig == "" {
				sig, sigWhere = s, where
			} else if s != sig {
				return fmt.Errorf("shared variable %q binds surfaces with different glasses: %s, %s", sv.Name, sigWhere, where)
			}
		}
	}
	return nil
}

func findInputConfig(input *types.Input, id string) *types.Config {
	for i := range input.Configs {
		if input.Configs[i].ID == id {
			return &input.Configs[i]
		}
	}
	return nil
}

// initialGlassSignature identifies a surface's starting glass so two bindings
// can be compared: the catalogue key, or the inline nd/vd, or the raw nd/vd.
func initialGlassSignature(cfg *types.Config, id int, gc *glass.Catalog) string {
	for i := range cfg.Surfaces {
		if cfg.Surfaces[i].ID != id {
			continue
		}
		m := cfg.Surfaces[i].Material
		if m.HasKey() {
			return "key:" + m.Key
		}
		if m.HasModel() {
			return fmt.Sprintf("nd:%.5f/vd:%.4f", m.ND, m.VD)
		}
		return "AIR"
	}
	return "missing"
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
