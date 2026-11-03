package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"

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
	args := os.Args[2:]
	fs := flag.NewFlagSet("trace", flag.ExitOnError)
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	traceVerbose := fs.Bool("verbose", false, "print per-ray trace info to stderr")
	traceDetails := fs.Bool("details", false, "add per-surface detail (AOI, n1/n2, Fresnel) to output YAML")
	fs.String("lenient", "", "trace rays leniently: true|false (default: rays.lenient from the input YAML, else false)")
	fs.Parse(args)

	input := parseYAML[types.Input](data)

	gc, cc := openGlass(&input, *glassDir)
	surfaces := configSurfaces(input.Configs, configFlag)
	surface.Precompute(surfaces)

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
	lenient, lenientSet, err := boolFlag(fs, "lenient")
	if err != nil {
		errExitf("Error: %s", err)
	}
	if !lenientSet && input.Rays != nil && input.Rays.Lenient {
		lenient = input.Rays.Lenient
	}
	if lenientSet {
		// Principle 3: echo the effective value back into the output section.
		if input.Rays == nil {
			input.Rays = &types.RayInput{Polarization: pol}
		}
		input.Rays.Lenient = lenient
	}
	rayList := gatherTraceRays(input.Rays, chiefRays, pol)
	if len(rayList) == 0 {
		errExitf("Error: 'rays' or 'chief_rays' section is required")
	}

	output := types.Output{
		Input:     input,
		Results:   make([]types.RayResult, len(rayList)),
		ChiefRays: chiefRays,
	}
	withOutputMetadata(&output.Input, "trace", subcmdArgs())

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
				result := engine.TraceRay(*r, surfaces, *traceDetails)
				if result.Error != "" {
					errMsg := fmt.Sprintf("{\"ray\":%q,\"error\":%q,\"error_code\":%q}\n", r.ID, result.Error, result.ErrorCode)
					errorsMu.Lock()
					if *traceVerbose {
						fmt.Fprint(os.Stderr, errMsg)
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

	output.Results = append(output.Results, results...)

	writeYAML(&output)
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
