package optimize

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestWarnFieldThroughput pins the field-killing guard (G4): a spot-only merit
// cannot see a field whose pupil grid has been clipped away, so the optimizer
// must say so, and must stay quiet once the merit itself guards the field.
func TestWarnFieldThroughput(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	// The image plane (surface 3, diameter 3) is far smaller than the 30 deg
	// field's image height, so that field's whole grid misses it while the
	// on-axis field survives.
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 6.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 42.0, Material: types.Material{}, Diameter: 50.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 3.0},
	}
	surface.Precompute(surfaces)

	fields := []types.FieldItem{{ID: 0, AngleDeg: 0}, {ID: 1, AngleDeg: 30}}
	spot := []MeritTerm{
		{Kind: MeritSpotRMS, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1},
		{Kind: MeritSpotRMS, FieldAngle: 30, FieldIndex: 1, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1},
	}
	newOpt := func(terms []MeritTerm) *Optimizer {
		return NewOptimizer(Config{
			Surfaces: surfaces, Fields: fields, MeritTerms: terms,
			GlassCatalog: gc, StopSurface: 1, RefSurface: 3, NumRays: 8,
		})
	}

	prev := Warnf
	defer func() { Warnf = prev }()
	var warns []string
	Warnf = func(format string, args ...interface{}) {
		warns = append(warns, fmt.Sprintf(format, args...))
	}

	opt := newOpt(spot)
	opt.WarnFieldThroughput(opt.getInitialState())
	if len(warns) != 1 {
		t.Fatalf("warnings = %v, want exactly one dead-field warning", warns)
	}
	if !strings.Contains(warns[0], `field 1`) || !strings.Contains(warns[0], "0%") {
		t.Errorf("warning = %q, want the dead off-axis field 1 at 0%%", warns[0])
	}

	// The same dead field with a field_alive term is guarded: no warning.
	guarded := append(append([]MeritTerm{}, spot...),
		MeritTerm{Kind: MeritFieldAlive, FieldAngle: 30, FieldIndex: 1, WavWeight: 1, Weight: 1})
	opt2 := newOpt(guarded)
	warns = nil
	opt2.WarnFieldThroughput(opt2.getInitialState())
	if len(warns) != 0 {
		t.Errorf("guarded config warned: %v", warns)
	}
}

// TestFieldThroughputs pins the measurement the escape feasibility check uses
// (G6): the surviving fraction of the merit's own grids, so "feasible" means
// what the objective saw. A field whose grid cannot be built reports Points 0
// (unreachable), one whose rays are all clipped reports Points > 0 and Ratio 0,
// and the ray-count argument changes the density without changing the launch.
func TestFieldThroughputs(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 6.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 42.0, Material: types.Material{}, Diameter: 50.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 8.0},
	}
	surface.Precompute(surfaces)

	fields := []types.FieldItem{{ID: 0, AngleDeg: 0}, {ID: 1, AngleDeg: 30}}
	spot := []MeritTerm{
		{Kind: MeritSpotRMS, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1},
		{Kind: MeritSpotRMS, FieldAngle: 30, FieldIndex: 1, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1},
	}
	opt := NewOptimizer(Config{
		Surfaces: surfaces, Fields: fields, MeritTerms: spot,
		GlassCatalog: gc, StopSurface: 1, RefSurface: 3, NumRays: 8,
	})

	fts := opt.FieldThroughputs(opt.getInitialState(), 0)
	if len(fts) != 2 {
		t.Fatalf("throughputs = %+v, want one entry per field", fts)
	}
	if fts[0].Config != "config1" || fts[0].Field != 0 || fts[0].Angle != 0 {
		t.Errorf("entry 0 = %+v, want config1 field 0 at 0 deg", fts[0])
	}
	if fts[0].Ratio <= 0 || fts[0].Points == 0 {
		t.Errorf("on-axis field = %+v, want surviving rays", fts[0])
	}
	if fts[1].Ratio != 0 || fts[1].Points == 0 {
		t.Errorf("30 deg field = %+v, want a built grid with no survivors", fts[1])
	}

	// The validation ray count overrides the DLS grid density.
	dense := opt.FieldThroughputs(opt.getInitialState(), 16)
	if dense[0].Points <= fts[0].Points {
		t.Errorf("16-ray grid has %d points, 8-ray grid %d: want denser", dense[0].Points, fts[0].Points)
	}
	if dense[0].Ratio <= 0 {
		t.Errorf("on-axis field at 16 rays = %+v, want surviving rays", dense[0])
	}
}
