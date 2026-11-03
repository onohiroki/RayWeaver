package clispec

// List is the flag surface of `rayweave list`. The targets are positional
// arguments, not flags. --format is MCPSkip because the MCP tool always passes
// its resolved format explicitly.
var List = Spec{Command: "list", Flags: []Flag{
	{Name: "format", Kind: String, Default: "table", Role: RoleOutput, MCPSkip: true,
		Help: "output format: table | yaml | json | csv"},
	{Name: "config", Kind: String, Role: RoleSelector,
		Help: "select config by id (multi-config mode)"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
	{Name: "curvature", Kind: Bool, Role: RoleOutput,
		Help: "show curvature instead of radius"},
	{Name: "auto-aperture", Kind: Bool, Role: RoleOutput,
		Help: "for surfaces: show the auto_aperture state column (auto/fixed)"},
	{Name: "all-glasses", Kind: Bool, Role: RoleOutput,
		Help: "for glasses: include glass_catalog entries not used by any surface"},
	{Name: "roles", Kind: Bool, Role: RoleOutput,
		Help: "for paraxial: also show the element roles table"},
	{Name: "summary", Kind: Bool, Role: RoleOutput,
		Help: "for rays: show only the summary (no per-surface detail)"},
}}
