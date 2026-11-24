package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// updateCLIGoldens rewrites the golden files of the CLI matrix instead of
// comparing against them:
//
//	go test ./cmd/rayweave -run TestCLIMatrix -update-cli-goldens
var updateCLIGoldens = flag.Bool("update-cli-goldens", false, "rewrite CLI matrix golden files")

// cliCase is one end-to-end CLI regression case. steps run as a pipeline (each
// step's stdout feeds the next step's stdin); probes are `query` invocations
// applied to the final document. A case without probes stores the final stdout
// verbatim, which is only done for small outputs — the document-producing
// commands are pinned by a handful of probe values so the goldens stay reviewable.
type cliCase struct {
	name  string
	stdin string // sample file under samples/, "" for no input
	// stdinFile is a package-relative input used when the fixture is synthetic
	// (e.g. a tiny deterministic escape document that samples/ does not carry).
	stdinFile string
	steps     [][]string // argv without the program name
	probes    [][]string // query argv applied to the final stdout
}

// cliCases is the regression matrix. Inputs are the tracked demo documents in
// samples/, so editing one is expected to change its golden.
var cliCases = []cliCase{
	{name: "help-chief", steps: [][]string{{"--help", "chief"}}},

	{name: "paraxial", stdin: "us2645157.yaml", steps: [][]string{{"paraxial"}},
		probes: [][]string{
			{"-r", "paraxial_result.focal_length"},
			{"-r", "paraxial_result.entrance_pupil_diameter"},
		}},

	{name: "paraxial-4wl", stdin: "polychromatic-4wl.yaml",
		steps: [][]string{{"paraxial", "--wavelength-set", "design"}},
		probes: [][]string{
			{"-r", "paraxial_result.color.secondary_spectrum_mm"},
			{"-r", "paraxial_result.color.axial_color[2].shift_mm"},
			{"-r", "paraxial_result.color.white_back_focal"},
		}},

	{name: "chief", stdin: "us2645157.yaml", steps: [][]string{{"chief"}},
		probes: [][]string{
			{"--len", "chief_rays"},
			{"-r", "chief_rays[0].spot_stats.rms_r"},
			{"-r", "chief_rays[1].field_angle"},
		}},

	{name: "chief-4wl", stdin: "polychromatic-4wl.yaml",
		steps: [][]string{{"chief"}},
		probes: [][]string{
			{"-r", "chief_rays[1].lateral_color.max_mm"},
			{"-r", "chief_rays[1].white_spot_stats.rms_r"},
			{"-r", "chief_rays[1].wavelengths[3].label"},
		}},

	{name: "chief-spectral-4wl", stdin: "polychromatic-4wl.yaml",
		steps: [][]string{{"chief", "--wavelength-set", "spectral"}},
		probes: [][]string{
			{"-r", "chief_rays[1].lateral_color.rms_mm"},
			{"-r", "chief_rays[1].white_spot_stats.rms_r"},
			{"--len", "chief_rays[0].wavelengths"},
		}},

	// Per-config ray pipeline: chief --all-configs and trace store each zoom
	// position's own rays inside configs[], while the top-level sections keep
	// describing the first config.
	{name: "chief-all-configs", stdin: "simple-zoom.yaml",
		steps: [][]string{{"chief", "--all-configs", "--marginal-rays"}},
		probes: [][]string{
			{"--len", "chief_rays"},
			{"--len", "configs[0].chief_rays"},
			{"--len", "configs[2].chief_rays"},
			{"--len", "configs[1].rays.rays"},
			{"-r", "configs[2].chief_rays[1].field_angle"},
		}},

	{name: "trace-all-configs", stdin: "simple-zoom.yaml",
		steps: [][]string{{"chief", "--all-configs", "--marginal-rays"}, {"trace"}},
		probes: [][]string{
			{"--len", "results"},
			{"--len", "configs[0].results"},
			{"--len", "configs[2].results"},
			{"-r", "configs[1].results[0].opl_total"},
			{"-r", "configs[2].results[0].opl_total"},
		}},

	{name: "clean-all-configs", stdin: "simple-zoom.yaml",
		steps: [][]string{{"chief", "--all-configs", "--marginal-rays"}, {"trace"}, {"clean"}},
		probes: [][]string{
			{"--len", "results"},
			{"--len", "configs[0].chief_rays"},
			{"--len", "configs[0].results"},
			{"--len", "configs[1].rays.rays"},
		}},

	{name: "trace", stdin: "us2645157.yaml", steps: [][]string{{"chief"}, {"trace"}},
		probes: [][]string{
			{"--len", "results"},
			{"-r", "results[0].opl_total"},
		}},

	// `trace single` is the one-ray debugging path: it reads the document
	// directly (no chief_rays needed) and writes a single trace_single result.
	{name: "trace-single", stdin: "us2645157.yaml", steps: [][]string{{"trace", "single"}},
		probes: [][]string{
			{"--len", "results"},
			{"-r", "results[0].opl_total"},
			{"-r", "results[0].surfaces[3].position"},
			{"-r", "results[0].intensity_s"},
		}},

	{name: "zoom", stdin: "simple-zoom.yaml", steps: [][]string{{"zoom"}},
		probes: [][]string{
			{"-r", "zoom_summary.configs[0].focal_length"},
			{"-r", "zoom_summary.configs[2].fields[1].spot_rms"},
			{"-r", "zoom_summary.configs[0].fields[0].white_spot_rms"},
			{"-r", "zoom_summary.configs[0].color.white_back_focal"},
		}},

	{name: "scale", stdin: "6elements-init.yaml", steps: [][]string{{"scale", "--efl", "50"}},
		probes: [][]string{
			{"-r", "scale.efl"},
			{"--len", "configs"},
		}},

	// `stop` materializes the virtual entrance pupil as a plane stop: the
	// solved stop diameter, the resulting pupil, the removed model and the
	// removed pupil-model variable are all pinned.
	{name: "stop", stdinFile: "testdata/cli/stop-input.yaml",
		steps: [][]string{{"stop"}},
		probes: [][]string{
			{"-r", "chief.stop_surface"},
			{"-r", "stop_insert_result.configs[0].stop_surface"},
			{"-r", "stop_insert_result.configs[0].stop_diameter"},
			{"-r", "stop_insert_result.configs[0].entrance_pupil_z"},
			{"-r", "stop_insert_result.configs[0].entrance_pupil_diameter"},
			{"-r", "configs[0].surfaces[2].role"},
			{"-r", "optimization.variables[0].name"},
			{"--len", "configs[0].surfaces"},
		}},
	{name: "error-stop-no-pupil", stdin: "us2645157.yaml",
		steps: [][]string{{"stop"}}},

	{name: "asphere", stdin: "asphere-demo-init.yaml", steps: [][]string{{"asphere"}},
		probes: [][]string{
			{"-r", "asphere_candidate_result.rankings[0].surface_id"},
			{"-r", "asphere_candidate_result.rankings[0].score"},
			{"-r", "asphere_candidate_result.rankings[0].conflict"},
		}},

	{name: "psf", stdin: "psf-mtf-demo.yaml",
		steps: [][]string{{"psf", "--psf-grid", "16", "--num-rays", "32", "--converge-check", "false"}},
		probes: [][]string{
			{"-r", "psf_results[0].strehl_ratio"},
			{"-r", "psf_results[0].fwhm_x"},
		}},

	{name: "focus-spot", stdin: "psf-mtf-demo.yaml",
		steps: [][]string{{"focus", "spot", "--num-rays", "64"}},
		probes: [][]string{
			{"-r", "focus_comparison.spot.rows[0].file.spot_rms"},
			{"-r", "focus_comparison.spot.rows[0].field_angle"},
		}},

	{name: "list-wavelengths-4wl", stdin: "polychromatic-4wl.yaml",
		steps: [][]string{{"list", "wavelengths"}}},

	{name: "psf-white-4wl", stdin: "polychromatic-4wl.yaml",
		steps: [][]string{{"psf", "--wavelength-set", "design", "--focus-plane", "white",
			"--psf-grid", "12", "--num-rays", "32", "--fields", "0", "--converge-check", "false"}},
		probes: [][]string{
			{"-r", "psf_results[0].spectral_curve"},
			{"-r", "psf_results[0].strehl_ratio"},
		}},

	{name: "focus-white-4wl", stdin: "polychromatic-4wl.yaml",
		steps: [][]string{{"focus", "mtf", "--planes", "file,white", "--wavelength-set", "design",
			"--num-rays", "32", "--psf-grid", "12", "--fields", "0", "--frequencies", "30"}},
		probes: [][]string{
			{"-r", "focus_comparison.mtf.focus_plane_white_shift_mm"},
			{"-r", "focus_comparison.mtf.rows[0].focus_plane_white.strehl"},
		}},

	{name: "optimize-4wl", stdin: "polychromatic-4wl.yaml",
		steps: [][]string{{"optimize"}},
		probes: [][]string{
			{"-r", "opt_results.status"},
			{"-r", "opt_results.back_focus.method"},
			{"-r", "opt_results.wavelength_summary[1].weight"},
			{"--len", "opt_results.wavelength_summary"},
		}},

	{name: "wavefront", stdin: "us2645157.yaml",
		steps: [][]string{{"wavefront", "--num-rays", "64"}},
		probes: [][]string{
			{"-r", "wavefront_result.fields[0].statistics.rms"},
			{"-r", "wavefront_result.fields[0].statistics.strehl"},
		}},

	{name: "wavefront-white-4wl", stdin: "polychromatic-4wl.yaml",
		steps: [][]string{{"wavefront", "--wavelength-set", "design", "--num-rays", "64", "--fields", "0"}},
		probes: [][]string{
			{"-r", "wavefront_result.white.fields[0].rms"},
			{"-r", "wavefront_result.white.fields[0].strehl"},
			{"--len", "wavefront_result.white.wavelengths"},
		}},

	{name: "clean", stdin: "us2645157.yaml", steps: [][]string{{"chief"}, {"clean"}},
		probes: [][]string{
			{"-r", "metadata.tool.generator"},
			{"--len", "configs"},
		}},

	{name: "list-surfaces-json", stdin: "us2645157.yaml",
		steps: [][]string{{"list", "surfaces", "--format", "json"}}},
	{name: "list-glasses-csv", stdin: "glass-color-demo.yaml",
		steps: [][]string{{"list", "glasses", "--format", "csv"}}},
	{name: "list-paraxial", stdin: "us2645157.yaml", steps: [][]string{{"list", "paraxial"}}},
	{name: "list-fields", stdin: "multi-config-zoom.yaml", steps: [][]string{{"list", "fields"}}},
	{name: "list-merit", stdin: "optimize-demo.yaml", steps: [][]string{{"list", "merit"}}},
	{name: "list-optimization", stdin: "optimize-demo.yaml", steps: [][]string{{"list", "optimization"}}},
	{name: "list-global", stdin: "escape-demo.yaml", steps: [][]string{{"list", "global"}}},

	// `list summary` is the compact view the MCP server returns as its tool
	// summary; it folds the bulk sections and passes the rest through.
	{name: "list-summary", stdin: "us2645157.yaml",
		steps: [][]string{{"chief"}, {"trace"}, {"list", "summary", "--format", "json"}},
		probes: [][]string{
			{"--len", "results"},
			{"-r", "results[0].opl_total"},
			{"-r", "chief_rays[0].spot_stats.rms_r"},
		}},

	{name: "list-rays-json", stdin: "us2645157.yaml",
		steps: [][]string{{"chief"}, {"trace"}, {"list", "rays", "--format", "json"}},
		probes: [][]string{
			{"--len", "summary"},
			{"-r", "summary[0].opl_total"},
		}},

	{name: "query-get", stdin: "us2645157.yaml",
		steps: [][]string{{"query", "-r", "surfaces[3].curvature"}}},
	{name: "query-count", stdin: "us2645157.yaml",
		steps: [][]string{{"query", "--count", "surfaces"}}},
	{name: "tmm", stdin: "ar-coating.yaml", steps: [][]string{{"tmm"}}},
	{name: "export-codev", stdin: "us2645157.yaml",
		steps: [][]string{{"export", "--format", "codev"}}},

	// The global search is the one long-running family the matrix used to skip.
	// With --stream false and one worker its document is deterministic, so a
	// probe-pinned smoke run is cheap enough (about two seconds) to keep here.
	{name: "escape-single", stdinFile: "testdata/cli/escape-input.yaml",
		steps: [][]string{{"escape", "--stream", "false"}},
		probes: [][]string{
			{"-r", "global_result.best_merit"},
			{"--len", "global_result.minima"},
			{"-r", "global_result.minima[0].index"},
		}},
	{name: "escape-clean-dls-off", stdinFile: "testdata/cli/escape-input.yaml",
		steps: [][]string{{"escape", "--stream", "false", "--clean-dls", "false"}},
		probes: [][]string{
			{"-r", "global_result.best_merit"},
			{"--len", "global_result.minima"},
		}},

	{name: "error-list-format", stdin: "us2645157.yaml",
		steps: [][]string{{"list", "surfaces", "--format", "nope"}}},
}

// TestCLIMatrixHelper re-executes the test binary as the rayweave CLI. The
// commands call os.Exit (and the error cases must exit non-zero), so they
// cannot run in the test process itself.
func TestCLIMatrixHelper(t *testing.T) {
	if os.Getenv("GO_CLI_MATRIX_HELPER") != "1" {
		return
	}
	var args []string
	_ = json.Unmarshal([]byte(os.Getenv("GO_CLI_MATRIX_ARGS")), &args)
	os.Args = append([]string{"rayweave"}, args...)
	main()
	os.Exit(0)
}

// runCLI runs one rayweave invocation in a subprocess against stdin.
func runCLI(t *testing.T, args []string, stdin string) (stdout, stderr string, exit int) {
	t.Helper()
	raw, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCLIMatrixHelper$")
	cmd.Env = append(os.Environ(), "GO_CLI_MATRIX_HELPER=1", "GO_CLI_MATRIX_ARGS="+string(raw))
	cmd.Stdin = strings.NewReader(stdin)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run rayweave %v: %v", args, err)
		}
		exit = exitErr.ExitCode()
	}
	return out.String(), errBuf.String(), exit
}

// createdAtRE drops the run timestamp, the only field that changes between two
// runs of the same command on the same document.
var createdAtRE = regexp.MustCompile(`(?m)^\s*created_at:.*\n`)

func normalizeCLI(s string) string {
	return createdAtRE.ReplaceAllString(s, "")
}

// runPipeline executes the case's steps, feeding each stdout to the next stdin.
func runPipeline(t *testing.T, c cliCase) (stdout, stderr string, exit int) {
	t.Helper()
	in := ""
	switch {
	case c.stdinFile != "":
		data, err := os.ReadFile(c.stdinFile)
		if err != nil {
			t.Fatalf("read %s: %v", c.stdinFile, err)
		}
		in = string(data)
	case c.stdin != "":
		data, err := os.ReadFile(filepath.Join("..", "..", "samples", c.stdin))
		if err != nil {
			t.Fatalf("read samples/%s: %v", c.stdin, err)
		}
		in = string(data)
	}
	for i, step := range c.steps {
		stdout, stderr, exit = runCLI(t, step, in)
		if exit != 0 {
			return stdout, stderr, exit
		}
		in = stdout
		_ = i
	}
	return stdout, stderr, exit
}

// goldenText renders the comparable form of one case: exit status, stderr and
// either the stdout (small outputs) or the probe values.
func goldenText(t *testing.T, c cliCase, stdout, stderr string, exit int) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "exit: %d\n", exit)
	b.WriteString("stderr:\n")
	b.WriteString(normalizeCLI(stderr))
	if len(c.probes) == 0 {
		b.WriteString("stdout:\n")
		b.WriteString(normalizeCLI(stdout))
		return b.String()
	}
	for _, probe := range c.probes {
		out, errOut, code := runCLI(t, append([]string{"query"}, probe...), stdout)
		label := strings.Join(probe, " ")
		if code != 0 {
			fmt.Fprintf(&b, "probe %s: ERROR exit %d: %s\n", label, code, strings.TrimSpace(errOut))
			continue
		}
		fmt.Fprintf(&b, "probe %s: %s\n", label, strings.TrimSpace(out))
	}
	return b.String()
}

// firstDiffLine reports the first differing line of two golden texts so a
// failure points at the changed value instead of dumping both documents.
func firstDiffLine(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("line %d:\n  want: %q\n  got:  %q", i+1, w, g)
		}
	}
	return "no difference"
}

// TestCLIMatrix pins the CLI output of every command family. It is the safety
// net for refactors that touch shared plumbing (escape/single-vs-multi, query
// path handling, the MCP flag surface): those change code, not output.
func TestCLIMatrix(t *testing.T) {
	const dir = "testdata/cli"
	for _, c := range cliCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			stdout, stderr, exit := runPipeline(t, c)
			got := goldenText(t, c, stdout, stderr, exit)

			path := filepath.Join(dir, c.name+".golden")
			if *updateCLIGoldens {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", dir, err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v (regenerate with -update-cli-goldens)", path, err)
			}
			if string(want) != got {
				t.Fatalf("CLI output changed for %s (%s)\n%s\n\ngolden: %s",
					c.name, strings.Join(c.steps[len(c.steps)-1], " "), firstDiffLine(string(want), got), path)
			}
		})
	}
}
