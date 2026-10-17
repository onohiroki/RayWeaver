package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// flagWasSet reports whether name was explicitly given on the command line.
// It distinguishes "flag not given" from a flag's default value, which the
// CLI/YAML precedence rule needs so an unset flag never overrides YAML.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// anyFlagSet reports whether at least one of the named flags was explicitly
// given on the command line. It collapses the multi-flag write-back guards
// ("only flags actually set are echoed into the output").
func anyFlagSet(fs *flag.FlagSet, names ...string) bool {
	for _, n := range names {
		if flagWasSet(fs, n) {
			return true
		}
	}
	return false
}

// boolFlag parses a value-taking boolean flag introduced as a string flag.
// A Go bool flag cannot consume a following "true"/"false" argument (a bare
// `--name false` silently ignores "false"), so settings that need to be
// forced off from the CLI are declared as strings and resolved here. Valid
// values follow strconv.ParseBool ("true"/"false"/"1"/"0"/"t"/"f", any case).
// set reports whether the flag was explicitly given; err is non-nil for an
// explicitly given value that is not a boolean.
func boolFlag(fs *flag.FlagSet, name string) (v bool, set bool, err error) {
	if !flagWasSet(fs, name) {
		return false, false, nil
	}
	s := fs.Lookup(name).Value.String()
	v, perr := strconv.ParseBool(s)
	if perr != nil {
		return false, true, fmt.Errorf("--%s expects a boolean (true/false), got %q", name, s)
	}
	return v, true, nil
}

// intOrYAML resolves an int setting under the CLI/YAML precedence rule: the
// CLI value when non-zero (per the convention that every flag default is the
// zero value, "unset"), else the YAML value. An unset flag therefore never
// overrides YAML; an explicit zero flag is treated as unset, matching the
// `x > 0`/`x <= 0` guards every zero-default subcommand uses. Settings whose
// flag carries a meaningful non-zero default (e.g. vignette's 0.5 mm glass
// path) and sentinel-default flags (e.g. asphere --sensitivity-samples=-1)
// keep their flagWasSet-based handling.
func intOrYAML(cliVal, yamlVal int) int {
	if cliVal != 0 {
		return cliVal
	}
	return yamlVal
}

// floatOrYAML is intOrYAML for float64 settings.
func floatOrYAML(cliVal, yamlVal float64) float64 {
	if cliVal != 0 {
		return cliVal
	}
	return yamlVal
}

// wavefrontSetting returns the field the input's wavefront: section carries
// for the given key, or 0 when the section is absent or the value is
// non-positive. Non-positive YAML settings are treated as unset, matching the
// `c.X > 0` guards of the flag-or-YAML resolution blocks it replaces.
func wavefrontSetting(input types.Input, get func(*types.WavefrontConfig) int) int {
	if input.Wavefront == nil {
		return 0
	}
	v := get(input.Wavefront)
	if v > 0 {
		return v
	}
	return 0
}

// writeBackGlassDir records the --glass-dir CLI value into the output YAML's
// glass_catalog.directory (principle 3 of the CLI/YAML rule). It is a no-op
// when the flag was not given, so an unset flag leaves the YAML untouched.
func writeBackGlassDir(input *types.Input, dir string) {
	if dir == "" {
		return
	}
	if input.GlassCatalog == nil {
		input.GlassCatalog = &types.GlassCatalog{}
	}
	input.GlassCatalog.Directory = dir
}

// Version is the RayWeaver build version, intended to be injected at link time
// via `-ldflags "-X main.Version=..."`. It defaults to "dev" for ad-hoc builds
// and is stamped into output metadata.tool.version when set.
var Version = "dev"

// subcmdArgs returns the subcommand's arguments (os.Args minus the binary and
// subcommand names) for recording in metadata.tool.command.
func subcmdArgs() []string {
	if len(os.Args) > 2 {
		return os.Args[2:]
	}
	return nil
}

// newMetadata returns a fresh identity-only Metadata marking the document as
// RayWeaver-managed (tool name, repository URL, current schema version).
func newMetadata() *types.Metadata {
	return &types.Metadata{
		Tool: types.ToolInfo{
			Name:          types.RayweaverTool,
			URL:           types.RayweaverURL,
			SchemaVersion: types.SchemaVersion,
		},
	}
}

// withOutputMetadata stamps a pipeline document with its generation provenance.
// The caller's metadata (notes, created_at, tool) is otherwise preserved.
// generator is the producing subcommand; argv are its arguments (excluding the
// binary name). It returns the document pointer for convenience.
func withOutputMetadata(input *types.Input, generator string, argv []string) *types.Input {
	if input == nil {
		return input
	}
	if input.Metadata == nil {
		input.Metadata = newMetadata()
	} else {
		input.Metadata.Tool.Name = types.RayweaverTool
		input.Metadata.Tool.URL = types.RayweaverURL
		input.Metadata.Tool.SchemaVersion = types.SchemaVersion
	}
	if generator != "" {
		input.Metadata.Tool.Generator = generator
	}
	if len(argv) > 0 {
		input.Metadata.Tool.Command = argv
	}
	if Version != "" {
		input.Metadata.Tool.Version = Version
	}
	input.Metadata.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	return input
}

// warnToolMismatch emits a stderr warning when a parsed document is marked as
// managed by a different tool. It is a no-op for RayWeaver-managed or unmarked
// documents.
func warnToolMismatch(in *types.Input) {
	if in == nil || in.Metadata == nil {
		return
	}
	t := strings.TrimSpace(in.Metadata.Tool.Name)
	if t == "" || strings.EqualFold(t, types.RayweaverTool) {
		return
	}
	errOut("input metadata.tool.name = %q is not RayWeaver; continuing", t)
}

// parseYAML unmarshals a document into a value of type T, exiting on error.
func parseYAML[T any](data []byte) T {
	var out T
	if err := yaml.Unmarshal(data, &out); err != nil {
		errOut("Error parsing YAML: %v", err)
		os.Exit(1)
	}
	switch v := any(&out).(type) {
	case *types.Input:
		warnToolMismatch(v)
	case *types.Output:
		warnToolMismatch(&v.Input)
	}
	return out
}

// chiefRefSurface returns the chief reference surface ID (0 when the chief
// section is absent), used to seed the dynamic-pupil recomputation during
// optimisation.
func chiefRefSurface(input types.Input) int {
	if input.Chief == nil {
		return 0
	}
	return input.Chief.ReferenceSurface
}

// pupilModelForConfig returns the virtual entrance pupil config from the chief
// section, or nil when not in use.
func pupilModelForConfig(input types.Input) *types.PupilModelConfig {
	if input.Chief == nil {
		return nil
	}
	return input.Chief.PupilModel
}

// cloneChiefForOutput deep-copies the chief section (and its pupil model) so
// write-backs on a materialised pipeline document never mutate the caller's
// input.
func cloneChiefForOutput(input types.Input) types.Input {
	if input.Chief == nil {
		return input
	}
	ch := *input.Chief
	if input.Chief.PupilModel != nil {
		pm := *input.Chief.PupilModel
		ch.PupilModel = &pm
	}
	input.Chief = &ch
	return input
}

// pupilModelForInput returns the document's virtual entrance pupil, or nil when
// it declares none.
func pupilModelForInput(input *types.Input) *types.PupilModelConfig {
	if input == nil || input.Chief == nil {
		return nil
	}
	return input.Chief.PupilModel
}

// resolvedPupilModel copies base with the pupil-model variables of x applied
// (axial_position / diameter). It returns nil only when the document declares
// no pupil model and x carries none — the "no virtual pupil" case, which must
// stay indistinguishable from a document that never mentioned a pupil model.
// base is never mutated.
func resolvedPupilModel(base *types.PupilModelConfig, variables []optimize.Variable, x []float64) *types.PupilModelConfig {
	var out *types.PupilModelConfig
	if base != nil {
		cp := *base
		out = &cp
	}
	for i, v := range variables {
		if i >= len(x) {
			return out
		}
		switch v.Param {
		case "pupil_model_axial_position", "pupil_model_diameter":
		default:
			continue
		}
		if out == nil {
			out = &types.PupilModelConfig{Mode: "virtual_entrance_pupil"}
		}
		if v.Param == "pupil_model_axial_position" {
			out.AxialPosition = x[i]
		} else {
			out.Diameter = x[i]
		}
	}
	return out
}

// pupilModelForX returns the virtual entrance pupil of the minimum x
// materialises: the document's declared model with x's pupil-model values on
// top. A solve handed this model focuses the bundle the deliverable actually
// declares — the declared model alone is only the seed, because
// axial_position/diameter are optimisation variables (passing the declared one
// moved a v50 minimum's delivered plane 0.30 mm from its own best focus).
func pupilModelForX(input *types.Input, variables []optimize.Variable, x []float64) *types.PupilModelConfig {
	return resolvedPupilModel(pupilModelForInput(input), variables, x)
}

// applyPupilVariables writes the virtual-entrance-pupil variables of x into
// the input's chief pupil model, so pipeline outputs (escape minima files and
// final results) carry the optimised pupil position/diameter. Single-config
// variables come from the flat optimize.Variable list (params
// pupil_model_axial_position / pupil_model_diameter).
func applyPupilVariables(input *types.Input, variables []optimize.Variable, x []float64) {
	base := pupilModelForInput(input)
	m := resolvedPupilModel(base, variables, x)
	if m == nil {
		return
	}
	if base != nil {
		// Write the values in place: the model may be shared with a caller
		// that already holds the pointer (the optimizer's config).
		*base = *m
		return
	}
	if input.Chief == nil {
		input.Chief = &types.ChiefInput{}
	}
	input.Chief.PupilModel = m
}

// resolvedPupilModelMulti is resolvedPupilModel for the multi-config flat
// vector: the index mirrors applyEscapeMulti's active-only ordering (shared
// variables first, then local variables) and the pipeline YAML carries one
// top-level pupil_model, shared by every config.
func resolvedPupilModelMulti(base *types.PupilModelConfig, opt *types.OptimizationConfig, x []float64) *types.PupilModelConfig {
	var out *types.PupilModelConfig
	if base != nil {
		cp := *base
		out = &cp
	}
	varIdx := 0
	for _, sv := range opt.SharedVariables {
		if !sv.Active {
			continue
		}
		varIdx++
	}
	for _, lv := range opt.LocalVariables {
		if !lv.Active {
			continue
		}
		if varIdx < len(x) && lv.Target.Type == "pupil_model" {
			if out == nil {
				out = &types.PupilModelConfig{Mode: "virtual_entrance_pupil"}
			}
			switch lv.Target.Param {
			case "axial_position":
				out.AxialPosition = x[varIdx]
			case "diameter":
				out.Diameter = x[varIdx]
			}
		}
		varIdx++
	}
	return out
}

// pupilModelForXMulti is pupilModelForX for a multi-config document: the
// minimum's own virtual entrance pupil (see pupilModelForX).
func pupilModelForXMulti(input *types.Input, opt *types.OptimizationConfig, x []float64) *types.PupilModelConfig {
	return resolvedPupilModelMulti(pupilModelForInput(input), opt, x)
}

// applyPupilVariablesMulti writes the pupil-model local variables of the flat
// multi-config vector x into the shared chief pupil model (the pipeline YAML
// carries one top-level pupil_model, shared by every config). The index
// mirrors applyEscapeMulti's active-only ordering (shared variables first,
// then local variables).
func applyPupilVariablesMulti(input *types.Input, opt *types.OptimizationConfig, x []float64) {
	base := pupilModelForInput(input)
	m := resolvedPupilModelMulti(base, opt, x)
	if m == nil {
		return
	}
	if base != nil {
		*base = *m
		return
	}
	if input.Chief == nil {
		input.Chief = &types.ChiefInput{}
	}
	input.Chief.PupilModel = m
}

// computePupilZ returns the entrance pupil Z used to centre grid traces for one
// config's initial surfaces: the explicit stop's paraxial entrance pupil (the
// stop's image — where chief aims its own grid), else the dynamic pupil from a
// chief pass over the initial surfaces, else 0. It seeds the optimiser's grid
// centring (the pupil is recomputed during Phase-2 runs).
func computePupilZ(input types.Input, surfaces []types.Surface, gc *glass.Catalog) float64 {
	if input.Chief == nil {
		return 0
	}
	if input.Chief.StopSurface > 0 {
		// Match chief: a stop system's pupil is the paraxial entrance pupil, so
		// the seed and the reported pupil never disagree. The stop's own vertex
		// Z stays the fallback (a folded object side, or an under-determined
		// paraxial pupil).
		if z, ok := chief.StopEntrancePupilZ(surfaces, input.Chief.StopSurface, effectiveReferenceWavelength(input.Chief), gc); ok {
			return z
		}
		for _, s := range surfaces {
			if s.ID == input.Chief.StopSurface {
				return s.PhysicalZ
			}
		}
	}
	pupil, _ := dynamicEntrancePupil(surfaces, chiefFieldDefs(input), input.Chief.ReferenceSurface, input.Chief.NumRays, gc, polarization(input), input.Chief.GridType, input.Chief.PassThrough, input.Chief.PupilModel, chiefRayDefinition(input))
	if pupil != nil {
		return pupil.Center.Z
	}
	return 0
}

// chiefRayDefinition returns the document's chief-ray definition
// (chief.chief_ray_definition, "" when the chief section is absent).
func chiefRayDefinition(input types.Input) string {
	if input.Chief == nil {
		return ""
	}
	return input.Chief.ChiefRayDefinition
}

// polarization returns the ray polarization from the input rays section, or a
// default circular Jones vector.
func polarization(input types.Input) types.JonesVector {
	if input.Rays != nil {
		return input.Rays.Polarization
	}
	return types.NewCircularJones(true)
}

// chiefFieldDefs returns chief field definitions from the top-level chief
// section (explicit fields, else the angle list), or nil when absent.
func chiefFieldDefs(input types.Input) []types.FieldDef {
	if input.Chief == nil {
		return nil
	}
	if len(input.Chief.Fields) > 0 {
		return input.Chief.Fields
	}
	var fields []types.FieldDef
	for _, a := range input.Chief.FieldAngles {
		fields = append(fields, types.FieldDef{Angle: a, Direction: []float64{0, 1}})
	}
	return fields
}

// resolveConfigFieldDefs returns the field definitions a single-system command
// should analyse, in the same order `psf`/`focus`/`asphere` use:
//
//  1. the config named by --config, when it declares fields;
//  2. configs[0], when it declares fields;
//  3. the top-level chief fields (explicit `fields`, else `field_angles`).
//
// In a zoom every config carries its own fields (its own field angles, or the
// constant image heights of that zoom position), and the surfaces come from the
// same config (configSurfaces). Analysing one config with another's field list
// silently answers a different question, so the config's own fields win;
// a document that declares its fields once, in `chief`, keeps working through
// the fallback.
func resolveConfigFieldDefs(input types.Input, configFlag *string) []types.FieldDef {
	if configFlag != nil && *configFlag != "" {
		idx, err := resolveConfig(input.Configs, *configFlag)
		if idx < 0 {
			errOut("Error: %s", err)
			os.Exit(1)
		}
		if defs := fieldDefsFromItems(input.Configs[idx].Fields); len(defs) > 0 {
			return defs
		}
	}
	if len(input.Configs) > 0 {
		if defs := fieldDefsFromItems(input.Configs[0].Fields); len(defs) > 0 {
			return defs
		}
	}
	return chiefFieldDefs(input)
}

// fieldItemsFromDefs converts chief field definitions back into the per-config
// field-item shape (the inverse of fieldDefsFromItems), so a document whose
// fields live only in chief.fields can still drive per-config machinery such as
// the all-field back-focus solve.
func fieldItemsFromDefs(defs []types.FieldDef) []types.FieldItem {
	if len(defs) == 0 {
		return nil
	}
	out := make([]types.FieldItem, 0, len(defs))
	for _, d := range defs {
		dir := d.Direction
		if len(dir) == 0 {
			dir = []float64{0, 1}
		}
		out = append(out, types.FieldItem{
			AngleDeg:    d.Angle,
			ImageHeight: d.ImageHeight,
			Height:      d.Height,
			ObjectZ:     d.ObjectZ,
			Direction:   dir,
			Weight:      1,
			Vignetting:  d.Vignetting,
		})
	}
	return out
}

// fieldDefsFromItems converts per-config field items into chief field
// definitions for the dynamic-pupil pass.
func fieldDefsFromItems(items []types.FieldItem) []types.FieldDef {
	if len(items) == 0 {
		return nil
	}
	out := make([]types.FieldDef, len(items))
	for i, f := range items {
		out[i] = types.FieldDef{
			Angle:       f.AngleDeg,
			ImageHeight: f.ImageHeight,
			Height:      f.Height,
			ObjectZ:     f.ObjectZ,
			Direction:   f.Direction,
			Vignetting:  f.Vignetting,
		}
		if len(f.Direction) == 0 {
			out[i].Direction = []float64{0, 1}
		}
	}
	return out
}

// dynamicEntrancePupil runs the dynamic-pupil chief pass over surfaces with the
// given fields and reference surface and returns the first field's entrance
// pupil (nil when none is found). It derives the pupil for stop-free systems,
// which have no aperture stop to trace from. A non-nil pupilModel activates the
// virtual-entrance-pupil path (fixed pupil Z and diameter). rayDefinition is
// the document's chief-ray definition ("" = the per-system default).
//
// The error reports a field declared by image_height that the pass could not
// resolve; the caller decides whether to warn about it (the reporting commands)
// or ignore it (the optimiser's own pupil seeding, an asphere validation).
func dynamicEntrancePupil(surfaces []types.Surface, fields []types.FieldDef, refSurface, numRays int, gc *glass.Catalog, pol types.JonesVector, gridType types.GridType, passThrough *types.PassThroughTarget, pupilModel *types.PupilModelConfig, rayDefinition string) (*types.Pupil, error) {
	if len(fields) == 0 || len(surfaces) == 0 || refSurface <= 0 {
		return nil, nil
	}
	surface.Precompute(surfaces)
	results := chief.DetermineChiefRaysGridMode(
		types.System{Surfaces: surfaces},
		fields, refSurface, numRays, gc, pol,
		types.DefaultWavelength, false, gridType, passThrough, nil, nil, pupilModel, 0, 0,
		rayDefinition,
	)
	if err := chief.CheckFieldResolution("", results); err != nil {
		return nil, err
	}
	for _, r := range results {
		if r.EntrancePupil != nil {
			return r.EntrancePupil, nil
		}
	}
	return nil, nil
}

// dynamicPupilForInput returns the dynamic entrance pupil for the selected
// system: the per-config fields when --config is set, else the top-level chief
// fields. Nil when the chief section or a reference surface is absent. A
// virtual_entrance_pupil model is honored (fixed pupil Z and diameter).
func dynamicPupilForInput(input types.Input, configFlag *string, surfaces []types.Surface, gc *glass.Catalog) (*types.Pupil, error) {
	if input.Chief == nil || input.Chief.ReferenceSurface <= 0 {
		return nil, nil
	}
	fields := chiefFieldDefs(input)
	if configFlag != nil && *configFlag != "" {
		if idx, _ := resolveConfig(input.Configs, *configFlag); idx >= 0 {
			if cfgFields := fieldDefsFromItems(input.Configs[idx].Fields); len(cfgFields) > 0 {
				fields = cfgFields
			}
		}
	}
	return dynamicEntrancePupil(surfaces, fields, input.Chief.ReferenceSurface, input.Chief.NumRays, gc, polarization(input), types.GridPolar, nil, input.Chief.PupilModel, chiefRayDefinition(input))
}

// requireFieldResolution aborts a command that measures fields when a chief pass
// could not resolve a field declared by image_height to the height the document
// asks for. name labels the field set (a config id, or the command's own name).
//
// The chief pass itself keeps the closest reachable angle rather than folding
// the field onto the axis, because a caller that must keep going mid-search (the
// optimiser) cannot abort there. A command that measures the field must abort
// instead: the field it would otherwise report is not the one the document
// declares.
func requireFieldResolution(name string, results []chief.Result) {
	if err := chief.CheckFieldResolution(name, results); err != nil {
		errOut("Error: %v", err)
		os.Exit(1)
	}
}

// warnFieldResolution reports every field a chief pass could not resolve to the
// image height the document declares, without stopping. `list fields` uses it:
// that command's contract is to display the input/computed mismatch, so it must
// keep listing the field rather than refuse to report the document (see
// requireFieldResolution for the commands that measure the field and must stop).
func warnFieldResolution(name string, results []chief.Result) {
	for _, w := range chief.FieldResolutionWarnings(name, results) {
		errOut("Warning: %s", w)
	}
}

// warnFieldSkips is warnFieldResolution for the commands that leave such a
// field out of their result rather than measuring or optimising it (import,
// importsweep, vignette): those neither report the field nor need it to keep
// going, so the closest reachable field would only misrepresent the document —
// the warning says the field was dropped. The solutions must be passed in field
// order (chief.FieldSolutions), or the message numbers the wrong field.
func warnFieldSkips(name string, sols []chief.FieldSolution) {
	for i, sol := range sols {
		if sol.Resolved {
			continue
		}
		errOut("Warning: %s; the field is skipped", chief.FieldResolutionMessage(name, i, sol))
	}
}

// freezeFieldReport records the field angles the run's model froze at its start
// geometry in the output document (optimization.field_resolution) and warns
// about the image_height fields that geometry cannot form.
//
// A solve cannot stop on such a field, so it optimises the closest reachable
// angle instead — and that has to be said out loud and in the document, or a
// merit looking fine on the wrong field ships as if it were the declared one.
// The model is the run's freshly built one: the freeze happened in its
// constructor, so the report is available before the search starts.
func freezeFieldReport(input *types.Input, model any) {
	fr, ok := model.(dls.FieldResolutionReporter)
	if !ok {
		return
	}
	rows := fr.FieldResolutionDiagnostics()
	if len(rows) == 0 {
		return
	}
	if input.Optimization != nil {
		input.Optimization.FieldResolution = rows
	}
	for _, r := range rows {
		if r.Resolved {
			continue
		}
		label := ""
		if r.Config != "" {
			label = fmt.Sprintf("config %q: ", r.Config)
		}
		errOut("Warning: %sfield %d declares image_height %g mm, which this system cannot form — the closest reachable field is %g mm at %g deg, and that angle is what this run evaluates for it",
			label, r.FieldIndex, r.Requested, r.Reached, r.AngleDeg)
	}
}

// writeYAML marshals a value to stdout, exiting on error.
func writeYAML(v any) {
	outData, err := yaml.Marshal(v)
	if err != nil {
		errOut("Error marshaling output: %v", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}

// buildOptimizeVariables converts single-config `optimization.variables` into
// the unified Optimizer variable list (surface targets only).
func buildOptimizeVariables(opt *types.OptimizationConfig, gc *glass.Catalog) []optimize.Variable {
	var variables []optimize.Variable
	for _, v := range opt.Variables {
		if !v.Active {
			continue
		}

		switch v.Target.Type {
		case "surface":
			var min, max float64 = v.Min, v.Max
			if min == 0 && max == 0 {
				switch v.Target.Param {
				case "curvature":
					min, max = -0.5, 0.5
				case "conic":
					min, max = -1.0, 1.0
				case "a4", "coefficient_0":
					min, max = -1e-2, 1e-2
				case "a6", "coefficient_1":
					min, max = -1e-3, 1e-3
				case "a8", "coefficient_2":
					min, max = -1e-4, 1e-4
				case "a10", "coefficient_3", "a12", "coefficient_4":
					min, max = -1e-5, 1e-5
				case "thickness":
					min, max = 0.1, 50.0
				case "nd":
					min, max = 1.4, 1.9
				case "vd":
					min, max = 20.0, 80.0
				}
			}
			variables = append(variables, optimize.Variable{
				Name:      v.Name,
				SurfaceID: v.Target.ID,
				Param:     v.Target.Param,
				Min:       min,
				Max:       max,
			})
		case "pupil_model":
			var min, max float64 = v.Min, v.Max
			if min == 0 && max == 0 {
				switch v.Target.Param {
				case "axial_position":
					min, max = -200.0, 50.0
				case "diameter":
					min, max = 5.0, 50.0
				}
			}
			variables = append(variables, optimize.Variable{
				Name:  v.Name,
				Param: "pupil_model_" + v.Target.Param,
				Min:   min,
				Max:   max,
			})
		default:
			continue
		}
	}
	return variables
}

// buildMeritTerms converts the first config's merit terms into the unified
// Optimizer term list (single-config escape / evaluate paths).
func buildMeritTerms(input types.Input) []optimize.MeritTerm {
	var terms []optimize.MeritTerm

	if len(input.Configs) > 0 {
		cfg := input.Configs[0]
		if cfg.Merit != nil {
			for _, mt := range cfg.Merit.Terms {
				kind := mt.Kind
				if kind == "" {
					kind = "spot_rms"
				}

				fieldIndex := mt.Field
				var fieldAngle, fieldWeight float64
				var fieldDir = []float64{0, 1}
				for i, f := range cfg.Fields {
					if f.ID == mt.Field {
						fieldIndex = i
						fieldAngle = f.AngleDeg
						fieldWeight = f.Weight
						if len(f.Direction) >= 2 {
							fieldDir = f.Direction
						}
						break
					}
				}
				if fieldWeight == 0 {
					fieldWeight = 1.0
				}

				var wavWeight float64
				for _, w := range cfg.Wavelengths {
					if math.Abs(w.Value-mt.Wavelength) < 1e-12 {
						wavWeight = w.Weight
						break
					}
				}
				if wavWeight == 0 {
					wavWeight = 1.0
				}

				terms = append(terms, optimize.MeritTerm{
					Kind:                 kind,
					FieldAngle:           fieldAngle,
					FieldDir:             fieldDir,
					FieldIndex:           fieldIndex,
					FieldWeight:          fieldWeight,
					Wavelength:           mt.Wavelength,
					ComparisonWavelength: mt.ComparisonWavelength,
					WavWeight:            wavWeight,
					Weight:               mt.Weight,
					Target:               mt.Target,
					Fraction:             mt.Fraction,
					SurfaceSet:           mt.SurfaceSet,
				})
			}
		}
	}

	return terms
}

// quoteCSV escapes one CSV row: cells containing comma, quote or newline are
// double-quoted with embedded quotes doubled (RFC 4180). Shared by the query
// and list subcommands' --csv output.
func quoteCSV(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		if strings.ContainsAny(c, ",\"\n") {
			out[i] = "\"" + strings.ReplaceAll(c, "\"", "\"\"") + "\""
		} else {
			out[i] = c
		}
	}
	return out
}

// applyDegenerate copies the optimization.degenerate penalties (spot/opd/
// wavefront) onto an optimizer. Non-positive values keep the built-in defaults
// (spot 0.1, opd 0.01, wavefront 0.001 mm).
func applyDegenerate(opt *optimize.Optimizer, dg *types.DegenerateConfig) {
	if dg == nil {
		return
	}
	opt.SetDegenerate(dg.SpotValue, dg.OPDValue, dg.WavefrontValue)
}
