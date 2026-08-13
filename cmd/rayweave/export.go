package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"

	"github.com/hiroki/rayweaver/internal/exporter"
	"github.com/hiroki/rayweaver/internal/types"
)

// runExport renders a pipeline YAML document back out as a native lens file
// for another optical design tool: ZEMAX ZMX, CODE V SEQ or OSLO LEN.
//
// Foreign-format output is exempt from the CLI/YAML precedence rules (like
// `import`): every flag is a pure output setting and nothing is written back
// into the (text) output.
//
// Config selection: ZEMAX and CODE V export every config by default (as ZEMAX
// multi-config / CODE V zoom positions); OSLO exports config 0. --config
// forces a single config in every case.
//
// Output: with -o/--output FILE the foreign format is written to FILE and the
// input YAML is passed through to stdout unchanged (like `plot -o`), so the
// pipeline keeps flowing; without it the foreign format goes to stdout.
func runExport(data []byte) {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	format := fs.String("format", "", "zemax|codev|oslo (defaults to the -o file extension)")
	configFlag := fs.String("config", "", "select config by id (single-config export)")
	ndVD := fs.Bool("nd-vd", false, "CODE V: write every glass as its inline nd:vd model form instead of the catalog name")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	outPath := fs.String("o", "", "output file path (default: stdout); with -o the foreign format is written to FILE and the YAML passes through to stdout")
	fs.StringVar(outPath, "output", "", "alias for -o")
	fs.Parse(os.Args[2:])

	// The format is the explicit --format flag, else the -o file extension
	// (.zmx / .seq / .len), like plot inferring SVG/PNG from the extension.
	formatName := resolveExportFormat(*format, *outPath)
	if formatName == "" {
		errOut("Error: --format is required (zemax|codev|oslo), or give -o FILE with a .zmx/.seq/.len extension")
		os.Exit(1)
	}

	input := parseYAML[types.Input](data)
	gc, _ := loadCatalogs(&input, *glassDir)
	warn := func(format string, args ...any) {
		errOut(format, args...)
	}

	configs := configIndicesForExport(&input, formatName, configFlag)

	var out []byte
	var err error
	switch formatName {
	case "zemax":
		out, err = exporter.WriteZemax(&input, configs, gc, warn)
	case "codev":
		out, err = exporter.WriteCodeV(&input, configs, gc, warn, *ndVD)
	case "oslo":
		idx := 0
		if len(configs) > 0 {
			idx = configs[0]
		}
		out, err = exporter.WriteOslo(&input, idx, gc, warn)
	default:
		errOut("Error: unknown format %q", formatName)
		os.Exit(1)
	}
	if err != nil {
		errOut("Error: export failed: %v", err)
		os.Exit(1)
	}
	exportOutput(*outPath, out, data)
}

// resolveExportFormat returns the effective export format: the explicit
// --format flag, else the format inferred from the -o output file extension
// (.zmx/.seq/.len). It returns "" when neither determines one.
func resolveExportFormat(format, outPath string) string {
	if format != "" {
		return format
	}
	return formatFromExt(outPath)
}

// formatFromExt maps an output file extension to the export format, or "" when
// the extension is not one of .zmx / .seq / .len.
func formatFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".zmx":
		return "zemax"
	case ".seq":
		return "codev"
	case ".len":
		return "oslo"
	}
	return ""
}

// exportOutput writes the exported foreign format. With outPath the format is
// written to the file and the input data (the pipeline YAML) is passed through
// to stdout unchanged (plot -o semantics); without it the foreign format goes
// to stdout.
func exportOutput(outPath string, foreign, data []byte) {
	if outPath == "" {
		os.Stdout.Write(foreign)
		return
	}
	if err := os.WriteFile(outPath, foreign, 0644); err != nil {
		errOut("Error writing %s: %v", outPath, err)
		os.Exit(1)
	}
	os.Stdout.Write(data)
}

// configIndicesForExport resolves the configs to write: with --config a single
// config; otherwise ZEMAX/CODE V take every config, OSLO the first.
func configIndicesForExport(input *types.Input, format string, configFlag *string) []int {
	if configFlag != nil && *configFlag != "" {
		idx, msg := resolveConfig(input.Configs, *configFlag)
		if idx < 0 {
			errOut("Error: %s", msg)
			os.Exit(1)
		}
		return []int{idx}
	}
	if format == "oslo" {
		if len(input.Configs) == 0 {
			errOut("Error: no configs to export")
			os.Exit(1)
		}
		return []int{0}
	}
	var configs []int
	for i := range input.Configs {
		configs = append(configs, i)
	}
	if len(configs) == 0 {
		errOut("Error: no configs to export")
		os.Exit(1)
	}
	return configs
}
