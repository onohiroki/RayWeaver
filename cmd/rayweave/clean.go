package main

import (
	"encoding/json"
	"flag"
	"os"

	"github.com/hiroki/rayweaver/internal/clispec"
	"github.com/hiroki/rayweaver/internal/types"
)

func runClean(data []byte) {
	fs := flag.NewFlagSet("clean", flag.ExitOnError)
	opts := clispec.Bind(clispec.Clean, fs)
	if err := opts.Parse(os.Args[2:]); err != nil {
		errExitf("Error: %v", err)
	}

	output := parseYAML[types.Output](data)

	type removedEntry struct {
		Field string `json:"field"`
		Count int    `json:"count"`
	}
	var removed []removedEntry

	if n := len(output.ChiefRays); n > 0 {
		removed = append(removed, removedEntry{Field: "chief_rays", Count: n})
		output.ChiefRays = nil
	}
	if n := len(output.Results); n > 0 {
		removed = append(removed, removedEntry{Field: "results", Count: n})
		output.Results = nil
	}
	if output.ParaxialResult != nil {
		removed = append(removed, removedEntry{Field: "paraxial_result", Count: 1})
		output.ParaxialResult = nil
	}
	if output.OptResults != nil {
		removed = append(removed, removedEntry{Field: "opt_results", Count: 1})
		output.OptResults = nil
	}
	if output.GlobalResult != nil {
		removed = append(removed, removedEntry{Field: "global_result", Count: 1})
		output.GlobalResult = nil
	}
	if output.Input.GlobalMinimum != nil {
		removed = append(removed, removedEntry{Field: "global_minimum", Count: 1})
		output.Input.GlobalMinimum = nil
	}
	if output.Vignetting != nil {
		removed = append(removed, removedEntry{Field: "vignetting_result", Count: 1})
		output.Vignetting = nil
	}
	if output.AsphereResult != nil {
		removed = append(removed, removedEntry{Field: "asphere_candidate_result", Count: 1})
		output.AsphereResult = nil
	}
	if n := len(output.PsfResults); n > 0 {
		removed = append(removed, removedEntry{Field: "psf_results", Count: n})
		output.PsfResults = nil
	}
	if output.FocusComparison != nil {
		removed = append(removed, removedEntry{Field: "focus_comparison", Count: 1})
		output.FocusComparison = nil
	}
	if output.WavefrontResults != nil {
		removed = append(removed, removedEntry{Field: "wavefront_result", Count: 1})
		output.WavefrontResults = nil
	}
	// Per-config ray results (chief --all-configs and trace) live inside
	// configs[]; their computed sections are removed like the top-level ones.
	// configs[].rays is a ray definition (like the top-level rays section), so
	// it is preserved.
	var cfgChief, cfgResults int
	for i := range output.Configs {
		cfgChief += len(output.Configs[i].ChiefRays)
		output.Configs[i].ChiefRays = nil
		cfgResults += len(output.Configs[i].Results)
		output.Configs[i].Results = nil
	}
	if cfgChief > 0 {
		removed = append(removed, removedEntry{Field: "configs[].chief_rays", Count: cfgChief})
	}
	if cfgResults > 0 {
		removed = append(removed, removedEntry{Field: "configs[].results", Count: cfgResults})
	}
	// optimization.field_resolution is a run result (the field angles a solve
	// froze at its start geometry), not a setting: a re-run resolves it again.
	if output.Input.Optimization != nil && len(output.Input.Optimization.FieldResolution) > 0 {
		removed = append(removed, removedEntry{Field: "optimization.field_resolution", Count: len(output.Input.Optimization.FieldResolution)})
		output.Input.Optimization.FieldResolution = nil
	}

	withOutputMetadata(&output.Input, "clean", subcmdArgs())

	if opts.Bool("verbose") {
		for _, r := range removed {
			line, _ := json.Marshal(r)
			os.Stderr.Write(line)
			os.Stderr.Write([]byte{'\n'})
		}
	}

	writeYAML(&output)
}
