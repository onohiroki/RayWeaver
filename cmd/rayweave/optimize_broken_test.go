package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// brokenClearanceYAML is a singlet whose on-axis bundle misses surface 2's
// R = 5 mm sphere over most of the pupil. A spot merit would be blind there;
// the beam_clearance term reads the relaxed envelope and drives the first
// surface's curvature.
const brokenClearanceYAML = `glass_catalog:
  entries:
    - {name: N-BK7, nd: 1.5168, vd: 64.17}

chief:
  stop_surface: 1
  reference_surface: 3
  num_rays: 16

optimization:
  method: dls
  max_iter: 10
  variables:
    - name: s1_curvature
      target: {type: surface, id: 1, param: curvature}
      min: 0.005
      max: 0.05
      active: true

configs:
  - id: 0
    active: true
    fields:
      - {id: 0, angle_deg: 0.0, weight: 1.0}
    wavelengths:
      - {id: 0, value: 0.00058756, weight: 1.0}
    surfaces:
      - {id: 1, type: sphere, radius: 100, thickness: 20, material: {key: N-BK7}, diameter: 40, role: stop}
      - {id: 2, type: sphere, radius: 5, thickness: 40, material: AIR, diameter: 10}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 40}
    merit:
      type: weighted_sum
      terms:
        - {kind: beam_clearance, field: 0, surface_set: [2], target: 0, weight: 1.0}
`

// brokenClearanceScheduleYAML is the same design with a ray_validity schedule:
// the analytic mode (focal_length) runs while the clearance is 0, so the
// clipped design must stay in that mode.
const brokenClearanceScheduleYAML = `glass_catalog:
  entries:
    - {name: N-BK7, nd: 1.5168, vd: 64.17}

chief:
  stop_surface: 1
  reference_surface: 3
  num_rays: 16

optimization:
  method: dls
  max_iter: 10
  variables:
    - name: s1_curvature
      target: {type: surface, id: 1, param: curvature}
      min: 0.005
      max: 0.05
      active: true
  merit_schedule:
    metric: ray_validity
    curve: linear
    anchor_from: 0.1
    anchor_to: 0.9
    modes:
      - {name: analytic, weight_from: 1, weight_to: 0}
      - {name: real, weight_from: 0, weight_to: 1}

configs:
  - id: 0
    active: true
    fields:
      - {id: 0, angle_deg: 0.0, weight: 1.0}
    wavelengths:
      - {id: 0, value: 0.00058756, weight: 1.0}
    surfaces:
      - {id: 1, type: sphere, radius: 100, thickness: 20, material: {key: N-BK7}, diameter: 40, role: stop}
      - {id: 2, type: sphere, radius: 5, thickness: 40, material: AIR, diameter: 10}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 40}
    merit_modes:
      - name: analytic
        terms:
          - {kind: focal_length, target: 100, weight: 10.0}
      - name: real
        terms:
          - {kind: beam_clearance, field: 0, surface_set: [2], target: 0, weight: 1.0}
`

func parseSummaryMerit(t *testing.T, stderr, label string) float64 {
	t.Helper()
	re := regexp.MustCompile(label + `\s+([0-9.eE+-]+)`)
	m := re.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("%s not found in stderr:\n%s", label, stderr)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("parse %s %q: %v", label, m[1], err)
	}
	return v
}

func TestOptimizeBeamClearanceBrokenDesign(t *testing.T) {
	out, errOut, exit := runCLI(t, []string{"optimize"}, brokenClearanceYAML)
	if exit != 0 {
		t.Fatalf("optimize exited %d\nstderr:\n%s", exit, errOut)
	}
	if strings.Contains(out, "NaN") || strings.Contains(out, "Inf") {
		t.Errorf("output carries a non-finite value")
	}
	before := parseSummaryMerit(t, errOut, "Before:")
	after := parseSummaryMerit(t, errOut, "After:")
	if !(after < before) {
		t.Errorf("beam_clearance merit did not improve: before %v, after %v", before, after)
	}
}

func TestOptimizeRayValidityScheduleStaysAnalytic(t *testing.T) {
	out, errOut, exit := runCLI(t, []string{"optimize"}, brokenClearanceScheduleYAML)
	if exit != 0 {
		t.Fatalf("optimize exited %d\nstderr:\n%s", exit, errOut)
	}
	if !strings.Contains(out, "active_mode: analytic") {
		t.Errorf("clipped design did not stay in the analytic mode; output:\n%s", out)
	}
}
