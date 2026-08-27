package main

import (
	"encoding/json"
	"flag"
	"os"

	"github.com/hiroki/rayweaver/internal/types"
)

func runClean(data []byte) {
	fs := flag.NewFlagSet("clean", flag.ExitOnError)
	verbose := fs.Bool("verbose", false, "print removed fields as JSONL to stderr")
	fs.Parse(os.Args[2:])

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
	if output.EscapeResult != nil {
		removed = append(removed, removedEntry{Field: "escape_result", Count: 1})
		output.EscapeResult = nil
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
	if output.WavefrontResults != nil {
		removed = append(removed, removedEntry{Field: "wavefront_result", Count: 1})
		output.WavefrontResults = nil
	}

	withOutputMetadata(&output.Input, "clean", subcmdArgs())

	if *verbose {
		for _, r := range removed {
			line, _ := json.Marshal(r)
			os.Stderr.Write(line)
			os.Stderr.Write([]byte{'\n'})
		}
	}

	writeYAML(&output)
}
