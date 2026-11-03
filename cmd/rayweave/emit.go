package main

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// emitListOutput writes a `list` target's structured document as YAML or JSON.
// Every target used to repeat the same marshal/error/print pair in its own
// "yaml" and "json" switch arms; keeping it here gives all targets one
// marshal-error path and one newline convention (YAML already ends with a
// newline, JSON gets one appended).
func emitListOutput(format string, v any) {
	switch format {
	case "json":
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			errExitf("Error marshaling list output: %v", err)
		}
		os.Stdout.Write(append(data, '\n'))
	default:
		data, err := yaml.Marshal(v)
		if err != nil {
			errExitf("Error marshaling list output: %v", err)
		}
		os.Stdout.Write(data)
	}
}

// emitEmptyList writes the "no rows" document of a list target: `key: []` for
// YAML and `{"key":[]}` for JSON, so an empty result keeps the same shape as a
// non-empty one.
func emitEmptyList(format, key string) {
	switch format {
	case "json":
		fmt.Printf("{\"%s\":[]}\n", key)
	default:
		fmt.Printf("%s: []\n", key)
	}
}
