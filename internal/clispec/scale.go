package clispec

// Scale is the flag surface of `rayweave scale`.
var Scale = Spec{Command: "scale", Flags: []Flag{
	{Name: "config", Kind: String, Role: RoleSelector,
		Help: "select config by id (multi-config mode); its EFL sets the scale for all configs"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
	{Name: "efl", Kind: Float, Role: RoleSetting, YAML: "scale.efl",
		Help: "target effective focal length in mm"},
}}
