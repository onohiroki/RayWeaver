package clispec

// Zoom is the flag surface of `rayweave zoom`.
var Zoom = Spec{Command: "zoom", Flags: []Flag{
	{Name: "config", Kind: String, Role: RoleSelector,
		Help: "summarise only this config by id (default: every active config)"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
	// Resolved with the document fallback and always written back.
	{Name: "wl", Kind: Float, Role: RoleSetting,
		Help: "reference wavelength in mm for the first-order trace and the grids"},
	// Mirrors chief.num_rays and is written back only when given.
	{Name: "num-rays", Kind: Int, Role: RoleSetting, YAML: "chief.num_rays",
		Help: "pupil-grid rays per field (default: chief.num_rays, else 64)"},
}}
