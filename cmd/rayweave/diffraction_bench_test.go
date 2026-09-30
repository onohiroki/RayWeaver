package main

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// Diffraction-MTF cost probe:
//
//	RW_BENCH_DOC=<doc.yaml> go test ./cmd/rayweave -run TestDiffractionMTFBench -v -timeout 30m
//
// Times psf.ComputeDiffractionMTF — the machinery the diffraction_mtf_sag/tan
// merit kinds run — for a set of entrance-pupil ray counts and image grids on a
// real system, so optimization.diffraction_mtf defaults (num_rays / max_grid)
// can be chosen from measurements rather than estimates. It reports the grid
// the auto-enlargement rule actually picked, since that (not num_rays alone)
// drives the Huygens cost, and the sagittal/tangential values so the cheaper
// settings can be checked against the 1600-ray reference for the same state.
func TestDiffractionMTFBench(t *testing.T) {
	path := os.Getenv("RW_BENCH_DOC")
	if path == "" {
		t.Skip("set RW_BENCH_DOC=<doc.yaml>")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	input := parseYAML[types.Input](data)
	setReferenceWavelength(input.Chief)
	gc, _ := loadCatalogs(&input, "")
	surfaces := configSurfaces(input.Configs, new(string))
	surface.Precompute(surfaces)
	sys := types.System{Surfaces: surfaces, StopSurface: input.Chief.StopSurface}
	ref := psf.DefaultReferenceSurface(surfaces)
	wl := input.Chief.ReferenceWavelength

	angles := []float64{0, 19.55, 23}
	if v := os.Getenv("RW_BENCH_ANGLES"); v != "" {
		angles = nil
		for _, a := range parseFloats(v) {
			angles = append(angles, a)
		}
	}
	freq := 10.0
	if v := os.Getenv("RW_BENCH_FREQ"); v != "" {
		freq = parseFloats(v)[0]
	}
	// The virtual-pupil diameter the optimizer applies for every evaluation.
	epd := 0.0
	if pm := input.Chief.PupilModel; pm != nil && pm.Mode == "virtual_entrance_pupil" && pm.Diameter > 0 {
		epd = pm.Diameter
	}

	type setting struct {
		numRays, gridSize, maxGrid int
		pols                       []string
		label                      string
	}
	// The "gate" row is the standalone measurement the deliverable is judged by
	// (1600 rays, uncapped grid). The rest is the sampling sweep that fixes the
	// merit's own defaults: how the MTF at the hinge converges with the number
	// of effective pupil samples, what the pixel cap costs, and whether the
	// single polarization matches the RCP+LCP average.
	settings := []setting{
		{label: "gate", numRays: 1600, gridSize: 64, maxGrid: -1, pols: []string{"RCP+LCP"}},
		{label: "", numRays: 64, gridSize: 64, maxGrid: psf.DefaultDiffractionMaxGrid, pols: []string{"RCP+LCP"}},
		{label: "", numRays: 128, gridSize: 64, maxGrid: psf.DefaultDiffractionMaxGrid, pols: []string{"RCP+LCP"}},
		{label: "", numRays: 200, gridSize: 64, maxGrid: psf.DefaultDiffractionMaxGrid, pols: []string{"RCP+LCP"}},
		{label: "", numRays: 256, gridSize: 64, maxGrid: psf.DefaultDiffractionMaxGrid, pols: []string{"RCP+LCP"}},
		{label: "", numRays: 300, gridSize: 64, maxGrid: psf.DefaultDiffractionMaxGrid, pols: []string{"RCP+LCP"}},
		{label: "", numRays: 400, gridSize: 64, maxGrid: psf.DefaultDiffractionMaxGrid, pols: []string{"RCP+LCP"}},
		{label: "", numRays: 128, gridSize: 64, maxGrid: psf.DefaultDiffractionMaxGrid, pols: []string{"RCP"}},
		{label: "", numRays: 400, gridSize: 64, maxGrid: psf.DefaultDiffractionMaxGrid, pols: []string{"RCP"}},
		{label: "", numRays: 400, gridSize: 64, maxGrid: 256, pols: []string{"RCP"}},
		{label: "", numRays: 400, gridSize: 64, maxGrid: 512, pols: []string{"RCP"}},
		{label: "", numRays: 400, gridSize: 64, maxGrid: -1, pols: []string{"RCP"}},
	}

	for _, angle := range angles {
		// Use the document's own field definition when the angle matches, so
		// the declared vignetting ellipse clips the bundle the way the gate's
		// run does; otherwise a plain angle field (+Y direction).
		fd := types.FieldDef{Angle: angle, Direction: []float64{0, 1}}
		for _, docFd := range input.Chief.Fields {
			if docFd.Angle == angle {
				fd = docFd
				break
			}
		}
		// One settled pupil Z, reused by every setting so the comparison
		// excludes the dynamic-pupil iteration cost.
		frozenZ := 0.0
		if fg, err := psf.ComputeFieldGrid(sys, gc, fd, ref, 128, wl, types.GridPolar, input.Chief.PupilModel, ""); err == nil && fg != nil && fg.EntrancePupil != nil {
			frozenZ = fg.EntrancePupil.Center.Z
		}
		z := frozenZ
		// The optimizer's vignetting compensation (optimize.vignettedNumRays):
		// the pupil builders clip the nominal grid against the ellipse, so a
		// vignetted field needs proportionally more launch rays to reach the
		// target number of SURVIVING samples. Report both so the sweep shows the
		// configuration the merit actually runs.
		area := 1.0
		if v := fd.Vignetting; v != nil && !v.IsZero() {
			area = (1 - v.CompressionX) * (1 - v.CompressionY)
		}
		launch := func(target int) int {
			if area <= 0 || area >= 1 {
				return target
			}
			return int(math.Ceil(float64(target) / area))
		}
		t.Logf("angle %.2f°: frozen pupil Z = %.4f, vignetting area %.3f", angle, z, area)

		for _, s := range settings {
			// The "gate" row is the standalone measurement: it launches exactly
			// what the gate asks for with no vignetting compensation and no
			// pixel cap, so it is the reference the merit must track.
			numRays := launch(s.numRays)
			if s.label == "gate" {
				numRays = s.numRays
			}
			opts := psf.DiffractionMTFOptions{
				Wavelength:     wl,
				Frequency:      freq,
				NumRays:        numRays,
				GridSize:       s.gridSize,
				MaxGrid:        s.maxGrid,
				Polarizations:  s.pols,
				ApertureMargin: 1.0,
				// The optimizer always passes the applied virtual-pupil
				// diameter (appliedPupil.dia); without it the frozen grid
				// falls back to the surface-derived radius and clips every
				// ray on a virtual-pupil system.
				EPDOverride: epd,
				PupilModel:  input.Chief.PupilModel,
			}
			start := time.Now()
			res, err := psf.ComputeDiffractionMTF(sys, gc, fd, &z, opts)
			el := time.Since(start)
			if err != nil {
				t.Logf("  %-4s rays=%-4d grid<=%-4d pols=%-8s  ERROR: %v", s.label, numRays, s.maxGrid, s.pols, err)
				continue
			}
			t.Logf("  %-4s rays=%-4d grid<=%-4d pols=%-8s  %-8s  grid=%d half=%.3fmm valid=%d  sag=%.5f tan=%.5f",
				s.label, numRays, s.maxGrid, s.pols, el.Round(time.Millisecond), res.GridSize, res.HalfWidth, res.Valid,
				res.Sagittal, res.Tangential)
		}
	}
}

// parseFloats splits a comma-separated float list (RW_BENCH_ANGLES,
// RW_BENCH_FREQ).
func parseFloats(v string) []float64 {
	var out []float64
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			continue
		}
		out = append(out, f)
	}
	return out
}
