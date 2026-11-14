package clispec

import (
	"github.com/hiroki/rayweaver/internal/types"
)

// Chief is the flag surface of `rayweave chief`.
var Chief = Spec{Command: "chief", Flags: []Flag{
	{Name: "config", Kind: String, Role: RoleSelector,
		Help: "select config by id (multi-config mode)"},
	{Name: "all-configs", Kind: Bool, Role: RoleSelector,
		Help: "compute every active config (document order) and write per-config chief_rays/rays"},
	{Name: "configs", Kind: String, Role: RoleSelector,
		Help: "configs to compute, comma-separated ids or 0-based indexes (implies --all-configs)"},
	{Name: "pass-through", Kind: Int, Role: RoleAction,
		Help: "constrain the chief ray to pass through (0,0) centre of surface N"},
	{Name: "chief-ray", Kind: String, Role: RoleSetting,
		YAML: "chief.chief_ray_definition",
		Help: "chief-ray definition: entrance_pupil_centre | centroid | vignetting_centre (empty = chief.chief_ray_definition)"},
	{Name: "clear-aperture", Kind: Bool, Role: RoleAction,
		Help: "trace grid rays through every surface and size auto_aperture surfaces from the beam footprint"},
	{Name: "clear-aperture-margin-mm", Kind: Float, Default: 0.2, Role: RoleAction,
		Help: "clearance added to each side of the beam footprint in mm"},
	{Name: "clear-aperture-rays", Kind: Int, Role: RoleAction,
		Help: "ray count for --clear-aperture beam tracing (0 = chief.num_rays)"},
	{Name: "preserve-rays", Kind: Bool, Role: RoleAction,
		Help: "with --clear-aperture, keep the existing rays section instead of replacing it with chief rays"},
	{Name: "marginal-rays", Kind: Bool, Role: RoleAction,
		Help: "extract the marginal rays (max/min image Y) for trace/plot"},
	{Name: "ray-fan", Kind: Bool, Role: RoleAction,
		Help: "compute the ray fan (transverse aberration) of every field"},
	{Name: "fan-plane", Kind: String, Role: RoleAction,
		Help: "fan plane: yz or xz (implies ray-fan)"},
	{Name: "fan-rotation", Kind: FloatList, Role: RoleAction, Repeat: true,
		Help: "fan planes rotated by DEG around Z (implies ray-fan)"},

	// The virtual entrance pupil: --epd/--epz are the canonical spellings, the
	// pupil_model_* long forms are aliases of the same value slots. Resolving
	// them (activating the model, converting --fnum through the paraxial EFL)
	// stays in the command, which has the surfaces and catalog it needs.
	{Name: "epd", Kind: Float, Role: RoleSetting, Alias: "pupil-model-diameter",
		Help: "virtual entrance pupil diameter in mm (activates the pupil model)"},
	{Name: "fnum", Kind: Float, Role: RoleSetting,
		Help: "virtual entrance pupil diameter = paraxial EFL / N"},
	{Name: "epz", Kind: Float, Role: RoleSetting, Alias: "pupil-model-axial-position",
		Help: "virtual entrance pupil axial position Z in mm (activates the pupil model)"},
	{Name: "pupil-model-mode", Kind: String, Default: "virtual_entrance_pupil", Role: RoleSetting,
		Help: "pupil model mode (virtual_entrance_pupil)"},

	{Name: "wl", Kind: Float, Default: types.DefaultWavelength, Role: RoleSetting, Always: true,
		Help: "reference wavelength in mm for grid ray tracing"},
	{Name: "wavelengths", Kind: FloatList, Role: RoleSetting, Repeat: true,
		Help: "explicit per-wavelength stats list in mm (custom set)"},
	{Name: "wavelength-set", Kind: String, Role: RoleSetting,
		Help: "per-wavelength stats set: design | spectral | custom"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
}}
