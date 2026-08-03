package types

import (
	"fmt"
	"math"

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

type DecenterStep struct {
	Shift   Vec3 `yaml:"shift"`
	Tilt    Vec3 `yaml:"tilt"`
	Reflect bool `yaml:"reflect,omitempty"`
}

type Surface struct {
	ID           int            `yaml:"id"`
	Type         SurfaceType    `yaml:"type"`
	Curvature    float64        `yaml:"curvature,omitempty"`
	Conic        float64        `yaml:"conic"`
	Thickness    float64        `yaml:"thickness"`
	Material     string         `yaml:"material"`
	Diameter     float64        `yaml:"diameter,omitempty"`
	Coefficients []float64      `yaml:"coefficients,omitempty"`
	NormRadius   float64        `yaml:"norm_radius,omitempty"`
	Decenter     []DecenterStep `yaml:"decenter,omitempty"`
	Coating      string         `yaml:"coating,omitempty"`
	Role         string         `yaml:"role,omitempty"`
	AutoAperture bool           `yaml:"auto_aperture,omitempty"`
	MinGlassPath float64        `yaml:"min_glass_path,omitempty"`
	MaxGlassPath float64        `yaml:"max_glass_path,omitempty"`

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

func (s Surface) Reflects() bool {
	for _, d := range s.Decenter {
		if d.Reflect {
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
	ID                 string             `yaml:"id"`
	Wavelength         float64            `yaml:"wavelength"`
	Initial            RayState           `yaml:"initial"`
	Aim                *Vec3              `yaml:"aim,omitempty"`
	PassThrough        *PassThroughTarget `yaml:"pass_through,omitempty"`
	Path               []int              `yaml:"path"`
	Jones              JonesVector        `yaml:"-"`
	SkipGlassPathCheck bool               `yaml:"-"`
	SkipApertureCheck  bool               `yaml:"-"`
}

type SurfaceResult struct {
	SurfaceID   int             `yaml:"surface_id"`
	Position    Vec3            `yaml:"position"`
	Direction   Vec3            `yaml:"direction"`
	Interaction InteractionType `yaml:"interaction"`
	Thickness   float64         `yaml:"thickness"`
	OPL         float64         `yaml:"opl"`
	Jones       JonesVector     `yaml:"jones"`
	IntensityS  float64         `yaml:"intensity_s"`
	IntensityP  float64         `yaml:"intensity_p"`
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
	Directory string   `yaml:"directory,omitempty"`
	Files     []string `yaml:"files,omitempty"`
	Entries   []Glass  `yaml:"entries,omitempty"`
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
}

type ChiefInput struct {
	FieldAngles      []float64          `yaml:"field_angles,omitempty"`
	Fields           []FieldDef         `yaml:"fields,omitempty"`
	ReferenceSurface int                `yaml:"reference_surface"`
	StopSurface      int                `yaml:"stop_surface"`
	NumRays          int                `yaml:"num_rays"`
	GridType         GridType           `yaml:"grid_type,omitempty"`
	DumpMap          bool               `yaml:"dump_map,omitempty"`
	PassThrough      *PassThroughTarget `yaml:"pass_through,omitempty"`
	Wavelengths      []float64          `yaml:"wavelengths,omitempty"`
}

type RayInput struct {
	Polarization JonesVector `yaml:"polarization,omitempty"`
	Rays         []Ray       `yaml:"rays"`
}

type FieldItem struct {
	ID          int     `yaml:"id"`
	AngleDeg    float64 `yaml:"angle_deg"`
	ImageHeight float64 `yaml:"image_height,omitempty"`
	Weight      float64 `yaml:"weight"`
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
	SurfaceSet  []int   `yaml:"surface_set"`
	Weight      float64 `yaml:"weight"`
}

type MeritFunction struct {
	Type  string      `yaml:"type"`
	Terms []MeritTerm `yaml:"terms"`
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

type OptimizationConfig struct {
	Method          string                 `yaml:"method"`
	Aggregate       string                 `yaml:"aggregate,omitempty"`
	Mu              float64                `yaml:"mu,omitempty"`
	MaxIter         int                    `yaml:"max_iter,omitempty"`
	Tol             float64                `yaml:"tol,omitempty"`
	Epsilon         float64                `yaml:"epsilon,omitempty"`
	NumRays         int                    `yaml:"num_rays,omitempty"`
	MuConMax        float64                `yaml:"mu_con_max,omitempty"`
	ApertureMargin  float64                `yaml:"aperture_margin,omitempty"`
	JacobianWorkers int                    `yaml:"jacobian_workers,omitempty"`
	Variables       []OptimizationVariable `yaml:"variables,omitempty"`
	SharedVariables []SharedVariable       `yaml:"shared_variables,omitempty"`
	LocalVariables  []LocalVariableDef     `yaml:"local_variables,omitempty"`
	Constraints     []ConstraintOperand    `yaml:"constraints,omitempty"`
	GlassHull       *GlassHullConfig       `yaml:"glass_hull,omitempty"`
	Escape          *EscapeConfig          `yaml:"escape,omitempty"`
}

// EscapeConfig configures the escape-function global optimisation loop
// (Ishiki-Ono style local-minimum escape for DLS).
type EscapeConfig struct {
	MaxCycles         int                `yaml:"max_cycles,omitempty"`
	EscapeWorkers     int                `yaml:"escape_workers,omitempty"`
	MaxSeconds        float64            `yaml:"max_seconds,omitempty"`
	DistanceThreshold float64            `yaml:"distance_threshold,omitempty"`
	HInitial          float64            `yaml:"h_initial,omitempty"`
	WInitial          float64            `yaml:"w_initial,omitempty"`
	HMult             float64            `yaml:"h_mult,omitempty"`
	WMult             float64            `yaml:"w_mult,omitempty"`
	VariableWeights   map[string]float64 `yaml:"variable_weights,omitempty"`
}

type MeritTermResult struct {
	Kind       string  `yaml:"kind"`
	Field      int     `yaml:"field"`
	Wavelength float64 `yaml:"wavelength"`
	SurfaceSet []int   `yaml:"surface_set"`
	Weight     float64 `yaml:"weight"`
	Before     float64 `yaml:"before"`
	After      float64 `yaml:"after"`
}

type MeritBeforeAfter struct {
	Before      float64 `yaml:"before"`
	After       float64 `yaml:"after"`
	Improvement float64 `yaml:"improvement,omitempty"`
	Ratio       float64 `yaml:"ratio,omitempty"`
}

type OptimizationResult struct {
	TotalMerit        *MeritBeforeAfter       `yaml:"total_merit"`
	ConstraintPenalty *MeritBeforeAfter       `yaml:"constraint_penalty,omitempty"`
	Status            string                  `yaml:"status"`
	Iterations        int                     `yaml:"iterations"`
	Reason            string                  `yaml:"reason,omitempty"`
	Constraints       []ConstraintMeasurement `yaml:"constraints,omitempty"`
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
	HInitial          float64            `yaml:"h_initial"`
	WInitial          float64            `yaml:"w_initial"`
	HMult             float64            `yaml:"h_mult"`
	WMult             float64            `yaml:"w_mult"`
	DistanceThreshold float64            `yaml:"distance_threshold"`
	MaxCycles         int                `yaml:"max_cycles"`
	EscapeWorkers     int                `yaml:"escape_workers,omitempty"`
	MaxSeconds        float64            `yaml:"max_seconds,omitempty"`
	VariableWeights   map[string]float64 `yaml:"variable_weights,omitempty"`
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

type Provenance struct {
	OptimizedFrom    string `yaml:"optimized_from,omitempty"`
	OptimizerVersion string `yaml:"optimizer_version,omitempty"`
}

type Input struct {
	GlassCatalog   *GlassCatalog       `yaml:"glass_catalog,omitempty"`
	CoatingCatalog *CoatingCatalog     `yaml:"coating_catalog,omitempty"`
	Version        int                 `yaml:"version,omitempty"`
	System         System              `yaml:"-"`
	Optimization   *OptimizationConfig `yaml:"optimization,omitempty"`
	Configs        []Config            `yaml:"configs,omitempty"`
	Chief          *ChiefInput         `yaml:"chief,omitempty"`
	Rays           *RayInput           `yaml:"rays,omitempty"`
	Paraxial       *ParaxialInput      `yaml:"paraxial,omitempty"`
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
	EntrancePupil Pupil             `yaml:"entrance_pupil"`
	GridPoints    []GridPoint       `yaml:"grid_points,omitempty"`
	SpotStats     *SpotStats        `yaml:"spot_stats,omitempty"`
	RayFan        *RayFan           `yaml:"ray_fan,omitempty"`
	Wavelengths   []WavelengthStats `yaml:"wavelengths,omitempty"`
}

type Pupil struct {
	Center Vec3    `yaml:"center"`
	Radius float64 `yaml:"radius"`
}

type ParaxialResult struct {
	ObjectSpaceIndex         float64 `yaml:"object_space_index"`
	ImageSpaceIndex          float64 `yaml:"image_space_index"`
	EntrancePupilDiameter    float64 `yaml:"entrance_pupil_diameter,omitempty"`
	ObjectConeAngle          float64 `yaml:"object_cone_angle,omitempty"`
	ObjectSpaceFNumber       float64 `yaml:"object_space_f_number,omitempty"`
	ObjectSpaceNA            float64 `yaml:"object_space_na,omitempty"`
	InfConjImageSpaceFNumber float64 `yaml:"inf_conj_image_space_f_number,omitempty"`
	InfConjImageSpaceNA      float64 `yaml:"inf_conj_image_space_na,omitempty"`
	ImageSpaceFNumber        float64 `yaml:"image_space_f_number,omitempty"`
	ImageSpaceNA             float64 `yaml:"image_space_na,omitempty"`
	EntrancePupilLocation    float64 `yaml:"entrance_pupil_location,omitempty"`
	FocalLength              float64 `yaml:"focal_length,omitempty"`
	Magnification            float64 `yaml:"magnification,omitempty"`
	Minification             float64 `yaml:"minification,omitempty"`
	ExitPupilLocation        float64 `yaml:"exit_pupil_location,omitempty"`
	ExitPupilDiameter        float64 `yaml:"exit_pupil_diameter,omitempty"`
	HalfAngleOfView          float64 `yaml:"half_angle_of_view,omitempty"`
	TotalTrack               float64 `yaml:"total_track"`
	FirstFocalLength         float64 `yaml:"first_focal_length,omitempty"`
	FirstNodalPoint          float64 `yaml:"first_nodal_point,omitempty"`
	FirstPrincipalFocus      float64 `yaml:"first_principal_focus,omitempty"`
	FirstPrincipalPoint      float64 `yaml:"first_principal_point,omitempty"`
	SecondFocalLength        float64 `yaml:"second_focal_length,omitempty"`
	SecondNodalPoint         float64 `yaml:"second_nodal_point,omitempty"`
	SecondPrincipalFocus     float64 `yaml:"second_principal_focus,omitempty"`
	SecondPrincipalPoint     float64 `yaml:"second_principal_point,omitempty"`
}

type ParaxialInput struct {
	ObjectHeight float64 `yaml:"object_height,omitempty"`
}

type StopInfo struct {
	SurfaceID int     `yaml:"surface_id"`
	PhysicalZ float64 `yaml:"physical_z"`
	Diameter  float64 `yaml:"diameter"`
}

type Output struct {
	Input          `yaml:",inline"`
	ChiefRays      []ChiefRayResult    `yaml:"chief_rays,omitempty"`
	Results        []RayResult         `yaml:"results,omitempty"`
	ParaxialResult *ParaxialResult     `yaml:"paraxial_result,omitempty"`
	OptResults     *OptimizationResult `yaml:"opt_results,omitempty"`
	EscapeResult   *EscapeResult       `yaml:"escape_result,omitempty"`
	Provenance     *Provenance         `yaml:"provenance,omitempty"`
	Stop           *StopInfo           `yaml:"stop,omitempty"`
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
