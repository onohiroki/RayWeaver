package main

import (
	"os"
	"strconv"

	"github.com/hiroki/rayweaver/internal/types"
)

// The escape cycle has two independently switchable phases: the escape-function
// bumps of the exploration phase, and the clean DLS that closes the cycle. Each
// is a value-taking boolean CLI flag (the same reason --stream takes a value: a
// Go bool flag cannot consume a following "true"/"false" argument, and the
// documented form is `--clean-dls false`), declared by both the escape and the
// pso flag sets. The raw strings travel here with their "was it given?" state
// so an unset flag never overrides the input YAML (see escapePhaseEnabled).
// escape and pso parse their own flag sets; only one pair is ever set per run.
type phaseFlagValues struct {
	escapeBumps    string
	escapeBumpsSet bool
	cleanDLS       string
	cleanDLSSet    bool
}

var (
	optEscapePhases phaseFlagValues
	optPSOPhases    phaseFlagValues
)

// Flag names of the two cycle-phase switches (the YAML keys use '_').
const (
	phaseEscapeBumpsFlag = "escape-bumps"
	phaseCleanDLSFlag    = "clean-dls"
)

// phaseFlag returns the raw value of the named cycle-phase flag on the running
// subcommand's flag set and whether it was given.
func phaseFlag(command, name string) (value string, set bool) {
	p := optEscapePhases
	if command == "pso" {
		p = optPSOPhases
	}
	switch name {
	case phaseEscapeBumpsFlag:
		return p.escapeBumps, p.escapeBumpsSet
	case phaseCleanDLSFlag:
		return p.cleanDLS, p.cleanDLSSet
	}
	return "", false
}

// escapePhaseEnabled resolves one of the two cycle-phase switches under the
// three principles: the CLI value when the flag was given, else the YAML value
// when the document sets it, else the built-in default (the phase is on).
// writeBack reports that the CLI supplied the value, so only then is the
// effective setting echoed into the output document — a document that never
// mentions escape_bumps / clean_dls stays silent about them in the output too.
func escapePhaseEnabled(cfg *types.EscapeConfig, command, name string) (enabled, writeBack bool) {
	raw, set := phaseFlag(command, name)
	if !set {
		if cfg == nil {
			return true, false
		}
		yaml := cfg.EscapeBumps
		if name == phaseCleanDLSFlag {
			yaml = cfg.CleanDLS
		}
		if yaml == nil {
			return true, false
		}
		return *yaml, false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		errOut("Error: --%s %q: expected true or false", name, raw)
		os.Exit(1)
	}
	return v, true
}

// applyCyclePhaseFlags writes the effective values of --escape-bumps /
// --clean-dls into the escape section of the output document. runEscapeCore
// runs it for both `escape` and `pso`; runPSO aliases optimization.pso's
// embedded escape config there, so one write covers both sections.
func applyCyclePhaseFlags(input *types.Input, command string) {
	if input.Optimization == nil || input.Optimization.Escape == nil {
		return
	}
	esc := input.Optimization.Escape
	if v, writeBack := escapePhaseEnabled(esc, command, phaseEscapeBumpsFlag); writeBack {
		esc.EscapeBumps = &v
	}
	if v, writeBack := escapePhaseEnabled(esc, command, phaseCleanDLSFlag); writeBack {
		esc.CleanDLS = &v
	}
}
