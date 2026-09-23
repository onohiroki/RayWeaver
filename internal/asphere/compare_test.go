package asphere

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestJointFitSyntheticRecoversRadial(t *testing.T) {
	// A pure radial r^4 OPD sampled on a decentred footprint (the case the ring
	// bins mishandle) must be represented nearly perfectly by the raw-ray joint
	// fit: high fit quality, common energy ≈ 1, and no sagittal asymmetry.
	sid := 5
	cx, cy := 8.0, 12.0 // footprint centre, decentred from the axis
	const w = 6e-4
	var fp []FieldFootprintData
	for _, fid := range []int{1, 2} {
		var hits []RayHit
		for i := 0; i <= 200; i++ {
			th := float64(i) / 200 * 2 * math.Pi
			rr := 1.2 + 0.3*math.Sin(3*th+float64(fid)) // near-disk footprint
			x := cx + rr*math.Cos(th)
			y := cy + rr*math.Sin(th)
			r := math.Hypot(x, y)
			h := RayHit{
				OPD:  w * r * r * r * r,
				Hits: map[int]SurfaceHit{sid: {Position: types.Vec3{X: x, Y: y}}},
				OK:   true, Weight: 1,
			}
			hits = append(hits, h)
		}
		fp = append(fp, FieldFootprintData{FieldID: fid, Weight: 1, RayHits: hits})
	}
	res := JointRadialFit(fp, sid, 2)
	// The radial basis spans a pure r⁴ OPD, so the fit should capture nearly all
	// the energy. The ridge (λ=0.05, the same as the OLD cell fit) shrinks the
	// two nearly-collinear columns on this narrow band, so ~0.94 is the honest
	// floor; the invariants that matter are the energy capture, the zero
	// sagittal-asymmetry and the zero inter-field conflict.
	if res.CommonE < 0.90 {
		t.Fatalf("common energy = %v, want >= 0.9", res.CommonE)
	}
	asym := BeamFrameAsym(fp, sid, res.Coef, res.RMax, 4, 2, res.Total)
	// A pure radial OPD has no sagittal-antisymmetric part; the residual ~0.2%
	// is sampling imbalance between the +s/−s half-bins (irregular synthetic
	// footprint), a lower bound on what the real-grid diagnostic can resolve.
	if asym > 0.01 {
		t.Fatalf("asym = %v, want ~0 (pure radial OPD)", asym)
	}
	conf, uniq := SharedConflictUnique(fp, sid, res.Coef, res.RMax, 2.5, res.Total)
	if conf > 1e-3 || uniq > 1e-3 {
		t.Fatalf("conflict/unique = %v/%v, want ~0 (identical fields)", conf, uniq)
	}
}
