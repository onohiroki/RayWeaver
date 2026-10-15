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
