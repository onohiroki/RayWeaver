package exporter

import (
	"math"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/importer"
	"github.com/hiroki/rayweaver/internal/types"
)

// testInput returns a small triplet-like system: two cemented lenses, angle
// fields and two wavelengths, ending in a flat image plane.
func testInput() *types.Input {
	return &types.Input{
		Metadata: &types.Metadata{Tool: "RayWeaver", URL: "https://github.com/onohiroki/RayWeaver", SchemaVersion: 1},
		Chief:    &types.ChiefInput{StopSurface: 2},
		Configs: []types.Config{
			{
				ID: "config1", Name: "Config1", Weight: 1, Active: true,
				Fields: []types.FieldItem{
					{ID: 0, AngleDeg: 0, Weight: 1},
					{ID: 1, AngleDeg: 10, Weight: 1},
				},
				Wavelengths: []types.WavelengthItem{
					{ID: 0, Value: 0.00058756, Weight: 1, Primary: true},
					{ID: 1, Value: 0.00048613, Weight: 0.5},
				},
				Surfaces: []types.Surface{
					{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Diameter: 20, Material: types.Material{Key: "N-BK7"}},
					{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 2.0, Diameter: 20},
					{ID: 3, Type: types.Sphere, Curvature: 0.03, Thickness: 5.0, Diameter: 16, Material: types.Material{Key: "SF5"}},
					{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 0},
				},
			},
		},
	}
}

// roundTrip exports the input in the given format and re-imports it, returning
// the parsed result.
func roundTrip(t *testing.T, format string, in *types.Input, configs []int, w Warn) *importer.ParseResult {
	t.Helper()
	var out []byte
	var err error
	switch format {
	case "zemax":
		out, err = WriteZemax(in, configs, nil, w)
	case "codev":
		out, err = WriteCodeV(in, configs, nil, w)
	case "oslo":
		out, err = WriteOslo(in, configs[0], nil, w)
	}
	if err != nil {
		t.Fatalf("%s export failed: %v", format, err)
	}
	var pr *importer.ParseResult
	switch format {
	case "zemax":
		pr, err = importer.ParseZemax(string(out))
	case "codev":
		pr, err = importer.ParseCodeV(string(out))
	case "oslo":
		pr, err = importer.ParseOslo(string(out))
	}
	if err != nil {
		t.Fatalf("%s re-import failed: %v\n--- output ---\n%s", format, err, out)
	}
	return pr
}

func assertSurface(t *testing.T, label string, got, want types.Surface) {
	t.Helper()
	if math.Abs(got.Curvature-want.Curvature) > 1e-9 {
		t.Errorf("%s curvature: expected %g, got %g", label, want.Curvature, got.Curvature)
	}
	if math.Abs(got.Thickness-want.Thickness) > 1e-9 {
		t.Errorf("%s thickness: expected %g, got %g", label, want.Thickness, got.Thickness)
	}
	if math.Abs(got.Conic-want.Conic) > 1e-9 {
		t.Errorf("%s conic: expected %g, got %g", label, want.Conic, got.Conic)
	}
	if math.Abs(got.Diameter-want.Diameter) > 1e-9 {
		t.Errorf("%s diameter: expected %g, got %g", label, want.Diameter, got.Diameter)
	}
	if !sameCoeffs(got.Coefficients, want.Coefficients) {
		t.Errorf("%s coefficients: expected %v, got %v", label, want.Coefficients, got.Coefficients)
	}
	if want.Material.HasKey() && got.Material.Key != want.Material.Key {
		t.Errorf("%s material key: expected %q, got %q", label, want.Material.Key, got.Material.Key)
	}
	if want.Material.HasModel() && got.Material.HasModel() &&
		(math.Abs(got.Material.ND-want.Material.ND) > 1e-6 || math.Abs(got.Material.VD-want.Material.VD) > 1e-3) {
		t.Errorf("%s model glass: expected nd=%.6f vd=%.2f, got nd=%.6f vd=%.2f", label, want.Material.ND, want.Material.VD, got.Material.ND, got.Material.VD)
	}
}

func assertSurfaces(t *testing.T, format string, got []types.Surface, want []types.Surface) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s surface count: expected %d, got %d", format, len(want), len(got))
	}
	for i := range want {
		assertSurface(t, format+fmtSurface(i), got[i], want[i])
	}
}

func fmtSurface(i int) string { return " surface " + itoa(i+1) }

func cloneSurfaces(in []types.Surface) []types.Surface {
	out := make([]types.Surface, len(in))
	copy(out, in)
	return out
}

func TestZemaxRoundTrip(t *testing.T) {
	in := testInput()
	pr := roundTrip(t, "zemax", in, []int{0}, nil)
	assertSurfaces(t, "zemax", pr.Surfaces, in.Configs[0].Surfaces)
	if pr.StopSurface != 2 {
		t.Errorf("stop: expected 2, got %d", pr.StopSurface)
	}
	if len(pr.Fields) != 2 || math.Abs(pr.Fields[1].AngleDeg-10) > 1e-9 {
		t.Errorf("fields not preserved: %+v", pr.Fields)
	}
	if len(pr.Wavelengths) != 2 || math.Abs(pr.Wavelengths[0].Value-0.00058756) > 1e-12 {
		t.Errorf("wavelengths not preserved: %+v", pr.Wavelengths)
	}
}

func TestCodeVRoundTrip(t *testing.T) {
	in := testInput()
	pr := roundTrip(t, "codev", in, []int{0}, nil)
	assertSurfaces(t, "codev", pr.Surfaces, in.Configs[0].Surfaces)
	if pr.StopSurface != 2 {
		t.Errorf("stop: expected 2, got %d", pr.StopSurface)
	}
	if len(pr.Fields) != 2 || math.Abs(pr.Fields[1].AngleDeg-10) > 1e-9 {
		t.Errorf("fields not preserved: %+v", pr.Fields)
	}
}

func TestOsloRoundTrip(t *testing.T) {
	in := testInput()
	pr := roundTrip(t, "oslo", in, []int{0}, nil)
	assertSurfaces(t, "oslo", pr.Surfaces, in.Configs[0].Surfaces)
}

func TestModelGlassRoundTrip(t *testing.T) {
	in := testInput()
	in.Configs[0].Surfaces[0].Material = types.Material{ND: 1.77250, VD: 49.60}
	for _, format := range []string{"zemax", "codev", "oslo"} {
		pr := roundTrip(t, format, in, []int{0}, nil)
		got := pr.Surfaces[0].Material
		nd, _ := resolvedNDV(pr, got)
		if math.Abs(nd-1.77250) > 1e-5 {
			t.Errorf("%s model glass: expected nd=1.77250, got nd=%.6f (mat=%+v)", format, nd, got)
		}
	}
}

// resolvedNDV resolves a material's d-line index through the re-imported glass
// catalog: inline model glasses keep their nd; keyed glasses look up the
// registered entry (nd/vd or a tabulated index table).
func resolvedNDV(pr *importer.ParseResult, m types.Material) (nd, vd float64) {
	if m.HasModel() {
		return m.ND, m.VD
	}
	if !m.HasKey() {
		return 0, 0
	}
	for _, g := range pr.GlassEntries {
		if !strings.EqualFold(g.Label, m.Key) && !strings.EqualFold(g.Key, m.Key) {
			continue
		}
		if g.ND > 0 {
			return g.ND, g.VD
		}
		for _, e := range g.RefractiveIndices {
			if math.Abs(e.Wavelength-0.00058756) < 1e-8 {
				return e.Value, 0
			}
		}
	}
	return 0, 0
}

func TestConicAsphereRoundTrip(t *testing.T) {
	in := testInput()
	in.Configs[0].Surfaces[0].Conic = -0.5
	in.Configs[0].Surfaces[0].Type = types.AspherePolynomial
	in.Configs[0].Surfaces[0].Coefficients = []float64{1e-5, -2e-7, 3e-9}
	for _, format := range []string{"zemax", "codev"} {
		pr := roundTrip(t, format, in, []int{0}, nil)
		assertSurface(t, format, pr.Surfaces[0], in.Configs[0].Surfaces[0])
	}
}

func TestZemaxDecenterRoundTrip(t *testing.T) {
	in := testInput()
	in.Configs[0].Surfaces[1].Decenter = []types.DecenterStep{
		{Shift: types.Vec3{X: 1.5, Y: -2.0}, Tilt: types.Vec3{X: 1.0, Y: 0.5}},
	}
	pr := roundTrip(t, "zemax", in, []int{0}, nil)
	// The COORDBRK is removed on import and its decenter lands on surface 2;
	// the surface count must not grow.
	if len(pr.Surfaces) != 4 {
		t.Fatalf("surface count: expected 4 (COORDBRK folded away), got %d", len(pr.Surfaces))
	}
	want := in.Configs[0].Surfaces[1].Decenter[0]
	got := pr.Surfaces[1].Decenter
	if len(got) != 1 {
		t.Fatalf("expected 1 decenter step, got %v", got)
	}
	if math.Abs(got[0].Shift.X-want.Shift.X) > 1e-9 || math.Abs(got[0].Shift.Y-want.Shift.Y) > 1e-9 {
		t.Errorf("decenter shift: expected %+v, got %+v", want.Shift, got[0].Shift)
	}
	if math.Abs(got[0].Tilt.X-want.Tilt.X) > 1e-9 || math.Abs(got[0].Tilt.Y-want.Tilt.Y) > 1e-9 {
		t.Errorf("decenter tilt: expected %+v, got %+v", want.Tilt, got[0].Tilt)
	}
}

func TestCodeVDecenterRoundTrip(t *testing.T) {
	in := testInput()
	in.Configs[0].Surfaces[1].Decenter = []types.DecenterStep{
		{Shift: types.Vec3{Y: 2.5}, Tilt: types.Vec3{X: 1.0}},
	}
	pr := roundTrip(t, "codev", in, []int{0}, nil)
	if len(pr.Surfaces) != 4 {
		t.Fatalf("surface count: expected 4, got %d", len(pr.Surfaces))
	}
	got := pr.Surfaces[1].Decenter
	if len(got) != 1 {
		t.Fatalf("expected 1 decenter step, got %v", got)
	}
	if math.Abs(got[0].Shift.Y-2.5) > 1e-9 || math.Abs(got[0].Tilt.X-1.0) > 1e-9 {
		t.Errorf("DAR decenter: expected shift.Y=2.5 tilt.X=1, got %+v", got[0])
	}
}

func TestZemaxVignettingRoundTrip(t *testing.T) {
	in := testInput()
	in.Configs[0].Fields[1].Vignetting = &types.VignettingDef{
		DecenterX: 0.1, DecenterY: -0.05, CompressionX: 0.2, CompressionY: 0.15,
	}
	pr := roundTrip(t, "zemax", in, []int{0}, nil)
	if len(pr.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(pr.Fields))
	}
	v := pr.Fields[1].Vignetting
	if v == nil {
		t.Fatal("field 1 should carry vignetting")
	}
	want := *in.Configs[0].Fields[1].Vignetting
	if math.Abs(v.DecenterX-want.DecenterX) > 1e-9 || math.Abs(v.DecenterY-want.DecenterY) > 1e-9 ||
		math.Abs(v.CompressionX-want.CompressionX) > 1e-9 || math.Abs(v.CompressionY-want.CompressionY) > 1e-9 {
		t.Errorf("vignetting: expected %+v, got %+v", want, *v)
	}
}

func TestCodeVVignettingRoundTrip(t *testing.T) {
	in := testInput()
	in.Configs[0].Fields[1].Vignetting = &types.VignettingDef{
		DecenterX: 0.1, DecenterY: -0.05, CompressionX: 0.2, CompressionY: 0.15,
	}
	pr := roundTrip(t, "codev", in, []int{0}, nil)
	v := pr.Fields[1].Vignetting
	if v == nil {
		t.Fatal("field 1 should carry vignetting")
	}
	want := *in.Configs[0].Fields[1].Vignetting
	if math.Abs(v.DecenterX-want.DecenterX) > 1e-9 || math.Abs(v.DecenterY-want.DecenterY) > 1e-9 ||
		math.Abs(v.CompressionX-want.CompressionX) > 1e-9 || math.Abs(v.CompressionY-want.CompressionY) > 1e-9 {
		t.Errorf("vignetting: expected %+v, got %+v", want, *v)
	}
}

// multiConfigInput returns a 3-config zoom: the second config moves the second
// lens group, the third config moves it further. Each config carries its own
// fields/wavelengths (deep copies) so per-config vignetting can be set.
func multiConfigInput() *types.Input {
	in := testInput()
	cfg1 := in.Configs[0]
	cloneCfg := func(id, name string, f func(*types.Surface)) types.Config {
		c := cfg1
		c.ID = id
		c.Name = name
		c.Surfaces = cloneSurfaces(cfg1.Surfaces)
		c.Fields = append([]types.FieldItem(nil), cfg1.Fields...)
		c.Wavelengths = append([]types.WavelengthItem(nil), cfg1.Wavelengths...)
		for j := range c.Fields {
			if v := c.Fields[j].Vignetting; v != nil {
				vv := *v
				c.Fields[j].Vignetting = &vv
			}
		}
		if f != nil {
			f(&c.Surfaces[1])
		}
		return c
	}
	cfg2 := cloneCfg("config2", "Config2", func(s *types.Surface) { s.Thickness = 10 })
	cfg2.Surfaces[2].Thickness = 8
	cfg3 := cloneCfg("config3", "Config3", func(s *types.Surface) { s.Thickness = 15 })
	cfg3.Surfaces[2].Thickness = 3
	in.Configs = []types.Config{cfg1, cfg2, cfg3}
	return in
}

func TestZemaxMultiConfigRoundTrip(t *testing.T) {
	in := multiConfigInput()
	pr := roundTrip(t, "zemax", in, []int{0, 1, 2}, nil)
	// The base config plus the two overrides round-trip as configs 1, 2, 3.
	if len(pr.ConfigSurfaces) != 0 && len(pr.ConfigThickness) == 0 {
		t.Fatal("expected config overrides")
	}
	base := importer.ConfigSurfaceSet(pr, 0)
	assertSurfaces(t, "zemax base", base, in.Configs[0].Surfaces)
	for k := 1; k <= 2; k++ {
		got := importer.ConfigSurfaceSet(pr, k+1)
		assertSurfaces(t, "zemax cfg"+itoa(k+1), got, in.Configs[k].Surfaces)
	}
}

func TestCodeVZoomRoundTrip(t *testing.T) {
	in := multiConfigInput()
	pr := roundTrip(t, "codev", in, []int{0, 1, 2}, nil)
	base := importer.ConfigSurfaceSet(pr, 0)
	assertSurfaces(t, "codev base", base, in.Configs[0].Surfaces)
	for pos := 2; pos <= 3; pos++ {
		got := importer.ConfigSurfaceSet(pr, pos)
		assertSurfaces(t, "codev zoom"+itoa(pos), got, in.Configs[pos-1].Surfaces)
	}
}

func TestCodeVZoomVignettingRoundTrip(t *testing.T) {
	in := multiConfigInput()
	in.Configs[1].Fields[1].Vignetting = &types.VignettingDef{CompressionY: 0.1, CompressionX: 0.05}
	in.Configs[2].Fields[1].Vignetting = &types.VignettingDef{CompressionY: 0.2}
	pr := roundTrip(t, "codev", in, []int{0, 1, 2}, nil)
	fv2 := importer.ConfigFields(pr, 2)
	v := fv2[1].Vignetting
	if v == nil || math.Abs(v.CompressionY-0.1) > 1e-9 || math.Abs(v.CompressionX-0.05) > 1e-9 {
		t.Errorf("zoom position 2 field 1: expected compressionY=0.1 X=0.05, got %+v", v)
	}
	fv3 := importer.ConfigFields(pr, 3)
	v3 := fv3[1].Vignetting
	if v3 == nil || math.Abs(v3.CompressionY-0.2) > 1e-9 {
		t.Errorf("zoom position 3 field 1: expected compressionY=0.2, got %+v", v3)
	}
}

// collectWarn records warnings into a slice.
func collectWarn(log *[]string) Warn {
	return func(format string, args ...any) {
		*log = append(*log, format)
	}
}

func TestExportWarnings(t *testing.T) {
	cases := []struct {
		format  string
		mutate  func(*types.Input)
		wantSub string
	}{
		{"zemax", func(in *types.Input) { in.Configs[0].Surfaces[1].Reflect = true }, "mirror"},
		{"codev", func(in *types.Input) { in.Configs[0].Surfaces[1].Type = types.AsphereZernike }, "Zernike"},
		{"oslo", func(in *types.Input) { in.Configs[0].Surfaces[1].Conic = -0.5 }, "conic"},
		{"oslo", func(in *types.Input) {
			in.Configs[0].Surfaces[1].Decenter = []types.DecenterStep{{Shift: types.Vec3{Y: 1}}}
		}, "decenter"},
	}
	for _, c := range cases {
		t.Run(c.format+"/"+c.wantSub, func(t *testing.T) {
			in := testInput()
			c.mutate(in)
			var log []string
			roundTrip(t, c.format, in, []int{0}, collectWarn(&log))
			found := false
			for _, msg := range log {
				if strings.Contains(msg, c.wantSub) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected a warning containing %q, got %v", c.wantSub, log)
			}
		})
	}
}

func TestSingleConfigNoZoomRows(t *testing.T) {
	in := multiConfigInput()
	out, err := WriteCodeV(in, []int{0}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "ZOO") {
		t.Error("single-config export must not emit ZOO rows")
	}
	out, err = WriteZemax(in, []int{0}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "MNUM") || strings.Contains(string(out), "THIC") {
		t.Error("single-config export must not emit multi-config rows")
	}
}
