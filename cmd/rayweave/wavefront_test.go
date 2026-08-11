package main

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// TestWavefrontWriteBack verifies the wavefront CLI/YAML rule: flag-won
// settings are echoed into the wavefront: section, the best-focus shift is
// computed and applied, and a wavefront_result is produced.
func TestWavefrontWriteBack(t *testing.T) {
	args := []string{"rayweave", "wavefront",
		"--num-rays", "48", "--zernike-order", "9", "--best-focus"}
	out := runCommand(t, args, func() { runWavefront([]byte(chiefInput(0))) })
	var res types.Output
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Wavefront == nil {
		t.Fatal("wavefront section missing in output")
	}
	if res.Wavefront.NumRays != 48 || res.Wavefront.ZernikeMaxOrder != 9 {
		t.Errorf("wavefront num_rays/zernike_max_order = %d/%d, want 48/9",
			res.Wavefront.NumRays, res.Wavefront.ZernikeMaxOrder)
	}
	if res.Wavefront.BestFocus == nil || !res.Wavefront.BestFocus.Enabled {
		t.Error("wavefront.best_focus.enabled = false, want true")
	}
	if res.WavefrontResults == nil {
		t.Fatal("wavefront_result missing")
	}
	if len(res.WavefrontResults.Fields) == 0 {
		t.Fatal("wavefront_result.fields empty")
	}
	f0 := res.WavefrontResults.Fields[0]
	if f0.Paraboloid.X2 == 0 && f0.Paraboloid.Constant == 0 {
		t.Error("paraboloid coefficients not filled")
	}
	if res.WavefrontResults.BestFocus == nil {
		t.Fatal("wavefront_result.best_focus missing")
	}
	if !math.IsNaN(res.WavefrontResults.BestFocus.WeightedAverage.ShiftMM) &&
		res.WavefrontResults.BestFocus.WeightedAverage.ShiftMM == 0 {
		t.Error("best_focus weighted shift is zero")
	}
	// The image-plane decenter must carry the shift (or a new decenter added).
	// The singlet's image plane is at 100 mm while its focal length is ~48 mm,
	// so the best-focus shift is substantial (the code must find it, not 0).
	last := res.Configs[0].Surfaces[len(res.Configs[0].Surfaces)-1]
	if len(last.Decenter) == 0 {
		t.Error("image plane has no decenter after --best-focus")
	} else {
		z := last.Decenter[len(last.Decenter)-1].Shift.Z
		if z == 0 {
			t.Error("image-plane shift is zero, want a (nonzero) best-focus shift")
		}
	}

	// YAML-sourced config: wavefront: section honoured without flags.
	yamlCfg := chiefInput(0) + `wavefront:
  num_rays: 48
  zernike_max_order: 9
  best_focus:
    enabled: true
    weight_type: custom
    custom_weights: [1.0]
`
	out = runCommand(t, []string{"rayweave", "wavefront"}, func() { runWavefront([]byte(yamlCfg)) })
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.WavefrontResults == nil || res.WavefrontResults.BestFocus == nil {
		t.Fatal("YAML best_focus not honoured")
	}
	if res.WavefrontResults.BestFocus.WeightType != "custom" {
		t.Errorf("best_focus.weight_type = %q, want custom (from YAML)", res.WavefrontResults.BestFocus.WeightType)
	}
}
