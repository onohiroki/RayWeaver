package optimize

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestDegenerateFallbacksPenaliseFailure guards the P2 fix: the wavefront
// degenerate fallback was 0.001 mm, smaller than a realistic off-axis residual
// (the campaign's starting design reaches ~0.007 mm = 12 waves at 587.6 nm),
// so a failed wavefront fit was cheaper than a real measurement and the
// solver could prefer killing the fit.
func TestDegenerateFallbacksPenaliseFailure(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	opt := NewOptimizer(Config{
		Surfaces:     singletSurfaces(),
		Variables:    []Variable{},
		GlassCatalog: gc,
		NumRays:      16,
	})

	const wl = 0.0005876
	realistic := 12 * wl // ~0.007 mm, the campaign start
	if opt.wavefrontDegenerate < realistic {
		t.Errorf("wavefront degenerate fallback %v is below a realistic residual %v; failure would be rewarded",
			opt.wavefrontDegenerate, realistic)
	}
	if opt.opdDegenerate < realistic {
		t.Errorf("opd degenerate fallback %v is below a realistic residual %v; failure would be rewarded",
			opt.opdDegenerate, realistic)
	}
	if opt.wavefrontDegenerate != opt.opdDegenerate {
		t.Errorf("wavefront fallback %v should match the opd fallback %v",
			opt.wavefrontDegenerate, opt.opdDegenerate)
	}
}
