package main

import (
	"flag"
	"fmt"
	"math"
	"os"

	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// runScale scales a system so its effective focal length equals --efl.
// A uniform scale of every length (radii, thicknesses, diameters, asphere
// coefficients and normalization radii) by s = target/current_EFL scales the
// EFL exactly by s, preserving the f-number and the normalized aberration
// balance.
func runScale(data []byte) {
	fs := flag.NewFlagSet("scale", flag.ExitOnError)
	eflTarget := fs.Float64("efl", 0, "target effective focal length (mm)")
	configFlag := fs.String("config", "", "select config by id (multi-config mode); its EFL sets the scale for all configs")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	fs.Parse(os.Args[2:])

	input := parseYAML[types.Input](data)
	setReferenceWavelength(input.Chief)

	// Target EFL: --efl (flag) wins over scale.efl (YAML).
	target := *eflTarget
	if !flagWasSet(fs, "efl") && input.Scale != nil && input.Scale.EFL > 0 {
		target = input.Scale.EFL
	}
	if target <= 0 {
		errOut("Error: --efl TARGET (mm) is required (or set scale.efl in the input YAML)")
		os.Exit(1)
	}
	if flagWasSet(fs, "efl") {
		// Principle 3: echo the effective value back into the output section.
		if input.Scale == nil {
			input.Scale = &types.ScaleConfig{}
		}
		input.Scale.EFL = target
	}

	gc, _ := loadCatalogs(&input, *glassDir)
	writeBackGlassDir(&input, *glassDir)

	refSurfaces := configSurfaces(input.Configs, configFlag)
	if len(refSurfaces) == 0 {
		errOut("Error: no surfaces defined")
		os.Exit(1)
	}
	surface.Precompute(refSurfaces)

	wavelength := effectiveReferenceWavelength(input.Chief)
	refSys := types.System{Surfaces: refSurfaces}
	cur := paraxial.Compute(refSys, wavelength, gc, 0, nil).FocalLength
	if math.Abs(cur) < 1e-9 {
		errOut("Error: current EFL is zero; cannot scale")
		os.Exit(1)
	}

	s := target / cur

	// Scale every config's surfaces by the same factor (keeps zoom ratios).
	for ci := range input.Configs {
		scaled := make([]types.Surface, len(input.Configs[ci].Surfaces))
		for i, sf := range input.Configs[ci].Surfaces {
			scaled[i] = scaleSurface(sf, s)
		}
		input.Configs[ci].Surfaces = scaled
	}

	output := types.Output{
		Input: input,
	}
	withOutputMetadata(&output.Input, "scale", subcmdArgs())
	writeYAML(&output)

	fmt.Fprintf(os.Stderr, "=== Scale complete ===\n")
	fmt.Fprintf(os.Stderr, "  EFL:      %.4f -> %.4f mm\n", cur, target)
	fmt.Fprintf(os.Stderr, "  Factor:   %.6f\n", s)
}

// scaleSurface returns a uniformly scaled copy of sf by factor s.
func scaleSurface(sf types.Surface, s float64) types.Surface {
	if sf.Curvature != 0 {
		sf.Curvature = sf.Curvature / s
	}
	sf.Thickness *= s
	if sf.Diameter > 0 {
		sf.Diameter *= s
	}
	if sf.NormRadius > 0 {
		sf.NormRadius *= s
	}
	// Conic constant is dimensionless and unchanged.
	// Asphere coefficients a_n for h^n scale as s^(n-1), n = 2i+4.
	for i, c := range sf.Coefficients {
		power := 2*i + 4
		sf.Coefficients[i] = c * math.Pow(s, float64(power-1))
	}
	return sf
}
