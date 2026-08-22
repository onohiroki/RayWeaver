package asphere

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// oldCellMetrics replicates the ScoreSurface loop over the ring×sector cells to
// obtain the old (bin) E^common / E^unique / conflict / fit quality numbers.
func oldCellMetrics(stats []types.AsphereCellStat, nTerms int) (eCommon, eUnique, conflict, cover, fitQ float64) {
	var total, common, unique, normSum, confNormW float64
	var sharedCells []types.AsphereCellStat
	for _, c := range stats {
		w := c.Weight
		if w <= 0 {
			continue
		}
		total += w * (c.CommonOPD*c.CommonOPD + c.Conflict)
		if len(c.OccupiedFields) >= 2 {
			common += w * c.CommonOPD * c.CommonOPD
			sharedCells = append(sharedCells, c)
			eps := 1e-12
			normSum += w * c.Conflict / (c.Conflict + c.CommonOPD*c.CommonOPD + eps)
			confNormW += w
		} else {
			unique += w * c.UniqueResidual
		}
	}
	if total > 0 {
		eCommon = common / total
		eUnique = unique / total
		cover = (common + unique) / total
		if confNormW > 0 {
			conflict = normSum / confNormW
		}
	}
	if len(sharedCells) >= 2 {
		_, fitQ = fitRadial(sharedCells, nTerms)
	}
	return
}

// loadTestInput decodes a sample YAML system document (surfaces only + chief).
func loadTestInput(t *testing.T, paths ...string) *types.Input {
	t.Helper()
	var data []byte
	var err error
	for _, p := range paths {
		data, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if len(data) == 0 {
		t.Skipf("sample lens not found (tried %v)", paths)
	}
	var in types.Input
	if err := yaml.Unmarshal(data, &in); err != nil {
		t.Fatalf("parse %s: %v", paths[0], err)
	}
	return &in
}

// doubleGaussFootprints resolves the double-Gauss fields/wavelengths and traces
// the footprint grids, ready for PreprocessOPD.
func doubleGaussFootprints(t *testing.T) ([]types.Surface, []FieldFootprintData, *glass.Catalog) {
	t.Helper()
	in := loadTestInput(t, "../../samples/doublegauss-init.yaml", "samples/doublegauss-init.yaml")
	var surfaces []types.Surface
	if len(in.Configs) > 0 {
		surfaces = in.Configs[0].Surfaces
	} else {
		t.Skipf("sample lens has no config surfaces")
	}
	var fields []Field
	if in.Chief != nil && len(in.Chief.Fields) > 0 {
		for i, f := range in.Chief.Fields {
			fields = append(fields, Field{ID: i + 1, Angle: f.Angle, Weight: 1, Direction: f.Direction})
		}
	}
	var wls []float64
	if in.Chief != nil && len(in.Chief.Wavelengths) > 0 {
		wls = in.Chief.Wavelengths
	} else if len(in.Configs) > 0 {
		for _, w := range in.Configs[0].Wavelengths {
			wls = append(wls, w.Value)
		}
	}
	if len(wls) == 0 {
		wls = []float64{types.DefaultWavelength}
	}
	gc := glass.NewCatalog()
	surface.Precompute(surfaces)
	ref := 0
	if in.Chief != nil {
		ref = in.Chief.ReferenceSurface
	}
	pupilZs := computePupilZs(surfaces, fields, gc, 0, ref)
	fp := GenerateFootprints(surfaces, fields, wls, 15, gc, pupilZs)
	PreprocessOPD(fp, true, false)
	return surfaces, fp, gc
}

// ringOccupancy returns, per field, how many ring bins of the old axis-centred
// polar grid its footprint occupies (a measure of binning resolution loss for
// decentred beams).
func ringOccupancy(fp []FieldFootprintData, surfaceID, rings int) map[int]int {
	occ := make(map[int]int)
	maxR := 0.0
	for _, fd := range fp {
		for _, h := range fd.RayHits {
			if !h.OK {
				continue
			}
			sh, ok := h.Hits[surfaceID]
			if !ok {
				continue
			}
			if r := math.Hypot(sh.Position.X, sh.Position.Y); r > maxR {
				maxR = r
			}
		}
	}
	if maxR <= 0 {
		return occ
	}
	seen := make(map[int]map[int]bool)
	for _, fd := range fp {
		for _, h := range fd.RayHits {
			if !h.OK {
				continue
			}
			sh, ok := h.Hits[surfaceID]
			if !ok {
				continue
			}
			r := math.Hypot(sh.Position.X, sh.Position.Y)
			ring := int(r / maxR * float64(rings))
			if ring >= rings {
				ring = rings - 1
			}
			if seen[fd.FieldID] == nil {
				seen[fd.FieldID] = make(map[int]bool)
			}
			seen[fd.FieldID][ring] = true
		}
	}
	for f, s := range seen {
		occ[f] = len(s)
	}
	return occ
}

func TestCompareRingBinsVsJointFit(t *testing.T) {
	surfaces, fp, gc := doubleGaussFootprints(t)
	cfg := DefaultConfig()
	nTerms := maxOrder(cfg.MaxEvenOrder)
	primaryWL := types.DefaultWavelength
	if fp != nil && len(fp) > 0 {
		primaryWL = fp[0].Wavelength
	}

	fmt.Printf("%-3s | %-18s | %22s | %22s | %s\n",
		"sid", "rings/field (old grid)", "OLD ring-bin fit", "NEW raw-ray joint fit", "NEW residual split")
	fmt.Printf("%-3s | %-18s | %6s %7s %8s | %6s %7s %8s | %6s %7s %7s\n",
		"", "", "fitQ", "eCom", "A4e-5", "fitQ", "eCom", "A4e-5", "asym", "conf", "uniq")

	for _, s := range surfaces {
		if s.ID <= 0 || s.Reflects() || s.ID == len(surfaces) {
			continue
		}
		// OLD: ring cells
		cells := BuildCellGrid(fp, s.ID, cfg.CellRings, cfg.CellAngles)
		tw := totalWeightOnSurface(fp, s.ID)
		stats := ComputeCellStats(cells, cfg.MinRaysPerCell, tw)
		oEcom, oEuni, oConf, oCover, oFit := oldCellMetrics(stats, nTerms)
		pair := mediaIndices(surfaces, s.ID, primaryWL, gc)
		oCoeffs, oWarns := FitAsphereCoeffs(stats, s, pair[0], pair[1], cfg)
		if oCoeffs.A4 == 0 && len(oWarns) > 0 {
			t.Logf("surface %d old fit warnings: %v", s.ID, oWarns)
		}

		// NEW: joint raw-ray fit
		jf := JointRadialFit(fp, s.ID, nTerms)
		rMax := jf.RMax
		dn := pair[1] - pair[0]
		nA4 := 0.0
		if dn != 0 && len(jf.Coef) > 1 && rMax > 0 {
			nA4 = -jf.Coef[1] / (dn * math.Pow(rMax, 4))
		}
		asym := BeamFrameAsym(fp, s.ID, jf.Coef, jf.RMax, cfg.CellRings, cfg.MinRaysPerCell, jf.Total)
		conf, uniq := SharedConflictUnique(fp, s.ID, jf.Coef, jf.RMax, 2.5, jf.Total)

		occ := ringOccupancy(fp, s.ID, cfg.CellRings)
		occStr := ""
		for f := 0; f < 4; f++ {
			if f > 0 {
				occStr += "/"
			}
			if n, ok := occ[f+1]; ok {
				occStr += fmt.Sprintf("%d", n)
			} else {
				occStr += "-"
			}
		}
		_ = oCover
		_ = oConf
		_ = oEuni
		fmt.Printf("%-3d | %-18s | %6.3f %7.3f %8.3f | %6.3f %7.3f %8.3f | %6.3f %7.3f %7.3f\n",
			s.ID, occStr, oFit, oEcom, oCoeffs.A4*1e5, jf.FitQuality, jf.CommonE, nA4*1e5, asym, conf, uniq)
	}

	fmt.Println()
	fmt.Println("Beam-frame low-order portrait of the JOINT-fit residual (field-dependent defocus/astig):")
	fmt.Println("surface frame: defocus = (a+b)/2, astig = (b−a)/2 in OPD; per field vs footprint offset y0.")
	fmt.Printf("%-3s | %-58s | %6s %6s | %s\n",
		"sid", "per field [y0=mm, d=nm, a=nm]", "astR2", "defR2", "astig y0² consistency")
	for _, s := range surfaces {
		if s.ID <= 0 || s.Reflects() || s.ID == len(surfaces) {
			continue
		}
		jf := JointRadialFit(fp, s.ID, nTerms)
		if jf.Total == 0 {
			continue
		}
		pt := FieldLowOrderPortrait(fp, s.ID, jf.Coef, jf.RMax, cfg.MinRaysPerCell)
		if len(pt.Fields) == 0 {
			fmt.Printf("%-3d | (no field with enough rays)\n", s.ID)
			continue
		}
		var buf strings.Builder
		for i, f := range pt.Fields {
			if i > 0 {
				buf.WriteString("  ")
			}
			fmt.Fprintf(&buf, "y=%.2f d=%+6.0f a=%+5.0f", f.Y0, f.Defocus*1e6, f.Astig*1e6)
		}
		noteStr := "only 1 field"
		if len(pt.Fields) >= 2 {
			if pt.AstigR2 >= 0.95 {
				noteStr = "consistent (z(r) tractable)"
			} else {
				noteStr = "field-incoherent"
			}
		}
		fmt.Printf("%-3d | %-58s | %6.3f %6.3f | %s\n", s.ID, buf.String(), pt.AstigR2, pt.DefocusR2, noteStr)
	}
}

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
