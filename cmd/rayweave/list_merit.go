package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	psoopt "github.com/hiroki/rayweaver/internal/pso"
	"github.com/hiroki/rayweaver/internal/types"
)

// listMerit renders the merit function definition (terms, modes, constraints)
// from the input configs, and the optimization result when available from the
// piped output.
func listMerit(input types.Input, output types.Output, format string) {
	var terms []MeritTermRow
	var modes []MeritModeRow
	var constraints []MeritConstraintRow

	// optimization.constraints is the fallback source when a config defines
	// no constraints of its own (matching the optimizer's per-config
	// resolution in optimize.go); also used when configs[] is absent entirely
	// (the synthetic single-config path).
	var optConstraints []types.ConstraintOperand
	if input.Optimization != nil {
		optConstraints = input.Optimization.Constraints
	}

	appendConstraints := func(cfgID string, ops []types.ConstraintOperand, inherited bool) {
		for _, c := range ops {
			constraints = append(constraints, MeritConstraintRow{
				Config:    cfgID,
				ID:        c.ID,
				Kind:      string(c.Kind),
				Measure:   string(c.Measure),
				Field:     c.Field,
				Target:    c.Target,
				Lower:     c.Lower,
				Upper:     c.Upper,
				BandWidth: c.BandWidth,
				Weight:    c.Weight,
				Active:    c.Active,
				Inherited: inherited,
			})
		}
	}

	for _, cfg := range input.Configs {
		cfgID := configDisplayName(cfg)

		// Fixed merit terms.
		if cfg.Merit != nil {
			for _, t := range cfg.Merit.Terms {
				row := MeritTermRow{
					Config:               cfgID,
					Kind:                 t.Kind,
					Field:                t.Field,
					Wavelength:           t.Wavelength,
					ComparisonWavelength: t.ComparisonWavelength,
					Target:               t.Target,
					Fraction:             t.Fraction,
					SurfaceSet:           t.SurfaceSet,
					Weight:               t.Weight,
				}
				terms = append(terms, row)
			}
		}

		// Merit modes.
		for _, m := range cfg.MeritModes {
			modes = append(modes, MeritModeRow{
				Config:   cfgID,
				Name:     m.Name,
				NumTerms: len(m.Terms),
			})
		}

		// Constraints: per-config first, else inherited from
		// optimization.constraints.
		if len(cfg.Constraints) > 0 {
			appendConstraints(cfgID, cfg.Constraints, false)
		} else if len(optConstraints) > 0 {
			appendConstraints(cfgID, optConstraints, true)
		}
	}

	// Synthetic single-config fallback: no configs[] at all, constraints only
	// under optimization (mirrors the optimizer's synthetic config1).
	if len(input.Configs) == 0 && len(optConstraints) > 0 {
		appendConstraints("single-config", optConstraints, true)
	}

	// Optimization result (only when piped from optimize or a global run).
	var optResult *OptResultSummary
	if output.OptResults != nil {
		optResult = &OptResultSummary{
			Status:      output.OptResults.Status,
			Iterations:  output.OptResults.Iterations,
			Interrupted: output.OptResults.Interrupted,
		}
	}
	if output.GlobalResult != nil {
		if optResult == nil {
			optResult = &OptResultSummary{}
		}
		optResult.Merit = output.GlobalResult.BestMerit
		if output.GlobalResult.Interrupted {
			optResult.Interrupted = true
		}
		if optResult.Status == "" {
			optResult.Status = "global"
		}
	}

	isEmpty := len(terms) == 0 && len(modes) == 0 && len(constraints) == 0 && optResult == nil
	if isEmpty {
		switch format {
		case "yaml", "json":
			emitEmptyList(format, "terms")
		default:
			fmt.Println("Merit: (no merit defined)")
		}
		return
	}

	switch format {
	case "yaml", "json":
		emitListOutput(format, meritListOutput{
			Terms: terms, Modes: modes, Constraints: constraints, OptResult: optResult,
		})
	case "csv":
		if len(terms) > 0 {
			fmt.Println("Merit Terms:")
			fmt.Println("config,kind,field,wavelength,comparison_wavelength,target,fraction,surface_set,weight")
			for _, r := range terms {
				cells := []string{
					r.Config, r.Kind,
					strconv.Itoa(r.Field),
					strconv.FormatFloat(r.Wavelength, 'g', -1, 64),
					strconv.FormatFloat(r.ComparisonWavelength, 'g', -1, 64),
					strconv.FormatFloat(r.Target, 'g', -1, 64),
					strconv.FormatFloat(r.Fraction, 'g', -1, 64),
					fmt.Sprintf("%v", r.SurfaceSet),
					strconv.FormatFloat(r.Weight, 'g', -1, 64),
				}
				fmt.Println(strings.Join(quoteCSV(cells), ","))
			}
		}
		if len(modes) > 0 {
			fmt.Println()
			fmt.Println("Merit Modes:")
			fmt.Println("config,name,num_terms")
			for _, r := range modes {
				fmt.Printf("%s,%s,%d\n", r.Config, r.Name, r.NumTerms)
			}
		}
		if len(constraints) > 0 {
			anyInherited := false
			for _, r := range constraints {
				if r.Inherited {
					anyInherited = true
					break
				}
			}
			fmt.Println()
			fmt.Println("Constraints:")
			header := "config,id,kind,measure,field,target,lower,upper,band_width,weight,active"
			if anyInherited {
				header += ",inherited"
			}
			fmt.Println(header)
			for _, r := range constraints {
				cells := []string{
					r.Config, r.ID, r.Kind, r.Measure,
					strconv.Itoa(r.Field),
					strconv.FormatFloat(r.Target, 'g', -1, 64),
					strconv.FormatFloat(r.Lower, 'g', -1, 64),
					strconv.FormatFloat(r.Upper, 'g', -1, 64),
					strconv.FormatFloat(r.BandWidth, 'g', -1, 64),
					strconv.FormatFloat(r.Weight, 'g', -1, 64),
					strconv.FormatBool(r.Active),
				}
				if anyInherited {
					cells = append(cells, strconv.FormatBool(r.Inherited))
				}
				fmt.Println(strings.Join(quoteCSV(cells), ","))
			}
		}
		if optResult != nil {
			fmt.Println()
			fmt.Println("Optimization Result:")
			fmt.Println("status,iterations,merit,interrupted")
			fmt.Printf("%s,%d,%s,%v\n", optResult.Status, optResult.Iterations,
				strconv.FormatFloat(optResult.Merit, 'g', -1, 64), optResult.Interrupted)
		}
	default: // "table"
		// Group terms by config for display.
		termsByConfig := groupMeritTermsByConfig(terms)
		for _, cfgID := range sortedConfigKeys(termsByConfig) {
			cfgTerms := termsByConfig[cfgID]
			// Find the merit type from the first term's config.
			meritType := "weighted_sum"
			for _, cfg := range input.Configs {
				if configDisplayName(cfg) == cfgID && cfg.Merit != nil {
					meritType = cfg.Merit.Type
					break
				}
			}
			fmt.Printf("Merit Terms (%s, %s):\n", cfgID, meritType)
			cols := []tableColumn{
				{header: "Kind"},
				{header: "Field", right: true},
				{header: "λ[mm]", right: true},
				{header: "Weight", right: true},
				{header: "Target", right: true},
				{header: "Surface Set"},
			}
			for _, r := range cfgTerms {
				cols[0].cells = append(cols[0].cells, r.Kind)
				cols[1].cells = append(cols[1].cells, strconv.Itoa(r.Field))
				cols[2].cells = append(cols[2].cells, formatTableFloat(r.Wavelength))
				cols[3].cells = append(cols[3].cells, formatTableFloat(r.Weight))
				cols[4].cells = append(cols[4].cells, formatTableFloat(r.Target))
				sset := "-"
				if len(r.SurfaceSet) > 0 {
					sset = fmt.Sprintf("%v", r.SurfaceSet)
				}
				cols[5].cells = append(cols[5].cells, sset)
			}
			fmt.Print(renderTable(cols))
			fmt.Println()
		}

		// Merit modes.
		if len(modes) > 0 {
			modesByConfig := groupMeritModesByConfig(modes)
			for _, cfgID := range sortedConfigKeys(modesByConfig) {
				cfgModes := modesByConfig[cfgID]
				fmt.Printf("Merit Modes (%s):\n", cfgID)
				cols := []tableColumn{
					{header: "Mode"},
					{header: "Terms", right: true},
				}
				for _, r := range cfgModes {
					cols[0].cells = append(cols[0].cells, r.Name)
					cols[1].cells = append(cols[1].cells, strconv.Itoa(r.NumTerms))
				}
				fmt.Print(renderTable(cols))
				fmt.Println()
			}
		}

		// Constraints.
		if len(constraints) > 0 {
			constraintsByConfig := groupMeritConstraintsByConfig(constraints)
			for _, cfgID := range sortedConfigKeys(constraintsByConfig) {
				cfgConstraints := constraintsByConfig[cfgID]
				if len(cfgConstraints) > 0 && cfgConstraints[0].Inherited {
					fmt.Printf("Constraints (%s, inherited from optimization):\n", cfgID)
				} else {
					fmt.Printf("Constraints (%s):\n", cfgID)
				}
				cols := []tableColumn{
					{header: "ID"},
					{header: "Kind"},
					{header: "Measure"},
					{header: "Field", right: true},
					{header: "Target", right: true},
					{header: "BandWidth", right: true},
					{header: "Weight", right: true},
					{header: "Active"},
				}
				for _, r := range cfgConstraints {
					cols[0].cells = append(cols[0].cells, r.ID)
					cols[1].cells = append(cols[1].cells, r.Kind)
					cols[2].cells = append(cols[2].cells, r.Measure)
					cols[3].cells = append(cols[3].cells, strconv.Itoa(r.Field))
					cols[4].cells = append(cols[4].cells, formatTableFloat(r.Target))
					cols[5].cells = append(cols[5].cells, formatTableFloat(r.BandWidth))
					cols[6].cells = append(cols[6].cells, formatTableFloat(r.Weight))
					cols[7].cells = append(cols[7].cells, strconv.FormatBool(r.Active))
				}
				fmt.Print(renderTable(cols))
				fmt.Println()
			}
		}

		// Optimization result.
		if optResult != nil {
			fmt.Println("Optimization Result:")
			cols := []tableColumn{
				{header: "Status"},
				{header: "Iterations", right: true},
				{header: "Merit", right: true},
				{header: "Interrupted"},
			}
			cols[0].cells = append(cols[0].cells, optResult.Status)
			cols[1].cells = append(cols[1].cells, strconv.Itoa(optResult.Iterations))
			cols[2].cells = append(cols[2].cells, formatTableFloat(optResult.Merit))
			cols[3].cells = append(cols[3].cells, strconv.FormatBool(optResult.Interrupted))
			fmt.Print(renderTable(cols))
		}
	}
}

// groupMeritTermsByConfig groups merit terms by config display name, preserving
// config order.
func groupMeritTermsByConfig(terms []MeritTermRow) map[string][]MeritTermRow {
	out := make(map[string][]MeritTermRow)
	for _, t := range terms {
		out[t.Config] = append(out[t.Config], t)
	}
	return out
}

// groupMeritModesByConfig groups merit modes by config display name.
func groupMeritModesByConfig(modes []MeritModeRow) map[string][]MeritModeRow {
	out := make(map[string][]MeritModeRow)
	for _, m := range modes {
		out[m.Config] = append(out[m.Config], m)
	}
	return out
}

// groupMeritConstraintsByConfig groups constraints by config display name.
func groupMeritConstraintsByConfig(constraints []MeritConstraintRow) map[string][]MeritConstraintRow {
	out := make(map[string][]MeritConstraintRow)
	for _, c := range constraints {
		out[c.Config] = append(out[c.Config], c)
	}
	return out
}

// sortedConfigKeys returns the keys of a map in the order they first appear
// in the values' Config field (insertion order).
func sortedConfigKeys[T any](m map[string][]T) []string {
	seen := map[string]bool{}
	var keys []string
	for _, items := range m {
		for _, item := range items {
			var key string
			switch v := any(item).(type) {
			case MeritTermRow:
				key = v.Config
			case MeritModeRow:
				key = v.Config
			case MeritConstraintRow:
				key = v.Config
			default:
				continue
			}
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// appendNumProp appends a non-zero numeric property to a key-value list.
func appendNumProp(props []propRow, name string, v float64) []propRow {
	if v != 0 {
		return append(props, propRow{Name: name, Value: formatTableFloat(v)})
	}
	return props
}

// appendIntProp appends a non-zero integer property to a key-value list.
func appendIntProp(props []propRow, name string, v int) []propRow {
	if v != 0 {
		return append(props, propRow{Name: name, Value: strconv.Itoa(v)})
	}
	return props
}

// appendStrProp appends a non-empty string property to a key-value list.
func appendStrProp(props []propRow, name, v string) []propRow {
	if v != "" {
		return append(props, propRow{Name: name, Value: v})
	}
	return props
}

// appendBoolTrueProp appends a boolean property only when it is true (unset
// boolean settings default to false and carry no information).
func appendBoolTrueProp(props []propRow, name string, v bool) []propRow {
	if v {
		return append(props, propRow{Name: name, Value: "true"})
	}
	return props
}

// appendBoolPtrProp appends a boolean property when the pointer is set.
func appendBoolPtrProp(props []propRow, name string, v *bool) []propRow {
	if v != nil {
		return append(props, propRow{Name: name, Value: strconv.FormatBool(*v)})
	}
	return props
}

// sortedFloatMapJoin renders a string→float map as sorted "k=v" pairs so the
// output is deterministic despite Go map iteration order.
func sortedFloatMapJoin(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%s", k, formatTableFloat(m[k]))
	}
	return strings.Join(parts, ",")
}

// sortedStringKeysJoin renders the sorted keys of a string-keyed map.
func sortedStringKeysJoin[T any](m map[string]T) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// sharedBindingsCompact renders a shared variable's bindings as a compact
// comma-joined list for table/csv cells: config:id:param with optional
// (×scale) / (+offset) suffixes when non-zero.
func sharedBindingsCompact(bindings []SharedBindingRow) string {
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		s := fmt.Sprintf("%s:%d:%s", b.Config, b.ID, b.Param)
		if b.Scale != 0 {
			s += fmt.Sprintf("(×%s)", formatTableFloat(b.Scale))
		}
		if b.Offset != 0 {
			s += fmt.Sprintf("(+%s)", formatTableFloat(b.Offset))
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// escapeSettings flattens the set fields of optimization.escape into
// key-value rows.
func escapeSettings(e *types.EscapeConfig) []propRow {
	var p []propRow
	p = appendIntProp(p, "Max Cycles", e.MaxCycles)
	p = appendIntProp(p, "Global Workers", e.GlobalWorkers)
	p = appendNumProp(p, "Max Seconds", e.MaxSeconds)
	p = appendNumProp(p, "Distance Threshold", e.DistanceThreshold)
	p = appendNumProp(p, "Fingerprint Distance Threshold", e.FingerprintDistanceThreshold)
	p = appendNumProp(p, "H Initial", e.HInitial)
	p = appendNumProp(p, "W Initial", e.WInitial)
	p = appendNumProp(p, "H Mult", e.HMult)
	p = appendNumProp(p, "W Mult", e.WMult)
	p = appendNumProp(p, "Escape Iter Frac", e.EscapeIterFrac)
	p = appendNumProp(p, "W Span", e.WSpan)
	p = appendNumProp(p, "Stall Window Frac", e.StallWindowFrac)
	p = appendNumProp(p, "Stall Rel Tol", e.StallRelTol)
	p = appendBoolPtrProp(p, "Stall Early Stop", e.StallEarlyStop)
	p = appendNumProp(p, "Initial Perturb", e.InitialPerturb)
	// Stream is only present when the run set it (an unset flag never writes
	// the value back), so it is reported the same way as the other pointer
	// property, Stall Early Stop.
	p = appendBoolPtrProp(p, "Stream", e.Stream)
	p = appendBoolPtrProp(p, "Hull Rescue", e.HullRescue)
	p = appendNumProp(p, "Hull Rescue Iter Frac", e.HullRescueIterFrac)
	if len(e.VariableWeights) > 0 {
		p = append(p, propRow{Name: "Variable Weights", Value: sortedFloatMapJoin(e.VariableWeights)})
	}
	return p
}

func psoSettings(pso *types.PSOConfig) []propRow {
	var p []propRow
	p = appendIntProp(p, "Swarm Size", pso.SwarmSize)
	p = appendIntProp(p, "PSO Iterations", pso.PsoIterations)
	p = appendNumProp(p, "Inertia", pso.Inertia)
	p = appendNumProp(p, "Cognitive", pso.Cognitive)
	p = appendNumProp(p, "Social", pso.Social)
	p = appendNumProp(p, "Velocity Clamp", pso.VelocityClamp)
	p = appendNumProp(p, "Init Spread", pso.InitSpread)
	p = appendNumProp(p, "Constraint Penalty", pso.ConstraintPenalty)
	// The criterion has a non-empty default, so it is reported even when the
	// document never set it — the scale the run ranked gbest on is what makes
	// two runs comparable.
	gbestRank := pso.Gbest
	if gbestRank == "" {
		gbestRank = psoopt.GbestMerit + " (default)"
	}
	p = append(p, propRow{Name: "GBest Rank", Value: gbestRank})
	// The cycle machinery is shared with `escape` and lives in the embedded
	// GlobalConfig, so it is shown as a collapsed sub-block under the method's
	// own knobs. The escape bump geometry (h_initial / w_initial / ...) is not
	// part of it and never appears here.
	g := &pso.GlobalConfig
	p = append(p, propRow{Name: "global (shared)", Value: "-"})
	p = appendIntProp(p, "  Max Cycles", g.MaxCycles)
	p = appendIntProp(p, "  Global Workers", g.GlobalWorkers)
	p = appendNumProp(p, "  Max Seconds", g.MaxSeconds)
	p = appendNumProp(p, "  Distance Threshold", g.DistanceThreshold)
	p = appendNumProp(p, "  Initial Perturb", g.InitialPerturb)
	p = appendBoolPtrProp(p, "  Stream", g.Stream)
	return p
}

// globalParamsSettings flattens the set fields of global_result.params into
// key-value rows.
func globalParamsSettings(p *types.GlobalParamsInfo) []propRow {
	var rows []propRow
	rows = appendIntProp(rows, "Max Cycles", p.MaxCycles)
	rows = appendIntProp(rows, "Global Workers", p.GlobalWorkers)
	rows = appendNumProp(rows, "Max Seconds", p.MaxSeconds)
	rows = appendNumProp(rows, "Distance Threshold", p.DistanceThreshold)
	rows = appendNumProp(rows, "Fingerprint Distance Threshold", p.FingerprintDistanceThreshold)
	rows = appendNumProp(rows, "H Initial", p.HInitial)
	rows = appendNumProp(rows, "W Initial", p.WInitial)
	rows = appendNumProp(rows, "H Mult", p.HMult)
	rows = appendNumProp(rows, "W Mult", p.WMult)
	rows = appendNumProp(rows, "Escape Iter Frac", p.EscapeIterFrac)
	rows = appendNumProp(rows, "W Span", p.WSpan)
	rows = appendNumProp(rows, "Stall Window Frac", p.StallWindowFrac)
	rows = appendNumProp(rows, "Stall Rel Tol", p.StallRelTol)
	rows = appendBoolPtrProp(rows, "Stall Early Stop", p.StallEarlyStop)
	rows = appendNumProp(rows, "Initial Perturb", p.InitialPerturb)
	if len(p.VariableWeights) > 0 {
		rows = append(rows, propRow{Name: "Variable Weights", Value: sortedFloatMapJoin(p.VariableWeights)})
	}
	return rows
}
