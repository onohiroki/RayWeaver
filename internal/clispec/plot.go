package clispec

// Plot is the flag surface of `rayweave plot`. Every flag is an output
// setting: the renderer never writes them into the pipeline document (the
// plot manual documents the exemption from the CLI/YAML write-back rule).
var Plot = Spec{Command: "plot", Flags: []Flag{
	{Name: "output", Short: "o", Kind: String, Role: RoleOutput, MCPSkip: true,
		Help: "output file path (.svg or .png; default: stdout = SVG)"},
	{Name: "config", Kind: String, Role: RoleSelector,
		Help: "select config by id (multi-config mode)"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
	{Name: "lens-width", Kind: Float, Default: 1.5, Role: RoleOutput,
		Help: "lens body stroke width in pixels"},
	{Name: "ray-width", Kind: Float, Default: 1.5, Role: RoleOutput,
		Help: "ray path stroke width in pixels"},
	{Name: "scale", Kind: Float, Role: RoleOutput,
		Help: "SVG scale factor (0 = auto)"},
	{Name: "right-margin", Kind: Float, Default: 20.0, Role: RoleOutput,
		Help: "right-side margin beyond the image plane (% of lens length)"},
	{Name: "fan-rays", Kind: Int, Default: 11, Role: RoleOutput,
		Help: "max fan rays drawn per field in the lens diagram (0 = hide fan rays)"},
	{Name: "element-color", Kind: StringList, Role: RoleOutput, Repeat: true,
		Help: "element fill colors: \"0=#ff0000,2=#00ff00\" (repeatable)"},
	{Name: "asphere-color", Kind: StringList, Role: RoleOutput, Repeat: true,
		Help: "aspheric surface edge colors: \"#ff0000\" or \"3=#ff0000,7=#0000ff\" (repeatable)"},
	{Name: "show-invalid-rays", Kind: Bool, Alias: "show-invalid-ray-fan", Role: RoleOutput,
		Help: "draw rays that carry an error code in full (traced results and fan rays)"},
	{Name: "clip-invalid-rays", Kind: Bool, Alias: "clip-invalid-ray-fan", Role: RoleOutput,
		Help: "draw error-coded rays only up to the first erroring surface"},
}}
