package types

import "math"

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

type DispersionFormula string

const (
	Sellmeier1 DispersionFormula = "sellmeier_1"
	Constant   DispersionFormula = "constant"
)

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
	Shift Vec3  `yaml:"shift"`
	Tilt  Vec3  `yaml:"tilt"`
}

type Surface struct {
	ID             int             `yaml:"id"`
	Type           SurfaceType    `yaml:"type"`
	Radius         float64        `yaml:"radius"`
	Conic          float64        `yaml:"conic"`
	Thickness      float64        `yaml:"thickness"`
	Material       string         `yaml:"material"`
	Diameter       float64        `yaml:"diameter,omitempty"`
	Coefficients   []float64      `yaml:"coefficients,omitempty"`
	NormRadius     float64        `yaml:"norm_radius,omitempty"`
	Decenter       []DecenterStep `yaml:"decenter,omitempty"`
	Coating        string         `yaml:"coating,omitempty"`

	LocalToGlobal  Mat4 `yaml:"-"`
	GlobalToLocal  Mat4 `yaml:"-"`
	ParaxialRadius float64 `yaml:"-"`
}

type RayState struct {
	Origin    Vec3 `yaml:"origin"`
	Direction Vec3 `yaml:"direction"`
}

type PassThroughTarget struct {
	Surface    int     `yaml:"surface"`
	Coordinate Vec3    `yaml:"coordinate"`
	Variable   string  `yaml:"variable,omitempty"` // "direction" (default) or "origin"
}

type Ray struct {
	ID          string             `yaml:"id"`
	Wavelength  float64            `yaml:"wavelength"`
	Initial     RayState           `yaml:"initial"`
	Aim         *Vec3              `yaml:"aim,omitempty"`
	PassThrough *PassThroughTarget `yaml:"pass_through,omitempty"`
	Path        []int              `yaml:"path"`
	Jones       JonesVector        `yaml:"-"`
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
	Surfaces   []SurfaceResult `yaml:"surfaces"`
	OPLTotal   float64         `yaml:"opl_total"`
	IntensityS float64         `yaml:"intensity_s"`
	IntensityP float64         `yaml:"intensity_p"`
}

type RefractiveIndexEntry struct {
	Wavelength float64 `yaml:"wavelength"`
	Value      float64 `yaml:"value"`
}

type Glass struct {
	Name              string                 `yaml:"name"`
	Manufacturer      string                 `yaml:"manufacturer,omitempty"`
	DispersionFormula DispersionFormula      `yaml:"dispersion_formula,omitempty"`
	ND                float64                `yaml:"nd,omitempty"`
	VD                float64                `yaml:"vd,omitempty"`
	Coefficients      []float64              `yaml:"coefficients,omitempty"`
	WavelengthMin     float64                `yaml:"wavelength_range_min,omitempty"`
	WavelengthMax     float64                `yaml:"wavelength_range_max,omitempty"`
	Aliases           []string               `yaml:"aliases,omitempty"`
	RefractiveIndices []RefractiveIndexEntry `yaml:"refractive_indices,omitempty"`
}

type GlassCatalog struct {
	Files   []string `yaml:"files,omitempty"`
	Entries []Glass  `yaml:"entries,omitempty"`
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
	Surfaces []Surface `yaml:"surfaces"`
}

type FieldDef struct {
	Angle       float64   `yaml:"angle,omitempty"`
	ImageHeight float64   `yaml:"image_height,omitempty"` // image height (mm) at reference surface; searched via field angle
	Height      float64   `yaml:"height,omitempty"`       // object height (mm), for finite-conjugate
	ObjectZ     float64   `yaml:"object_z,omitempty"`     // object plane Z (mm), default -1000
	Direction   []float64 `yaml:"direction,omitempty"`    // [dx, dy]; default [0, 1]
}

type ChiefInput struct {
	FieldAngles      []float64  `yaml:"field_angles,omitempty"`
	Fields           []FieldDef `yaml:"fields,omitempty"`
	ReferenceSurface int        `yaml:"reference_surface"`
	NumRays          int        `yaml:"num_rays"`
	GridType         GridType   `yaml:"grid_type,omitempty"`
	DumpMap          bool       `yaml:"dump_map,omitempty"`
}

type RayInput struct {
	Polarization JonesVector `yaml:"polarization,omitempty"`
	Rays         []Ray       `yaml:"rays"`
}

type Input struct {
	GlassCatalog   *GlassCatalog   `yaml:"glass_catalog,omitempty"`
	CoatingCatalog *CoatingCatalog `yaml:"coating_catalog,omitempty"`
	System         System          `yaml:"system"`
	Chief          *ChiefInput     `yaml:"chief,omitempty"`
	Rays           *RayInput       `yaml:"rays,omitempty"`
	Paraxial       *ParaxialInput  `yaml:"paraxial,omitempty"`
}

type GridPoint struct {
	PupilX    float64  `yaml:"pupil_x"`
	PupilY    float64  `yaml:"pupil_y"`
	ImageX    *float64 `yaml:"image_x"`
	ImageY    *float64 `yaml:"image_y"`
	Intensity float64  `yaml:"intensity"`
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

type ChiefRayResult struct {
	FieldAngle    float64     `yaml:"field_angle"`
	ChiefRay      Ray         `yaml:"chief_ray"`
	ImageHeight   Vec3        `yaml:"image_height"`
	EntrancePupil Pupil       `yaml:"entrance_pupil"`
	GridPoints    []GridPoint `yaml:"grid_points,omitempty"`
	SpotStats     *SpotStats  `yaml:"spot_stats,omitempty"`
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

type Output struct {
	Input          `yaml:",inline"`
	ChiefRays      []ChiefRayResult `yaml:"chief_rays,omitempty"`
	Results        []RayResult      `yaml:"results,omitempty"`
	ParaxialResult *ParaxialResult  `yaml:"paraxial_result,omitempty"`
}

type TMMInput struct {
	GlassCatalog *GlassCatalog `yaml:"glass_catalog,omitempty"`
	NAir         float64       `yaml:"n_air"`
	NSub         float64       `yaml:"n_substrate"`
	Layers       []CoatingLayer `yaml:"layers"`
	Lambda       float64       `yaml:"lambda"`
	ThetaDeg     float64       `yaml:"theta_deg"`
}

type TMMOutput struct {
	Input TMMInput `yaml:"input"`
	Rs    float64  `yaml:"rs"`
	Ts    float64  `yaml:"ts"`
	Rp    float64  `yaml:"rp"`
	Tp    float64  `yaml:"tp"`
}
