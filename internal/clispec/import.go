package clispec

// Import is the flag surface of `rayweave import`. --format is also passed
// explicitly by the MCP tool (it validates it), so it is MCPSkip there.
var Import = Spec{Command: "import", Flags: []Flag{
	{Name: "format", Kind: String, Role: RoleSetting, MCPSkip: true,
		Help: "input format: zemax | oslo | codev"},
	{Name: "config-id", Kind: String, Default: "config1", Role: RoleSetting,
		Help: "id of the created config (default config1)"},
	{Name: "config-name", Kind: String, Default: "Config1", Role: RoleSetting,
		Help: "display name of the created config"},
	{Name: "no-chief", Kind: Bool, Role: RoleAction,
		Help: "skip chief ray computation"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
}}
