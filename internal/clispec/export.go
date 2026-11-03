package clispec

// Export is the flag surface of `rayweave export`. The MCP tool resolves the
// output path itself, so `output` and the format it derives are MCPSkip.
var Export = Spec{Command: "export", Flags: []Flag{
	{Name: "format", Kind: String, Role: RoleSetting, MCPSkip: true,
		Help: "output format: zemax | codev | oslo (defaults to the -o file extension)"},
	{Name: "config", Kind: String, Role: RoleSelector,
		Help: "select config by id (single-config export)"},
	{Name: "nd-vd", Kind: Bool, Role: RoleSetting,
		Help: "CODE V: write every glass as its inline nd:vd model form"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
	{Name: "output", Short: "o", Kind: String, Role: RoleOutput, MCPSkip: true,
		Help: "output file path (default: stdout); with -o the foreign format is written to FILE and the YAML passes through to stdout"},
}}
