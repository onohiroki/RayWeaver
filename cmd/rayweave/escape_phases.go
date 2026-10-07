package main

import (
	"os"
	"strconv"

	"github.com/hiroki/rayweaver/internal/types"
)

// The global cycle has one switchable closing phase: the clean DLS that ends
// every cycle. It is a value-taking boolean CLI flag (the same reason --stream
// takes a value: a Go bool flag cannot consume a following "true"/"false"
// argument, and the documented form is `--clean-dls false`), declared by both
// the escape and the pso flag sets. The raw string travels here with its
// "was it given?" state so an unset flag never overrides the input YAML (see
// cleanDLSEnabled). escape and pso parse their own flag sets; only one is ever
// set per run.
type phaseFlagValues struct {
	cleanDLS    string
	cleanDLSSet bool
}

var (
	optEscapePhases phaseFlagValues
	optPSOPhases    phaseFlagValues
)

// phaseCleanDLSFlag is the flag name of the cycle-phase switch (the YAML key
// it writes back to uses '_').
const phaseCleanDLSFlag = "clean-dls"

// phaseFlag returns the raw --clean-dls value of the running subcommand's flag
// set and whether it was given.
func phaseFlag(command string) (value string, set bool) {
	if command == "pso" {
		return optPSOPhases.cleanDLS, optPSOPhases.cleanDLSSet
	}
	return optEscapePhases.cleanDLS, optEscapePhases.cleanDLSSet
}

// cleanDLSEnabled resolves the clean-DLS switch under the three principles: the
// CLI value when --clean-dls was given, else the YAML value when the document
// sets clean_dls, else the built-in default (the phase is on). writeBack
// reports that the CLI supplied the value, so only then is the effective
// setting echoed into the output document — a document that never mentions
// clean_dls stays silent about it in the output too.
func cleanDLSEnabled(cfg *types.GlobalConfig, command string) (enabled, writeBack bool) {
	raw, set := phaseFlag(command)
	if !set {
		if cfg == nil || cfg.CleanDLS == nil {
			return true, false
		}
		return *cfg.CleanDLS, false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		errOut("Error: --clean-dls %q: expected true or false", raw)
		os.Exit(1)
	}
	return v, true
}

// applyCyclePhaseFlags writes the effective value of --clean-dls into the
// output document. cfg points at the embedded GlobalConfig of whichever
// section this run uses (optimization.escape or optimization.pso), so the
// write lands in the right section without the caller caring which it is.
func applyCyclePhaseFlags(cfg *types.GlobalConfig, command string) {
	if v, writeBack := cleanDLSEnabled(cfg, command); writeBack {
		cfg.CleanDLS = &v
	}
}
