package optimize

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestFieldAliveUsesPrescribedPupil pins the one-vig contract: field_alive and
// pupil_fill are measured on dls.PrescribedSurvival (hex, no declared ellipse,
// margin 1.0), not on the merit's ellipsed grid. A field whose declared
// vignetting hides the clipped rim used to report a fully alive field while
// `zoom` reported the delivery.
func TestFieldAliveUsesPrescribedPupil(t *testing.T) {
	gc := vignettingTestGC()
	surfs := vignettingTestSurfaces() // 30 mm singlet, no stop
	clip := &types.VignettingDef{CompressionX: 0.5, CompressionY: 0.5}

	cfg := Config{
		Surfaces:  surfs,
		Variables: []Variable{},
		Fields:    []types.FieldItem{{ID: 0, AngleDeg: 20.0, Weight: 1, Vignetting: clip}},
		MeritTerms: []MeritTerm{
			{Kind: MeritFieldAlive, FieldAngle: 20.0, FieldIndex: 0, FieldWeight: 1, Target: 1.0, Wavelength: 0.00058756, WavWeight: 1, Weight: 1},
			{Kind: MeritPupilFill, FieldAngle: 20.0, FieldIndex: 0, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1},
		},
		GlassCatalog: gc,
		NumRays:      64,
	}
	opt := NewOptimizer(cfg)
	ccfg := opt.primaryConfig()
	opt.UpdatePupils(opt.InitialState())

	term := &ccfg.meritTerms[0]
	angle := opt.termFieldAngle(ccfg, term, surfs, gc)
	pupilZ := opt.gridCentring(ccfg, appliedPupil{}, angle)
	ratio, _ := dls.PrescribedSurvivalPoints(gc, surfs, 0, pupilZ, angle, []float64{0, 1}, 0.00058756, opt.numRays, 0, 1, 0)
	if ratio >= 1 {
		t.Fatalf("prescribed survival = %v, want a clipped off-axis pupil (test setup)", ratio)
	}

	if got, want := opt.evaluateFieldAliveTerm(ccfg, &ccfg.meritTerms[0], surfs, gc, nil, appliedPupil{}), 1.0-ratio; math.Abs(got-want) > 1e-9 {
		t.Errorf("field_alive = %v, want the prescribed deficit %v (ellipsed merit grid used instead)", got, want)
	}
	if got, want := opt.evaluatePupilFillTerm(ccfg, &ccfg.meritTerms[1], surfs, gc, nil, appliedPupil{}), pupilFillValue(ratio); math.Abs(got-want) > 1e-9 {
		t.Errorf("pupil_fill = %v, want %v", got, want)
	}
}

// TestAutoApertureMaxDiameter pins the mechanical bound: max_diameter caps the
// sized diameter instead of growing it to the beam.
func TestAutoApertureMaxDiameter(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	mkSurf := func(max float64) []types.Surface {
		return []types.Surface{
			{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 10.0, Material: types.Material{}, Diameter: 20.0, AutoAperture: true},
			{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 10.0, Material: types.Material{}, Diameter: 10.0, AutoAperture: true, MaxDiameter: max},
			{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 100.0, Material: types.Material{}, Diameter: 8.0},
		}
	}
	mkConfigs := func(surfs []types.Surface) []ConfigInput {
		return []ConfigInput{{
			ID:          "wide",
			Weight:      1.0,
			Surfaces:    surfs,
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}, {ID: 1, AngleDeg: 16, Weight: 1}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1}},
			MeritTerms:  []types.MeritTerm{{Field: 0, Wavelength: 0.00058756, Weight: 1}},
		}}
	}
	opt := NewMultiOptimizer(mkConfigs(mkSurf(12.0)), nil, nil, nil, gc, 10, 1.0, 1e-6, 1e-6, 1.0, 64, 100.0, 0, nil, nil, 0, 0, false, false, nil, nil)
	if got := opt.FinalApertures([]float64{})["wide"][2]; math.Abs(got-12.0) > 1e-9 {
		t.Errorf("capped auto aperture = %.4f, want max_diameter 12", got)
	}

	// Without the cap the same surface would grow past 12: the bound is what
	// holds it, not the beam.
	opt2 := NewMultiOptimizer(mkConfigs(mkSurf(0)), nil, nil, nil, gc, 10, 1.0, 1e-6, 1e-6, 1.0, 64, 100.0, 0, nil, nil, 0, 0, false, false, nil, nil)
	if got := opt2.FinalApertures([]float64{})["wide"][2]; got <= 12.0 {
		t.Errorf("uncapped sizing = %.4f, want > 12 (test setup: the cap must bind)", got)
	}
}

// TestFinalAperturesSharedAcrossConfigs pins the zoom rule: one physical
// element has one clear aperture, so every position gets the largest diameter
// any position needs.
func TestFinalAperturesSharedAcrossConfigs(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	mkSurf := func() []types.Surface {
		return []types.Surface{
			{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 10.0, Material: types.Material{}, Diameter: 20.0, AutoAperture: true},
			{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 10.0, Material: types.Material{}, Diameter: 20.0, AutoAperture: true},
			{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 100.0, Material: types.Material{}, Diameter: 8.0},
		}
	}
	wls := []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1}}
	configs := []ConfigInput{
		{ID: "wide", Weight: 1, Surfaces: mkSurf(), Fields: []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}, {ID: 1, AngleDeg: 20, Weight: 1}}, Wavelengths: wls,
			MeritTerms: []types.MeritTerm{{Field: 0, Wavelength: 0.00058756, Weight: 1}}},
		{ID: "tele", Weight: 1, Surfaces: mkSurf(), Fields: []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}, {ID: 1, AngleDeg: 5, Weight: 1}}, Wavelengths: wls,
			MeritTerms: []types.MeritTerm{{Field: 0, Wavelength: 0.00058756, Weight: 1}}},
	}
	opt := NewMultiOptimizer(configs, nil, nil, nil, gc, 10, 1.0, 1e-6, 1e-6, 1.0, 64, 100.0, 0, nil, nil, 0, 0, false, false, nil, nil)
	aps := opt.FinalApertures([]float64{})
	for id := 1; id <= 2; id++ {
		wide, tele := aps["wide"][id], aps["tele"][id]
		if math.Abs(wide-tele) > 1e-9 {
			t.Errorf("surface %d: wide %.4f vs tele %.4f — one element cannot have two clear apertures", id, wide, tele)
		}
	}
	// The merge must take the maximum, not the first config's value.
	wideSurfs := mkSurf()
	surface.Precompute(wideSurfs)
	wideEnv := dls.TraceFieldGridExtents(gc, wideSurfs, 0, 0, 20.0, []float64{0, 1}, 0.00058756, 1.0, 64, 0, 1, 0)
	if aps["wide"][2] < 2*wideEnv[2] {
		t.Errorf("merged surface 2 = %.4f, want >= the wide beam %.4f", aps["wide"][2], 2*wideEnv[2])
	}
}

// TestWarnThinEdges pins the sizing guard: a clear aperture that grows a
// strongly curved element to a sub-min_glass_path edge is reported.
func TestWarnThinEdges(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.05, Thickness: 2.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0, AutoAperture: true, MinGlassPath: 2.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 30.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 60.0},
	}
	opt := NewOptimizer(Config{
		Surfaces:     surfs,
		MeritTerms:   []MeritTerm{{Kind: MeritSpotRMS, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1}},
		GlassCatalog: gc,
		NumRays:      64,
	})
	var warns []string
	prev := Warnf
	Warnf = func(format string, args ...interface{}) { warns = append(warns, fmt.Sprintf(format, args...)) }
	defer func() { Warnf = prev }()
	opt.WarnThinEdges(opt.InitialState(), nil)
	found := false
	for _, w := range warns {
		if strings.Contains(w, "edge thickness") && strings.Contains(w, "surface 1") {
			found = true
		}
	}
	if !found {
		t.Errorf("no thin-edge warning: %v", warns)
	}
}
