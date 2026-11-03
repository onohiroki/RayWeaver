package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

// listOptimization renders the optimization configuration from
// input.optimization: solver settings, variables (per-config, shared, local),
// the merit schedule and the sub-config sections (escape, glass_hull,
// degenerate, power_solve, region_active, adaptive_damping). Constraints are
// NOT shown here — the effective constraint display lives in `list merit`
// (per-config constraints, else inherited from optimization.constraints).
// When piped from optimize, output.opt_results is appended.
func listOptimization(input types.Input, output types.Output, format string) {
	opt := input.Optimization

	var solver []propRow
	var variables []OptVarRow
	var shared []SharedVarRow
	var local []LocalVarRow
	var schedule *ScheduleSection
	var subConfigs []SubConfigSection

	if opt != nil {
		// Solver settings (set values only).
		solver = appendStrProp(solver, "Method", opt.Method)
		solver = appendStrProp(solver, "Aggregate", opt.Aggregate)
		solver = appendIntProp(solver, "Max Iterations", opt.MaxIter)
		solver = appendNumProp(solver, "Tolerance", opt.Tol)
		solver = appendNumProp(solver, "Mu", opt.Mu)
		solver = appendNumProp(solver, "Epsilon", opt.Epsilon)
		solver = appendIntProp(solver, "Num Rays", opt.NumRays)
		solver = appendNumProp(solver, "Mu Con Max", opt.MuConMax)
		solver = appendNumProp(solver, "Aperture Margin", opt.ApertureMargin)
		solver = appendNumProp(solver, "Aperture Margin MM", opt.ApertureMarginMM)
		solver = appendIntProp(solver, "Jacobian Workers", opt.JacobianWorkers)
		solver = appendBoolTrueProp(solver, "Central Diff", opt.CentralDiff)
		solver = appendBoolTrueProp(solver, "BFGS", opt.BFGS)

		for _, v := range opt.Variables {
			variables = append(variables, OptVarRow{
				Name:    v.Name,
				Config:  v.Target.Config,
				Surface: v.Target.ID,
				Param:   v.Target.Param,
				Min:     v.Min,
				Max:     v.Max,
				Step:    v.Step,
				Active:  v.Active,
			})
		}
		for _, v := range opt.SharedVariables {
			bindings := make([]SharedBindingRow, 0, len(v.Bindings))
			for _, b := range v.Bindings {
				bindings = append(bindings, SharedBindingRow{
					Config: b.Config, ID: b.ID, Param: b.Param,
					Scale: b.Scale, Offset: b.Offset,
				})
			}
			shared = append(shared, SharedVarRow{
				Name: v.Name, Min: v.Min, Max: v.Max, Active: v.Active,
				Bindings: bindings,
			})
		}
		for _, v := range opt.LocalVariables {
			local = append(local, LocalVarRow{
				Name: v.Name, Config: v.Config, Surface: v.Target.ID,
				Param: v.Target.Param, Min: v.Min, Max: v.Max, Active: v.Active,
			})
		}

		if ms := opt.MeritSchedule; ms != nil {
			schedule = &ScheduleSection{
				Metric:      ms.Metric,
				Curve:       ms.Curve,
				AnchorFrom:  ms.AnchorFrom,
				AnchorTo:    ms.AnchorTo,
				Aggregation: ms.MetricAggregation,
			}
			for _, m := range ms.Modes {
				schedule.Modes = append(schedule.Modes, ScheduleModeRow{
					Name: m.Name, WeightFrom: m.WeightFrom, WeightTo: m.WeightTo,
				})
			}
		}

		if esc := opt.Escape; esc != nil {
			subConfigs = append(subConfigs, SubConfigSection{Name: "escape", Settings: escapeSettings(esc)})
		}
		if pso := opt.PSO; pso != nil {
			subConfigs = append(subConfigs, SubConfigSection{Name: "pso", Settings: psoSettings(pso)})
		}
		if gh := opt.GlassHull; gh != nil {
			var p []propRow
			p = appendBoolTrueProp(p, "Enabled", gh.Enabled)
			source := gh.Source
			if source == "" {
				source = "union"
			}
			p = appendStrProp(p, "Source", source)
			p = appendNumProp(p, "Margin", gh.Margin)
			p = appendNumProp(p, "Weight", gh.Weight)
			if gh.GuardBand != nil {
				p = appendNumProp(p, "Guard Band", *gh.GuardBand)
			}
			if gh.ExplorationWeightFactor != nil {
				p = appendNumProp(p, "Exploration Weight Factor", *gh.ExplorationWeightFactor)
			}
			if len(gh.Glasses) > 0 {
				p = appendIntProp(p, "Glasses", len(gh.Glasses))
			}
			subConfigs = append(subConfigs, SubConfigSection{Name: "glass_hull", Settings: p})
		}
		if mn := opt.MeritNormalization; mn != nil {
			var p []propRow
			p = appendBoolTrueProp(p, "Enabled", mn.Enabled)
			if len(mn.Scales) > 0 {
				p = appendIntProp(p, "Scale Overrides", len(mn.Scales))
			}
			subConfigs = append(subConfigs, SubConfigSection{Name: "merit_normalization", Settings: p})
		}
		if ga := opt.GlassAttraction; ga != nil {
			var p []propRow
			p = appendBoolTrueProp(p, "Enabled", ga.Enabled)
			p = appendStrProp(p, "Kernel", ga.Kernel)
			p = appendNumProp(p, "Weight From", ga.WeightFrom)
			p = appendNumProp(p, "Weight To", ga.WeightTo)
			p = appendStrProp(p, "Metric", ga.Metric)
			p = appendStrProp(p, "Curve", ga.Curve)
			p = appendBoolTrueProp(p, "Sensitivity Weighted", ga.SensitivityWeighted)
			p = appendStrProp(p, "Sensitivity Metric", ga.SensitivityMetric)
			if ga.PostRefine {
				p = appendBoolTrueProp(p, "Post Refine", ga.PostRefine)
				p = appendIntProp(p, "Post Refine Top K", ga.PostRefineTop)
			}
			subConfigs = append(subConfigs, SubConfigSection{Name: "glass_attraction", Settings: p})
		}
		if dg := opt.Degenerate; dg != nil {
			var p []propRow
			p = appendNumProp(p, "Spot Value", dg.SpotValue)
			p = appendNumProp(p, "OPD Value", dg.OPDValue)
			p = appendNumProp(p, "Wavefront Value", dg.WavefrontValue)
			subConfigs = append(subConfigs, SubConfigSection{Name: "degenerate", Settings: p})
		}
		if ps := opt.PowerSolve; ps != nil {
			var p []propRow
			p = appendBoolTrueProp(p, "Enabled", ps.Enabled)
			if len(ps.Surfaces) > 0 {
				p = append(p, propRow{Name: "Surfaces", Value: fmt.Sprintf("%v", ps.Surfaces)})
			}
			subConfigs = append(subConfigs, SubConfigSection{Name: "power_solve", Settings: p})
		}
		if ra := opt.RegionActive; ra != nil {
			var p []propRow
			p = appendBoolTrueProp(p, "Enabled", ra.Enabled)
			p = appendNumProp(p, "Eps Activate", ra.EpsActivate)
			p = appendNumProp(p, "Eps Deactivate", ra.EpsDeactivate)
			p = appendNumProp(p, "Lambda Step", ra.LambdaStep)
			p = appendNumProp(p, "Max Lambda", ra.MaxLambda)
			subConfigs = append(subConfigs, SubConfigSection{Name: "region_active", Settings: p})
		}
		if ad := opt.AdaptiveDamping; ad != nil {
			var p []propRow
			p = appendNumProp(p, "Sensitivity EMA", ad.SensitivityEMA)
			p = appendNumProp(p, "Sensitivity Floor", ad.SensitivityFloor)
			p = appendNumProp(p, "Ratio Min", ad.RatioMin)
			p = appendNumProp(p, "Ratio Max", ad.RatioMax)
			p = appendNumProp(p, "Damping Min", ad.DampingMin)
			p = appendNumProp(p, "Damping Max", ad.DampingMax)
			p = appendNumProp(p, "Reject Boost", ad.RejectBoost)
			p = appendNumProp(p, "Accept Relax", ad.AcceptRelax)
			p = appendNumProp(p, "Contribution Threshold", ad.ContributionThreshold)
			if len(ad.Classes) > 0 {
				p = append(p, propRow{Name: "Classes", Value: sortedStringKeysJoin(ad.Classes)})
			}
			if len(ad.Variables) > 0 {
				p = append(p, propRow{Name: "Variables", Value: sortedStringKeysJoin(ad.Variables)})
			}
			subConfigs = append(subConfigs, SubConfigSection{Name: "adaptive_damping", Settings: p})
		}
	}

	// Piped optimization result (output.opt_results).
	var optResult *OptimizationResultSummary
	if output.OptResults != nil {
		or := output.OptResults
		optResult = &OptimizationResultSummary{
			Status:           or.Status,
			Iterations:       or.Iterations,
			ActiveMode:       or.ActiveMode,
			ModeWeights:      or.ModeWeights,
			ModeChanges:      or.ModeChanges,
			MetricValue:      or.MetricValue,
			EffectiveNumRays: or.EffectiveNumRays,
			Interrupted:      or.Interrupted,
			Reason:           or.Reason,
			GlassAttraction:  or.GlassAttraction,
			Snap:             or.Snap,
			SpotFlatness:     or.SpotFlatness,
		}
	}

	if len(solver) == 0 && len(variables) == 0 && len(shared) == 0 &&
		len(local) == 0 && schedule == nil && len(subConfigs) == 0 && optResult == nil {
		switch format {
		case "yaml", "json":
			emitEmptyList(format, "solver")
		default:
			fmt.Println("Optimization: (no optimization section)")
		}
		return
	}

	// optResultProps flattens the summary into key-value rows.
	optResultProps := func() []propRow {
		var p []propRow
		if optResult == nil {
			return p
		}
		if optResult.Status != "" {
			p = append(p, propRow{Name: "Status", Value: optResult.Status})
		}
		p = appendIntProp(p, "Iterations", optResult.Iterations)
		p = appendStrProp(p, "Active Mode", optResult.ActiveMode)
		if len(optResult.ModeWeights) > 0 {
			p = append(p, propRow{Name: "Mode Weights", Value: sortedFloatMapJoin(optResult.ModeWeights)})
		}
		p = appendIntProp(p, "Mode Changes", optResult.ModeChanges)
		p = appendNumProp(p, "Metric Value", optResult.MetricValue)
		p = appendIntProp(p, "Effective Num Rays", optResult.EffectiveNumRays)
		p = appendBoolTrueProp(p, "Interrupted", optResult.Interrupted)
		p = appendStrProp(p, "Reason", optResult.Reason)
		if ga := optResult.GlassAttraction; ga != nil {
			p = append(p, propRow{Name: "Glass Attraction Weight", Value: fmt.Sprintf("%g", ga.Weight)})
			for _, pair := range ga.Pairs {
				p = append(p, propRow{
					Name:  fmt.Sprintf("  %s", pair.Name),
					Value: fmt.Sprintf("nd=%.5f vd=%.2f → %s (nd=%.5f vd=%.2f, d=%.4f, scale=%.3f)", pair.ND, pair.VD, pair.NearestKey, pair.NearestND, pair.NearestVD, pair.Distance, pair.Scale),
				})
			}
		}
		if s := optResult.Snap; s != nil {
			p = append(p, propRow{Name: "Snap Before Merit", Value: fmt.Sprintf("%.6e", s.BeforeMerit)})
			p = append(p, propRow{Name: "Snap After Merit", Value: fmt.Sprintf("%.6e", s.AfterMerit)})
			p = append(p, propRow{Name: "Snap Cost", Value: fmt.Sprintf("%+.6e (%.3f%%)", s.Cost, s.CostPct)})
			for _, pair := range s.Pairs {
				p = append(p, propRow{
					Name:  fmt.Sprintf("  S%d", pair.SurfaceID),
					Value: fmt.Sprintf("nd=%.5f vd=%.3f → %s (nd=%.5f vd=%.3f, d=%.4f)", pair.FromND, pair.FromVD, pair.Name, pair.ToND, pair.ToVD, pair.Distance),
				})
			}
		}
		return p
	}

	// spotFlat is the piped spot-slope diagnostic (output.opt_results.
	// spot_flatness), one row per (config, field, wavelength): a table rather
	// than a key-value list, so it gets its own section.
	var spotFlat []types.SpotFlatnessRow
	if optResult != nil {
		spotFlat = optResult.SpotFlatness
	}

	// scheduleTitle builds the "Merit Schedule (...)" header suffix.
	scheduleTitle := func() string {
		parts := make([]string, 0, 4)
		if schedule.Metric != "" {
			parts = append(parts, "metric="+schedule.Metric)
		}
		if schedule.Curve != "" {
			parts = append(parts, "curve="+schedule.Curve)
		}
		if schedule.AnchorFrom != 0 || schedule.AnchorTo != 0 {
			parts = append(parts, fmt.Sprintf("anchors %s→%s",
				formatTableFloat(schedule.AnchorFrom), formatTableFloat(schedule.AnchorTo)))
		}
		if schedule.Aggregation != "" {
			parts = append(parts, "aggregation="+schedule.Aggregation)
		}
		if len(parts) == 0 {
			return "Merit Schedule:"
		}
		return "Merit Schedule (" + strings.Join(parts, ", ") + "):"
	}

	switch format {
	case "yaml", "json":
		emitListOutput(format, optimizationListOutput{
			Solver: solver, Variables: variables, SharedVariables: shared,
			LocalVariables: local, MeritSchedule: schedule,
			SubConfigs: subConfigs, OptResult: optResult,
		})
	case "csv":
		if len(solver) > 0 {
			fmt.Println("Optimization:")
			fmt.Println("property,value")
			for _, p := range solver {
				fmt.Println(strings.Join(quoteCSV([]string{p.Name, p.Value}), ","))
			}
		}
		printVarSectionCSV := func(title, header string, rows [][]string) {
			fmt.Println()
			fmt.Println(title + ":")
			fmt.Println(header)
			for _, cells := range rows {
				fmt.Println(strings.Join(quoteCSV(cells), ","))
			}
		}
		if len(variables) > 0 {
			var rows [][]string
			for _, r := range variables {
				rows = append(rows, []string{
					r.Name, r.Config, strconv.Itoa(r.Surface), r.Param,
					strconv.FormatFloat(r.Min, 'g', -1, 64),
					strconv.FormatFloat(r.Max, 'g', -1, 64),
					strconv.FormatFloat(r.Step, 'g', -1, 64),
					strconv.FormatBool(r.Active),
				})
			}
			printVarSectionCSV("Variables", "name,config,surface,param,min,max,step,active", rows)
		}
		if len(shared) > 0 {
			var rows [][]string
			for _, r := range shared {
				rows = append(rows, []string{
					r.Name,
					strconv.FormatFloat(r.Min, 'g', -1, 64),
					strconv.FormatFloat(r.Max, 'g', -1, 64),
					strconv.FormatBool(r.Active),
					sharedBindingsCompact(r.Bindings),
				})
			}
			printVarSectionCSV("Shared Variables", "name,min,max,active,bindings", rows)
		}
		if len(local) > 0 {
			var rows [][]string
			for _, r := range local {
				rows = append(rows, []string{
					r.Name, r.Config, strconv.Itoa(r.Surface), r.Param,
					strconv.FormatFloat(r.Min, 'g', -1, 64),
					strconv.FormatFloat(r.Max, 'g', -1, 64),
					strconv.FormatBool(r.Active),
				})
			}
			printVarSectionCSV("Local Variables", "name,config,surface,param,min,max,active", rows)
		}
		if schedule != nil {
			fmt.Println()
			fmt.Println(scheduleTitle())
			fmt.Println("mode,weight_from,weight_to")
			for _, m := range schedule.Modes {
				fmt.Printf("%s,%s,%s\n", m.Name,
					strconv.FormatFloat(m.WeightFrom, 'g', -1, 64),
					strconv.FormatFloat(m.WeightTo, 'g', -1, 64))
			}
		}
		for _, sc := range subConfigs {
			fmt.Println()
			fmt.Println(subConfigTitle(sc.Name) + ":")
			fmt.Println("property,value")
			for _, p := range sc.Settings {
				fmt.Println(strings.Join(quoteCSV([]string{p.Name, p.Value}), ","))
			}
		}
		if p := optResultProps(); len(p) > 0 {
			fmt.Println()
			fmt.Println("Optimization Result:")
			fmt.Println("property,value")
			for _, r := range p {
				fmt.Println(strings.Join(quoteCSV([]string{r.Name, r.Value}), ","))
			}
		}
		if len(spotFlat) > 0 {
			fmt.Println()
			fmt.Println("Spot Flatness:")
			fmt.Println("config,field,angle_deg,wavelength_mm,defocus_mm,flatness_mm,astig_mm,residual_mm,rays,ok")
			for _, r := range spotFlat {
				f := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
				fmt.Println(strings.Join(quoteCSV([]string{
					r.Config, strconv.Itoa(r.Field), f(r.AngleDeg), f(r.Wavelength),
					f(r.DefocusMM), f(r.FlatnessMM), f(r.AstigMM), f(r.ResidualMM),
					strconv.Itoa(r.Rays), strconv.FormatBool(r.OK),
				}), ","))
			}
		}
	default: // "table"
		title := "Optimization:"
		if opt != nil && opt.Method != "" {
			title = fmt.Sprintf("Optimization (%s):", opt.Method)
		}
		printPropsSection(title, solver)
		if len(variables) > 0 {
			fmt.Println("Variables:")
			cols := []tableColumn{
				{header: "Name"},
				{header: "Config"},
				{header: "Surface", right: true},
				{header: "Param"},
				{header: "Min", right: true},
				{header: "Max", right: true},
				{header: "Step", right: true},
				{header: "Active"},
			}
			for _, r := range variables {
				cols[0].cells = append(cols[0].cells, r.Name)
				cols[1].cells = append(cols[1].cells, r.Config)
				cols[2].cells = append(cols[2].cells, strconv.Itoa(r.Surface))
				cols[3].cells = append(cols[3].cells, r.Param)
				cols[4].cells = append(cols[4].cells, formatTableFloat(r.Min))
				cols[5].cells = append(cols[5].cells, formatTableFloat(r.Max))
				cols[6].cells = append(cols[6].cells, formatTableFloat(r.Step))
				cols[7].cells = append(cols[7].cells, strconv.FormatBool(r.Active))
			}
			fmt.Print(renderTable(cols))
			fmt.Println()
		}
		if len(shared) > 0 {
			fmt.Println("Shared Variables:")
			cols := []tableColumn{
				{header: "Name"},
				{header: "Min", right: true},
				{header: "Max", right: true},
				{header: "Active"},
				{header: "Bindings"},
			}
			for _, r := range shared {
				cols[0].cells = append(cols[0].cells, r.Name)
				cols[1].cells = append(cols[1].cells, formatTableFloat(r.Min))
				cols[2].cells = append(cols[2].cells, formatTableFloat(r.Max))
				cols[3].cells = append(cols[3].cells, strconv.FormatBool(r.Active))
				cols[4].cells = append(cols[4].cells, sharedBindingsCompact(r.Bindings))
			}
			fmt.Print(renderTable(cols))
			fmt.Println()
		}
		if len(local) > 0 {
			fmt.Println("Local Variables:")
			cols := []tableColumn{
				{header: "Name"},
				{header: "Config"},
				{header: "Surface", right: true},
				{header: "Param"},
				{header: "Min", right: true},
				{header: "Max", right: true},
				{header: "Active"},
			}
			for _, r := range local {
				cols[0].cells = append(cols[0].cells, r.Name)
				cols[1].cells = append(cols[1].cells, r.Config)
				cols[2].cells = append(cols[2].cells, strconv.Itoa(r.Surface))
				cols[3].cells = append(cols[3].cells, r.Param)
				cols[4].cells = append(cols[4].cells, formatTableFloat(r.Min))
				cols[5].cells = append(cols[5].cells, formatTableFloat(r.Max))
				cols[6].cells = append(cols[6].cells, strconv.FormatBool(r.Active))
			}
			fmt.Print(renderTable(cols))
			fmt.Println()
		}
		if schedule != nil {
			fmt.Println(scheduleTitle())
			cols := []tableColumn{
				{header: "Mode"},
				{header: "WeightFrom", right: true},
				{header: "WeightTo", right: true},
			}
			for _, m := range schedule.Modes {
				cols[0].cells = append(cols[0].cells, m.Name)
				cols[1].cells = append(cols[1].cells, formatTableFloat(m.WeightFrom))
				cols[2].cells = append(cols[2].cells, formatTableFloat(m.WeightTo))
			}
			fmt.Print(renderTable(cols))
			fmt.Println()
		}
		for _, sc := range subConfigs {
			printPropsSection(subConfigTitle(sc.Name)+":", sc.Settings)
		}
		if p := optResultProps(); len(p) > 0 {
			printPropsSection("Optimization Result:", p)
		}
		if len(spotFlat) > 0 {
			fmt.Println("Spot Flatness:")
			cols := []tableColumn{
				{header: "Config"},
				{header: "Field", right: true},
				{header: "Angle[deg]", right: true},
				{header: "Wl[mm]", right: true},
				{header: "Defocus[mm]", right: true},
				{header: "Flatness[mm]", right: true},
				{header: "Astig[mm]", right: true},
				{header: "Residual[mm]", right: true},
				{header: "Rays", right: true},
				{header: "OK"},
			}
			for _, r := range spotFlat {
				cols[0].cells = append(cols[0].cells, r.Config)
				cols[1].cells = append(cols[1].cells, strconv.Itoa(r.Field))
				cols[2].cells = append(cols[2].cells, formatTableFloat(r.AngleDeg))
				cols[3].cells = append(cols[3].cells, formatTableFloat(r.Wavelength))
				cols[4].cells = append(cols[4].cells, formatTableFloat(r.DefocusMM))
				cols[5].cells = append(cols[5].cells, formatTableFloat(r.FlatnessMM))
				cols[6].cells = append(cols[6].cells, formatTableFloat(r.AstigMM))
				cols[7].cells = append(cols[7].cells, formatTableFloat(r.ResidualMM))
				cols[8].cells = append(cols[8].cells, strconv.Itoa(r.Rays))
				cols[9].cells = append(cols[9].cells, strconv.FormatBool(r.OK))
			}
			fmt.Print(renderTable(cols))
			fmt.Println()
		}
	}
}

// subConfigTitle renders a sub-config key as a section title: underscores
// become spaces and each word is capitalised (power_solve → "Power Solve").
func subConfigTitle(name string) string {
	words := strings.Split(name, "_")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// printPropsSection renders one key-value section of `list optimization` as a
// two-column table (table format).
func printPropsSection(title string, props []propRow) {
	if len(props) == 0 {
		return
	}
	fmt.Println(title)
	cols := []tableColumn{
		{header: "Property"},
		{header: "Value", right: true},
	}
	for _, p := range props {
		cols[0].cells = append(cols[0].cells, p.Name)
		cols[1].cells = append(cols[1].cells, p.Value)
	}
	fmt.Print(renderTable(cols))
	fmt.Println()
}
