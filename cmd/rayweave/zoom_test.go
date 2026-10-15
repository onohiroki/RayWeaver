package main

import (
	"math"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// zoomSummaryYAML is a two-position "zoom": the tele config's curvatures are
// twice the wide one's, so its EFL is shorter, and a third config is inactive
// (the summary must skip it).
const zoomSummaryYAML = `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
chief:
  reference_surface: 3
  reference_wavelength: 0.00058756
  num_rays: 16
  grid_type: polar
  stop_surface: 1
configs:
  - id: wide
    active: true
    fields:
      - {id: 0, angle_deg: 0.0, weight: 1.0}
      - {id: 1, angle_deg: 5.0, weight: 1.0}
    wavelengths:
      - {id: 0, value: 0.00058756, weight: 1.0}
    surfaces:
      - {id: 1, type: sphere, radius: 100.0, thickness: 6.0, material: N-BK7, diameter: 20.0}
      - {id: 2, type: sphere, radius: -300.0, thickness: 90.0, material: AIR, diameter: 20.0}
      - {id: 3, type: sphere, radius: 0.0, thickness: 0.0, material: AIR, diameter: 20.0}
  - id: tele
    active: true
    fields:
      - {id: 0, angle_deg: 0.0, weight: 1.0}
      - {id: 1, angle_deg: 5.0, weight: 1.0}
    wavelengths:
      - {id: 0, value: 0.00058756, weight: 1.0}
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 6.0, material: N-BK7, diameter: 20.0}
      - {id: 2, type: sphere, radius: -150.0, thickness: 40.0, material: AIR, diameter: 20.0}
      - {id: 3, type: sphere, radius: 0.0, thickness: 0.0, material: AIR, diameter: 20.0}
  - id: parked
    active: false
    fields:
      - {id: 0, angle_deg: 0.0, weight: 1.0}
    wavelengths:
      - {id: 0, value: 0.00058756, weight: 1.0}
    surfaces:
      - {id: 1, type: sphere, radius: 80.0, thickness: 6.0, material: N-BK7, diameter: 20.0}
      - {id: 2, type: sphere, radius: -240.0, thickness: 60.0, material: AIR, diameter: 20.0}
      - {id: 3, type: sphere, radius: 0.0, thickness: 0.0, material: AIR, diameter: 20.0}
`

// runZoomSummary runs `rayweave zoom` with args on doc and returns the parsed
// zoom_summary plus the raw text.
func runZoomSummary(t *testing.T, args []string, doc string) (*types.ZoomSummary, string) {
	t.Helper()
	out := runCommand(t, append([]string{"rayweave", "zoom"}, args...), func() { runZoom([]byte(doc)) })

	var parsed types.Output
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("zoom output does not parse: %v\n%s", err, out)
	}
	if parsed.ZoomSummary == nil {
		t.Fatalf("no zoom_summary section in output:\n%s", out)
	}
	return parsed.ZoomSummary, string(out)
}

// TestZoomSummaryPerConfig checks the per-position table: one row per active
// config, the first order of that position, and one field row with an image
// height, spot RMS and pupil-grid survival.
func TestZoomSummaryPerConfig(t *testing.T) {
	sum, _ := runZoomSummary(t, nil, zoomSummaryYAML)

	if len(sum.Configs) != 2 {
		t.Fatalf("configs = %d, want 2 (the inactive config must be skipped)", len(sum.Configs))
	}
	wide, tele := sum.Configs[0], sum.Configs[1]
	if wide.ID != "wide" || tele.ID != "tele" {
		t.Fatalf("config order = %q, %q", wide.ID, tele.ID)
	}
	if !wide.Active || !tele.Active {
		t.Errorf("active flags = %v, %v", wide.Active, tele.Active)
	}
	if !(tele.FocalLength > 0 && wide.FocalLength > tele.FocalLength) {
		t.Errorf("focal lengths = wide %.4f, tele %.4f; want 0 < tele < wide",
			wide.FocalLength, tele.FocalLength)
	}
	if wide.EntrancePupilDiameter <= 0 {
		t.Errorf("entrance_pupil_diameter = %v", wide.EntrancePupilDiameter)
	}
	// F/# must be the EFL over the EPD it reports (the stop is a fixed 20 mm).
	for _, c := range sum.Configs {
		if want := math.Abs(c.FocalLength) / c.EntrancePupilDiameter; math.Abs(c.ImageSpaceFNumber-want) > 1e-6 {
			t.Errorf("%s: image_space_f_number %v, want %v (EFL/EPD)", c.ID, c.ImageSpaceFNumber, want)
		}
		// An unfolded system: the image-plane vertex sits at the track length.
		if math.Abs(c.ImagePlaneZ-c.TotalTrack) > 1e-9 {
			t.Errorf("%s: image_plane_z %v != total_track %v", c.ID, c.ImagePlaneZ, c.TotalTrack)
		}
	}
	if sum.NumRays != 16 || math.Abs(sum.ReferenceWavelength-0.00058756) > 1e-12 {
		t.Errorf("summary settings = %d rays at %g", sum.NumRays, sum.ReferenceWavelength)
	}

	for _, c := range sum.Configs {
		if len(c.Fields) != 2 {
			t.Fatalf("%s: %d field rows, want 2", c.ID, len(c.Fields))
		}
		axial, corner := c.Fields[0], c.Fields[1]
		if axial.AngleDeg != 0 || axial.ImageHeight != 0 {
			t.Errorf("%s: axial row = angle %v, height %v", c.ID, axial.AngleDeg, axial.ImageHeight)
		}
		if corner.AngleDeg != 5 {
			t.Errorf("%s: corner angle = %v, want 5", c.ID, corner.AngleDeg)
		}
		if corner.ImageHeight <= 0 {
			t.Errorf("%s: corner image height = %v, want > 0", c.ID, corner.ImageHeight)
		}
		for _, f := range c.Fields {
			if f.GridTotal <= 0 || f.GridSurviving <= 0 || f.GridSurviving > f.GridTotal {
				t.Errorf("%s field %d: %d/%d rays survived", c.ID, f.FieldIndex, f.GridSurviving, f.GridTotal)
			}
			if want := float64(f.GridSurviving) / float64(f.GridTotal); math.Abs(f.Vignetting-want) > 1e-12 {
				t.Errorf("%s field %d: vignetting %v != %d/%d", c.ID, f.FieldIndex, f.Vignetting, f.GridSurviving, f.GridTotal)
			}
			if f.SpotRMS < 0 {
				t.Errorf("%s field %d: spot_rms = %v", c.ID, f.FieldIndex, f.SpotRMS)
			}
		}
		// The axial beam always clears a 20 mm aperture on this system; a
		// smaller one must show up as a losing field.
		if axial.Vignetting != 1 {
			t.Errorf("%s: axial vignetting = %v, want 1", c.ID, axial.Vignetting)
		}
	}
}

// TestZoomSummaryReportsAClippedField checks that a field whose beam leaves a
// fixed aperture reports it: the same system with the wide config's rear
// surface drawn in to 6 mm must show a surviving fraction below one there.
func TestZoomSummaryReportsAClippedField(t *testing.T) {
	const generous = "{id: 2, type: sphere, radius: -300.0, thickness: 90.0, material: AIR, diameter: 20.0}"
	const clipped = "{id: 2, type: sphere, radius: -300.0, thickness: 90.0, material: AIR, diameter: 6.0}"
	doc := strings.Replace(zoomSummaryYAML, generous, clipped, 1)
	if doc == zoomSummaryYAML {
		t.Fatal("fixture changed: the surface line was not replaced")
	}

	sum, _ := runZoomSummary(t, nil, doc)
	wide := sum.Configs[0]
	if wide.ID != "wide" {
		t.Fatalf("first row = %q", wide.ID)
	}
	corner := wide.Fields[1]
	if corner.Vignetting >= 1 {
		t.Errorf("corner vignetting = %v, want < 1 with a 6 mm rear aperture", corner.Vignetting)
	}
	// The tele config is untouched by the edit and must stay clear.
	if sum.Configs[1].Fields[1].Vignetting != 1 {
		t.Errorf("tele corner vignetting = %v, want 1", sum.Configs[1].Fields[1].Vignetting)
	}
}

// TestZoomSummaryConfigSelectorAndWriteBack checks the two CLI conventions the
// command shares with the rest of the pipeline: --config selects one row
// (without being written anywhere), and --wl / --num-rays are written back into
// the chief section they mirror.
func TestZoomSummaryConfigSelectorAndWriteBack(t *testing.T) {
	sum, out := runZoomSummary(t, []string{"--config", "tele"}, zoomSummaryYAML)
	if len(sum.Configs) != 1 || sum.Configs[0].ID != "tele" {
		t.Fatalf("--config tele selected %d rows: %+v", len(sum.Configs), sum.Configs)
	}

	// The selector must not appear as a setting anywhere in the output.
	if strings.Contains(out, "config_flag") || strings.Contains(out, "selected_config") {
		t.Errorf("--config leaked into the output document:\n%s", out)
	}

	_, out = runZoomSummary(t, []string{"--num-rays", "32", "--wl", "0.00048613"}, zoomSummaryYAML)
	var parsed types.Output
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if parsed.Chief.NumRays != 32 {
		t.Errorf("chief.num_rays = %d, want the flag's 32", parsed.Chief.NumRays)
	}
	if math.Abs(parsed.Chief.ReferenceWavelength-0.00048613) > 1e-15 {
		t.Errorf("chief.reference_wavelength = %g, want the F line", parsed.Chief.ReferenceWavelength)
	}
	if parsed.ZoomSummary.NumRays != 32 {
		t.Errorf("zoom_summary.num_rays = %d, want 32", parsed.ZoomSummary.NumRays)
	}
}

// TestZoomSummaryDoesNotInjectDefaults checks the write-back rule: a run that
// set no flag must not add a chief.num_rays (or a wavelength) the document did
// not carry, while the summary still reports the value it used.
func TestZoomSummaryDoesNotInjectDefaults(t *testing.T) {
	doc := strings.Replace(zoomSummaryYAML, "  num_rays: 16\n", "", 1)
	if doc == zoomSummaryYAML {
		t.Fatal("fixture changed: chief.num_rays was not removed")
	}
	sum, out := runZoomSummary(t, nil, doc)

	var parsed types.Output
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if parsed.Chief.NumRays != 0 {
		t.Errorf("chief.num_rays = %d, but no flag and no YAML value was given", parsed.Chief.NumRays)
	}
	if sum.NumRays != 64 {
		t.Errorf("zoom_summary.num_rays = %d, want the built-in 64", sum.NumRays)
	}
	if len(sum.Configs) != 2 {
		t.Errorf("configs = %d, want 2", len(sum.Configs))
	}
}

// TestZoomSummaryDispersion verifies the first order follows the reference
// wavelength (the F line gives a shorter EFL than the C line), so the summary
// can be read as a quick colour check of each position.
func TestZoomSummaryDispersion(t *testing.T) {
	f, _ := runZoomSummary(t, []string{"--wl", "0.00048613"}, zoomSummaryYAML) // F
	c, _ := runZoomSummary(t, []string{"--wl", "0.00065627"}, zoomSummaryYAML) // C

	for i := range f.Configs {
		eflF, eflC := f.Configs[i].FocalLength, c.Configs[i].FocalLength
		if !(eflF < eflC) {
			t.Errorf("%s: EFL(F) %.6f is not below EFL(C) %.6f", f.Configs[i].ID, eflF, eflC)
		}
	}
}
