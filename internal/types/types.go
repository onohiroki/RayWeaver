package types

import (
	"fmt"
	"math"
	"strings"

	"gopkg.in/yaml.v3"
)

type SurfaceType string

const (
	Sphere            SurfaceType = "sphere"
	AspherePolynomial SurfaceType = "asphere_polynomial"
	AsphereZernike    SurfaceType = "asphere_zernike"
)

type GridType string

const (
	GridPolar  GridType = "polar"
	GridSquare GridType = "square"
	GridHex    GridType = "hex"
)

type InteractionType string

const (
	Transmit InteractionType = "TRANSMIT"
	Reflect  InteractionType = "REFLECT"
	// Missed indicates the ray passed through a surface location without
	// intersecting it (used in Lenient mode to record a skipped surface).
	Missed InteractionType = "MISSED"
)

type GlassType string

const (
	GlassTypeCatalog   GlassType = "catalog"
	GlassTypeModel     GlassType = "model"
	GlassTypeTabulated GlassType = "tabulated"
)

type DispersionFormula string

const (
	Schott     DispersionFormula = "schott"
	Sellmeier1 DispersionFormula = "sellmeier_1"
	Extended2  DispersionFormula = "extended_2"
	Extended3  DispersionFormula = "extended_3"
	Constant   DispersionFormula = "constant"
	Laurent    DispersionFormula = "laurent"

	// Cauchy and Hartmann are the CODE V PRV formula types CAU / HAR. Unlike
	// most of the others they yield n directly (not n²).
	Cauchy   DispersionFormula = "cauchy"
	Hartmann DispersionFormula = "hartmann"
)

// DefaultWavelength is the fallback design wavelength (587.56 nm) in mm.
const DefaultWavelength = 0.00058756

type JonesVector struct {
	Ex, Ey complex128
}

func NewCircularJones(rightHanded bool) JonesVector {
	if rightHanded {
		return JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	}
	return JonesVector{Ex: complex(1, 0), Ey: complex(0, -1)}
}

func NewLinearJones(angleDeg float64) JonesVector {
	rad := angleDeg * 3.141592653589793 / 180.0
	c := complex(math.Cos(rad), 0)
	s := complex(math.Sin(rad), 0)
	return JonesVector{Ex: c, Ey: s}
}

// Scope selects the reference frame a DecenterStep is applied to.
type Scope string

const (
	// ScopeSurface is the default (empty) scope: the step applies to the
	// surface itself and the beam frame returns after it (CODE V DAR
	// semantics).
	ScopeFrame Scope = "frame"
	// ScopeBoth applies the step to both the surface and the beam frame (the
	// CODE V BEN semantics, e.g. a fold mirror stepping the frame after it).
	ScopeBoth Scope = "both"
)

// Bends reports whether the step bends the beam frame for following surfaces.
func (s Scope) Bends() bool {
	return s == ScopeFrame || s == ScopeBoth
}

// MovesSurface reports whether the step repositions/tilts the surface itself.
func (s Scope) MovesSurface() bool {
	return s != ScopeFrame
}

type DecenterStep struct {
	Shift Vec3  `yaml:"shift"`
	Tilt  Vec3  `yaml:"tilt"`
	Scope Scope `yaml:"scope,omitempty"`
}

// Material identifies the optical medium after a surface. It is either a
// reference into the glass catalog (Key), a self-contained model glass
// (ND/VD), or empty for AIR. When both Key and ND are present, Key takes
// precedence: the surface resolves through the catalog.
type Material struct {
	Key string  `yaml:"key,omitempty"`
	ND  float64 `yaml:"nd,omitempty"`
	VD  float64 `yaml:"vd,omitempty"`
}

// IsAir reports whether the material is empty air.
func (m Material) IsAir() bool {
	return m.ND == 0 && (m.Key == "" || strings.EqualFold(m.Key, "AIR"))
}

// HasKey reports whether the material is a catalog reference (key takes
// precedence over an inline nd/vd when both are present).
func (m Material) HasKey() bool {
	return m.Key != "" && !strings.EqualFold(m.Key, "AIR")
}

// HasModel reports whether the material carries its own nd/vd model glass
// (no catalog key).
func (m Material) HasModel() bool {
	return m.Key == "" && m.ND > 0
}

// String returns a canonical key for cache keys, lookups and error messages.
func (m Material) String() string {
	if m.HasKey() {
		return m.Key
	}
	if m.HasModel() {
		return fmt.Sprintf("%.5f:%.2f", m.ND, m.VD)
	}
	return "AIR"
}

type Surface struct {
	ID           int            `yaml:"id"`
	Type         SurfaceType    `yaml:"type"`
	Curvature    float64        `yaml:"curvature,omitempty"`
	Conic        float64        `yaml:"conic"`
	Thickness    float64        `yaml:"thickness"`
	Material     Material       `yaml:"material"`
	Diameter     float64        `yaml:"diameter,omitempty"`
	Coefficients []float64      `yaml:"coefficients,omitempty"`
	NormRadius   float64        `yaml:"norm_radius,omitempty"`
	Decenter     []DecenterStep `yaml:"decenter,omitempty"`
	Coating      string         `yaml:"coating,omitempty"`
	Role         string         `yaml:"role,omitempty"`
	AutoAperture bool           `yaml:"auto_aperture,omitempty"`
	MinGlassPath float64        `yaml:"min_glass_path,omitempty"`
	MaxGlassPath float64        `yaml:"max_glass_path,omitempty"`
	Reflect      bool           `yaml:"reflect,omitempty"`

	LocalToGlobal  Mat4    `yaml:"-"`
	GlobalToLocal  Mat4    `yaml:"-"`
	ParaxialRadius float64 `yaml:"-"`
	PhysicalZ      float64 `yaml:"-"`

	radiusUsed bool `yaml:"-"`
}

func (s *Surface) Radius() float64 {
	if s.Curvature == 0 {
		return 0
	}
	return 1.0 / s.Curvature
}

func (s *Surface) SetRadius(r float64) {
	if r == 0 {
		s.Curvature = 0
	} else {
		s.Curvature = 1.0 / r
	}
}

// Reflects reports whether the surface is a mirror (top-level `reflect: true`).
func (s Surface) Reflects() bool {
	return s.Reflect
}

// Bends reports whether any decenter step bends the beam frame for surfaces
// after this one (a scope of frame or both).
func (s Surface) Bends() bool {
	for _, d := range s.Decenter {
		if d.Scope.Bends() {
			return true
		}
	}
	return false
}

type RayState struct {
	Origin    Vec3 `yaml:"origin"`
	Direction Vec3 `yaml:"direction"`
}

type PassThroughTarget struct {
	Surface    int    `yaml:"surface"`
	Coordinate Vec3   `yaml:"coordinate"`
	Variable   string `yaml:"variable,omitempty"` // "direction" (default) or "origin"
}

type Ray struct {
	ID          string             `yaml:"id"`
	Wavelength  float64            `yaml:"wavelength"`
	Initial     RayState           `yaml:"initial"`
	Aim         *Vec3              `yaml:"aim,omitempty"`
	PassThrough *PassThroughTarget `yaml:"pass_through,omitempty"`
	Path        []int              `yaml:"path"`
	Jones       JonesVector        `yaml:"-"`
	// InitialField optionally overrides the 3D input electric field. When nil,
	// the field is initialised from Jones as (Ex, Ey, 0) in global coordinates.
	InitialField       *Vec3C `yaml:"-"`
	SkipGlassPathCheck bool   `yaml:"-"`
	SkipApertureCheck  bool   `yaml:"-"`
	// SkipAutoApertureCheck skips the aperture check on auto_aperture surfaces
	// only, so their diameter can be measured from the true beam extent rather
	// than from a self-clipped set of rays. Fixed (auto_aperture: false)
	// surfaces still clip.
	SkipAutoApertureCheck bool `yaml:"-"`
	// Lenient, when true, traces the ray through all surfaces without enforcing
	// aperture or glass-path checks, and continues past surfaces the ray misses
	// or undergoes TIR at, recording each failure in the per-surface result.
	// Chief rays and marginal rays use Lenient mode so they trace as far as the
	// geometry allows even when partially vignetted.
	Lenient bool `yaml:"lenient,omitempty"`
	// IncludeErrorSurfaces, when true, appends a MISSED SurfaceResult for the
	// surface where a non-lenient trace stops (aperture, TIR, missed,
	// glass-path, surface-not-found), so the partial result shows where the ray
	// stopped. Only the error code is set; detail fields stay empty.
	IncludeErrorSurfaces bool `yaml:"-"`
}

type SurfaceResult struct {
	SurfaceID   int             `yaml:"surface_id"`
	Position    Vec3            `yaml:"position"`
	Direction   Vec3            `yaml:"direction"`
	Interaction InteractionType `yaml:"interaction"`
	Thickness   float64         `yaml:"thickness"`
	OPL         float64         `yaml:"opl"`
	Jones       JonesVector     `yaml:"jones"`
	// Field is the propagated 3D complex electric field in global coordinates
	// at this surface, produced by the polarized ray tracer.
	Field      Vec3C   `yaml:"-"`
	IntensityS float64 `yaml:"intensity_s"`
	IntensityP float64 `yaml:"intensity_p"`
	ErrorCode  string  `yaml:"error_code,omitempty"`
	// Per-surface detail (populated only when --details is active).
	AngleOfIncidence *float64 `yaml:"angle_of_incidence,omitempty"` // degrees
	N1               *float64 `yaml:"n1,omitempty"`                 // incident refractive index
	N2               *float64 `yaml:"n2,omitempty"`                 // emergent refractive index
	Rs               *float64 `yaml:"rs,omitempty"`                 // s-polarization Fresnel amplitude reflection
	Rp               *float64 `yaml:"rp,omitempty"`                 // p-polarization Fresnel amplitude reflection
	Ts               *float64 `yaml:"ts,omitempty"`                 // s-polarization Fresnel amplitude transmission
	Tp               *float64 `yaml:"tp,omitempty"`                 // p-polarization Fresnel amplitude transmission
	IntensityRs      *float64 `yaml:"intensity_rs,omitempty"`       // s-polarization power reflection (Rs^2)
	IntensityRp      *float64 `yaml:"intensity_rp,omitempty"`       // p-polarization power reflection (Rp^2)
	CoatingRs        *float64 `yaml:"coating_rs,omitempty"`         // coating power reflection s
	CoatingRp        *float64 `yaml:"coating_rp,omitempty"`         // coating power reflection p
	CoatingTs        *float64 `yaml:"coating_ts,omitempty"`         // coating power transmission s
	CoatingTp        *float64 `yaml:"coating_tp,omitempty"`         // coating power transmission p
}

type RayResult struct {
	ID         string          `yaml:"id"`
	Wavelength float64         `yaml:"wavelength"`
	Error      string          `yaml:"error,omitempty"`
	ErrorCode  string          `yaml:"error_code,omitempty"`
	Surfaces   []SurfaceResult `yaml:"surfaces"`
	OPLTotal   float64         `yaml:"opl_total"`
	IntensityS float64         `yaml:"intensity_s"`
	IntensityP float64         `yaml:"intensity_p"`
}

type RefractiveIndexEntry struct {
	Wavelength float64 `yaml:"wavelength"`
	Value      float64 `yaml:"index"`
}

type RefractiveIndexTable []RefractiveIndexEntry

func (t *RefractiveIndexTable) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var list []RefractiveIndexEntry
		if err := node.Decode(&list); err != nil {
			return err
		}
		*t = list
		return nil
	case yaml.MappingNode:
		var compact struct {
			Index      []float64 `yaml:"index"`
			Wavelength []float64 `yaml:"wavelength"`
		}
		if err := node.Decode(&compact); err != nil {
			return err
		}
		if len(compact.Index) != len(compact.Wavelength) {
			return fmt.Errorf("refractive_indices: index and wavelength arrays must have same length (got %d vs %d)", len(compact.Index), len(compact.Wavelength))
		}
		*t = make(RefractiveIndexTable, len(compact.Index))
		for i := range compact.Index {
			(*t)[i] = RefractiveIndexEntry{
				Wavelength: compact.Wavelength[i],
				Value:      compact.Index[i],
			}
		}
		return nil
	default:
		return fmt.Errorf("refractive_indices must be a list of {wavelength, index} or a map with index and wavelength arrays")
	}
}

func (t RefractiveIndexTable) MarshalYAML() (interface{}, error) {
	index := make([]float64, len(t))
	wavelength := make([]float64, len(t))
	for i, e := range t {
		index[i] = e.Value
		wavelength[i] = e.Wavelength
	}
	return struct {
		Index      []float64 `yaml:"index"`
		Wavelength []float64 `yaml:"wavelength"`
	}{Index: index, Wavelength: wavelength}, nil
}

type Glass struct {
	Type              GlassType            `yaml:"type,omitempty"`
	Key               string               `yaml:"key,omitempty"`
	Name              string               `yaml:"name,omitempty"`
	Label             string               `yaml:"label,omitempty"`
	Manufacturer      string               `yaml:"manufacturer,omitempty"`
	DispersionFormula DispersionFormula    `yaml:"dispersion_formula,omitempty"`
	ND                float64              `yaml:"nd,omitempty"`
	VD                float64              `yaml:"vd,omitempty"`
	Coefficients      []float64            `yaml:"coefficients,omitempty"`
	WavelengthMin     float64              `yaml:"wavelength_range_min,omitempty"`
	WavelengthMax     float64              `yaml:"wavelength_range_max,omitempty"`
	Aliases           []string             `yaml:"aliases,omitempty"`
	RefractiveIndices RefractiveIndexTable `yaml:"refractive_indices,omitempty"`
}

type GlassCatalog struct {
	Directory         string   `yaml:"directory,omitempty"`
	Files             []string `yaml:"files,omitempty"`
	Entries           []Glass  `yaml:"entries,omitempty"`
	ManufacturerOrder []string `yaml:"manufacturer_order,omitempty"`
}

type CoatingLayer struct {
	Material  string  `yaml:"material,omitempty"`
	Thickness float64 `yaml:"thickness"`
	N         float64 `yaml:"n,omitempty"`
}

type CoatingEntry struct {
	Name   string         `yaml:"name"`
	Layers []CoatingLayer `yaml:"layers"`
}

type CoatingCatalog struct {
	Entries []CoatingEntry `yaml:"entries,omitempty"`
}

type System struct {
	Surfaces    []Surface `yaml:"-"`
	StopSurface int       `yaml:"-"`
}

type FieldDef struct {
	Angle       float64   `yaml:"angle"`
	ImageHeight float64   `yaml:"image_height,omitempty"`
	Height      float64   `yaml:"height,omitempty"`
	ObjectZ     float64   `yaml:"object_z,omitempty"`
	Direction   []float64 `yaml:"direction,omitempty"`
	Path        []int     `yaml:"path,omitempty"`
	// Vignetting clips the field's entrance-pupil grid to the ZEMAX-style
	// vignetted pupil ellipse (see VignettingDef). Nil = no clipping.
	Vignetting *VignettingDef `yaml:"vignetting,omitempty"`
}

type ChiefInput struct {
	FieldAngles      []float64          `yaml:"field_angles,omitempty"`
	Fields           []FieldDef         `yaml:"fields,omitempty"`
	ReferenceSurface int                `yaml:"reference_surface"`
	StopSurface      int                `yaml:"stop_surface,omitempty"`
	NumRays          int                `yaml:"num_rays"`
	GridType         GridType           `yaml:"grid_type,omitempty"`
	DumpMap          bool               `yaml:"dump_map,omitempty"`
	PassThrough      *PassThroughTarget `yaml:"pass_through,omitempty"`
	// ReferenceWavelength is the system reference wavelength (mm) used for
	// chief-ray and paraxial calculations. The effective value is written back
	// by commands that resolve it.
	ReferenceWavelength float64 `yaml:"reference_wavelength"`
}

type RayInput struct {
	Polarization JonesVector `yaml:"polarization,omitempty"`
	Rays         []Ray       `yaml:"rays"`
	// Lenient relaxes ray tracing: skip aperture/glass-path checks and
	// continue past missed surfaces and TIR (the trace --lenient flag). The
	// effective value (flag wins over YAML) is written back on output.
	Lenient bool `yaml:"lenient,omitempty"`
}

// RayTraceConfig configures a single-ray trace via `trace single`.
// All fields are optional; CLI flags override YAML values.
type RayTraceConfig struct {
	Origin      []float64 `yaml:"origin,omitempty"`
	Direction   []float64 `yaml:"direction,omitempty"`
	Aim         []float64 `yaml:"aim,omitempty"`
	AngleYZ     *float64  `yaml:"angle_yz,omitempty"`
	PassThrough []float64 `yaml:"pass_through,omitempty"` // [surface, Y, X]
	Path        []int     `yaml:"path,omitempty"`
	Wavelength  float64   `yaml:"wavelength,omitempty"`
	ID          string    `yaml:"id,omitempty"`
	Lenient     bool      `yaml:"lenient,omitempty"`
	Details     bool      `yaml:"details,omitempty"`
}

type FieldItem struct {
	ID          int       `yaml:"id"`
	AngleDeg    float64   `yaml:"angle_deg"`
	ImageHeight float64   `yaml:"image_height,omitempty"`
	Height      float64   `yaml:"height,omitempty"`
	ObjectZ     float64   `yaml:"object_z,omitempty"`
	Direction   []float64 `yaml:"direction,omitempty"`
	Weight      float64   `yaml:"weight"`
	// Vignetting clips the field's entrance-pupil grid to the ZEMAX-style
	// vignetted pupil ellipse (see VignettingDef). Nil = no clipping.
	Vignetting *VignettingDef `yaml:"vignetting,omitempty"`
}

// VignettingDef is a per-field entrance-pupil ellipse clip in the ZEMAX
// vignetting-factor convention (VDX/VDY/VCX/VCY/VANN). Relative to the nominal
// entrance pupil of radius R centred on the pupil centre, the vignetted pupil
// is an ellipse centred at (DecenterX·R, DecenterY·R) with semi-axes
// (1−CompressionX)·R and (1−CompressionY)·R, rotated by atan(Tangent).
// All-zero factors describe no vignetting.
type VignettingDef struct {
	DecenterX    float64 `yaml:"decenter_x"`
	DecenterY    float64 `yaml:"decenter_y"`
	CompressionX float64 `yaml:"compression_x"`
	CompressionY float64 `yaml:"compression_y"`
	Tangent      float64 `yaml:"tangent,omitempty"`
}

// IsZero reports whether no vignetting clip is active.
func (v *VignettingDef) IsZero() bool {
	return v == nil || (v.DecenterX == 0 && v.DecenterY == 0 &&
		v.CompressionX == 0 && v.CompressionY == 0 && v.Tangent == 0)
}

// Contains reports whether the point (x, y) at the entrance-pupil plane (both
// relative to the nominal pupil centre) survives the vignetted-pupil clip for
// the nominal entrance-pupil radius R. An absent/zero vignetting passes every
// point inside the unit circle.
func (v *VignettingDef) Contains(x, y, radius float64) bool {
	if v == nil {
		return true
	}
	theta := math.Atan(v.Tangent)
	ct := math.Cos(theta)
	st := math.Sin(theta)
	dx := x - v.DecenterX*radius
	dy := y - v.DecenterY*radius
	sx := (1 - v.CompressionX) * radius
	sy := (1 - v.CompressionY) * radius
	if sx <= 0 || sy <= 0 {
		return false
	}
	// Rotate into the ellipse frame: u along the major axis (at angle -theta).
	u := dx*ct + dy*st
	vr := -dx*st + dy*ct
	norm := (u*u)/(sx*sx) + (vr*vr)/(sy*sy)
	return norm <= 1
}

type WavelengthItem struct {
	ID     int     `yaml:"id"`
	Value  float64 `yaml:"value"`
	Label  string  `yaml:"label,omitempty"`
	Weight float64 `yaml:"weight"`
}

type RayPath struct {
	ID            string `yaml:"id"`
	ObjectSurface int    `yaml:"object_surface"`
	ImageSurface  int    `yaml:"image_surface"`
	StopSurface   int    `yaml:"stop_surface"`
}

type MeritTerm struct {
	Kind        string  `yaml:"kind"`
	Field       int     `yaml:"field"`
	Wavelength  float64 `yaml:"wavelength"`
	Wavelength2 float64 `yaml:"wavelength2,omitempty"`
	Target      float64 `yaml:"target,omitempty"`
	// Fraction is the encircled-energy fraction for the spot_ee_radius kind
	// (0..1, default 0.8 = EE80). Ignored by other kinds.
	Fraction   float64 `yaml:"fraction,omitempty"`
	SurfaceSet []int   `yaml:"surface_set"`
	Weight     float64 `yaml:"weight"`
}

type MeritFunction struct {
	Type  string      `yaml:"type"`
	Terms []MeritTerm `yaml:"terms"`
}

// MeritMode is one named term list of a conditional merit schedule. When a
// config declares merit_modes, the active `optimization.merit_schedule` mode's
// terms replace the config's fixed `merit`.
type MeritMode struct {
	Name    string      `yaml:"name"`
	NumRays int         `yaml:"num_rays,omitempty"`
	Terms   []MeritTerm `yaml:"terms"`
}

// MeritScheduleMode assigns one mode a weight that interpolates between
// WeightFrom (at metric == anchor_from) and WeightTo (at metric == anchor_to).
type MeritScheduleMode struct {
	Name       string  `yaml:"name"`
	WeightFrom float64 `yaml:"weight_from"`
	WeightTo   float64 `yaml:"weight_to"`
}

// MeritScheduleConfig configures the smooth merit blend
// (`optimization.merit_schedule`). The total merit becomes
// Σ_configs w_cfg · Σ_modes w_k(s(x)) · M_{cfg,k}(x) where s is a scalar state
// metric and each mode weight w_k is a monotone curve between the anchors.
type MeritScheduleConfig struct {
	// Metric is the state signal driving the blend: merit_ratio (default),
	// iteration, glass_role (the glass-role residual magnitude summed
	// over GlassSurfaces across all configs), or spot_diffraction (the
	// per-field weighted-average geometric spot RMS / Airy radius, normalised
	// by the initial value so the metric always starts at 1.0 and drops as the
	// geometric spot shrinks toward the diffraction limit).
	Metric string `yaml:"metric"`
	// Curve is the interpolation shape between the anchors: linear (default),
	// sigmoid, or step (a hard mode switch at the midpoint).
	Curve string `yaml:"curve"`
	// AnchorFrom/AnchorTo bound the metric range over which the weights ramp
	// (t = clamp((s − anchor_from)/(anchor_to − anchor_from), 0, 1)).
	AnchorFrom float64 `yaml:"anchor_from"`
	AnchorTo   float64 `yaml:"anchor_to"`
	// GlassSurfaces are the glass surface IDs evaluated for the glass_role
	// metric (required when Metric is glass_role).
	GlassSurfaces []int `yaml:"glass_surfaces,omitempty"`
	// MetricAggregation combines the per-field spot/Airy ratios into one
	// scalar for the spot_diffraction metric: mean (config×field weighted
	// average, default) or max (worst field).
	MetricAggregation string               `yaml:"metric_aggregation,omitempty"`
	Modes             []MeritScheduleMode  `yaml:"modes"`
}

type Config struct {
	ID          string              `yaml:"id"`
	Name        string              `yaml:"name"`
	Weight      float64             `yaml:"weight"`
	Active      bool                `yaml:"active"`
	Fields      []FieldItem         `yaml:"fields"`
	Wavelengths []WavelengthItem    `yaml:"wavelengths"`
	RayPaths    []RayPath           `yaml:"ray_paths"`
	Surfaces    []Surface           `yaml:"surfaces"`
	Merit       *MeritFunction      `yaml:"merit,omitempty"`
	MeritModes  []MeritMode         `yaml:"merit_modes,omitempty"`
	Constraints []ConstraintOperand `yaml:"constraints,omitempty"`
}

type VariableTarget struct {
	Type   string `yaml:"type"`
	Config string `yaml:"config,omitempty"`
	ID     int    `yaml:"id"`
	Param  string `yaml:"param"`
}

type OptimizationVariable struct {
	Name   string         `yaml:"name"`
	Target VariableTarget `yaml:"target"`
	Min    float64        `yaml:"min"`
	Max    float64        `yaml:"max"`
	Step   float64        `yaml:"step,omitempty"`
	Active bool           `yaml:"active"`
}

type SharedVariableBinding struct {
	Config string  `yaml:"config"`
	ID     int     `yaml:"id"`
	Param  string  `yaml:"param"`
	Scale  float64 `yaml:"scale,omitempty"`
	Offset float64 `yaml:"offset,omitempty"`
}

type SharedVariable struct {
	Name     string                  `yaml:"name"`
	Min      float64                 `yaml:"min"`
	Max      float64                 `yaml:"max"`
	Active   bool                    `yaml:"active"`
	Bindings []SharedVariableBinding `yaml:"bindings"`
}

type LocalVariableDef struct {
	Name   string         `yaml:"name"`
	Config string         `yaml:"config"`
	Target VariableTarget `yaml:"target"`
	Min    float64        `yaml:"min"`
	Max    float64        `yaml:"max"`
	Active bool           `yaml:"active"`
}

type ConstraintKind string

const (
	ConstraintEquality        ConstraintKind = "equality"
	ConstraintInequalityUpper ConstraintKind = "inequality_upper"
	ConstraintInequalityLower ConstraintKind = "inequality_lower"
	ConstraintBand            ConstraintKind = "band"
	ConstraintFuzzy           ConstraintKind = "fuzzy"
)

type ConstraintMeasure string

const (
	MeasureImageHeight           ConstraintMeasure = "image_height"
	MeasureIncidentAngle         ConstraintMeasure = "incident_angle"
	MeasureThickness             ConstraintMeasure = "thickness"
	MeasureEFL                   ConstraintMeasure = "efl"
	MeasureAbsEFL                ConstraintMeasure = "abs_efl"
	MeasureSystemLength          ConstraintMeasure = "system_length"
	MeasureEntrancePupilDiameter ConstraintMeasure = "entrance_pupil_diameter"
	MeasureEdgeThickness         ConstraintMeasure = "edge_thickness"
	MeasureDiameter              ConstraintMeasure = "diameter"
	MeasureFNumber               ConstraintMeasure = "f_number"
	MeasureBeamClearance         ConstraintMeasure = "beam_clearance"
	MeasureVignettingFactor      ConstraintMeasure = "vignetting_factor"
	MeasureBeamDiameter          ConstraintMeasure = "beam_diameter"
)

type ConstraintOperand struct {
	ID         string            `yaml:"id"`
	Kind       ConstraintKind    `yaml:"kind"`
	Measure    ConstraintMeasure `yaml:"measure"`
	Field      int               `yaml:"field,omitempty"`
	Wavelength float64           `yaml:"wavelength,omitempty"`
	Surface    int               `yaml:"surface,omitempty"`
	Surface2   int               `yaml:"surface2,omitempty"`
	Target     float64           `yaml:"target,omitempty"`
	Lower      float64           `yaml:"lower,omitempty"`
	Upper      float64           `yaml:"upper,omitempty"`
	BandWidth  float64           `yaml:"band_width,omitempty"`
	Softness   float64           `yaml:"softness,omitempty"`
	Weight     float64           `yaml:"weight"`
	Active     bool              `yaml:"active"`
}

type GlassHullConfig struct {
	Enabled bool    `yaml:"enabled,omitempty"`
	Margin  float64 `yaml:"margin,omitempty"`
	Weight  float64 `yaml:"weight,omitempty"`
}

// RegionActiveConfig configures the Okudaira Region Active Method: Lagrange
// multipliers with hysteresis for dynamic active-set management of inequality
// constraints. Equality constraints are always treated as active regardless
// of this setting.
type RegionActiveConfig struct {
	// Enabled turns on the region active method. Enabled by default when
	// present (nil or absent = disabled; explicit Enabled: true or just
	// presence enables it — resolved in the optimizer constructor).
	Enabled bool `yaml:"enabled,omitempty"`
	// EpsActivate is the threshold above which an inactive constraint becomes
	// active (violation > eps_activate). Default: 1e-3 (mm).
	EpsActivate float64 `yaml:"eps_activate,omitempty"`
	// EpsDeactivate is the threshold below which an active constraint becomes
	// inactive (violation < eps_deactivate AND lambda small). Default: 1e-4 (mm).
	// Must satisfy eps_activate > eps_deactivate for hysteresis.
	EpsDeactivate float64 `yaml:"eps_deactivate,omitempty"`
	// LambdaStep is the Lagrange multiplier update step size (alpha in
	// mu_k <- max(0, mu_k + alpha * g_k)). Default: 1.0.
	LambdaStep float64 `yaml:"lambda_step,omitempty"`
	// MaxLambda caps the Lagrange multiplier magnitude. Default: 1e6.
	MaxLambda float64 `yaml:"max_lambda,omitempty"`
}

type OptimizationConfig struct {
	Method           string                 `yaml:"method"`
	Aggregate        string                 `yaml:"aggregate,omitempty"`
	Mu               float64                `yaml:"mu,omitempty"`
	MaxIter          int                    `yaml:"max_iter,omitempty"`
	Tol              float64                `yaml:"tol,omitempty"`
	Epsilon          float64                `yaml:"epsilon,omitempty"`
	NumRays          int                    `yaml:"num_rays,omitempty"`
	MuConMax         float64                `yaml:"mu_con_max,omitempty"`
	ApertureMargin   float64                `yaml:"aperture_margin,omitempty"`
	ApertureMarginMM float64                `yaml:"aperture_margin_mm,omitempty"`
	JacobianWorkers  int                    `yaml:"jacobian_workers,omitempty"`
	CentralDiff      bool                   `yaml:"central_diff,omitempty"`
	BFGS             bool                   `yaml:"bfgs,omitempty"`
	Variables        []OptimizationVariable `yaml:"variables,omitempty"`
	SharedVariables  []SharedVariable       `yaml:"shared_variables,omitempty"`
	LocalVariables   []LocalVariableDef     `yaml:"local_variables,omitempty"`
	Constraints      []ConstraintOperand    `yaml:"constraints,omitempty"`
	GlassHull        *GlassHullConfig       `yaml:"glass_hull,omitempty"`
	Escape           *EscapeConfig          `yaml:"escape,omitempty"`
	MeritSchedule    *MeritScheduleConfig   `yaml:"merit_schedule,omitempty"`
	Degenerate       *DegenerateConfig      `yaml:"degenerate,omitempty"`
	PowerSolve       *PowerSolveConfig      `yaml:"power_solve,omitempty"`
	RegionActive     *RegionActiveConfig    `yaml:"region_active,omitempty"`
	AdaptiveDamping  *AdaptiveDampingConfig `yaml:"adaptive_damping,omitempty"`
}

// AdaptiveDampingConfig configures per-variable adaptive damping for the DLS
// solver. Instead of the fixed μI damping, the solver uses μD where D is a
// diagonal matrix derived from Jacobian sensitivity, variable class, and
// accept/reject history. This gives high-sensitivity variables (curvature,
// asphere) stronger damping while letting low-sensitivity variables (thickness)
// move more freely.
type AdaptiveDampingConfig struct {
	// SensitivityEMA is the exponential moving average coefficient for
	// smoothing the per-variable sensitivity across iterations (0 = no
	// smoothing, 0.7 = typical). Higher values reduce oscillation.
	SensitivityEMA float64 `yaml:"sensitivity_ema,omitempty"`
	// SensitivityFloor is the minimum value for the Hessian diagonal H_jj,
	// preventing division by zero for variables with zero Jacobian columns.
	SensitivityFloor float64 `yaml:"sensitivity_floor,omitempty"`
	// RatioMin/RatioMax clamp the per-variable sensitivity ratio q_j =
	// h_j / h_ref, preventing extreme damping values.
	RatioMin float64 `yaml:"ratio_min,omitempty"`
	RatioMax float64 `yaml:"ratio_max,omitempty"`
	// DampingMin/DampingMax clamp the final per-variable damping diagonal
	// d_j, ensuring numerical stability.
	DampingMin float64 `yaml:"damping_min,omitempty"`
	DampingMax float64 `yaml:"damping_max,omitempty"`
	// RejectBoost multiplies localFactor[j] for variables that contributed
	// significantly to a rejected step (default 2.0).
	RejectBoost float64 `yaml:"reject_boost,omitempty"`
	// AcceptRelax multiplies localFactor[j] toward 1.0 for variables that
	// contributed to an accepted step (default 0.85).
	AcceptRelax float64 `yaml:"accept_relax,omitempty"`
	// ContributionThreshold is the minimum normalised contribution |g_j δ_j|
	// / Σ|g δ| for a variable to be considered responsible for a rejected step
	// (default 0.10).
	ContributionThreshold float64 `yaml:"contribution_threshold,omitempty"`
	// Classes maps variable class names (curvature, thickness, diameter, nd,
	// vd, conic, asphere, shared) to per-class damping parameters. Unset
	// classes use built-in defaults.
	Classes map[string]DampingClassConfig `yaml:"classes,omitempty"`
	// Variables maps individual variable names to damping overrides that take
	// precedence over class settings.
	Variables map[string]DampingVarConfig `yaml:"variables,omitempty"`
}

// DampingClassConfig holds per-class damping parameters for a variable class
// (curvature, thickness, asphere, etc.).
type DampingClassConfig struct {
	// SensitivityPower is the exponent α applied to the sensitivity ratio:
	// q_j^α. Values > 1 amplify the difference between high and low
	// sensitivity variables; values < 1 compress it.
	SensitivityPower *float64 `yaml:"sensitivity_power,omitempty"`
	// Multiplier is the base class coefficient c applied to the damping
	// diagonal: d_j = q_j^α × c × localFactor. Higher values mean stronger
	// damping for the entire class.
	Multiplier *float64 `yaml:"multiplier,omitempty"`
}

// DampingVarConfig holds per-variable damping overrides. When a variable name
// matches an entry in AdaptiveDampingConfig.Variables, these values take
// precedence over the class-level settings.
type DampingVarConfig struct {
	SensitivityPower *float64 `yaml:"sensitivity_power,omitempty"`
	Multiplier       *float64 `yaml:"multiplier,omitempty"`
	// Min/Max override the global DampingMin/DampingMax for this variable.
	Min *float64 `yaml:"min,omitempty"`
	Max *float64 `yaml:"max,omitempty"`
}

// PowerSolveConfig configures the power-preserving hard solve: the curvatures
// of the listed surfaces become dependent variables whose values are
// recomputed after every variable application so the thin-lens power of the
// element containing each surface stays equal to the initial (snapshot) power.
// This isolates glass-swap chromatic optimisation (LCA/TCA) from layout/power
// drift: the paraxial element powers — and therefore the nominal focal lengths
// — are held constant while only the dispersions (nd/vd) are free.
type PowerSolveConfig struct {
	// Enabled turns the power-preserving solve on. Surfaces default to empty
	// (no solve); even when Enabled the solve only touches listed surfaces, so
	// a user can enable it and enumerate exactly which elements to pin.
	Enabled bool `yaml:"enabled,omitempty"`
	// Surfaces are the surface IDs whose curvature is recomputed to preserve
	// the containing element's thin-lens power. Each must be a surface of a
	// refractive lens element (an air-separated singlet / the outer surface of
	// a cemented group); mirrors are skipped.
	Surfaces []int `yaml:"surfaces,omitempty"`
}

// DegenerateConfig configures the bounded penalty applied when a merit term
// cannot be evaluated (a pupil grid with no valid rays, or a wavefront fit
// that fails). Without it the legacy 1e6 sentinel feeds weight·1e12 into the
// merit and stalls the DLS line search. Values are in the metric's units (mm);
// each is clamped to the given default when unset.
type DegenerateConfig struct {
	SpotValue      float64 `yaml:"spot_value,omitempty"`
	OPDValue       float64 `yaml:"opd_value,omitempty"`
	WavefrontValue float64 `yaml:"wavefront_value,omitempty"`
}

// EscapeConfig configures the escape-function global optimisation loop
// (Ishiki-Ono style local-minimum escape for DLS).
type EscapeConfig struct {
	MaxCycles         int     `yaml:"max_cycles,omitempty"`
	EscapeWorkers     int     `yaml:"escape_workers,omitempty"`
	MaxSeconds        float64 `yaml:"max_seconds,omitempty"`
	DistanceThreshold float64 `yaml:"distance_threshold,omitempty"`
	// FingerprintDistanceThreshold is the design-fingerprint (element-power)
	// distance below which two candidates close in variable space are still the
	// same local minimum. A candidate is a repeat only when it is close in
	// variable space AND close in fingerprint space, so numerically-close but
	// structurally-different solutions are recorded as distinct minima. 0
	// disables the fingerprint criterion (variable distance only).
	FingerprintDistanceThreshold float64            `yaml:"fingerprint_distance_threshold,omitempty"`
	HInitial                     float64            `yaml:"h_initial,omitempty"`
	WInitial                     float64            `yaml:"w_initial,omitempty"`
	HMult                        float64            `yaml:"h_mult,omitempty"`
	WMult                        float64            `yaml:"w_mult,omitempty"`
	VariableWeights              map[string]float64 `yaml:"variable_weights,omitempty"`
	EscapeIterFrac               float64            `yaml:"escape_iter_frac,omitempty"`
	WSpan                        float64            `yaml:"w_span,omitempty"`
	StallWindowFrac              float64            `yaml:"stall_window_frac,omitempty"`
	StallRelTol                  float64            `yaml:"stall_rel_tol,omitempty"`
	StallEarlyStop               *bool              `yaml:"stall_early_stop,omitempty"`
	InitialPerturb               float64            `yaml:"initial_perturb,omitempty"`
}

type MeritBeforeAfter struct {
	Before      float64 `yaml:"before"`
	After       float64 `yaml:"after"`
	Improvement float64 `yaml:"improvement,omitempty"`
	Ratio       float64 `yaml:"ratio,omitempty"`
}

type OptimizationResult struct {
	Status      string                  `yaml:"status"`
	Iterations  int                     `yaml:"iterations"`
	Reason      string                  `yaml:"reason,omitempty"`
	Interrupted bool                    `yaml:"interrupted,omitempty"`
	Constraints []ConstraintMeasurement `yaml:"constraints,omitempty"`
	// Merit-schedule state: the mode with the largest final weight and the
	// final per-mode weights (present only with optimization.merit_schedule).
	ActiveMode  string             `yaml:"active_mode,omitempty"`
	ModeWeights map[string]float64 `yaml:"mode_weights,omitempty"`
	ModeChanges int                `yaml:"mode_changes,omitempty"`
	// MetricValue is the last evaluated merit-schedule metric (e.g. the
	// spot/Airy ratio for spot_diffraction). Present only with a schedule.
	MetricValue float64 `yaml:"metric_value,omitempty"`
	// EffectiveNumRays is the final num_rays resolved from the merit-mode
	// schedule (max across configs). Present only with a schedule that
	// declares per-mode num_rays.
	EffectiveNumRays int `yaml:"effective_num_rays,omitempty"`
}

// ConstraintMeasurement records the final measured value and residual of one
// active constraint at the optimisation result. Value is the raw constraint
// measure (e.g. the vignetting factor for MeasureVignettingFactor), residual is
// the weighted error (0 when the constraint is satisfied).
type ConstraintMeasurement struct {
	ID       string  `yaml:"id"`
	Config   string  `yaml:"config,omitempty"`
	Kind     string  `yaml:"kind"`
	Measure  string  `yaml:"measure"`
	Field    int     `yaml:"field,omitempty"`
	Value    float64 `yaml:"value"`
	Residual float64 `yaml:"residual"`
}

// EscapeResult is the escape-function global optimisation report. The best
// solution's surfaces live in the top-level system/configs (pipeline-compatible);
// every discovered local minimum is listed here with its full surface data.
type EscapeResult struct {
	BestIndex   int              `yaml:"best_index"`
	BestMerit   float64          `yaml:"best_merit"`
	Params      EscapeParamsInfo `yaml:"params"`
	TimedOut    bool             `yaml:"timed_out,omitempty"`
	Interrupted bool             `yaml:"interrupted,omitempty"`
	Minima      []EscapeMinimum  `yaml:"minima"`
}

// EscapeParamsInfo records the escape parameter values actually used.
type EscapeParamsInfo struct {
	HInitial          float64 `yaml:"h_initial"`
	WInitial          float64 `yaml:"w_initial"`
	HMult             float64 `yaml:"h_mult"`
	WMult             float64 `yaml:"w_mult"`
	DistanceThreshold float64 `yaml:"distance_threshold"`
	// FingerprintDistanceThreshold is the design-fingerprint distance used for
	// the distinct-minimum criterion (0 = fingerprint criterion disabled).
	FingerprintDistanceThreshold float64            `yaml:"fingerprint_distance_threshold,omitempty"`
	MaxCycles                    int                `yaml:"max_cycles"`
	EscapeWorkers                int                `yaml:"escape_workers,omitempty"`
	MaxSeconds                   float64            `yaml:"max_seconds,omitempty"`
	VariableWeights              map[string]float64 `yaml:"variable_weights,omitempty"`
	EscapeIterFrac               float64            `yaml:"escape_iter_frac,omitempty"`
	WSpan                        float64            `yaml:"w_span,omitempty"`
	StallWindowFrac              float64            `yaml:"stall_window_frac,omitempty"`
	StallRelTol                  float64            `yaml:"stall_rel_tol,omitempty"`
	StallEarlyStop               *bool              `yaml:"stall_early_stop,omitempty"`
	InitialPerturb               float64            `yaml:"initial_perturb,omitempty"`
}

// ConfigFeatures is one config's feature set for a local minimum — a compact
// fingerprint used to compare minima against each other. ElementPowers holds
// the thin-lens power of every lens element in system order.
type ConfigFeatures struct {
	ID            string    `yaml:"id"`
	ElementPowers []float64 `yaml:"element_powers,omitempty"`
}

// EscapeMinimum is one discovered local minimum. Single-config runs populate
// Surfaces; multi-config runs populate Configs. Features lists the fingerprint
// of each config; Merit stays at the minimum level as the objective scalar.
type EscapeMinimum struct {
	Index     int              `yaml:"index"`
	Merit     float64          `yaml:"merit"`
	File      string           `yaml:"file,omitempty"`
	Configs   []Config         `yaml:"configs,omitempty"`
	Surfaces  []Surface        `yaml:"surfaces,omitempty"`
	Variables []EscapeVarState `yaml:"variables"`
	Features  []ConfigFeatures `yaml:"features,omitempty"`
}

// EscapeVarState records the variable values at a local minimum.
type EscapeVarState struct {
	Name   string  `yaml:"name"`
	Config string  `yaml:"config,omitempty"`
	Surf   int     `yaml:"surf,omitempty"`
	Param  string  `yaml:"param"`
	After  float64 `yaml:"after"`
}

// RayweaverTool is the canonical marker written in every pipeline document's
// metadata.tool.name field, identifying it as RayWeaver-managed YAML.
const RayweaverTool = "RayWeaver"

// RayweaverURL is the project repository, written in metadata.tool.url.
const RayweaverURL = "https://github.com/onohiroki/RayWeaver"

// SchemaVersion is the current RayWeaver pipeline-YAML schema version. It lives
// in metadata.tool.schema_version and replaces the old top-level `version` field.
const SchemaVersion = 1

// ToolInfo holds the identity and generation metadata nested under
// metadata.tool. Name marks the document as RayWeaver-managed; SchemaVersion is
// the pipeline-YAML schema version (currently 1). Subcommands write back their
// Generator/Command/Version/SchemaVersion on output; the rest are round-tripped
// from the input. Input files hand-written or produced by `import` carry only
// the identity trio (name, url, schema_version).
type ToolInfo struct {
	Name          string   `yaml:"name"`
	URL           string   `yaml:"url"`
	Version       string   `yaml:"version,omitempty"`
	SchemaVersion int      `yaml:"schema_version"`
	Generator     string   `yaml:"generator,omitempty"`
	Command       []string `yaml:"command,omitempty"`
}

// Metadata carries the file-format identity and provenance of a pipeline
// document. Tool holds the identity fields (name, url, version, schema_version,
// generator, command). CreatedAt and Notes are top-level convenience fields.
type Metadata struct {
	Tool      ToolInfo `yaml:"tool"`
	CreatedAt string   `yaml:"created_at,omitempty"` // RFC3339, UTC
	Notes     string   `yaml:"notes,omitempty"`
}

// AsphereCandidateConfig configures the asphere candidate selection and initial
// sag estimation analysis (the `asphere` subcommand). All fields are optional;
// the command fills defaults for unset values.
type AsphereCandidateConfig struct {
	CandidateSurfaces       []int               `yaml:"candidate_surfaces,omitempty"`
	MaxEvenOrder            int                 `yaml:"max_even_order,omitempty"`
	IncludeConic            *bool               `yaml:"include_conic,omitempty"`
	PreserveVertexCurvature *bool               `yaml:"preserve_vertex_curvature,omitempty"`
	SagScale                float64             `yaml:"sag_scale,omitempty"`
	MaxSag                  float64             `yaml:"max_sag,omitempty"`
	MaxSlopeDeg             float64             `yaml:"max_slope_deg,omitempty"`
	MaxCurvatureVariation   float64             `yaml:"max_curvature_variation,omitempty"`
	CellRings               int                 `yaml:"cell_rings,omitempty"`
	CellAngles              int                 `yaml:"cell_angles,omitempty"`
	TBins                   int                 `yaml:"t_bins,omitempty"`
	PupilSamplesRadial      int                 `yaml:"pupil_samples_radial,omitempty"`
	SensitivitySamples      *int                `yaml:"sensitivity_samples,omitempty"`
	RemovePiston            *bool               `yaml:"remove_piston,omitempty"`
	RemoveTilt              *bool               `yaml:"remove_tilt,omitempty"`
	RemoveDefocus           *bool               `yaml:"remove_defocus,omitempty"`
	TopK                    int                 `yaml:"top_k,omitempty"`
	MinRaysPerCell          int                 `yaml:"min_rays_per_cell,omitempty"`
	ScoreWeights            AsphereScoreWeights `yaml:"score_weights,omitempty"`
	// Validate runs the Phase-4 short-DLS verification per fitted surface
	// (the --validate flag); Apply inserts the top-ranked validated asphere
	// onto its surface (the --apply flag, implies validate). ValidationDLSIter
	// and ValidationNumRays size that DLS (--dls-iter / --num-rays). Flags
	// win and the effective values are written back on output.
	Validate          *bool `yaml:"validate,omitempty"`
	Apply             *bool `yaml:"apply,omitempty"`
	ValidationDLSIter int   `yaml:"validation_dls_iter,omitempty"`
	ValidationNumRays int   `yaml:"validation_num_rays,omitempty"`
	// CalibrateScale replaces the fixed sag_scale per surface with a scale
	// derived from the measured ray-trace response (base/asphere merit and
	// d_merit_d_coef) when sensitivity_samples > 0 (--calibrate-scale; on by
	// default, disable with --calibrate-scale=false or calibrate_scale: false).
	// ScaleProbes overrides the quadratic estimate with an explicit list of
	// scales to trace and verify (--scale-probes "0.1,0.25,0.5,1.0").
	CalibrateScale *bool     `yaml:"calibrate_scale,omitempty"`
	ScaleProbes    []float64 `yaml:"scale_probes,omitempty"`
	// Diagnostics controls the verbose per-surface footprint data emitted by
	// the focus/OPD analysis. Recognized values are "opd" (OPD map, existing
	// opd_profiles) and "focus" (T/S focus channel). When empty, only the
	// rankings are emitted (backward compatible); with "focus", the result
	// carries the per-field T/S focus and the per-ray footprint maps. The
	// CLI flag is --diagnostics (comma-separated), the YAML field is
	// diagnostics.
	Diagnostics []string `yaml:"diagnostics,omitempty"`
	// FocusSamples is the number of rays per T/S fan for the focus channel
	// (CLI --focus-samples, YAML focus_samples). 0 means the default (17).
	FocusSamples int `yaml:"focus_samples,omitempty"`
	// FocusFans selects which fans to trace for the focus channel. Supported
	// values are "tangential" and "sagittal" (CLI --focus-fans, YAML
	// focus_fans). Empty means both.
	FocusFans []string `yaml:"focus_fans,omitempty"`
	// RankingProfile selects the ranking emphasis for --apply: "practical"
	// (default), "wavefront", "field", or "custom". Focus metrics are
	// diagnostic-only in the first version and do not change the default
	// practical_score; the profile only switches which column drives
	// --apply's selection when focus diagnostics are on.
	RankingProfile string `yaml:"ranking_profile,omitempty"`
}

// AsphereScoreWeights are the weights of the composite surface score
// S_s = w_com*E_common + w_uni*E_unique + w_fit*F + w_sens*H
//   - w_conf*C - w_mfg*M - w_unstable*U.
type AsphereScoreWeights struct {
	Common        float64 `yaml:"common,omitempty"`
	Unique        float64 `yaml:"unique,omitempty"`
	Fit           float64 `yaml:"fit,omitempty"`
	Sensitivity   float64 `yaml:"sensitivity,omitempty"`
	Conflict      float64 `yaml:"conflict,omitempty"`
	Manufacturing float64 `yaml:"manufacturing,omitempty"`
	Unstable      float64 `yaml:"unstable,omitempty"`
	Asym          float64 `yaml:"asym,omitempty"`
}

// AsphereCellStat is one polar cell's aggregated statistics over the fields
// that occupy it.
type AsphereCellStat struct {
	SurfaceID       int     `yaml:"surface_id"`
	Ring            int     `yaml:"ring"`
	Sector          int     `yaml:"sector"`
	MeanR           float64 `yaml:"mean_r"`
	OccupiedFields  []int   `yaml:"occupied_fields"`
	CommonOPD       float64 `yaml:"common_opd"`
	Conflict        float64 `yaml:"conflict"`
	UniqueResidual  float64 `yaml:"unique_residual"`
	AzimuthVariance float64 `yaml:"azimuth_variance"`
	RadialGradient  float64 `yaml:"radial_gradient"`
	Weight          float64 `yaml:"weight"`
}

// AsphereCoeffs holds an even-order polynomial asphere's coefficients.
type AsphereCoeffs struct {
	Conic float64 `yaml:"conic,omitempty"`
	A4    float64 `yaml:"A4,omitempty"`
	A6    float64 `yaml:"A6,omitempty"`
	A8    float64 `yaml:"A8,omitempty"`
	A10   float64 `yaml:"A10,omitempty"`
	A12   float64 `yaml:"A12,omitempty"`
}

// AsphereSensitivityMatrix is the finite-difference sensitivity of the traced
// merit to each even-order coefficient on a candidate surface.
type AsphereSensitivityMatrix struct {
	BaseMerit    float64   `yaml:"base_merit"`               // weighted RMS OPD without an asphere
	AsphereMerit float64   `yaml:"asphere_merit"`            // weighted RMS OPD with the fitted asphere applied
	Improvement  float64   `yaml:"improvement"`              // relative merit reduction (1 - asphere/base)
	DMeritDCoef  []float64 `yaml:"d_merit_d_coef,omitempty"` // per-coefficient ∂Merit/∂c_j
	// CalibratedScale is the embedded scale chosen by the measured-response
	// calibration (asphere.CalibrateScale): a quadratic estimate of the
	// merit-minimizing scale, clamped and verified by one re-trace (or an
	// explicit scale_probes scan). 0 when calibration is disabled or skipped.
	// CalibratedMerit / CalibratedImprovement are the verified merit at that
	// scale and its relative reduction (1 - calibrated/base, floored at 0).
	CalibratedScale       float64 `yaml:"calibrated_scale,omitempty"`
	CalibratedMerit       float64 `yaml:"calibrated_merit,omitempty"`
	CalibratedImprovement float64 `yaml:"calibrated_improvement,omitempty"`
}

// AsphereSurfaceScore is one candidate surface's ranking breakdown and fitted
// coefficients.
type AsphereSurfaceScore struct {
	SurfaceID              int                       `yaml:"surface_id"`
	Score                  float64                   `yaml:"score"`
	Coverage               float64                   `yaml:"coverage"`
	CommonEnergy           float64                   `yaml:"common_energy"`
	Conflict               float64                   `yaml:"conflict"`
	UniqueEnergy           float64                   `yaml:"unique_energy"`
	FitQuality             float64                   `yaml:"fit_quality"`
	ManufacturingPenalty   float64                   `yaml:"manufacturing_penalty"`
	SensitivityPenalty     float64                   `yaml:"sensitivity_penalty"`
	AsymResidual           float64                   `yaml:"asym_residual"`
	FieldConsistency       float64                   `yaml:"field_consistency"`
	AstigY0R2              float64                   `yaml:"astig_y0_r2,omitempty"`
	DefocusY0R2            float64                   `yaml:"defocus_y0_r2,omitempty"`
	Coefficients           AsphereCoeffs             `yaml:"coefficients,omitempty"`
	ScaledCoefficients     AsphereCoeffs             `yaml:"scaled_coefficients,omitempty"`
	CalibratedCoefficients AsphereCoeffs             `yaml:"calibrated_coefficients,omitempty"`
	Sensitivity            *AsphereSensitivityMatrix `yaml:"sensitivity,omitempty"`
	Validation             *AsphereValidation        `yaml:"validation,omitempty"`
	Warnings               []string                  `yaml:"warnings,omitempty"`
	// Focus channel (diagnostic-only in the first version). Populated when the
	// focus diagnostics are enabled; they report how a trial rotationally
	// symmetric asphere on this surface moves the per-field tangential and
	// sagittal best-focus planes.
	FocusMeanFitR2     float64 `yaml:"focus_mean_fit_r2,omitempty"`
	FocusTSFitR2       float64 `yaml:"focus_ts_fit_r2,omitempty"`
	BaseRMSMeanFocus   float64 `yaml:"base_rms_mean_focus_mm,omitempty"`
	TrialRMSMeanFocus  float64 `yaml:"trial_rms_mean_focus_mm,omitempty"`
	BaseRMSTSSplit     float64 `yaml:"base_rms_ts_split_mm,omitempty"`
	TrialRMSTSSplit    float64 `yaml:"trial_rms_ts_split_mm,omitempty"`
	FieldCurvatureGain float64 `yaml:"field_curvature_gain,omitempty"`
	AstigmatismGain    float64 `yaml:"astigmatism_gain,omitempty"`
	TangentialGain     float64 `yaml:"tangential_gain,omitempty"`
	SagittalGain       float64 `yaml:"sagittal_gain,omitempty"`
}

// AsphereOPDField is one field's mean OPD profile across a surface's polar
// rings. Ring i's OPD is the field's weight-mean OPD over the rays in that
// ring, referenced to the field's mean (piston removed, tilt/defocus per the
// asphere_candidate settings). RingRadius is the ring's mean |r| on the
// surface.
type AsphereOPDField struct {
	FieldID    int       `yaml:"field_id"`
	RingRadius []float64 `yaml:"ring_radius,omitempty"` // mean |r| per ring (mm)
	OPD        []float64 `yaml:"opd,omitempty"`         // mean OPD per ring (mm)
	TRadius    []float64 `yaml:"t_radius,omitempty"`    // mean tangential coordinate per bin (mm)
	OPDPlus    []float64 `yaml:"opd_plus,omitempty"`    // +s half-mean OPD per t bin (mm)
	OPDMinus   []float64 `yaml:"opd_minus,omitempty"`   // -s half-mean OPD per t bin (mm)
}

// AsphereFocusEntry is the best-focus position for one meridian (tangential or
// sagittal) of a (field, wavelength) pair.
type AsphereFocusEntry struct {
	BestZ        float64 `yaml:"best_z_mm"`
	RMSLineWidth float64 `yaml:"rms_line_width_mm"`
}

// AsphereTSFocus holds the tangential and sagittal best-focus entries.
type AsphereTSFocus struct {
	Tangential AsphereFocusEntry `yaml:"tangential"`
	Sagittal   AsphereFocusEntry `yaml:"sagittal"`
}

// AsphereDerivedFocus contains the combined tangential/sagittal quantities for
// one (field, wavelength).
type AsphereDerivedFocus struct {
	MeanFocusBase  float64 `yaml:"mean_focus_base_mm"`
	MeanFocusTrial float64 `yaml:"mean_focus_trial_mm"`
	TSSplitBase    float64 `yaml:"ts_split_base_mm"`
	TSSplitTrial   float64 `yaml:"ts_split_trial_mm"`
}

// AsphereFieldFocus is the per-field, per-wavelength T/S focus data.
type AsphereFieldFocus struct {
	FieldID    int                 `yaml:"field_id"`
	Wavelength float64             `yaml:"wavelength_nm"`
	Base       AsphereTSFocus      `yaml:"base"`
	Trial      AsphereTSFocus      `yaml:"trial"`
	Derived    AsphereDerivedFocus `yaml:"derived"`
}

// AsphereFocusSample is one ray's local focus residual on a candidate surface
// for the focus footprint map. HitX and HitY are the local intersection
// coordinates on that candidate surface; RMM remains the pupil radius.
type AsphereFocusSample struct {
	FieldID  int     `yaml:"field_id"`
	Trial    bool    `yaml:"trial,omitempty"`
	PupilX   float64 `yaml:"pupil_x"`
	PupilY   float64 `yaml:"pupil_y"`
	HitX     float64 `yaml:"hit_x_mm"`
	HitY     float64 `yaml:"hit_y_mm"`
	FanKind  string  `yaml:"fan_kind"`
	RMM      float64 `yaml:"r_mm"`
	Residual float64 `yaml:"residual_mm"`
	DeltaZ   float64 `yaml:"delta_z_mm"`
}

// AsphereRadialFit is the radial fit result for one target quantity on a
// candidate surface (wavefront OPD, mean focus, T-S split).
type AsphereRadialFit struct {
	Target       string    `yaml:"target"`
	Basis        []string  `yaml:"basis"`
	Coefficients []float64 `yaml:"coefficients"`
	R2           float64   `yaml:"r2"`
	RMS          float64   `yaml:"rms"`
}

// AsphereSurfaceSummary is the per-surface summary of both channels.
type AsphereSurfaceSummary struct {
	Wavefront AsphereWavefrontSummary `yaml:"wavefront"`
	Focus     AsphereFocusSummary     `yaml:"focus"`
}

// AsphereWavefrontSummary is the OPD channel summary for one surface.
type AsphereWavefrontSummary struct {
	RadialFitR2  float64 `yaml:"radial_fit_r2"`
	RadialFitRMS float64 `yaml:"radial_fit_rms_opd_mm"`
	Conflict     float64 `yaml:"conflict"`
	Asymmetry    float64 `yaml:"asymmetry"`
}

// AsphereFocusSummary is the focus channel summary for one surface.
type AsphereFocusSummary struct {
	MeanFitR2          float64 `yaml:"mean_fit_r2"`
	TSFitR2            float64 `yaml:"ts_fit_r2"`
	BaseRMSMeanFocus   float64 `yaml:"base_rms_mean_focus_mm"`
	TrialRMSMeanFocus  float64 `yaml:"trial_rms_mean_focus_mm"`
	BaseRMSTSSplit     float64 `yaml:"base_rms_ts_split_mm"`
	TrialRMSTSSplit    float64 `yaml:"trial_rms_ts_split_mm"`
	FieldCurvatureGain float64 `yaml:"field_curvature_gain"`
	AstigmatismGain    float64 `yaml:"astigmatism_gain"`
}

// AsphereSurfaceOutput carries the full diagnostic output for one candidate
// surface, including footprint maps and per-field focus data.
type AsphereSurfaceOutput struct {
	SurfaceID      int                   `yaml:"surface_id"`
	ApertureRadius float64               `yaml:"aperture_radius_mm"`
	Summary        AsphereSurfaceSummary `yaml:"summary"`
	RadialFits     []AsphereRadialFit    `yaml:"radial_fits,omitempty"`
	Samples        []AsphereFocusSample  `yaml:"samples,omitempty"`
	FieldFocus     []AsphereFieldFocus   `yaml:"field_focus,omitempty"`
}

// AsphereOPDProfile is the per-field OPD overlap data for one candidate
// surface: how each field's beam's wavefront error varies across the surface,
// and how much the fields' profiles overlap (the shared, aspherisable part).
type AsphereOPDProfile struct {
	SurfaceID int               `yaml:"surface_id"`
	MaxR      float64           `yaml:"max_r"` // footprint max radius (mm)
	Fields    []AsphereOPDField `yaml:"fields,omitempty"`
}

// AsphereCandidateResult is the `asphere` command's ranking output.
type AsphereCandidateResult struct {
	Rankings []AsphereSurfaceScore  `yaml:"rankings,omitempty"`
	Profiles []AsphereOPDProfile    `yaml:"opd_profiles,omitempty"`
	Surfaces []AsphereSurfaceOutput `yaml:"surfaces,omitempty"`
	Warnings []string               `yaml:"warnings,omitempty"`
}

// AsphereValidation is the Phase-4 short-DLS validation of one inserted
// asphere: the merit before insertion, after the short DLS solve, and the
// relative improvement. An empty DLS run (validation disabled) leaves the
// block nil.
type AsphereValidation struct {
	SurfaceID    int           `yaml:"surface_id"`
	BeforeMerit  float64       `yaml:"before_merit"`
	AfterMerit   float64       `yaml:"after_merit"`
	Improvement  float64       `yaml:"improvement"` // 1 - after/before
	Iterations   int           `yaml:"iterations"`
	Status       string        `yaml:"status,omitempty"`
	Coefficients AsphereCoeffs `yaml:"coefficients,omitempty"` // DLS-solved even-order coefficients
	Warnings     []string      `yaml:"warnings,omitempty"`
}

type Input struct {
	Metadata       *Metadata               `yaml:"metadata,omitempty"`
	GlassCatalog   *GlassCatalog           `yaml:"glass_catalog,omitempty"`
	CoatingCatalog *CoatingCatalog         `yaml:"coating_catalog,omitempty"`
	Configs        []Config                `yaml:"configs,omitempty"`
	Vignette       *VignetteConfig         `yaml:"vignette,omitempty"`
	Plot           *PlotConfig             `yaml:"plot,omitempty"`
	System         System                  `yaml:"-"`
	Optimization   *OptimizationConfig     `yaml:"optimization,omitempty"`
	Chief          *ChiefInput             `yaml:"chief,omitempty"`
	Rays           *RayInput               `yaml:"rays,omitempty"`
	TraceSingle    *RayTraceConfig         `yaml:"trace_single,omitempty"`
	Paraxial       *ParaxialInput          `yaml:"paraxial,omitempty"`
	Asphere        *AsphereCandidateConfig `yaml:"asphere_candidate,omitempty"`
	PSF            *PSFConfig              `yaml:"psf,omitempty"`
	Wavefront      *WavefrontConfig        `yaml:"wavefront,omitempty"`
	Scale          *ScaleConfig            `yaml:"scale,omitempty"`
}

type GridPoint struct {
	PupilX    float64  `yaml:"pupil_x"`
	PupilY    float64  `yaml:"pupil_y"`
	ImageX    *float64 `yaml:"image_x"`
	ImageY    *float64 `yaml:"image_y"`
	Intensity float64  `yaml:"intensity"`
	OPL       float64  `yaml:"opl,omitempty"`
	ErrorCode string   `yaml:"error_code,omitempty"`
	Origin    Vec3     `yaml:"-"`
	Direction Vec3     `yaml:"-"`
}

type SpotStats struct {
	Centroid   Vec3    `yaml:"centroid"`
	RMS_X      float64 `yaml:"rms_x"`
	RMS_Y      float64 `yaml:"rms_y"`
	RMS_R      float64 `yaml:"rms_r"`
	TotalRays  int     `yaml:"total_rays"`
	TracedRays int     `yaml:"traced_rays"`
	MissedRays int     `yaml:"missed_rays,omitempty"`
	MinX       float64 `yaml:"min_x"`
	MaxX       float64 `yaml:"max_x"`
	MinY       float64 `yaml:"min_y"`
	MaxY       float64 `yaml:"max_y"`
}

type FanPoint struct {
	PupilX float64         `yaml:"px,omitempty"`
	PupilY float64         `yaml:"py,omitempty"`
	EX     float64         `yaml:"ex,omitempty"`
	EY     float64         `yaml:"ey,omitempty"`
	Long   float64         `yaml:"long,omitempty"`
	Path   []SurfaceResult `yaml:"path,omitempty"`
}

type RotatedFan struct {
	AngleDeg float64    `yaml:"angle_deg"`
	Points   []FanPoint `yaml:"points"`
}

type RayFan struct {
	Meridional []FanPoint   `yaml:"meridional,omitempty"`
	Sagittal   []FanPoint   `yaml:"sagittal,omitempty"`
	Rotated    []RotatedFan `yaml:"rotated,omitempty"`
}

type RayFanConfig struct {
	Angles  []float64
	NumRays int
}

type WavelengthStats struct {
	Value     float64   `yaml:"value"`
	SpotStats SpotStats `yaml:"spot_stats"`
}

type ChiefRayResult struct {
	FieldAngle    float64           `yaml:"field_angle"`
	ChiefRay      Ray               `yaml:"chief_ray"`
	ImageHeight   Vec3              `yaml:"image_height"`
	EntrancePupil *Pupil            `yaml:"entrance_pupil,omitempty"`
	ExitPupil     *Pupil            `yaml:"exit_pupil,omitempty"`
	GridPoints    []GridPoint       `yaml:"grid_points,omitempty"`
	SpotStats     *SpotStats        `yaml:"spot_stats,omitempty"`
	RayFan        *RayFan           `yaml:"ray_fan,omitempty"`
	Wavelengths   []WavelengthStats `yaml:"wavelengths,omitempty"`
	// PupilProbe reports that the low-angle probe supplied the dynamic-pupil
	// aperture position (stop-free systems); PupilProbeZ is the aperture Z it
	// found. Both are informational only.
	PupilProbe  bool    `yaml:"pupil_probe,omitempty"`
	PupilProbeZ float64 `yaml:"pupil_probe_z,omitempty"`
}

type Pupil struct {
	Center Vec3    `yaml:"center"`
	Radius float64 `yaml:"radius"`
}

type ParaxialResult struct {
	ObjectSpaceIndex         float64       `yaml:"object_space_index"`
	ImageSpaceIndex          float64       `yaml:"image_space_index"`
	EntrancePupilDiameter    float64       `yaml:"entrance_pupil_diameter,omitempty"`
	ObjectConeAngle          float64       `yaml:"object_cone_angle,omitempty"`
	ObjectSpaceFNumber       float64       `yaml:"object_space_f_number,omitempty"`
	ObjectSpaceNA            float64       `yaml:"object_space_na,omitempty"`
	InfConjImageSpaceFNumber float64       `yaml:"inf_conj_image_space_f_number,omitempty"`
	InfConjImageSpaceNA      float64       `yaml:"inf_conj_image_space_na,omitempty"`
	ImageSpaceFNumber        float64       `yaml:"image_space_f_number,omitempty"`
	ImageSpaceNA             float64       `yaml:"image_space_na,omitempty"`
	EntrancePupilLocation    float64       `yaml:"entrance_pupil_location,omitempty"`
	FocalLength              float64       `yaml:"focal_length,omitempty"`
	Magnification            float64       `yaml:"magnification,omitempty"`
	Minification             float64       `yaml:"minification,omitempty"`
	ExitPupilLocation        float64       `yaml:"exit_pupil_location,omitempty"`
	ExitPupilDiameter        float64       `yaml:"exit_pupil_diameter,omitempty"`
	HalfAngleOfView          float64       `yaml:"half_angle_of_view,omitempty"`
	TotalTrack               float64       `yaml:"total_track"`
	FirstFocalLength         float64       `yaml:"first_focal_length,omitempty"`
	FirstNodalPoint          float64       `yaml:"first_nodal_point,omitempty"`
	FirstPrincipalFocus      float64       `yaml:"first_principal_focus,omitempty"`
	FirstPrincipalPoint      float64       `yaml:"first_principal_point,omitempty"`
	SecondFocalLength        float64       `yaml:"second_focal_length,omitempty"`
	SecondNodalPoint         float64       `yaml:"second_nodal_point,omitempty"`
	SecondPrincipalFocus     float64       `yaml:"second_principal_focus,omitempty"`
	SecondPrincipalPoint     float64       `yaml:"second_principal_point,omitempty"`
	ElementRoles             []ElementRole `yaml:"element_roles,omitempty"`
}

// ElementRole is the YAML-serializable mirror of the paraxial glass-role
// classification of one lens element (paraxial.GlassRoles). The types package
// cannot import paraxial (import cycle), so paraxial.Compute maps its internal
// role records onto this struct.
type ElementRole struct {
	SurfaceIDs []int   `yaml:"surface_ids,omitempty"`
	Phi        float64 `yaml:"phi"`
	Y          float64 `yaml:"y"`
	W          float64 `yaml:"w"`
	Role       string  `yaml:"role"` // dominant | compensating | neutral
	VTarget    float64 `yaml:"vd_target"`
	NDTarget   float64 `yaml:"nd_target"`
}

type ParaxialInput struct {
	ObjectHeight float64 `yaml:"object_height,omitempty"`
}

// ScaleConfig configures the `scale` subcommand (the `scale:` YAML section).
// EFL is the target effective focal length, the counterpart of the --efl flag
// (flag wins; the effective value is written back on output).
type ScaleConfig struct {
	EFL float64 `yaml:"efl,omitempty"`
}

type StopInfo struct {
	SurfaceID int     `yaml:"surface_id"`
	PhysicalZ float64 `yaml:"physical_z"`
	Diameter  float64 `yaml:"diameter"`
}

// VignetteConfig configures the `vignette` subcommand (the `vignette:` YAML
// section). All fields are optional; flags on the command line override them
// and the effective values are written back into the output section.
type VignetteConfig struct {
	Iterations   int     `yaml:"iterations,omitempty"`
	MinGlassPath float64 `yaml:"min_glass_path,omitempty"`
	MarginMM     float64 `yaml:"margin_mm,omitempty"`
}

// PlotConfig configures the `plot` subcommand (the `plot:` YAML section).
// All fields are optional; flags on the command line override them
// and the effective values are written back into the output section.
type PlotConfig struct {
	ElementColors   map[int]string `yaml:"element_colors,omitempty"`
	AsphereColors   map[int]string `yaml:"asphere_colors,omitempty"`
	AsphereColorAll string         `yaml:"asphere_color_all,omitempty"`
}

// VignettingField reports the per-field vignetting result for the `vignette`
// subcommand. Vignetting is the surviving fraction of the pupil grid;
// EntrancePupilZ / ExitPupilZ are the per-field dynamic pupils; BoundLower /
// BoundUpper are field 0's marginal-ray envelope at the field's entrance pupil
// plane; MarginalYLower / MarginalYUpper are this field's marginal-ray heights
// there.
type VignettingField struct {
	FieldIndex     int     `yaml:"field_index"`
	AngleDeg       float64 `yaml:"angle_deg"`
	Vignetting     float64 `yaml:"vignetting"`
	GridTotal      int     `yaml:"grid_total"`
	GridSurviving  int     `yaml:"grid_surviving"`
	EntrancePupilZ float64 `yaml:"entrance_pupil_z,omitempty"`
	ExitPupilZ     float64 `yaml:"exit_pupil_z,omitempty"`
	BoundLower     float64 `yaml:"bound_lower,omitempty"`
	BoundUpper     float64 `yaml:"bound_upper,omitempty"`
	MarginalYLower float64 `yaml:"marginal_y_lower,omitempty"`
	MarginalYUpper float64 `yaml:"marginal_y_upper,omitempty"`
}

// DiameterState records one surface's diameter before and after a `vignette`
// pass.
type DiameterState struct {
	SurfaceID int     `yaml:"surface_id"`
	Before    float64 `yaml:"before"`
	After     float64 `yaml:"after"`
}

// VignettingResult is the report emitted by the `vignette` subcommand.
type VignettingResult struct {
	Iterations   int               `yaml:"iterations"`
	MinGlassPath float64           `yaml:"min_glass_path"`
	StopSurface  int               `yaml:"stop_surface,omitempty"`
	Diameters    []DiameterState   `yaml:"diameters"`
	Fields       []VignettingField `yaml:"fields"`
}

// SpectralEntry is one point of a custom spectral power distribution
// (wavelength in nm, relative power). Used by psf.spectral_entries.
type SpectralEntry struct {
	Wavelength float64 `yaml:"wavelength"`
	Relative   float64 `yaml:"relative"`
}

// PSFMTFConfig configures the OTF/MTF computation derived from each PSF grid
// (the psf.mtf_config YAML section). A nil / empty config uses the defaults.
type PSFMTFConfig struct {
	// Frequencies are user-selected spatial frequencies (cycles/mm) at which
	// the OTF/MTF is reported under `evaluated`.
	Frequencies []float64 `yaml:"frequencies,omitempty"`
	// Thresholds are the MTF levels whose cut-off frequencies are reported
	// (default [0.50, 0.30, 0.10]).
	Thresholds []float64 `yaml:"thresholds,omitempty"`
	// MaxFrequency caps the reported curves in cycles/mm; 0 = the Nyquist
	// frequency of the image-plane grid.
	MaxFrequency float64 `yaml:"max_frequency,omitempty"`
	// FrequencyPoints is the number of samples along the reported curve; 0 =
	// the FFT grid size / 2.
	FrequencyPoints int `yaml:"frequency_points,omitempty"`
	// SpectralCurve selects the SPD for polychromatic MTF weighting:
	// "" (monochromatic), "D65", "FLAT". Independent of psf.spectral_curve.
	SpectralCurve string `yaml:"spectral_curve,omitempty"`
	// SpectralEntries overrides SpectralCurve with custom (nm, relative) points.
	SpectralEntries []SpectralEntry `yaml:"spectral_entries,omitempty"`
	// CombinationMethod selects how to combine wavelengths for MTF:
	// "intensity" (default) = incoherent sum of intensities -> MTF from combined PSF
	// "otf" = weighted average of monochromatic OTFs -> MTF from combined OTF
	CombinationMethod string `yaml:"combination_method,omitempty"`
}

// PSFMTFPoint is one OTF/MTF sample at a spatial frequency.
type PSFMTFPoint struct {
	Frequency float64 `yaml:"frequency"` // cycles/mm
	OTFReal   float64 `yaml:"otf_real"`
	OTFImag   float64 `yaml:"otf_imag"`
	MTF       float64 `yaml:"mtf"`
	PTF       float64 `yaml:"ptf,omitempty"` // phase in radians, about the PSF centroid
}

// PSFMTFCross is an MTF-level crossing: the frequency at which the MTF equals
// the given level.
type PSFMTFCross struct {
	MTF       float64 `yaml:"mtf"`
	Frequency float64 `yaml:"frequency"` // cycles/mm
}

// PSFMTFAxis holds the OTF/MTF data along one image-plane axis
// (sagittal = X, tangential = Y, the image-height direction).
type PSFMTFAxis struct {
	Curve      []PSFMTFPoint `yaml:"curve,omitempty"`
	Thresholds []PSFMTFCross `yaml:"thresholds,omitempty"`
	Evaluated  []PSFMTFPoint `yaml:"evaluated,omitempty"`
}

// WavelengthMTF holds per-wavelength MTF threshold crossings and evaluated
// points for polychromatic MTF results. Curve is omitted to keep output compact.
type WavelengthMTF struct {
	Wavelength      float64    `yaml:"wavelength"`       // mm
	SpectralWeight  float64    `yaml:"spectral_weight"`  // SPD weight × transmittance × Δλ
	Sagittal        PSFMTFAxis `yaml:"sagittal"`         // Thresholds, Evaluated only
	Tangential      PSFMTFAxis `yaml:"tangential"`       // Thresholds, Evaluated only
}

// PSFMTFSummary is the MTF/OTF summary of one PSF result.
type PSFMTFSummary struct {
	Sagittal           PSFMTFAxis     `yaml:"sagittal"`
	Tangential         PSFMTFAxis     `yaml:"tangential"`
	SpectralCurve      string         `yaml:"spectral_curve,omitempty"`
	CombinationMethod  string         `yaml:"combination_method,omitempty"`
	WavelengthMTFs     []WavelengthMTF `yaml:"wavelength_mtfs,omitempty"`
}

// PSFConfig configures the `psf` subcommand (the `psf:` YAML section).
// All fields are optional; flags on the command line override them.
type PSFConfig struct {
	ReferenceSurface int       `yaml:"reference_surface,omitempty"`
	GridSize         int       `yaml:"grid_size,omitempty"`
	HalfWidth        float64   `yaml:"half_width,omitempty"`
	NumRays          int       `yaml:"num_rays,omitempty"`
	Fields           []int     `yaml:"fields,omitempty"`
	Wavelengths      []float64 `yaml:"wavelengths,omitempty"`
	Polarization     string    `yaml:"polarization,omitempty"`
	// Workers bounds the Huygens-integration and wavefront-tracing
	// parallelism (0 = runtime.NumCPU()).
	Workers int `yaml:"huygens_workers,omitempty"`
	// SpectralCurve selects a polychromatic ("white") PSF computation:
	// "D65" (CIE standard illuminant, the default) or "FLAT". When set, each
	// field's monochromatic PSFs are combined with the SPD-weighted sum.
	SpectralCurve string `yaml:"spectral_curve,omitempty"`
	// SpectralEntries overrides SpectralCurve with a custom spectral power
	// distribution (wavelength nm, relative power).
	SpectralEntries []SpectralEntry `yaml:"spectral_entries,omitempty"`
	// BestFocus evaluates each field at its best-focus image plane (removes
	// field-curvature defocus before the Huygens integral).
	BestFocus bool `yaml:"best_focus,omitempty"`
	// GridType selects the entrance-pupil grid ("polar" default, "square",
	// "hex"); "" keeps the default.
	GridType GridType `yaml:"grid_type,omitempty"`
	// ConvergeCheck re-evaluates each result at a higher ray count to label
	// sampling convergence (Converged / strehl_rel_change in psf_results[]).
	// A nil pointer means "not specified" — the psf command then defaults to ON;
	// false explicitly disables. Ptr distinguishes "absent" from "set false".
	ConvergeCheck *bool `yaml:"converge_check,omitempty"`
	// ConvergeTol is the relative Strehl change threshold for convergence
	// (default 0.10); ignored when ConvergeCheck is false.
	ConvergeTol float64 `yaml:"converge_tol,omitempty"`
	// MTFCfg configures the OTF/MTF computation (defaults when absent).
	MTFCfg *PSFMTFConfig `yaml:"mtf_config,omitempty"`
}

// WavefrontConfig configures the `wavefront` subcommand (the `wavefront:`
// YAML section). All fields are optional; flags on the command line override
// them (CLI wins, effective values written back).
type WavefrontConfig struct {
	// ReferenceSurface is the surface on which the wavefront is sampled.
	// 0 (default) = the last optical surface.
	ReferenceSurface int `yaml:"reference_surface,omitempty"`
	// NumRays is the entrance-pupil grid ray count per field (default 400).
	NumRays int `yaml:"num_rays,omitempty"`
	// Fields selects which chief field indices to analyse (default: all).
	Fields []int `yaml:"fields,omitempty"`
	// Wavelengths in mm (default: chief wavelengths, else 587.56 nm).
	Wavelengths []float64 `yaml:"wavelengths,omitempty"`
	// Polarization input state: RCP (default) | LCP | X | Y | RCP+LCP.
	Polarization string `yaml:"polarization,omitempty"`
	// Workers bounds the per-field task parallelism (0 = runtime.NumCPU()).
	Workers int `yaml:"workers,omitempty"`
	// ZernikeMaxOrder is the highest Fringe index to fit (default 15).
	ZernikeMaxOrder int `yaml:"zernike_max_order,omitempty"`
	// MapGrid is the per-side resolution of the interpolated wavefront map
	// written by --csv (default 64).
	MapGrid int `yaml:"map_grid,omitempty"`
	// BestFocus configures the weighted best-image-plane shift.
	BestFocus *WavefrontBestFocusConfig `yaml:"best_focus,omitempty"`
}

// WavefrontBestFocusConfig is the best-focus sub-section of the wavefront:
// section. When enabled, the best-fit-sphere center Z per field is combined
// (weight_type: uniform | custom) into a single image-plane shift that is
// applied to the output configs' image-plane decenter.
type WavefrontBestFocusConfig struct {
	Enabled bool `yaml:"enabled,omitempty"`
	// WeightType is "uniform" (default) or "custom".
	WeightType string `yaml:"weight_type,omitempty"`
	// CustomWeights is used when WeightType == "custom". It must match the
	// number of analysed fields.
	CustomWeights []float64 `yaml:"custom_weights,omitempty"`
	// OutputShiftedLens writes a standalone shifted-lens document to FILE.
	OutputShiftedLens string `yaml:"output_shifted_lens,omitempty"`
}

// PolarizationLabel identifies an input polarization state in PSF output.
type PolarizationLabel string

const (
	PolRCP    PolarizationLabel = "RCP"
	PolLCP    PolarizationLabel = "LCP"
	PolX      PolarizationLabel = "X"
	PolY      PolarizationLabel = "Y"
	PolRCPLCP PolarizationLabel = "RCP+LCP"
)

// PSFResult is the per-field/wavelength/polarization PSF summary carried in
// the pipeline YAML. The full intensity grid is written to --yaml/--csv files
// (see PSFResult.OutputFile) to keep the pipeline stream small.
type PSFResult struct {
	FieldIndex        int     `yaml:"field_index"`
	FieldAngle        float64 `yaml:"field_angle"`
	Wavelength        float64 `yaml:"wavelength,omitempty"`
	Polarization      string  `yaml:"polarization"`
	StrehlRatio       float64 `yaml:"strehl_ratio"`
	FWHMX             float64 `yaml:"fwhm_x"`
	FWHMY             float64 `yaml:"fwhm_y"`
	CentroidX         float64 `yaml:"centroid_x"`
	CentroidY         float64 `yaml:"centroid_y"`
	PeakValue         float64 `yaml:"peak_value"`
	PeakX             float64 `yaml:"peak_x"`
	PeakY             float64 `yaml:"peak_y"`
	EncircledEnergy50 float64 `yaml:"encircled_energy_50"`
	AiryRadius        float64 `yaml:"airy_radius"`
	GridSize          int     `yaml:"grid_size"`
	Resolution        float64 `yaml:"resolution"`
	TotalRays         int     `yaml:"total_rays"`
	ValidRays         int     `yaml:"valid_rays"`
	Vignetted         int     `yaml:"vignetted,omitempty"`
	OutputFile        string  `yaml:"output_file,omitempty"`
	// BestFocusShift is the applied image-plane shift in mm; 0 = fixed plane.
	BestFocusShift float64 `yaml:"best_focus_shift_mm,omitempty"`
	// Sampling-convergence report (present when psf.converge_check is on):
	// Converged is whether the Strehl was stable when re-evaluated at the higher
	// ray count CheckRays; StrehlRelChange is the relative Strehl change. The
	// pointer keeps `converged: false` visible in the output (omitempty would
	// hide it) while remaining absent when the check is disabled.
	Converged       *bool   `yaml:"converged,omitempty"`
	StrehlRelChange float64 `yaml:"strehl_rel_change,omitempty"`
	CheckRays       int     `yaml:"check_rays,omitempty"`
	// SpectralCurve is set for polychromatic results (wavelength is omitted).
	SpectralCurve string `yaml:"spectral_curve,omitempty"`
	// MTF is the OTF/MTF summary (thresholds, and evaluated frequencies when
	// configured). Full curves go to the --yaml file.
	MTF *PSFMTFSummary `yaml:"mtf,omitempty"`
}

// WavefrontResult is the per-run wavefront analysis carried in the pipeline
// YAML. It holds per-field paraboloid / best-fit-sphere / stabilized Zernike
// summaries (coefficients only — wavefront maps go to --yaml/--csv files) and,
// when best focus is enabled, the weighted image-plane shift.
type WavefrontResult struct {
	// Fields is one entry per analysed (field, wavelength, polarization).
	Fields []WavefrontFieldResult `yaml:"fields"`
	// BestFocus is present when the best-focus shift was computed.
	BestFocus *WavefrontBestFocus `yaml:"best_focus,omitempty"`
}

// WavefrontFieldResult is the wavefront analysis of one (field, wavelength,
// polarization): the always-computed paraboloid, the best-fit sphere seeded
// from it, the stabilized Zernike decomposition of the residual, and summary
// statistics. All OPD/RMS values are in mm.
type WavefrontFieldResult struct {
	FieldIndex   int     `yaml:"field_index"`
	FieldAngle   float64 `yaml:"field_angle"`
	Wavelength   float64 `yaml:"wavelength"`
	Polarization string  `yaml:"polarization"`
	// Paraboloid is the least-squares quadratic fit
	// ax² + by² + cxy + dx + ey + f to the sampled OPD.
	Paraboloid WavefrontParaboloid `yaml:"paraboloid"`
	// Sphere is the best-fit sphere (radius + center), seeded analytically
	// from the paraboloid.
	Sphere WavefrontSphere `yaml:"best_fit_sphere"`
	// Zernike is the Fringe decomposition of the residual after removing the
	// paraboloid + best-fit-sphere low-order terms.
	Zernike    WavefrontZernike    `yaml:"zernike"`
	Statistics WavefrontStatistics `yaml:"statistics"`
	Samples    WavefrontSamples    `yaml:"samples"`
	// OutputFile references the full structured data written by --yaml.
	OutputFile string `yaml:"output_file,omitempty"`
}

// WavefrontParaboloid holds the paraboloid fit coefficients (in mm per mm² /
// mm / mm as appropriate) and the derived low-order aberration magnitudes.
type WavefrontParaboloid struct {
	X2          float64 `yaml:"x2"`
	Y2          float64 `yaml:"y2"`
	XY          float64 `yaml:"xy"`
	X           float64 `yaml:"x"`
	Y           float64 `yaml:"y"`
	Constant    float64 `yaml:"constant"`
	Defocus     float64 `yaml:"defocus"`
	Astigmatism float64 `yaml:"astigmatism"`
	Tilt        float64 `yaml:"tilt"`
	// RMSResidual is the RMS of OPD minus the paraboloid, in mm.
	RMSResidual float64 `yaml:"rms_residual"`
}

// WavefrontSphere is the best-fit sphere to the sampled OPD, in the reference
// surface's local frame (vertex at the origin).
type WavefrontSphere struct {
	Radius float64 `yaml:"radius"`
	Center Vec3    `yaml:"center"`
	// RMSResidual is the RMS of OPD minus the sphere, in mm.
	RMSResidual float64 `yaml:"rms_residual"`
}

// WavefrontZernike is a Fringe-Zernike decomposition of the wavefront
// residual (low-order paraboloid/sphere terms removed).
type WavefrontZernike struct {
	Basis        string                 `yaml:"basis"`
	MaxOrder     int                    `yaml:"max_order"`
	RemovedTerms []int                  `yaml:"removed_terms"`
	Terms        []WavefrontZernikeTerm `yaml:"terms"`
	// RMSResidual is the RMS of the fitted residual, in mm.
	RMSResidual float64 `yaml:"rms_residual"`
}

// WavefrontZernikeTerm is one Fringe Zernike term. The coefficient is in the
// Zemax convention (peak value on the unit circle = coefficient).
type WavefrontZernikeTerm struct {
	Index       int     `yaml:"index"`
	Name        string  `yaml:"name"`
	Coefficient float64 `yaml:"coefficient"`
}

// WavefrontStatistics summarizes the wavefront residual after best-fit-sphere
// removal.
type WavefrontStatistics struct {
	// RMS is the residual RMS OPD in mm.
	RMS float64 `yaml:"rms"`
	// PV is the residual peak-to-valley OPD in mm.
	PV float64 `yaml:"pv"`
	// Strehl is the exact peak-ratio Strehl |⟨e^{i(2π/λ)W}⟩|² of the residual
	// wavefront W: the pupil-area-weighted coherent average over the samples.
	// Unlike the Marechal approximation exp(-(2πσ/λ)²) — valid only for
	// σ ≲ 0.2λ, beyond which it collapses towards 0 — the exact average stays
	// meaningful for highly aberrated fields and matches psf's peak-ratio
	// Strehl at best focus.
	Strehl float64 `yaml:"strehl"`
}

// WavefrontSamples counts the pupil grid rays by outcome.
type WavefrontSamples struct {
	Total  int `yaml:"total"`
	Valid  int `yaml:"valid"`
	Missed int `yaml:"missed"`
}

// WavefrontBestFocus is the weighted best-image-plane shift computed from the
// per-field best-fit-sphere centers, plus the shift applied to the output
// configs' image plane.
type WavefrontBestFocus struct {
	WeightType      string                   `yaml:"weight_type"`
	PerField        []WavefrontFocusPerField `yaml:"per_field"`
	WeightedAverage WavefrontFocusAverage    `yaml:"weighted_average"`
	ShiftedLens     WavefrontShiftedLens     `yaml:"shifted_lens"`
}

// WavefrontFocusPerField is one field's contribution to the best-focus shift.
type WavefrontFocusPerField struct {
	FieldIndex       int     `yaml:"field_index"`
	ShiftWavelengths float64 `yaml:"shift_wavelengths"`
	ShiftMM          float64 `yaml:"shift_mm"`
	Weight           float64 `yaml:"weight"`
}

// WavefrontFocusAverage is the weighted average of the per-field shifts.
type WavefrontFocusAverage struct {
	ShiftWavelengths float64 `yaml:"shift_wavelengths"`
	ShiftMM          float64 `yaml:"shift_mm"`
}

// WavefrontShiftedLens records the image-plane shift and the (optional)
// shifted-lens file written by --output-shifted-lens.
type WavefrontShiftedLens struct {
	ShiftMM    float64 `yaml:"shift_mm"`
	OutputFile string  `yaml:"output_file,omitempty"`
}

type Output struct {
	Input            `yaml:",inline"`
	ChiefRays        []ChiefRayResult        `yaml:"chief_rays,omitempty"`
	Results          []RayResult             `yaml:"results,omitempty"`
	ParaxialResult   *ParaxialResult         `yaml:"paraxial_result,omitempty"`
	OptResults       *OptimizationResult     `yaml:"opt_results,omitempty"`
	EscapeResult     *EscapeResult           `yaml:"escape_result,omitempty"`
	Vignetting       *VignettingResult       `yaml:"vignetting_result,omitempty"`
	AsphereResult    *AsphereCandidateResult `yaml:"asphere_candidate_result,omitempty"`
	PsfResults       []PSFResult             `yaml:"psf_results,omitempty"`
	WavefrontResults *WavefrontResult        `yaml:"wavefront_result,omitempty"`
	Stop             *StopInfo               `yaml:"stop,omitempty"`
}

type TMMInput struct {
	GlassCatalog *GlassCatalog  `yaml:"glass_catalog,omitempty"`
	NAir         float64        `yaml:"n_air"`
	NSub         float64        `yaml:"n_substrate"`
	Layers       []CoatingLayer `yaml:"layers"`
	Lambda       float64        `yaml:"lambda"`
	ThetaDeg     float64        `yaml:"theta_deg"`
}

type TMMOutput struct {
	Input TMMInput `yaml:"input"`
	Rs    float64  `yaml:"rs"`
	Ts    float64  `yaml:"ts"`
	Rp    float64  `yaml:"rp"`
	Tp    float64  `yaml:"tp"`
}
