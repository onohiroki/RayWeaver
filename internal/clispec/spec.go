// Package clispec is the single declaration of a subcommand's command-line
// flags. The CLI registers its flags from it, the write-back of effective
// values is driven by it, and the MCP tools derive their argv mapping from it,
// so a flag is added or renamed in one place.
//
// The CLI/YAML rule is encoded in the types: an unset flag keeps its Default
// (never overriding the document), Set reports whether it was given, and
// WriteBack writes only the values that were given (or the flags marked
// Always, such as the resolved reference wavelength).
package clispec

import (
	"encoding/json"
	"flag"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// Kind is the value type a flag takes on the command line.
type Kind int

const (
	Bool Kind = iota
	Int
	Float
	String
	FloatList  // repeatable or space-separated numbers (--fan-rotation)
	IntList    // repeatable or comma-separated integers (--fields)
	StringList // repeatable string values (--element-color)
	BoolString // a two-valued flag written as --name true|false (--stream)
)

// Role describes what the flag does to the document or the run.
type Role int

const (
	// RoleSetting is a computation setting backed by a YAML field; it is a
	// write-back candidate.
	RoleSetting Role = iota
	// RoleSelector selects data (--config) and is never written back.
	RoleSelector
	// RoleAction changes the run's output (--clear-aperture, --marginal-rays);
	// its effect is written back, not the flag value.
	RoleAction
	// RoleOutput names an output file (profile, --yaml, --csv).
	RoleOutput
)

// Flag declares one command-line flag.
type Flag struct {
	Name    string // long name without leading dashes
	Short   string // single-dash alias, e.g. "o"
	Kind    Kind
	Default any // the unset value (false / 0 / "" / nil / []float64)
	Help    string

	Role Role
	// YAML is the dotted path the effective value is written back to, e.g.
	// "chief.reference_wavelength". Simple scalar settings only.
	YAML string
	// Apply writes back a setting that a path cannot express (activating a
	// pupil model, converting f-number to a diameter, ...). It runs only when
	// the flag was given, unless Always is set.
	Apply func(*types.Input, *Options)
	// Always writes the effective value back even when the flag was not given,
	// for settings the command always resolves (the reference wavelength).
	Always bool
	// Alias is the long spelling of the canonical Name: both register, and
	// either one marks the canonical flag as given. The canonical value wins
	// when both are spelled out; the command can still test Given(alias) for a
	// mutual-exclusion error.
	Alias string
	// Repeat documents that a FloatList flag may be given more than once.
	Repeat bool
	// MCPSkip keeps the flag out of the MCP tool schema (output-file and
	// profile flags, which the dedicated tools own).
	MCPSkip bool
}

// Spec is one command's flag surface.
type Spec struct {
	Command string
	Flags   []Flag
}

// Options holds the registered flags and their parsed values.
type Options struct {
	spec      *Spec
	fs        *flag.FlagSet
	canonical map[string]string // registered name (canonical/short/alias) -> canonical

	bools    map[string]*bool
	ints     map[string]*int
	floats   map[string]*float64
	strs     map[string]*string
	lists    map[string]*[]float64
	strLists map[string]*[]string
	intLists map[string]*[]int

	given map[string]bool // raw registered name was on the command line
	set   map[string]bool // canonical name was given (via any spelling)
}

// Bind registers every flag of spec on fs and returns the value holder.
func Bind(spec Spec, fs *flag.FlagSet) *Options {
	o := &Options{
		spec:      &spec,
		fs:        fs,
		canonical: map[string]string{},
		bools:     map[string]*bool{},
		ints:      map[string]*int{},
		floats:    map[string]*float64{},
		strs:      map[string]*string{},
		lists:     map[string]*[]float64{},
		strLists:  map[string]*[]string{},
		intLists:  map[string]*[]int{},
		given:     map[string]bool{},
		set:       map[string]bool{},
	}
	for i := range spec.Flags {
		f := &spec.Flags[i]
		o.canonical[f.Name] = f.Name
		if f.Short != "" {
			o.canonical[f.Short] = f.Name
		}
		if f.Alias != "" {
			o.canonical[f.Alias] = f.Name
		}
		o.register(f)
	}
	return o
}

// register adds the canonical flag plus its short and long aliases, all
// sharing one value slot.
func (o *Options) register(f *Flag) {
	names := []string{f.Name}
	if f.Short != "" {
		names = append(names, f.Short)
	}
	if f.Alias != "" {
		names = append(names, f.Alias)
	}
	switch f.Kind {
	case Bool:
		v := new(bool)
		if d, ok := f.Default.(bool); ok {
			*v = d
		}
		o.bools[f.Name] = v
		for _, n := range names {
			o.fs.BoolVar(v, n, *v, f.Help)
		}
	case Int:
		v := new(int)
		if d, ok := f.Default.(int); ok {
			*v = d
		}
		o.ints[f.Name] = v
		for _, n := range names {
			o.fs.IntVar(v, n, *v, f.Help)
		}
	case Float:
		v := new(float64)
		if d, ok := f.Default.(float64); ok {
			*v = d
		}
		o.floats[f.Name] = v
		for _, n := range names {
			o.fs.Float64Var(v, n, *v, f.Help)
		}
	case String:
		v := new(string)
		if d, ok := f.Default.(string); ok {
			*v = d
		}
		o.strs[f.Name] = v
		for _, n := range names {
			o.fs.StringVar(v, n, *v, f.Help)
		}
	case FloatList:
		v := &[]float64{}
		o.lists[f.Name] = v
		for _, n := range names {
			o.fs.Var(&floatListValue{v: v}, n, f.Help)
		}
	case IntList:
		v := &[]int{}
		o.intLists[f.Name] = v
		for _, n := range names {
			o.fs.Var(&intListValue{v: v}, n, f.Help)
		}
	case StringList:
		v := &[]string{}
		o.strLists[f.Name] = v
		for _, n := range names {
			o.fs.Var(&stringListValue{v: v}, n, f.Help)
		}
	case BoolString:
		v := new(string)
		if d, ok := f.Default.(string); ok {
			*v = d
		}
		o.strs[f.Name] = v
		for _, n := range names {
			o.fs.StringVar(v, n, *v, f.Help)
		}
	}
}

// Parse parses args and records which registered names were given.
func (o *Options) Parse(args []string) error {
	if err := o.fs.Parse(args); err != nil {
		return err
	}
	o.fs.Visit(func(f *flag.Flag) {
		o.given[f.Name] = true
		if c, ok := o.canonical[f.Name]; ok {
			o.set[c] = true
		}
	})
	return nil
}

// Given reports whether the exact registered spelling was used (a canonical
// name, a short alias or a long alias).
func (o *Options) Given(name string) bool { return o.given[name] }

// Set reports whether the canonical flag was given through any spelling.
func (o *Options) Set(name string) bool {
	c, ok := o.canonical[name]
	if !ok {
		return false
	}
	return o.set[c]
}

// Bool, Int, Float, String and Floats return the parsed value (the Default
// when the flag was not given).
func (o *Options) Bool(name string) bool {
	if v, ok := o.bools[name]; ok {
		return *v
	}
	return false
}

func (o *Options) Int(name string) int {
	if v, ok := o.ints[name]; ok {
		return *v
	}
	return 0
}

func (o *Options) Float(name string) float64 {
	if v, ok := o.floats[name]; ok {
		return *v
	}
	return 0
}

func (o *Options) String(name string) string {
	if v, ok := o.strs[name]; ok {
		return *v
	}
	return ""
}

func (o *Options) Floats(name string) []float64 {
	if v, ok := o.lists[name]; ok {
		return *v
	}
	return nil
}

func (o *Options) Ints(name string) []int {
	if v, ok := o.intLists[name]; ok {
		return *v
	}
	return nil
}

// IntsString renders an IntList as the comma-separated form the CLI helpers
// take (--fields 0,1,2).
func (o *Options) IntsString(name string) string {
	vals := o.Ints(name)
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

func (o *Options) Strings(name string) []string {
	if v, ok := o.strLists[name]; ok {
		return *v
	}
	return nil
}

// BoolString reads a two-valued flag (`--lenient true|false`) and reports
// whether it was given. The value is parsed with strconv.ParseBool, the same
// spelling the CLI accepts (true/false/1/0/t/f).
func (o *Options) BoolString(name string) (value bool, given bool, err error) {
	if !o.Set(name) {
		return false, false, nil
	}
	s := o.String(name)
	v, perr := strconv.ParseBool(s)
	if perr != nil {
		return false, true, fmt.Errorf("--%s expects a boolean (true/false), got %q", name, s)
	}
	return v, true, nil
}

// AnySet reports whether at least one of the named flags was given.
func (o *Options) AnySet(names ...string) bool {
	for _, n := range names {
		if o.Set(n) {
			return true
		}
	}
	return false
}

// WriteBack applies the effective value of every setting flag that was given,
// in declaration order. YAML-path flags are written into input directly;
// Apply hooks handle settings whose write-back is conditional or computed.
func (o *Options) WriteBack(input *types.Input) {
	for i := range o.spec.Flags {
		f := &o.spec.Flags[i]
		if f.Role != RoleSetting {
			continue
		}
		if !f.Always && !o.Set(f.Name) {
			continue
		}
		if f.Apply != nil {
			f.Apply(input, o)
			continue
		}
		if f.YAML == "" {
			continue
		}
		setYAMLPath(input, f.YAML, o.value(f.Name, f.Kind))
	}
}

// value returns the typed value as an any for the YAML write-back.
func (o *Options) value(name string, kind Kind) any {
	switch kind {
	case Bool:
		return o.Bool(name)
	case Int:
		return o.Int(name)
	case Float:
		return o.Float(name)
	case String:
		return o.String(name)
	case FloatList:
		return o.Floats(name)
	case BoolString:
		return o.String(name)
	}
	return nil
}

// setYAMLPath writes value at a dotted path inside the document. The input is
// marshalled once, patched and unmarshalled back, which keeps the code free of
// reflection over pointers and slices; the cost is irrelevant (once per run).
func setYAMLPath(input *types.Input, path string, value any) {
	if input == nil || path == "" {
		return
	}
	data, err := yaml.Marshal(input)
	if err != nil {
		return
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return
	}
	parts := strings.Split(path, ".")
	cur := doc
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = value
	data, err = yaml.Marshal(doc)
	if err != nil {
		return
	}
	_ = yaml.Unmarshal(data, input)
}

// floatListValue is the flag.Value behind a FloatList flag: each Set appends,
// so repeated flags and space-separated groups both accumulate.
type floatListValue struct {
	v *[]float64
}

func (f *floatListValue) String() string {
	if f == nil || f.v == nil {
		return ""
	}
	parts := make([]string, len(*f.v))
	for i, x := range *f.v {
		parts[i] = strconv.FormatFloat(x, 'g', -1, 64)
	}
	return strings.Join(parts, ",")
}

func (f *floatListValue) Set(s string) error {
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		x, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return fmt.Errorf("invalid number %q", part)
		}
		*f.v = append(*f.v, x)
	}
	return nil
}

// Args renders the argv for the flags a tool received. in is JSON-marshalled
// and the spec decides how each value becomes an argument, so the MCP layer no
// longer repeats one mapping line per flag. overrides renames a JSON field when
// it differs from the flag (e.g. "wavelength" -> "wl"); everything else is
// snake_case -> kebab-case. Fields that name no flag in the spec (document
// input, nested settings the tool handles itself) are ignored, as are
// MCPSkip flags. An absent or zero MCP argument emits nothing, so it never
// overrides the document.
func Args(spec Spec, in any, overrides map[string]string) []string {
	canonical := map[string]string{}
	byName := map[string]Flag{}
	for _, f := range spec.Flags {
		byName[f.Name] = f
		canonical[f.Name] = f.Name
		if f.Short != "" {
			canonical[f.Short] = f.Name
		}
		if f.Alias != "" {
			canonical[f.Alias] = f.Name
		}
	}

	data, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return nil
	}

	type arg struct {
		name  string
		value string
		bare  bool
	}
	var out []arg
	for field, v := range values {
		name := overrides[field]
		if name == "" {
			name = strings.ReplaceAll(field, "_", "-")
		}
		canon, ok := canonical[name]
		if !ok {
			continue
		}
		f, ok := byName[canon]
		if !ok || f.MCPSkip {
			continue
		}
		switch f.Kind {
		case Bool:
			if b, ok := v.(bool); ok && b {
				out = append(out, arg{name: canon, bare: true})
			}
		case BoolString:
			if b, ok := v.(bool); ok {
				out = append(out, arg{name: canon, value: strconv.FormatBool(b)})
			}
		case Int:
			if n, ok := v.(float64); ok && n != 0 {
				out = append(out, arg{name: canon, value: strconv.Itoa(int(n))})
			}
		case Float:
			if n, ok := v.(float64); ok && n != 0 {
				out = append(out, arg{name: canon, value: strconv.FormatFloat(n, 'g', -1, 64)})
			}
		case String:
			switch val := v.(type) {
			case string:
				if val != "" {
					out = append(out, arg{name: canon, value: val})
				}
			case []any:
				// A comma-separated flag whose MCP field is an array.
				parts := make([]string, 0, len(val))
				for _, e := range val {
					if s, ok := e.(string); ok && s != "" {
						parts = append(parts, s)
					}
				}
				if len(parts) > 0 {
					out = append(out, arg{name: canon, value: strings.Join(parts, ",")})
				}
			}
		case FloatList:
			arr, ok := v.([]any)
			if !ok || len(arr) == 0 {
				continue
			}
			parts := make([]string, 0, len(arr))
			for _, e := range arr {
				if n, ok := e.(float64); ok {
					parts = append(parts, strconv.FormatFloat(n, 'g', -1, 64))
				}
			}
			if len(parts) > 0 {
				out = append(out, arg{name: canon, value: strings.Join(parts, ",")})
			}
		case IntList:
			arr, ok := v.([]any)
			if !ok || len(arr) == 0 {
				continue
			}
			parts := make([]string, 0, len(arr))
			for _, e := range arr {
				if n, ok := e.(float64); ok {
					parts = append(parts, strconv.Itoa(int(n)))
				}
			}
			if len(parts) > 0 {
				out = append(out, arg{name: canon, value: strings.Join(parts, ",")})
			}
		case StringList:
			// Values may contain commas (colour maps), so every element is
			// emitted as its own flag occurrence.
			arr, ok := v.([]any)
			if !ok {
				continue
			}
			for _, e := range arr {
				if s, ok := e.(string); ok && s != "" {
					out = append(out, arg{name: canon, value: s})
				}
			}
		}
	}

	// Sort for a deterministic argv (the order does not affect the CLI, but it
	// keeps tests and logs stable).
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	args := make([]string, 0, len(out))
	for _, a := range out {
		args = append(args, "--"+a.name)
		if !a.bare {
			args = append(args, a.value)
		}
	}
	return args
}

// All lists the command flag tables. The help-consistency test and the MCP
// drift test walk it, so a new table must be added here when a command is
// migrated.
var All = []*Spec{&Chief, &Trace, &Paraxial, &Vignette, &Plot, &Zoom, &Cam, &Scale, &TMM, &Clean, &Wavefront, &Asphere, &PSF, &Focus, &FocusMTF}

// stringListValue is the flag.Value behind a StringList flag: every Set
// appends the raw value, so repeated flags accumulate.
type stringListValue struct {
	v *[]string
}

func (s *stringListValue) String() string {
	if s == nil || s.v == nil {
		return ""
	}
	return strings.Join(*s.v, ",")
}

func (s *stringListValue) Set(v string) error {
	*s.v = append(*s.v, v)
	return nil
}

// intListValue is the flag.Value behind an IntList flag: each Set appends the
// comma-separated integers it contains.
type intListValue struct {
	v *[]int
}

func (s *intListValue) String() string {
	if s == nil || s.v == nil {
		return ""
	}
	parts := make([]string, len(*s.v))
	for i, x := range *s.v {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}

func (s *intListValue) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return fmt.Errorf("invalid integer %q", part)
		}
		*s.v = append(*s.v, n)
	}
	return nil
}
