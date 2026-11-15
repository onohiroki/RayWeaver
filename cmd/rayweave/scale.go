package main

import (
	"flag"
	"fmt"
	"math"
	"os"

	"github.com/hiroki/rayweaver/internal/clispec"
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
	opts := clispec.Bind(clispec.Scale, fs)
	if err := opts.Parse(os.Args[2:]); err != nil {
		errExitf("Error: %v", err)
	}
	configFlag := opts.String("config")

	input := parseYAML[types.Input](data)
	setReferenceWavelength(input.Chief)

	// Target EFL: --efl (flag) wins over scale.efl (YAML).
	target := opts.Float("efl")
	if !opts.Set("efl") && input.Scale != nil && input.Scale.EFL > 0 {
		target = input.Scale.EFL
	}
	if target <= 0 {
		errExitf("Error: --efl TARGET (mm) is required (or set scale.efl in the input YAML)")
	}
	// Principle 3: the table echoes the given --efl into scale.efl.
	opts.WriteBack(&input)

	gc, _ := openGlass(&input, opts.String("glass-dir"))

	refSurfaces := configSurfaces(input.Configs, &configFlag)
	if len(refSurfaces) == 0 {
		errExitf("Error: no surfaces defined")
	}
	surface.Precompute(refSurfaces)

	wavelength := effectiveReferenceWavelength(input.Chief)
	refSys := types.System{Surfaces: refSurfaces}
	cur := paraxial.Compute(refSys, wavelength, gc, 0, nil).FocalLength
	if math.Abs(cur) < 1e-9 {
		errExitf("Error: current EFL is zero; cannot scale")
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
	// Field image heights, object distances and virtual-pupil positions are
	// lengths too; leaving them at the old scale would make the scaled
	// document describe a different field than the surfaces form.
	scaleFieldLengths(input.Chief, s)
	for ci := range input.Configs {
		for fi := range input.Configs[ci].Fields {
			scaleFieldItem(&input.Configs[ci].Fields[fi], s)
		}
	}
	scalePupilModel(input.PupilModel, s)
	if input.Chief != nil {
		scalePupilModel(input.Chief.PupilModel, s)
		if input.Chief.PassThrough != nil {
			input.Chief.PassThrough.Coordinate.X *= s
			input.Chief.PassThrough.Coordinate.Y *= s
			input.Chief.PassThrough.Coordinate.Z *= s
		}
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
	//
	// A polynomial asphere term is a_n*h^n (n = 2i+4).  Under h -> s*h the
	// sag must scale by s, so a_n -> a_n/s^(n-1).  A Zernike asphere instead
	// evaluates c_i*(h/norm_radius)^k: scaling both h and norm_radius leaves
	// the normalised shape — and therefore the coefficients — unchanged.
	switch sf.Type {
	case types.AspherePolynomial:
		for i, c := range sf.Coefficients {
			power := 2*i + 4
			sf.Coefficients[i] = c / math.Pow(s, float64(power-1))
		}
	case types.AsphereZernike:
		// coefficients untouched; NormRadius was scaled above
	}
	return sf
}

// scaleFieldItem scales the length-valued fields of a per-config field.
func scaleFieldItem(f *types.FieldItem, s float64) {
	f.ImageHeight *= s
	f.Height *= s
	f.ObjectZ *= s
}

// scaleFieldLengths scales the length-valued fields of the top-level chief
// field table.
func scaleFieldLengths(c *types.ChiefInput, s float64) {
	if c == nil {
		return
	}
	for i := range c.Fields {
		c.Fields[i].ImageHeight *= s
		c.Fields[i].Height *= s
		c.Fields[i].ObjectZ *= s
	}
}

// scalePupilModel scales the virtual entrance pupil geometry.
func scalePupilModel(p *types.PupilModelConfig, s float64) {
	if p == nil {
		return
	}
	p.AxialPosition *= s
	p.Diameter *= s
}
