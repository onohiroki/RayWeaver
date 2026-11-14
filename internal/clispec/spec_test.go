package clispec

import (
	"flag"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func bind(t *testing.T, spec Spec, args ...string) *Options {
	t.Helper()
	fs := flag.NewFlagSet(spec.Command, flag.ContinueOnError)
	o := Bind(spec, fs)
	if err := o.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return o
}

// TestBindValuesAndAliases pins the CLI/YAML rule on the value holder: an
// unset flag keeps its default, Set reports any spelling, Given reports the
// exact spelling (for mutual-exclusion errors), and list flags accumulate.
func TestBindValuesAndAliases(t *testing.T) {
	spec := Spec{Command: "t", Flags: []Flag{
		{Name: "epd", Kind: Float, Alias: "pupil-model-diameter", Help: "d"},
		{Name: "mode", Kind: String, Default: "default-mode", Help: "m"},
		{Name: "count", Kind: Int, Short: "n", Help: "c"},
		{Name: "planes", Kind: FloatList, Repeat: true, Help: "p"},
	}}
	o := bind(t, spec, "--pupil-model-diameter", "5", "-n", "3", "--planes", "1,2", "--planes", "3")

	if !o.Set("epd") {
		t.Error("Set(epd) = false, want true through the alias")
	}
	if !o.Given("pupil-model-diameter") {
		t.Error("Given(alias) = false, want true")
	}
	if o.Given("epd") {
		t.Error("Given(epd) = true, want false (the alias was used)")
	}
	if got := o.Float("epd"); got != 5 {
		t.Errorf("Float(epd) = %v, want 5 (the alias shares the canonical slot)", got)
	}
	if got := o.String("mode"); got != "default-mode" {
		t.Errorf("String(mode) = %q, want the default", got)
	}
	if o.Set("mode") {
		t.Error("Set(mode) = true for an unset flag")
	}
	if got := o.Int("count"); got != 3 {
		t.Errorf("Int(count) = %d, want 3 (short -n)", got)
	}
	if got := o.Floats("planes"); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("Floats(planes) = %v, want [1 2 3]", got)
	}
}

// TestWriteBackOnlyGiven guards principle 3: an unset flag must not touch the
// document, a given one writes only its own value.
func TestWriteBackOnlyGiven(t *testing.T) {
	spec := Spec{Command: "t", Flags: []Flag{
		{Name: "chief-ray", Kind: String, Role: RoleSetting, YAML: "chief.chief_ray_definition", Help: "d"},
	}}

	untouched := types.Input{}
	bind(t, spec).WriteBack(&untouched)
	if untouched.Chief != nil {
		t.Fatalf("unset flag created the chief section: %+v", untouched.Chief)
	}

	input := types.Input{Chief: &types.ChiefInput{ChiefRayDefinition: "centroid"}}
	bind(t, spec, "--chief-ray", "vignetting_centre").WriteBack(&input)
	if got := input.Chief.ChiefRayDefinition; got != "vignetting_centre" {
		t.Fatalf("chief_ray_definition = %q, want the flag value", got)
	}
}

// TestChiefSpecShape checks the table itself: the default's type matches the
// kind, canonical names are unique, and an alias never repeats the name.
func TestChiefSpecShape(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Chief.Flags {
		if seen[f.Name] {
			t.Errorf("duplicate flag %q", f.Name)
		}
		seen[f.Name] = true
		if f.Alias == f.Name {
			t.Errorf("%q aliases itself", f.Name)
		}
		if f.Help == "" {
			t.Errorf("%q has no help text", f.Name)
		}
		switch f.Kind {
		case Bool:
			if f.Default != nil {
				if _, ok := f.Default.(bool); !ok {
					t.Errorf("%q default %T is not bool", f.Name, f.Default)
				}
			}
		case Int:
			if f.Default != nil {
				if _, ok := f.Default.(int); !ok {
					t.Errorf("%q default %T is not int", f.Name, f.Default)
				}
			}
		case Float:
			if f.Default != nil {
				if _, ok := f.Default.(float64); !ok {
					t.Errorf("%q default %T is not float64", f.Name, f.Default)
				}
			}
		case String:
			if f.Default != nil {
				if _, ok := f.Default.(string); !ok {
					t.Errorf("%q default %T is not string", f.Name, f.Default)
				}
			}
		case FloatList:
			if f.Default != nil {
				t.Errorf("%q default %T, want nil for a list", f.Name, f.Default)
			}
		}
	}
}

// TestArgsRendersToolInput pins the MCP-side mapping: JSON fields become the
// canonical flag, aliases collapse to the canonical spelling, lists join with
// commas, and an unset field emits nothing.
func TestArgsRendersToolInput(t *testing.T) {
	type chiefInput struct {
		Wavelength    float64   `json:"wavelength"`
		PassThrough   int       `json:"pass_through"`
		ClearAperture bool      `json:"clear_aperture"`
		FanRotation   []float64 `json:"fan_rotation"`
		PupilDiameter float64   `json:"pupil_model_diameter"`
		Config        string    `json:"config"`
		Document      string    `json:"document"`
	}
	got := Args(Chief, chiefInput{
		Wavelength:    0.5,
		PassThrough:   3,
		ClearAperture: true,
		FanRotation:   []float64{0, 45},
		PupilDiameter: 7,
		Config:        "tele",
		Document:      "ignored",
	}, map[string]string{"wavelength": "wl"})

	want := []string{"--clear-aperture", "--config", "tele", "--epd", "7", "--fan-rotation", "0,45", "--pass-through", "3", "--wl", "0.5"}
	if len(got) != len(want) {
		t.Fatalf("Args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Args = %v, want %v", got, want)
		}
	}

	if args := Args(Chief, chiefInput{}, nil); len(args) != 0 {
		t.Fatalf("Args on an empty input = %v, want none (an absent argument must not override the document)", args)
	}
}

// TestArgsRendersPlotMultiConfig pins the MCP mapping of the plot
// multi-config selectors: a bool becomes a bare flag and a string slice is
// joined into the comma-separated --configs value the CLI parses.
func TestArgsRendersPlotMultiConfig(t *testing.T) {
	type plotInput struct {
		MultiConfig bool     `json:"multi_config"`
		Configs     []string `json:"configs"`
	}
	got := Args(Plot, plotInput{MultiConfig: true, Configs: []string{"wide", "tele"}}, nil)
	want := []string{"--configs", "wide,tele", "--multi-config"}
	if len(got) != len(want) {
		t.Fatalf("Args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Args = %v, want %v", got, want)
		}
	}
}

// TestArgsRendersAllConfigs pins the same mapping for chief, which still takes
// an explicit --all-configs (trace infers the scope from the document).
func TestArgsRendersAllConfigs(t *testing.T) {
	type stageInput struct {
		AllConfigs bool     `json:"all_configs"`
		Configs    []string `json:"configs"`
	}
	in := stageInput{AllConfigs: true, Configs: []string{"wide", "tele"}}
	want := []string{"--all-configs", "--configs", "wide,tele"}
	got := Args(Chief, in, nil)
	if len(got) != len(want) {
		t.Fatalf("chief: Args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chief: Args = %v, want %v", got, want)
		}
	}

	// trace has no --all-configs: the same input must not invent the flag.
	if args := Args(Trace, in, nil); len(args) != 2 || args[0] != "--configs" {
		t.Errorf("trace: Args = %v, want only --configs wide,tele", args)
	}
}
