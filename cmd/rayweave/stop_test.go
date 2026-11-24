package main

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// stopTestSystem is an air-spaced pair whose 5 mm air gap hosts the stop; the
// virtual pupil sits just inside that gap.
const stopTestSystem = `glass_catalog:
  entries: []
configs:
  - id: cfg1
    active: true
    fields:
      - {id: 0, angle_deg: 0, weight: 1}
      - {id: 1, angle_deg: 3, weight: 1}
    wavelengths:
      - {id: 0, value: 0.0005876, weight: 1}
    surfaces:
      - {id: 1, type: sphere, radius: 50, thickness: 5, material: {nd: 1.5, vd: 50}, diameter: 30}
      - {id: 2, type: sphere, radius: -50, thickness: 5, material: AIR, diameter: 30}
      - {id: 3, type: sphere, radius: 50, thickness: 5, material: {nd: 1.5, vd: 50}, diameter: 30}
      - {id: 4, type: sphere, thickness: 100, material: AIR, diameter: 30}
chief:
  reference_surface: 4
  fields:
    - {angle: 0, direction: [0, 1]}
    - {angle: 3, direction: [0, 1]}
  pupil_model:
    mode: virtual_entrance_pupil
    axial_position: 6
    diameter: 10
  num_rays: 64
optimization:
  variables:
    - {name: vp_z, target: {type: pupil_model, param: axial_position}, min: 0, max: 20, active: true}
    - {name: s1_c, target: {type: surface, id: 1, param: curvature}, min: 0, max: 0.1, active: true}
`

func runStopWithArgs(t *testing.T, args []string, data []byte) []byte {
	t.Helper()
	return runCommand(t, append([]string{"rayweaver", "stop"}, args...), func() { runStop(data) })
}

// TestStopMaterializesInteriorPupil: the interior conversion inserts a plane
// stop, points chief.stop_surface at it, removes the virtual model and the
// pupil-model variables, and reports the requested pupil.
func TestStopMaterializesInteriorPupil(t *testing.T) {
	out := runStopWithArgs(t, nil, []byte(stopTestSystem))

	var doc types.Output
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if doc.Chief == nil || doc.Chief.StopSurface != 5 {
		t.Fatalf("chief.stop_surface = %+v, want 5", doc.Chief)
	}
	if doc.Chief.PupilModel != nil {
		t.Errorf("chief.pupil_model survived: %+v", doc.Chief.PupilModel)
	}
	surfaces := doc.Configs[0].Surfaces
	if len(surfaces) != 5 || surfaces[2].ID != 5 {
		t.Fatalf("surfaces = %+v, want 5 with the stop at index 2", surfaces)
	}
	stop := surfaces[2]
	if stop.Role != "stop" || stop.AutoAperture || !stop.Material.IsAir() || stop.Curvature != 0 {
		t.Errorf("inserted stop = %+v, want a plane air stop with role stop", stop)
	}
	if math.Abs(surfaces[1].Thickness+stop.Thickness-5) > 1e-9 {
		t.Errorf("gap split = %v, want 5", surfaces[1].Thickness+stop.Thickness)
	}
	if doc.Stop == nil || doc.Stop.SurfaceID != 5 {
		t.Errorf("stop block = %+v, want surface 5", doc.Stop)
	}
	res := doc.StopInsertResult
	if res == nil || len(res.Configs) != 1 {
		t.Fatalf("stop_insert_result = %+v", res)
	}
	cr := res.Configs[0]
	if math.Abs(cr.EPZErrorMM) > 1e-6 || math.Abs(cr.EPDErrorMM) > 1e-6 {
		t.Errorf("pupil errors = (%v, %v), want ~0", cr.EPZErrorMM, cr.EPDErrorMM)
	}
	if res.Verification == nil || res.Verification.MaxRMSDeltaMM > 0.001 {
		t.Errorf("verification = %+v, want an agreeing chief pass", res.Verification)
	}
	if doc.Optimization == nil || len(doc.Optimization.Variables) != 1 || doc.Optimization.Variables[0].Name != "s1_c" {
		t.Errorf("variables = %+v, want only s1_c (the pupil variable removed)", doc.Optimization)
	}
}

// TestStopFrontPupilShiftsFrame: an entrance pupil at or before the first
// surface is the stop plane itself; the document frame shifts by |Z| and the
// absolute-Z inputs move with it.
func TestStopFrontPupilShiftsFrame(t *testing.T) {
	data := `glass_catalog:
  entries: []
configs:
  - id: cfg1
    active: true
    fields:
      - {id: 0, height: 1, weight: 1}
    surfaces:
      - {id: 1, type: sphere, radius: 50, thickness: 5, material: {nd: 1.5, vd: 50}, diameter: 30}
      - {id: 2, type: sphere, thickness: 100, material: AIR, diameter: 30}
chief:
  reference_surface: 2
  fields:
    - {height: 1, direction: [0, 1]}
  pupil_model:
    mode: virtual_entrance_pupil
    axial_position: -20
    diameter: 10
rays:
  rays:
    - id: r
      wavelength: 0.0005876
      initial: {origin: [0, 0, -100], direction: [0, 0, 1]}
      path: [0, 1, 2]
`
	out := runStopWithArgs(t, nil, []byte(data))
	var doc types.Output
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	res := doc.StopInsertResult
	if res == nil || math.Abs(res.FrameShiftMM-20) > 1e-9 {
		t.Fatalf("frame shift = %+v, want 20", res)
	}
	surfaces := doc.Configs[0].Surfaces
	if len(surfaces) != 3 || surfaces[0].Role != "stop" || math.Abs(surfaces[0].Thickness-20) > 1e-9 {
		t.Fatalf("front stop = %+v, want the first surface 20 mm in front", surfaces[0])
	}
	if res.Configs[0].StopZ != 0 || math.Abs(res.Configs[0].StopDiameter-10) > 1e-9 {
		t.Errorf("front report = %+v, want stop Z=0 diameter 10", res.Configs[0])
	}
	// The implicit -1000 mm object plane becomes -980 so the object distance is
	// preserved; the explicit ray origin shifts from -100 to -80.
	if doc.Chief == nil || math.Abs(doc.Chief.Fields[0].ObjectZ-(-980)) > 1e-9 {
		t.Errorf("chief field object_z = %v, want -980", doc.Chief.Fields[0].ObjectZ)
	}
	if math.Abs(doc.Configs[0].Fields[0].ObjectZ-(-980)) > 1e-9 {
		t.Errorf("config field object_z = %v, want -980", doc.Configs[0].Fields[0].ObjectZ)
	}
	if doc.Rays == nil || math.Abs(doc.Rays.Rays[0].Initial.Origin.Z-(-80)) > 1e-9 {
		t.Errorf("ray origin Z = %v, want -80", doc.Rays.Rays[0].Initial.Origin.Z)
	}
}

// TestStopDryRunLeavesDocumentAlone: the preview reports the plan but keeps the
// virtual model and the surface list.
func TestStopDryRunLeavesDocumentAlone(t *testing.T) {
	out := runStopWithArgs(t, []string{"--dry-run"}, []byte(stopTestSystem))
	var doc types.Output
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if doc.Chief == nil || doc.Chief.PupilModel == nil || doc.Chief.StopSurface != 0 {
		t.Errorf("dry run changed the pupil definition: %+v", doc.Chief)
	}
	if len(doc.Configs[0].Surfaces) != 4 {
		t.Errorf("dry run changed the surface count to %d", len(doc.Configs[0].Surfaces))
	}
	if doc.StopInsertResult == nil || !doc.StopInsertResult.DryRun {
		t.Errorf("stop_insert_result = %+v, want dry_run: true", doc.StopInsertResult)
	}
	if doc.StopInsertResult != nil && len(doc.StopInsertResult.Configs) > 0 {
		if got := doc.StopInsertResult.Configs[0].StopSurface; got != 5 {
			t.Errorf("planned stop surface = %d, want 5", got)
		}
	}
}

// TestDropPupilModelVariables checks the removal of pupil-model targets and of
// the links that chain off them, so a later optimize/escape run has no dangling
// source.
func TestDropPupilModelVariables(t *testing.T) {
	opt := &types.OptimizationConfig{
		Variables: []types.OptimizationVariable{
			{Name: "vp_z", Target: types.VariableTarget{Type: "pupil_model", Param: "axial_position"}},
			{Name: "s1", Target: types.VariableTarget{Type: "surface", ID: 1, Param: "curvature"}},
		},
		VariableLinks: []types.VariableLink{
			{Name: "link_a", Target: types.VariableTarget{Type: "surface", ID: 2, Param: "curvature"}, Source: "vp_z"},
			{Name: "link_b", Target: types.VariableTarget{Type: "surface", ID: 3, Param: "curvature"}, Source: "link_a"},
			{Name: "link_c", Target: types.VariableTarget{Type: "surface", ID: 4, Param: "curvature"}, Source: "s1"},
		},
	}
	removed := dropPupilModelVariables(opt)
	want := map[string]bool{"vp_z": true, "link_a": true, "link_b": true}
	if len(removed) != 3 {
		t.Fatalf("removed = %v, want vp_z, link_a, link_b", removed)
	}
	for _, name := range removed {
		if !want[name] {
			t.Errorf("unexpected removed variable %q", name)
		}
	}
	if len(opt.Variables) != 1 || opt.Variables[0].Name != "s1" {
		t.Errorf("variables = %+v, want only s1", opt.Variables)
	}
	if len(opt.VariableLinks) != 1 || opt.VariableLinks[0].Name != "link_c" {
		t.Errorf("links = %+v, want only link_c", opt.VariableLinks)
	}
}
