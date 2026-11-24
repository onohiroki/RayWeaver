package clispec

import "github.com/hiroki/rayweaver/internal/types"

// Stop is the flag surface of `rayweave stop`: materialize the virtual
// entrance pupil as a real plane stop.
var Stop = Spec{Command: "stop", Flags: []Flag{
	{Name: "config", Kind: String, Role: RoleSelector,
		Help: "reference config by id (multi-config documents); its stop plane is solved and the other configs follow it"},
	{Name: "anchor", Kind: String, Role: RoleSetting, YAML: "stop_insert.anchor",
		Help: "placement in the other configs: rear (default; keep the distance to the surface behind the stop) | front (keep the distance to the surface in front)"},
	{Name: "id", Kind: Int, Role: RoleSetting, YAML: "stop_insert.id",
		Help: "surface ID for the inserted stop (0 = one above the largest existing ID)"},
	{Name: "wl", Kind: Float, Default: types.DefaultWavelength, Role: RoleSetting,
		YAML:   "chief.reference_wavelength",
		Always: true,
		Help:   "reference wavelength in mm for the paraxial pupil solve"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
	{Name: "no-verify", Kind: Bool, Role: RoleAction,
		Help: "skip the chief-ray spot-RMS comparison of the virtual-pupil and materialized systems"},
	{Name: "dry-run", Kind: Bool, Role: RoleAction,
		Help: "report the planned stop without modifying the document"},
}}
