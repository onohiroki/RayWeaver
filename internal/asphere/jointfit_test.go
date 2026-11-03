package asphere

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// deterministicFootprints builds several decentred fields whose rays populate
// many tangential bins with contributions of different magnitude. That is the
// shape that exposed the old map-order dependence: every metric summed a map
// (bins, field ids) whose iteration order Go randomises per range statement.
func deterministicFootprints() ([]FieldFootprintData, int) {
	const sid = 7
	var fp []FieldFootprintData
	for fi := 1; fi <= 6; fi++ {
		az := float64(fi) * 0.13
		var hits []RayHit
		for i := 0; i < 240; i++ {
			th := float64(i) / 240 * 2 * math.Pi
			// An irregular, field-dependent ring so the bins are unevenly fed.
			rr := 1.0 + 0.6*math.Sin(2*th+float64(fi)) + 0.15*float64(i%5)
			x := 5*float64(fi) + rr*math.Cos(th)
			y := 3*float64(fi) + rr*math.Sin(th)
			r := math.Hypot(x, y)
			// Radial part plus a field-scaled sagittal-antisymmetric term so
			// asym and conflict are both non-zero and order-sensitive.
			opd := 1e-4*r*r*r*r + 1e-6*float64(fi)*x*(x*x-3*y*y)
			hits = append(hits, RayHit{
				OPD:    opd,
				Weight: 1 + 0.1*float64(i%3),
				Hits:   map[int]SurfaceHit{sid: {Position: types.Vec3{X: x, Y: y}}},
				OK:     true,
			})
		}
		fp = append(fp, FieldFootprintData{
			FieldID:   fi,
			Weight:    1,
			Direction: []float64{math.Cos(az), math.Sin(az)},
			RayHits:   hits,
		})
	}
	return fp, sid
}

// TestSurfaceMetricsDeterministic pins the determinism contract of the asphere
// analysis: the per-surface metrics are summed in a fixed order, so repeated
// evaluations of the same footprints must agree bit-for-bit. Before the fix
// (bins and field ids iterated as maps) the last bits of asym/conflict and the
// field-consistency R² changed on every run.
func TestSurfaceMetricsDeterministic(t *testing.T) {
	fp, sid := deterministicFootprints()

	const runs = 20
	var (
		firstAsym, firstConf, firstUniq uint64
		firstAstigR2, firstDefocusR2    uint64
		firstFieldOrder                 []int
	)
	for i := 0; i < runs; i++ {
		res := JointRadialFit(fp, sid, 3)
		asym := BeamFrameAsym(fp, sid, res.Coef, res.RMax, 10, 2, res.Total)
		conf, uniq := SharedConflictUnique(fp, sid, res.Coef, res.RMax, 2.5, res.Total)
		portrait := FieldLowOrderPortrait(fp, sid, res.Coef, res.RMax, 2)

		order := make([]int, len(portrait.Fields))
		for j, f := range portrait.Fields {
			order[j] = f.FieldID
		}
		if i == 0 {
			firstAsym = math.Float64bits(asym)
			firstConf = math.Float64bits(conf)
			firstUniq = math.Float64bits(uniq)
			firstAstigR2 = math.Float64bits(portrait.AstigR2)
			firstDefocusR2 = math.Float64bits(portrait.DefocusR2)
			firstFieldOrder = order
			if len(order) < 2 {
				t.Fatalf("portrait has %d fields, want >= 2 (the fixture must exercise the regression)", len(order))
			}
			continue
		}
		if got := math.Float64bits(asym); got != firstAsym {
			t.Fatalf("run %d: BeamFrameAsym = %v, first run = %v", i, asym, math.Float64frombits(firstAsym))
		}
		if got := math.Float64bits(conf); got != firstConf {
			t.Fatalf("run %d: conflict = %v, first run = %v", i, conf, math.Float64frombits(firstConf))
		}
		if got := math.Float64bits(uniq); got != firstUniq {
			t.Fatalf("run %d: unique = %v, first run = %v", i, uniq, math.Float64frombits(firstUniq))
		}
		if got := math.Float64bits(portrait.AstigR2); got != firstAstigR2 {
			t.Fatalf("run %d: AstigR2 = %v, first run = %v", i, portrait.AstigR2, math.Float64frombits(firstAstigR2))
		}
		if got := math.Float64bits(portrait.DefocusR2); got != firstDefocusR2 {
			t.Fatalf("run %d: DefocusR2 = %v, first run = %v", i, portrait.DefocusR2, math.Float64frombits(firstDefocusR2))
		}
		for j := range order {
			if order[j] != firstFieldOrder[j] {
				t.Fatalf("run %d: portrait field order = %v, first run = %v", i, order, firstFieldOrder)
			}
		}
	}
}

// TestSharedConflictSeparatesWavelengths pins the (field, wavelength) sample
// semantics of SharedConflictUnique: two wavelengths of the same field that
// demand different OPD at the same pupil region are inter-sample conflict. The
// old code collapsed footprints by FieldID, merged both wavelengths into one
// field mean, and therefore reported exactly zero conflict for this case.
func TestSharedConflictSeparatesWavelengths(t *testing.T) {
	const sid = 9
	makeSample := func(wl, offset float64) FieldFootprintData {
		var hits []RayHit
		for i := 0; i < 120; i++ {
			th := float64(i) / 120 * 2 * math.Pi
			rr := 1.0 + 0.25*math.Sin(3*th)
			x := 4.0 + rr*math.Cos(th)
			y := -2.0 + rr*math.Sin(th)
			r := math.Hypot(x, y)
			hits = append(hits, RayHit{
				OPD:    1e-4*r*r*r*r + offset,
				Weight: 1,
				Hits:   map[int]SurfaceHit{sid: {Position: types.Vec3{X: x, Y: y}}},
				OK:     true,
			})
		}
		return FieldFootprintData{
			FieldID:    1,
			Wavelength: wl,
			Weight:     1,
			Direction:  []float64{1, 0},
			RayHits:    hits,
		}
	}

	// One field, two wavelengths, opposite OPD offsets at the same region.
	fp := []FieldFootprintData{makeSample(0.0004861, 5e-4), makeSample(0.0006563, -5e-4)}
	res := JointRadialFit(fp, sid, 2)
	conf, uniq := SharedConflictUnique(fp, sid, res.Coef, res.RMax, 2.5, res.Total)
	// The fraction is small (the total OPD energy is dominated by the r⁴ term
	// both wavelengths share); what matters is that it is no longer exactly
	// zero, which is what the FieldID collapse produced.
	if conf <= 1e-5 {
		t.Fatalf("conflict = %v, want > 0: the two wavelengths disagree at the same region", conf)
	}
	if uniq > 1e-6 {
		t.Fatalf("unique = %v, want ~0: every ray lies inside the other sample's region", uniq)
	}

	// Control: identical samples agree, so there is neither conflict nor unique
	// energy (every ray is shared).
	same := []FieldFootprintData{makeSample(0.0004861, 0), makeSample(0.0006563, 0)}
	resSame := JointRadialFit(same, sid, 2)
	confSame, uniqSame := SharedConflictUnique(same, sid, resSame.Coef, resSame.RMax, 2.5, resSame.Total)
	if confSame > 1e-9 || uniqSame > 1e-9 {
		t.Fatalf("identical samples: conflict/unique = %v/%v, want 0/0", confSame, uniqSame)
	}
}
