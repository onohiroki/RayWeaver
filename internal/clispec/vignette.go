package clispec

import "github.com/hiroki/rayweaver/internal/types"

// Vignette is the flag surface of `rayweave vignette`.
var Vignette = Spec{Command: "vignette", Flags: []Flag{
	{Name: "config", Kind: String, Role: RoleSelector,
		Help: "select config by id (multi-config mode)"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
	{Name: "iterations", Kind: Int, Role: RoleSetting, YAML: "vignette.iterations",
		Help: "number of diameter/pupil passes (default 3, else vignette.iterations)"},
	{Name: "min-glass-path", Kind: Float, Default: 0.5, Role: RoleSetting, YAML: "vignette.min_glass_path",
		Help: "minimum glass path (edge thickness) below which a ray fails, applied to every glass element (mm)"},
	{Name: "margin-mm", Kind: Float, Default: 0.2, Role: RoleSetting, YAML: "vignette.margin_mm",
		Help: "clearance added to each side of the beam footprint when sizing auto_aperture surfaces (mm)"},
	// Resolved with its YAML fallback and written back unconditionally by the
	// command, because it is shared with chief and paraxial.
	{Name: "wl", Kind: Float, Default: types.DefaultWavelength, Role: RoleSetting,
		Help: "reference wavelength in mm for grid ray tracing"},
}}
