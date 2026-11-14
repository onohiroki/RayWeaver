package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/hiroki/rayweaver/internal/clispec"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

func runTrace(data []byte, singleMode bool) {
	if singleMode {
		runTraceSingle(data)
		return
	}
	fs := flag.NewFlagSet("trace", flag.ExitOnError)
	opts := clispec.Bind(clispec.Trace, fs)
	if err := opts.Parse(os.Args[2:]); err != nil {
		errExitf("Error: %v", err)
	}

	input := parseYAML[types.Input](data)

	var configList []string
	if opts.Set("configs") {
		configList = splitConfigList(opts.String("configs"))
		if len(configList) == 0 {
			errExitf("Error: --configs needs at least one config id")
		}
	}
	// Trace what the document carries: a document with per-config rays
	// (chief --all-configs) is traced config by config without any flag; the
	// selectors only narrow that scope.
	perConfig := hasPerConfigRays(input.Configs)
	autoMulti := perConfig && opts.String("config") == "" && len(configList) == 0
	targets, multi, warnings, err := configTargets(
		input.Configs, opts.String("config"), autoMulti, configList, 0, "--configs")
	if err != nil {
		errExitf("Error: %v", err)
	}
	for _, w := range warnings {
		errOut("Warning: %s", w)
	}
	if multi && !perConfig {
		errExitf("Error: no per-config rays to trace (run chief --all-configs first)")
	}

	gc, cc := openGlass(&input, opts.String("glass-dir"))
	engine := ray.NewEngine(gc, cc)

	// Preserve chief_rays from input data if present
	var chiefRays []types.ChiefRayResult
	var temp struct {
		ChiefRays []types.ChiefRayResult `yaml:"chief_rays"`
	}
	if err := yaml.Unmarshal(data, &temp); err == nil {
		chiefRays = temp.ChiefRays
	}

	// The ray list to trace: the `rays` section (marginal / user rays) plus
	// the per-field chief rays from `chief_rays` (see gatherTraceRays). Chief
	// rays are the authoritative copy, so `trace` must accept a chief output
	// whose rays section only carries the extras.
	pol := types.NewCircularJones(true)
	if input.Rays != nil {
		pol = input.Rays.Polarization
	}
	// Lenient tracing: --lenient true|false (flag) wins over rays.lenient (YAML).
	lenient, lenientSet, err := opts.BoolString("lenient")
	if err != nil {
		errExitf("Error: %s", err)
	}
	if !lenientSet && input.Rays != nil && input.Rays.Lenient {
		lenient = input.Rays.Lenient
	}
	// Write back the flags that were given (the table owns the effective
	// lenient value and its rays section).
	opts.WriteBack(&input)

	// Trace every target. A document with per-config rays uses each config's
	// own (configs[].rays / configs[].chief_rays, written by chief
	// --all-configs); borrowing another config's rays would trace them through
	// the wrong surfaces, so a config without its own rays is skipped.
	var firstResults []types.RayResult
	traced := 0
	for _, idx := range targets {
		cfg := &input.Configs[idx]
		surfaces := cfg.Surfaces
		surface.Precompute(surfaces)

		rayInput := input.Rays
		configChiefRays := chiefRays
		configPol := pol
		label := ""
		if perConfig {
			label = configLabel(input.Configs, idx)
			rayInput = cfg.Rays
			configChiefRays = cfg.ChiefRays
			if cfg.Rays != nil {
				configPol = cfg.Rays.Polarization
			}
		}

		rayList := gatherTraceRays(rayInput, configChiefRays, configPol)
		if len(rayList) == 0 {
			if multi {
				errOut("Warning: config %q has no rays to trace (run chief --all-configs first) — skipped", label)
				continue
			}
			if perConfig {
				errExitf("Error: config %q has no rays to trace (run chief --all-configs)", label)
			}
			errExitf("Error: 'rays' or 'chief_rays' section is required")
		}

		results := traceRayList(rayList, surfaces, engine, opts.Bool("details"), opts.Bool("verbose"), lenient, configPol, label)
		traced++
		if firstResults == nil {
			firstResults = results
		}
		if perConfig {
			cfg.Results = results
		}
	}

	if multi && traced == 0 {
		errExitf("Error: no config has rays to trace (run chief --all-configs first)")
	}

	if multi && traced == 0 {
		errExitf("Error: no config has rays to trace (run chief --all-configs first)")
	}

	output := types.Output{
		Input:     input,
		Results:   firstResults,
		ChiefRays: chiefRays,
	}
	withOutputMetadata(&output.Input, "trace", subcmdArgs())

	writeYAML(&output)
}

// traceRayList traces one config's ray list with a worker pool and returns the
// results in ray order. label names the config in error messages ("" in
// single-config mode).
func traceRayList(rayList []types.Ray, surfaces []types.Surface, engine *ray.Engine, details, verbose, lenient bool, pol types.JonesVector, label string) []types.RayResult {
	results := make([]types.RayResult, len(rayList))
	var errorsMu sync.Mutex
	var wg sync.WaitGroup
	workers := runtime.GOMAXPROCS(0)
	if workers > len(rayList) {
		workers = len(rayList)
	}
	jobs := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				r := &rayList[i]
				r.Jones = pol
				if lenient {
					r.Lenient = true
				}
				ray.ResolveRay(r, surfaces, engine)
				result := engine.TraceRay(*r, surfaces, details)
				if result.Error != "" {
					errMsg := fmt.Sprintf("{\"ray\":%q,\"error\":%q,\"error_code\":%q}\n", r.ID, result.Error, result.ErrorCode)
					if label != "" {
						errMsg = fmt.Sprintf("{\"config\":%q,\"ray\":%q,\"error\":%q,\"error_code\":%q}\n", label, r.ID, result.Error, result.ErrorCode)
					}
					errorsMu.Lock()
					if verbose {
						fmt.Fprint(os.Stderr, errMsg)
					} else if label != "" {
						errOut("Warning: config %q: ray %q error: %s", label, r.ID, result.Error)
					} else {
						errOut("Warning: ray %q error: %s", r.ID, result.Error)
					}
					errorsMu.Unlock()
				}
				results[i] = result
			}
		}()
	}
	for i := range rayList {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

// gatherTraceRays returns the merged ray list to trace: the input `rays`
// section plus the per-field chief rays from `chief_rays`. Chief rays are
// identified in the rays section by the synthetic "chief_<angle>deg" ids, so a
// duplicate copy there is not traced twice.
func gatherTraceRays(rays *types.RayInput, chiefRays []types.ChiefRayResult, pol types.JonesVector) []types.Ray {
	total := 0
	if rays != nil {
		total += len(rays.Rays)
	}
	total += len(chiefRays)
	rayList := make([]types.Ray, 0, total)

	seen := make(map[string]bool, len(chiefRays))
	if rays != nil {
		for _, r := range rays.Rays {
			if r.ID != "" {
				seen[r.ID] = true
			}
			rayList = append(rayList, r)
		}
	}
	for _, cr := range chiefRays {
		id := fmt.Sprintf("chief_%.0fdeg", cr.FieldAngle)
		if seen[id] {
			continue
		}
		r := cr.ChiefRay
		r.ID = id
		r.Jones = pol
		rayList = append(rayList, r)
	}
	return rayList
}
