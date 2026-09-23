package optimize

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestFieldAliveThresholdConsistent guards the P7 fix: the untraceable-grid
// path used a laxer 0.1 default while the traced path used 0.3, so a field
// whose grid could not be traced was penalised less than a fully dead traced
// field. Both paths now share fieldAliveThreshold.
func TestFieldAliveThresholdConsistent(t *testing.T) {
	if got := fieldAliveThreshold(0); got != 0.3 {
		t.Errorf("fieldAliveThreshold(0) = %v, want 0.3", got)
	}
	if got := fieldAliveThreshold(-1); got != 0.3 {
		t.Errorf("fieldAliveThreshold(-1) = %v, want 0.3", got)
	}
	if got := fieldAliveThreshold(0.25); got != 0.25 {
		t.Errorf("fieldAliveThreshold(0.25) = %v, want 0.25", got)
	}

	// The traced path with the default threshold: a fully alive on-axis field
	// returns 0 (ratio 1 > 0.3), and the untraceable-grid path returns exactly
	// the default 0.3.
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := singletSurfaces()
	surface.Precompute(surfaces)
	cfg := Config{
		Surfaces:     surfaces,
		Variables:    []Variable{},
		MeritTerms:   []MeritTerm{{Kind: MeritFieldAlive, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1000}},
		GlassCatalog: gc,
		NumRays:      64,
	}
	opt := NewOptimizer(cfg)
	ccfg := opt.primaryConfig()
	if val := opt.evaluateFieldAliveTerm(ccfg, &ccfg.meritTerms[0], ccfg.surfaces, gc, nil, appliedPupil{}); val != 0 {
		t.Errorf("field_alive default on-axis = %v, want 0 (all rays alive)", val)
	}
}
