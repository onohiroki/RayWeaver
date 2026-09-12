package main

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// snapYAML is a minimal Cooke-style triplet whose S3 glass sits near N-BK7 in
// (nd, vd) space, so `optimize snap` has something to snap. Two catalog
// glasses bracket the search space.
const snapYAML = `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
    - {type: model, name: N-SF2, nd: 1.64769, vd: 33.82}
optimization:
  method: dls
  variables:
    - {name: s3_nd, target: {type: surface, id: 3, param: nd}, min: 1.4, max: 1.8, active: true}
    - {name: s3_vd, target: {type: surface, id: 3, param: vd}, min: 20, max: 70, active: true}
configs:
  - id: cfg1
    name: cfg1
    active: true
    fields:
      - {id: 0, angle_deg: 0.0, weight: 1.0}
    wavelengths:
      - {id: 0, value: 0.0005876, weight: 1.0}
    surfaces:
      - {id: 1, type: sphere, radius: 10.2871491742, thickness: 1.524, material: {nd: 1.5168, vd: 64.17}, diameter: 10.0}
      - {id: 2, type: sphere, radius: -239.3967954752, thickness: 2.3368, material: AIR, diameter: 10.0}
      - {id: 3, type: sphere, radius: -12.826987173, thickness: 0.508, material: {nd: 1.52, vd: 63.0}, diameter: 6.0}
      - {id: 4, type: sphere, radius: 10.5917184406, thickness: 1.4986, material: AIR, diameter: 6.0}
      - {id: 5, type: sphere, radius: 0, thickness: 1.016, material: AIR, diameter: 3.78}
      - {id: 6, type: sphere, radius: 61.84562942, thickness: 1.524, material: {nd: 1.64831, vd: 33.84}, diameter: 6.0}
      - {id: 7, type: sphere, radius: -10.0074859032, thickness: 21.36695183553, material: AIR, diameter: 6.0}
      - {id: 8, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 44.0}
    merit:
      type: weighted_sum
      terms:
        - {kind: spot_rms, field: 0, wavelength: 0.0005876, weight: 1.0}
`

func TestOptimizeSnapCLI(t *testing.T) {
	out := runCommand(t, []string{"rayweave", "optimize", "snap"}, func() {
		runOptimizeSnap([]byte(snapYAML), "")
	})

	var output types.Output
	if err := yaml.Unmarshal(out, &output); err != nil {
		t.Fatalf("output yaml.Unmarshal: %v\n%s", err, out)
	}
	if output.OptResults == nil || output.OptResults.Snap == nil {
		t.Fatalf("expected opt_results.snap, got %+v", output.OptResults)
	}
	snap := output.OptResults.Snap
	if len(snap.Pairs) != 1 {
		t.Fatalf("expected 1 snap pair, got %d", len(snap.Pairs))
	}
	p := snap.Pairs[0]
	if p.SurfaceID != 3 {
		t.Errorf("expected surface 3, got %d", p.SurfaceID)
	}
	if p.Name != "N-BK7" {
		t.Errorf("expected snap to N-BK7, got %q", p.Name)
	}
	if !(p.FromVD > 0 && p.ToVD > 0) {
		t.Errorf("expected valid from/to vd, got %v -> %v", p.FromVD, p.ToVD)
	}
	if snap.BeforeMerit == 0 || snap.AfterMerit == 0 {
		t.Errorf("expected non-zero before/after merit, got %v/%v", snap.BeforeMerit, snap.AfterMerit)
	}
	if snap.Cost != snap.AfterMerit-snap.BeforeMerit {
		t.Errorf("cost %v != after-before %v", snap.Cost, snap.AfterMerit-snap.BeforeMerit)
	}

	// The snapped surface must carry the catalog glass's nd/vd.
	for _, s := range output.Configs[0].Surfaces {
		if s.ID == 3 {
			if s.Material.ND != 1.5168 || s.Material.VD != 64.17 {
				t.Errorf("S3 material = %+v, want N-BK7 (1.5168, 64.17)", s.Material)
			}
		}
	}
}
