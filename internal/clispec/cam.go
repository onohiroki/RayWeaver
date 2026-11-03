package clispec

// Cam is the flag surface of `rayweave cam`.
var Cam = Spec{Command: "cam", Flags: []Flag{
	{Name: "config", Kind: String, Role: RoleSelector,
		Help: "solve only this config by id (default: every active config)"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
	{Name: "wl", Kind: Float, Role: RoleSetting, YAML: "chief.reference_wavelength",
		Help: "reference wavelength in mm for the paraxial solve"},
	{Name: "max-iter", Kind: Int, Role: RoleSetting, YAML: "cam.max_iterations",
		Help: "maximum solver iterations (default: cam.max_iterations, else 50)"},
	{Name: "tolerance", Kind: Float, Role: RoleSetting, YAML: "cam.tolerance",
		Help: "convergence tolerance on the residuals in mm (default: cam.tolerance, else 1e-9)"},
	{Name: "verbose", Kind: Bool, Role: RoleAction,
		Help: "print one line per accepted solver iteration to stderr"},
}}
