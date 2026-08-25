package exporter

import (
	"math"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/importer"
	"github.com/hiroki/rayweaver/internal/types"
)

// testInput returns a small triplet-like system: two cemented lenses, angle
// fields and two wavelengths, ending in a flat image plane.
func testInput() *types.Input {
	return &types.Input{
		Metadata: &types.Metadata{Tool: types.ToolInfo{Name: "RayWeaver", URL: "https://github.com/onohiroki/RayWeaver", SchemaVersion: 1}},
		Chief:    &types.ChiefInput{StopSurface: 2, ReferenceWavelength: 0.00058756},
		Configs: []types.Config{
			{
				ID: "config1", Name: "Config1", Weight: 1, Active: true,
				Fields: []types.FieldItem{
					{ID: 0, AngleDeg: 0, Weight: 1},
					{ID: 1, AngleDeg: 10, Weight: 1},
				},
				Wavelengths: []types.WavelengthItem{
					{ID: 0, Value: 0.00058756, Weight: 1},
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

// roundTrip exports the input in the given format (no glass catalog, no
// inline-nd:vd option) and re-imports it.
func roundTrip(t *testing.T, format string, in *types.Input, configs []int, w Warn) *importer.ParseResult {
	t.Helper()
	return roundTripWith(t, format, in, configs, nil, false, w)
}

// roundTripWith is roundTrip with an explicit glass catalog and the CODE V
// inline-nd:vd option.
func roundTripWith(t *testing.T, format string, in *types.Input, configs []int, gc *glass.Catalog, inlineNDVD bool, w Warn) *importer.ParseResult {
	t.Helper()
	var out []byte
	var err error
	switch format {
	case "zemax":
		out, err = WriteZemax(in, configs, gc, w)
	case "codev":
		out, err = WriteCodeV(in, configs, gc, w, inlineNDVD)
	case "oslo":
		out, err = WriteOslo(in, configs[0], gc, w)
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

func assertSurface(t *testing.T, format, label string, got, want types.Surface) {
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
	if want.Material.HasKey() {
		wantKey := want.Material.Key
		if strings.HasPrefix(format, "codev") {
			// CODE V glass names round-trip through the normalized
			// NAME_MANUFACTURER spelling (no catalog in these tests).
			wantKey = codeVGlassName(nil, want.Material.Key)
		}
		if got.Material.Key != wantKey {
			t.Errorf("%s material key: expected %q, got %q", label, wantKey, got.Material.Key)
		}
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
		assertSurface(t, format, format+fmtSurface(i), got[i], want[i])
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

func TestWavelengthExportUsesReferenceWavelength(t *testing.T) {
	in := testInput()
	in.Chief.ReferenceWavelength = 0.00048613

	zmx, err := WriteZemax(in, []int{0}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := string(zmx)
	if !strings.Contains(text, "WAVL 5.875600e-01 4.861300e-01") || !strings.Contains(text, "PWAV 2") {
		t.Fatalf("ZMX wavelength section does not use reference index: %s", text)
	}

	seq, err := WriteCodeV(in, []int{0}, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	text = string(seq)
	if !strings.Contains(text, "WL 587.56 486.13") || !strings.Contains(text, "REF 2") {
		t.Fatalf("SEQ wavelength section does not use reference index: %s", text)
	}
}

func TestWavelengthExportRejectsMissingReference(t *testing.T) {
	in := testInput()
	in.Chief.ReferenceWavelength = 0.00055
	if _, err := WriteZemax(in, []int{0}, nil, nil); err == nil {
		t.Fatal("ZMX export accepted a reference wavelength absent from the table")
	}
	if _, err := WriteCodeV(in, []int{0}, nil, nil, false); err == nil {
		t.Fatal("CODE V export accepted a reference wavelength absent from the table")
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
		assertSurface(t, format, format, pr.Surfaces[0], in.Configs[0].Surfaces[0])
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
	out, err := WriteCodeV(in, []int{0}, nil, nil, false)
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

// testCatalog builds a glass catalog with a manufacturer for codeVGlassName
// and --nd-vd tests.
func testCatalog() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeCatalog, Name: "N-BK7", Key: "N-BK7", Manufacturer: "SCHOTT", ND: 1.5168, VD: 64.17})
	gc.Add(types.Glass{Type: types.GlassTypeCatalog, Name: "SF5", Key: "SF5", Manufacturer: "SCHOTT", ND: 1.67270, VD: 32.21})
	return gc
}

func TestCodeVGlassName(t *testing.T) {
	gc := testCatalog()
	cases := []struct {
		name string
		gc   *glass.Catalog
		key  string
		want string
	}{
		{name: "normalized no catalog", key: "N-BK7", want: "NBK7"},
		{name: "plain key no catalog", key: "SF5", want: "SF5"},
		{name: "manufacturer from catalog", gc: gc, key: "N-BK7", want: "NBK7_SCHOTT"},
		{name: "manufacturer from catalog sf5", gc: gc, key: "SF5", want: "SF5_SCHOTT"},
		{name: "already codev form preserved", key: "NBK7_SCHOTT", want: "NBK7_SCHOTT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := codeVGlassName(c.gc, c.key); got != c.want {
				t.Errorf("codeVGlassName(%q) = %q, want %q", c.key, got, c.want)
			}
		})
	}
}

func TestCodeVGlassLabelNDVD(t *testing.T) {
	gc := testCatalog()
	var log []string
	warn := collectWarn(&log)

	// Model glass keeps the inline form.
	m := types.Material{ND: 1.77, VD: 49.6}
	if got := codeVGlassLabel(nil, m, false, warn); got != "1.77:49.6" {
		t.Errorf("model label = %q", got)
	}
	// Keyed glass resolves to nd:vd through the catalog.
	if got := codeVGlassLabel(gc, types.Material{Key: "N-BK7"}, true, warn); got != "1.5168:64.17" {
		t.Errorf("nd:vd label = %q, want 1.5168:64.17", got)
	}
	// Unresolvable key warns and falls back to the CODE V name.
	if got := codeVGlassLabel(nil, types.Material{Key: "N-BK7"}, true, warn); got != "NBK7" {
		t.Errorf("fallback label = %q, want NBK7", got)
	}
	found := false
	for _, msg := range log {
		if strings.Contains(msg, "not resolvable") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an unresolvable-glass warning, got %v", log)
	}
}

func TestCodeVInlineNDVDRoundTrip(t *testing.T) {
	in := testInput()
	gc := testCatalog()
	pr := roundTripWith(t, "codev", in, []int{0}, gc, true, nil)
	if len(pr.Surfaces) != 4 {
		t.Fatalf("surface count: expected 4, got %d", len(pr.Surfaces))
	}
	// N-BK7 and SF5 were exported as nd:vd; the re-imported surfaces carry
	// keyed references to the registered model glasses, resolvable to the
	// same nd.
	for idx, key := range map[int]string{0: "N-BK7", 2: "SF5"} {
		nd, _ := resolvedNDV(pr, pr.Surfaces[idx].Material)
		want := 0.0
		if g, ok := gc.Lookup(key); ok {
			if d, _, ok2 := glass.NDVD(g); ok2 {
				want = d
			}
		}
		if math.Abs(nd-want) > 1e-6 {
			t.Errorf("surface %d: expected nd=%.6f, got %.6f", idx, want, nd)
		}
	}
}

func TestZemaxImageHeightFieldsRoundTrip(t *testing.T) {
	// Regression: the fieldClass enum values were written as the ZEMAX FTYP
	// code, so image-height fields (class 1) came out as FTYP 1 (object
	// height) and lost their values. FTYP 0/1/2 are angle/object/image height.
	in := testInput()
	in.Configs[0].Fields = []types.FieldItem{
		{ID: 0, AngleDeg: 0, Weight: 1},
		{ID: 1, ImageHeight: 15.141, Weight: 1},
		{ID: 2, ImageHeight: 21.63, Weight: 1},
	}
	pr := roundTrip(t, "zemax", in, []int{0}, nil)
	if len(pr.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(pr.Fields))
	}
	want := []float64{15.141, 21.63}
	for i, ih := range want {
		if math.Abs(pr.Fields[i+1].ImageHeight-ih) > 1e-9 {
			t.Errorf("field %d image_height: expected %g, got %g", i+1, ih, pr.Fields[i+1].ImageHeight)
		}
	}
}

func TestZemaxFTYPCode(t *testing.T) {
	cases := []struct {
		c    fieldClass
		want int
	}{
		{fieldAngle, 0},
		{fieldObjectHeight, 1},
		{fieldImageHeight, 2},
	}
	for _, c := range cases {
		if got := zemaxFTYP(c.c); got != c.want {
			t.Errorf("zemaxFTYP(%v) = %d, want %d", c.c, got, c.want)
		}
	}
}

func TestExportNoChief(t *testing.T) {
	// Regression: exporting a config without a chief section (nil pointer)
	// used to panic in the stop resolution.
	in := testInput()
	in.Chief = nil
	for _, format := range []string{"zemax", "codev", "oslo"} {
		t.Run(format, func(t *testing.T) {
			switch format {
			case "zemax":
				if _, err := WriteZemax(in, []int{0}, nil, nil); err != nil {
					t.Fatalf("WriteZemax: %v", err)
				}
			case "codev":
				if _, err := WriteCodeV(in, []int{0}, nil, nil, false); err != nil {
					t.Fatalf("WriteCodeV: %v", err)
				}
			case "oslo":
				if _, err := WriteOslo(in, 0, nil, nil); err != nil {
					t.Fatalf("WriteOslo: %v", err)
				}
			}
		})
	}
}

func TestNeutralOnAxisFieldNoMixedWarning(t *testing.T) {
	// Regression: an image-height system whose on-axis field has image height 0
	// reads back as an angle-0 field (omitempty), which used to trip the
	// "mixed field types" warning even though the round trip is lossless.
	in := testInput()
	in.Configs[0].Fields = []types.FieldItem{
		{ID: 0, AngleDeg: 0, Weight: 1},         // image-height 0 (on-axis)
		{ID: 1, ImageHeight: 15.141, Weight: 1}, // image height
		{ID: 2, ImageHeight: 21.63, Weight: 1},
	}
	var log []string
	for _, format := range []string{"zemax", "codev"} {
		t.Run(format, func(t *testing.T) {
			log = nil
			pr := roundTrip(t, format, in, []int{0}, collectWarn(&log))
			for _, msg := range log {
				if strings.Contains(msg, "mixed") || strings.Contains(msg, "does not match") {
					t.Errorf("unexpected warning: %q", msg)
				}
			}
			if len(pr.Fields) != 3 {
				t.Errorf("expected 3 fields, got %d", len(pr.Fields))
			}
		})
	}
}

func TestGenuinelyMixedFieldsStillWarn(t *testing.T) {
	// A non-zero angle alongside image heights is genuinely unrepresentable
	// and must still warn.
	in := testInput()
	in.Configs[0].Fields = []types.FieldItem{
		{ID: 0, AngleDeg: 10, Weight: 1},
		{ID: 1, ImageHeight: 5, Weight: 1},
	}
	var log []string
	roundTrip(t, "codev", in, []int{0}, collectWarn(&log))
	found := false
	for _, msg := range log {
		if strings.Contains(msg, "mixed") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a mixed-field warning, got %v", log)
	}
}
