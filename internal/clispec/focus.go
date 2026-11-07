package clispec

// Focus is the flag surface shared by `focus mtf`, `focus psf` and
// `focus spot`. FocusMTF adds the frequency list that only the MTF table uses.
var Focus = Spec{Command: "focus", Flags: []Flag{
	{Name: "config", Kind: String, Role: RoleSelector,
		Help: "select config by id (multi-config mode)"},
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
	{Name: "ref-surface", Kind: Int, Role: RoleSetting,
		Help: "reference surface ID for wavefront sampling (default: last optical surface)"},
	{Name: "psf-grid", Kind: Int, Role: RoleSetting,
		Help: "image-plane pixels per side (default 64)"},
	{Name: "num-rays", Kind: Int, Role: RoleSetting,
		Help: "pupil grid rays (default 400)"},
	{Name: "fields", Kind: IntList, Role: RoleSetting, Repeat: true,
		Help: "comma-separated field indices to compute (default: all)"},
	{Name: "wavelengths", Kind: FloatList, Role: RoleSetting, Repeat: true,
		Help: "comma-separated wavelengths in mm (default: config wavelengths, else reference)"},
	{Name: "wavelength-set", Kind: String, Role: RoleSetting,
		Help: "wavelength set: design (config wavelengths with weights) | spectral (document spectrum) | custom"},
	{Name: "polarization", Kind: String, Role: RoleSetting,
		Help: "input polarization: RCP (default) | LCP | X | Y | RCP+LCP"},
	{Name: "planes", Kind: String, Role: RoleSetting,
		Help: "planes to compare: file,all,best (default: all three); with --through-focus: base planes file,all,on_axis (default: file)"},
	{Name: "through-focus", Kind: String, Role: RoleSetting,
		Help: "through-focus scan FROM,STEP,COUNT (mm, number of planes); scans around each base plane in --planes"},
	{Name: "converge-check", Kind: BoolString, Role: RoleSetting,
		Help: "label sampling convergence by re-evaluating at 1.5x rays (default: off; true|false)"},
}}

// FocusMTF is Focus plus the reported spatial frequencies (`focus mtf` only).
var FocusMTF = Spec{Command: "focus mtf", Flags: append(append([]Flag{}, Focus.Flags...),
	Flag{Name: "frequencies", Kind: FloatList, Role: RoleSetting, Repeat: true,
		Help: "comma-separated spatial frequencies in cycles/mm (default: focus.mtf.frequencies, else 50)"},
)}
