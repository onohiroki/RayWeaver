package clispec

import "github.com/hiroki/rayweaver/internal/types"

// Trace is the flag surface of `rayweave trace` (the pipeline trace; the
// one-ray `trace single` subcommand has its own table).
var Trace = Spec{Command: "trace", Flags: []Flag{
	{Name: "config", Kind: String, Role: RoleSelector,
		Help: "select config by id (multi-config mode)"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
	{Name: "verbose", Kind: Bool, Role: RoleAction,
		Help: "print per-ray trace info to stderr"},
	{Name: "details", Kind: Bool, Role: RoleAction,
		Help: "add per-surface detail (AOI, n1/n2, Fresnel) to the output"},
	{Name: "lenient", Kind: BoolString, Role: RoleSetting,
		Help: "trace rays leniently: true|false (default: rays.lenient from the input YAML, else false)",
		Apply: func(input *types.Input, o *Options) {
			// Principle 3: echo the effective value into the output section,
			// creating the rays section with the default polarization when the
			// document has none.
			lenient, _, err := o.BoolString("lenient")
			if err != nil {
				return
			}
			if input.Rays == nil {
				input.Rays = &types.RayInput{Polarization: types.NewCircularJones(true)}
			}
			input.Rays.Lenient = lenient
		}},
}}
