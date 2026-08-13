package main

import (
	"flag"
	"os"

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
func runExport(data []byte) {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	format := fs.String("format", "", "zemax|codev|oslo")
	configFlag := fs.String("config", "", "select config by id (single-config export)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	fs.Parse(os.Args[2:])

	if *format == "" {
		errOut("Error: --format is required (zemax|codev|oslo)")
		os.Exit(1)
	}

	input := parseYAML[types.Input](data)
	gc, _ := loadCatalogs(&input, *glassDir)
	warn := func(format string, args ...any) {
		errOut(format, args...)
	}

	configs := configIndicesForExport(&input, *format, configFlag)

	var out []byte
	var err error
	switch *format {
	case "zemax":
		out, err = exporter.WriteZemax(&input, configs, gc, warn)
	case "codev":
		out, err = exporter.WriteCodeV(&input, configs, gc, warn)
	case "oslo":
		idx := 0
		if len(configs) > 0 {
			idx = configs[0]
		}
		out, err = exporter.WriteOslo(&input, idx, gc, warn)
	default:
		errOut("Error: unknown format %q", *format)
		os.Exit(1)
	}
	if err != nil {
		errOut("Error: export failed: %v", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
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
