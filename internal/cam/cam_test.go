package cam

import (
	"fmt"
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// camGC is a two-glass catalog for the fixtures below.
func camGC() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "G1", ND: 1.62, VD: 36})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "G2", ND: 1.52, VD: 64})
	return gc
}

// camSurfaces is a three-element layout whose three airspaces — after surfaces
// 2 and 4, and the back focus after 6 — can act as cam variables. Surface 7 is
// the image plane.
func camSurfaces() []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 6, Material: types.Material{Key: "G1"}, Diameter: 40},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 12, Material: types.Material{}, Diameter: 40},
		{ID: 3, Type: types.Sphere, Curvature: 0.03, Thickness: 5, Material: types.Material{Key: "G2"}, Diameter: 40},
		{ID: 4, Type: types.Sphere, Curvature: -0.02, Thickness: 10, Material: types.Material{}, Diameter: 40},
		{ID: 5, Type: types.Sphere, Curvature: 0.015, Thickness: 6, Material: types.Material{Key: "G1"}, Diameter: 40},
		{ID: 6, Type: types.Sphere, Curvature: -0.03, Thickness: 4, Material: types.Material{}, Diameter: 40},
		{ID: 7, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 40},
	}
}

const camWavelength = types.DefaultWavelength

// baseState returns the fixture's EFL and image-plane Z.
func baseState(t *testing.T, gc *glass.Catalog) (float64, float64) {
	t.Helper()
	s := camSurfaces()
	surface.Precompute(s)
	efl := paraxial.Compute(types.System{Surfaces: s}, camWavelength, gc, 0, nil).FocalLength
	if efl == 0 {
		t.Fatal("fixture is afocal; the test needs a focal system")
	}
	return efl, s[len(s)-1].PhysicalZ
}

// testOptions returns the standard three-airspace solve of the fixture.
func testOptions(t *testing.T, gc *glass.Catalog) (Options, float64) {
	t.Helper()
	_, planeZ := baseState(t, gc)
	return Options{
		Surfaces:       []int{2, 4, 6},
		ImagePlane:     7,
		ImagePlaneZ:    planeZ,
		HoldImagePlane: true,
		Wavelength:     camWavelength,
		Tolerance:      1e-9,
	}, planeZ
}

// TestSolveHitsEveryTargetOnOnePlane is the zoom case: three positions, three
// different target EFLs, one image plane. Every position must reach its target
// with the focus on the plane and the plane at the same Z.
func TestSolveHitsEveryTargetOnOnePlane(t *testing.T) {
	gc := camGC()
	baseEFL, planeZ := baseState(t, gc)
	opts, _ := testOptions(t, gc)

	// The fixture reaches EFL 21.74 .. 26.51 on its held plane (see the reachable
	// range: 3 airspaces against 3 conditions), so the three positions sit
	// comfortably inside that band.
	targets := []float64{baseEFL * 0.94, baseEFL, baseEFL * 1.06}
	var configs []Config
	for i, efl := range targets {
		configs = append(configs, Config{
			ID:        fmt.Sprintf("config%d", i),
			Surfaces:  camSurfaces(),
			TargetEFL: efl,
		})
	}

	res, err := Solve(configs, gc, opts)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Converged {
		t.Fatalf("not converged: %s", describe(res))
	}
	for i, cr := range res.Configs {
		if math.Abs(cr.EFL-targets[i]) > 1e-8 {
			t.Errorf("%s: EFL %.9f, target %.9f", cr.ID, cr.EFL, targets[i])
		}
		if math.Abs(cr.ResidualFocus) > 1e-9 {
			t.Errorf("%s: focus residual %.3g mm, want ~0", cr.ID, cr.ResidualFocus)
		}
		if math.Abs(cr.ResidualImagePlane) > 1e-9 {
			t.Errorf("%s: image-plane residual %.3g mm, want ~0", cr.ID, cr.ResidualImagePlane)
		}
		if math.Abs(cr.ImagePlaneZ-planeZ) > 1e-6 {
			t.Errorf("%s: image plane at %.6f, want the fixed %.6f", cr.ID, cr.ImagePlaneZ, planeZ)
		}
		if math.Abs(cr.FocusZ-planeZ) > 1e-6 {
			t.Errorf("%s: focus at %.6f, want the image plane %.6f", cr.ID, cr.FocusZ, planeZ)
		}
		if cr.Iterations == 0 || cr.Iterations > 20 {
			t.Errorf("%s: %d iterations, want a handful", cr.ID, cr.Iterations)
		}
	}
	// The solved copy carries the new thicknesses, and the input is untouched.
	solved := res.Configs[2].Surfaces
	if want := res.Configs[2].Airspaces[2].To; math.Abs(solved[5].Thickness-want) > 1e-12 {
		t.Errorf("solved surface 6 thickness %.6f, airspace reports %.6f", solved[5].Thickness, want)
	}
	if in := camSurfaces(); in[5].Thickness != 4 {
		t.Errorf("input fixture was mutated: surface 6 thickness %.6f", in[5].Thickness)
	}
}

// TestSolveHoldsThePlaneOnlyWhenAsked checks that a free image plane leaves the
// plane where the geometry puts it while the EFL and focus conditions still
// hold.
func TestSolveHoldsThePlaneOnlyWhenAsked(t *testing.T) {
	gc := camGC()
	baseEFL, planeZ := baseState(t, gc)
	opts, _ := testOptions(t, gc)
	opts.HoldImagePlane = false

	res, err := Solve([]Config{{ID: "config0", Surfaces: camSurfaces(), TargetEFL: baseEFL * 0.8}}, gc, opts)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	cr := res.Configs[0]
	if !cr.Converged {
		t.Fatalf("not converged: %s", describe(res))
	}
	if math.Abs(cr.ResidualImagePlane) != 0 {
		t.Errorf("image-plane residual %.3g, want exactly 0 when the plane is not held", cr.ResidualImagePlane)
	}
	if math.Abs(cr.ResidualFocus) > 1e-9 {
		t.Errorf("focus residual %.3g mm, want ~0", cr.ResidualFocus)
	}
	if math.Abs(cr.EFL-baseEFL*0.8) > 1e-8 {
		t.Errorf("EFL %.9f, target %.9f", cr.EFL, baseEFL*0.8)
	}
	// The plane moved (otherwise the fixture would not exercise the freedom),
	// and it moved to wherever the focus is: the two conditions coincide.
	if math.Abs(cr.ImagePlaneZ-planeZ) < 1e-6 {
		t.Errorf("image plane did not move (%.9f); the test needs a free plane to have done something", cr.ImagePlaneZ)
	}
}

// TestSolveReportsTooFewAirspaces: one cam airspace cannot satisfy the EFL and
// the focus together. The solver must say so with its residuals instead of
// returning a lucky-looking answer.
func TestSolveReportsTooFewAirspaces(t *testing.T) {
	gc := camGC()
	baseEFL, _ := baseState(t, gc)
	opts, _ := testOptions(t, gc)
	opts.Surfaces = []int{6} // only the back focus, free image plane
	opts.HoldImagePlane = false

	target := baseEFL * 1.2
	res, err := Solve([]Config{{ID: "config0", Surfaces: camSurfaces(), TargetEFL: target}}, gc, opts)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	cr := res.Configs[0]
	if res.Converged || cr.Converged {
		t.Errorf("converged with one airspace and two conditions (EFL %.6f, target %.6f)", cr.EFL, target)
	}
	if math.Abs(cr.EFL-target) < 1.0 {
		t.Errorf("EFL %.6f reached %.6f with a single airspace; expected the target to be out of reach", cr.EFL, target)
	}
	// The focus condition is the one the single airspace can satisfy (the back
	// focus is exactly free).
	if math.Abs(cr.ResidualFocus) > 1e-6 {
		t.Errorf("focus residual %.3g mm, want the focus met", cr.ResidualFocus)
	}
}

// TestSolveIsDeterministic pins the determinism contract: two solves of the
// same input must produce bit-identical airspaces.
func TestSolveIsDeterministic(t *testing.T) {
	gc := camGC()
	baseEFL, _ := baseState(t, gc)
	opts, _ := testOptions(t, gc)

	run := func() []float64 {
		res, err := Solve([]Config{{ID: "config0", Surfaces: camSurfaces(), TargetEFL: baseEFL * 1.15}}, gc, opts)
		if err != nil {
			t.Fatalf("Solve: %v", err)
		}
		var out []float64
		for _, a := range res.Configs[0].Airspaces {
			out = append(out, a.To)
		}
		return out
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("solve %d is not deterministic: %v vs %v", i, a, b)
		}
	}
	if len(a) != 3 {
		t.Fatalf("got %d airspaces, want 3", len(a))
	}
}

// TestSolveNeverReturnsNegativeAirspace drives the solve hard and checks the
// thickness floor holds whatever the targets ask for.
func TestSolveNeverReturnsNegativeAirspace(t *testing.T) {
	gc := camGC()
	baseEFL, _ := baseState(t, gc)
	opts, _ := testOptions(t, gc)

	for _, factor := range []float64{0.2, 3.0} {
		res, err := Solve([]Config{{ID: "config0", Surfaces: camSurfaces(), TargetEFL: baseEFL * factor}}, gc, opts)
		if err != nil {
			t.Fatalf("Solve(%.1fx): %v", factor, err)
		}
		for _, a := range res.Configs[0].Airspaces {
			if a.To < 0 {
				t.Errorf("%.1fx: airspace after surface %d solved to %.6f mm", factor, a.Surface, a.To)
			}
		}
		if !res.Configs[0].Converged && res.Configs[0].HitFloor {
			for _, a := range res.Configs[0].Airspaces {
				if a.To < 0 {
					t.Errorf("a floored airspace is negative: %+v", a)
				}
			}
		}
	}
}

// TestStepAlpha covers the projection onto the thickness floor and the trust
// region: a step is shortened (never reversed) so no airspace crosses
// MinAirspace or jumps by more than its own scale.
func TestStepAlpha(t *testing.T) {
	s := &solver{opts: Options{MinAirspace: 0}}
	cases := []struct {
		t, d []float64
		want float64
	}{
		{[]float64{2, 4}, []float64{-1, -1}, 1},
		{[]float64{40}, []float64{-30}, 20.0 / 30.0}, // trust region binds
		{[]float64{0.2, 4}, []float64{-1, -1}, 0.2},  // floor binds
		{[]float64{0, 4}, []float64{-1, -1}, 0},      // at the floor, moving down
		{[]float64{0, 4}, []float64{1, -1}, 1},       // rising is allowed
	}
	for _, tc := range cases {
		if got := s.stepAlpha(tc.t, tc.d); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("stepAlpha(%v, %v) = %v, want %v", tc.t, tc.d, got, tc.want)
		}
	}
}

// TestSolveRejectsBadRequests covers the malformed inputs: they are errors
// (the caller has to fix the document), not non-converged results.
func TestSolveRejectsBadRequests(t *testing.T) {
	gc := camGC()
	opts, _ := testOptions(t, gc)
	cfg := Config{ID: "config0", Surfaces: camSurfaces(), TargetEFL: 80}

	cases := []struct {
		name   string
		config []Config
		mutate func(*Options)
	}{
		{"no configs", nil, nil},
		{"no cam surfaces", []Config{cfg}, func(o *Options) { o.Surfaces = nil }},
		{"unknown cam surface", []Config{cfg}, func(o *Options) { o.Surfaces = []int{2, 99} }},
		{"unknown image plane", []Config{cfg}, func(o *Options) { o.ImagePlane = 99 }},
		{"image plane not last", []Config{cfg}, func(o *Options) { o.ImagePlane = 6 }},
		{"no surfaces", []Config{{ID: "empty", TargetEFL: 80}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := opts
			if tc.mutate != nil {
				tc.mutate(&o)
			}
			if _, err := Solve(tc.config, gc, o); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

// TestSolveLogsIterations checks the verbose hook reports the start state and
// at least one accepted step.
func TestSolveLogsIterations(t *testing.T) {
	gc := camGC()
	baseEFL, _ := baseState(t, gc)
	opts, _ := testOptions(t, gc)
	var lines []string
	opts.Logf = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }

	if _, err := Solve([]Config{{ID: "config0", Surfaces: camSurfaces(), TargetEFL: baseEFL * 1.2}}, gc, opts); err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(lines) < 2 {
		t.Fatalf("expected a start line and at least one iteration, got %d line(s): %v", len(lines), lines)
	}
}

// describe renders the residuals of every position for a failure message.
func describe(res Result) string {
	out := ""
	for _, cr := range res.Configs {
		out += fmt.Sprintf("%s: EFL %.6f (target %.6f), focus %.3g, plane %.3g, %d iters; ",
			cr.ID, cr.EFL, cr.EFLTarget, cr.ResidualFocus, cr.ResidualImagePlane, cr.Iterations)
	}
	return out
}
