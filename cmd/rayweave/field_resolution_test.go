package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// frontStopFieldsInput declares a second field by image height (10 mm) that the
// lens cannot form: the stop sits on the first surface with a small clear
// aperture, so the off-axis beam clips and the reachable image height plateaus
// around 4.7 mm. The field the document declares is therefore not the field any
// angle delivers, which is exactly what the reporting commands must refuse to
// report silently.
const frontStopFieldsInput = `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
chief:
  fields:
    - {angle: 0.0, direction: [0, 1]}
    - {image_height: 10.0, direction: [0, 1]}
  reference_surface: 2
  stop_surface: 1
  num_rays: 16
configs:
  - id: frontstop
    active: true
    surfaces:
      - {id: 1, type: sphere, radius: 50, thickness: 6, material: N-BK7, diameter: 8, role: stop}
      - {id: 2, type: sphere, radius: -50, thickness: 60, material: AIR, diameter: 40}
`

// TestFieldResolutionCLIHelper re-executes the test binary as the rayweave CLI.
// It is the subprocess entry point for runFieldResolutionCLI and returns at once
// when the environment does not ask for it.
func TestFieldResolutionCLIHelper(t *testing.T) {
	if os.Getenv("GO_FIELD_RESOLUTION_HELPER") != "1" {
		return
	}
	var args []string
	_ = json.Unmarshal([]byte(os.Getenv("GO_FIELD_RESOLUTION_ARGS")), &args)
	os.Args = append([]string{"rayweave"}, args...)
	main()
	os.Exit(0)
}

// runFieldResolutionCLI runs one subcommand on input in a subprocess (the
// reporting commands exit non-zero on an unresolved field, which would end the
// test process if it ran in-process) and returns stdout, stderr and the exit
// error.
func runFieldResolutionCLI(t *testing.T, input string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^TestFieldResolutionCLIHelper$")
	cmd.Env = append(os.Environ(), "GO_FIELD_RESOLUTION_HELPER=1", "GO_FIELD_RESOLUTION_ARGS="+string(raw))
	cmd.Stdin = strings.NewReader(input)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return out.String(), errBuf.String(), err
}

// TestUnreachableImageHeightFieldResolution pins both halves of the policy: a
// command that measures the field (`chief`, `zoom`) exits non-zero and names the
// field, while `list fields` — whose contract is to display the input/computed
// mismatch — warns and still lists it. A reachable field height is unchanged.
func TestUnreachableImageHeightFieldResolution(t *testing.T) {
	for _, args := range [][]string{
		{"chief"},
		{"zoom"},
	} {
		stdout, stderr, err := runFieldResolutionCLI(t, frontStopFieldsInput, args...)
		if err == nil {
			t.Errorf("rayweave %v succeeded on an unreachable field, want a failure\nstdout:\n%s", args, stdout)
			continue
		}
		if stdout != "" {
			t.Errorf("rayweave %v wrote a document on an unreachable field:\n%s", args, stdout)
		}
		for _, want := range []string{"field 1", "image_height 10", "cannot form"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("rayweave %v stderr %q does not mention %q", args, stderr, want)
			}
		}
	}

	// `list fields` keeps listing the document and reports the mismatch.
	stdout, stderr, err := runFieldResolutionCLI(t, frontStopFieldsInput, "list", "fields")
	if err != nil {
		t.Fatalf("list fields failed on an unreachable field, want a warning instead: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"field 1", "image_height 10", "cannot form"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("list fields stderr %q does not mention %q", stderr, want)
		}
	}
	if !strings.Contains(stdout, "10.000000") {
		t.Errorf("list fields did not list the declared image height:\n%s", stdout)
	}

	// The same document with a reachable field height keeps reporting, silently.
	reachable := strings.Replace(frontStopFieldsInput, "image_height: 10.0", "image_height: 2.0", 1)
	stdout, stderr, err = runFieldResolutionCLI(t, reachable, "chief")
	if err != nil {
		t.Fatalf("chief failed on a reachable field height: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "chief_rays") {
		t.Errorf("chief on a reachable field height wrote no chief_rays:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("chief on a reachable field height warned: %q", stderr)
	}
}
