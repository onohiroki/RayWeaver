package clispec

// Paraxial is the flag surface of `rayweave paraxial`.
var Paraxial = Spec{Command: "paraxial", Flags: []Flag{
	{Name: "config", Kind: String, Role: RoleSelector,
		Help: "select config by id (multi-config mode)"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
	// The effective reference wavelength (flag > chief.reference_wavelength >
	// built-in default) is resolved and written back by the command, which also
	// needs it for the paraxial trace.
	{Name: "wl", Kind: Float, Role: RoleSetting,
		Help: "reference wavelength in mm for the paraxial calculation"},
	{Name: "wavelengths", Kind: FloatList, Role: RoleSetting, Repeat: true,
		Help: "comma-separated wavelengths in mm for a colour sweep (default: reference only)"},
	{Name: "wavelength-set", Kind: String, Role: RoleSetting,
		Help: "colour sweep set: design | spectral | custom (default: none)"},
}}
