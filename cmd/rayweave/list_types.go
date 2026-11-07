package main

import (
	"github.com/hiroki/rayweaver/internal/types"
)

// SurfaceListRow is one row of the `list surfaces` table. Radius is nil for a
// flat surface (infinite radius) and Curvature is set instead under
// --curvature; the pointers keep flat surfaces visible as null/empty rather
// than a misleading 0. AutoAperture is set only when --auto-aperture is given
// (nil = field absent from the structured output).
type SurfaceListRow struct {
	ID           int      `json:"id" yaml:"id"`
	Type         string   `json:"type" yaml:"type"`
	Radius       *float64 `json:"radius,omitempty" yaml:"radius,omitempty"`
	Curvature    *float64 `json:"curvature,omitempty" yaml:"curvature,omitempty"`
	Thickness    float64  `json:"thickness" yaml:"thickness"`
	Material     string   `json:"material" yaml:"material"`
	Diameter     float64  `json:"diameter" yaml:"diameter"`
	AutoAperture *bool    `json:"auto_aperture,omitempty" yaml:"auto_aperture,omitempty"`
}

// AsphereCoefRow is one row of the separate Asphere Coefficients table: the
// conic constant and even-order polynomial coefficients (a4..a12,
// coefficients[0..4]) of an aspheric surface. Nil pointers mean the term is
// not present on that surface. Fixed fields keep the YAML/JSON key order a4 <
// a6 < ... (a map would sort "a10"/"a12" ahead of "a4").
type AsphereCoefRow struct {
	ID    int      `json:"id" yaml:"id"`
	Type  string   `json:"type" yaml:"type"`
	Conic float64  `json:"conic" yaml:"conic"`
	A4    *float64 `json:"a4,omitempty" yaml:"a4,omitempty"`
	A6    *float64 `json:"a6,omitempty" yaml:"a6,omitempty"`
	A8    *float64 `json:"a8,omitempty" yaml:"a8,omitempty"`
	A10   *float64 `json:"a10,omitempty" yaml:"a10,omitempty"`
	A12   *float64 `json:"a12,omitempty" yaml:"a12,omitempty"`
}

// surfacesListOutput is the structured (yaml/json) shape of `list surfaces`:
// the surface table plus, only when present, asphere coefficients and
// cross-config thickness differences.
type surfacesListOutput struct {
	Surfaces            []SurfaceListRow   `json:"surfaces" yaml:"surfaces"`
	AsphereCoefficients []AsphereCoefRow   `json:"asphere_coefficients,omitempty" yaml:"asphere_coefficients,omitempty"`
	ThicknessDiffs      []ThicknessDiffRow `json:"thickness_differences,omitempty" yaml:"thickness_differences,omitempty"`
}

// RaySummaryRow is one row of the `list rays` summary table. IntensityS/
// IntensityP are the per-surface intensity of the last surface (as reported by
// the engine); TcumS/TcumP are the final cumulative transmittance from the
// entrance (incident intensity = 1).
type RaySummaryRow struct {
	ID          string  `json:"id" yaml:"id"`
	Wavelength  float64 `json:"wavelength" yaml:"wavelength"`
	OPLTotal    float64 `json:"opl_total" yaml:"opl_total"`
	IntensityS  float64 `json:"intensity_s" yaml:"intensity_s"`
	IntensityP  float64 `json:"intensity_p" yaml:"intensity_p"`
	TcumS       float64 `json:"tcum_s" yaml:"tcum_s"`
	TcumP       float64 `json:"tcum_p" yaml:"tcum_p"`
	Surfaces    int     `json:"surfaces" yaml:"surfaces"`
	Transmitted int     `json:"transmitted" yaml:"transmitted"`
	Missed      int     `json:"missed" yaml:"missed"`
	Error       string  `json:"error,omitempty" yaml:"error,omitempty"`
}

// RayDetailRow is one (ray, surface) record of the per-surface detail table.
// IntensityS/IntensityP are the per-surface intensity transmittance (matching
// SurfaceResult semantics); IntensityRs/IntensityRp are the per-surface power
// reflection (only when --details populated them); Jones is the Jones vector.
// TcumS/TcumP are the cumulative transmittance from the entrance (intensity
// 1 at the object plane).
type RayDetailRow struct {
	RayID            string            `json:"ray_id" yaml:"ray_id"`
	SurfaceID        int               `json:"surface_id" yaml:"surface_id"`
	Position         [3]float64        `json:"position" yaml:"position"`
	Direction        [3]float64        `json:"direction" yaml:"direction"`
	Interaction      string            `json:"interaction" yaml:"interaction"`
	Thickness        float64           `json:"thickness" yaml:"thickness"`
	OPL              float64           `json:"opl" yaml:"opl"`
	IntensityS       float64           `json:"intensity_s" yaml:"intensity_s"`
	IntensityP       float64           `json:"intensity_p" yaml:"intensity_p"`
	IntensityRs      *float64          `json:"intensity_rs,omitempty" yaml:"intensity_rs,omitempty"`
	IntensityRp      *float64          `json:"intensity_rp,omitempty" yaml:"intensity_rp,omitempty"`
	Jones            types.JonesVector `json:"jones" yaml:"jones"`
	TcumS            float64           `json:"tcum_s" yaml:"tcum_s"`
	TcumP            float64           `json:"tcum_p" yaml:"tcum_p"`
	AngleOfIncidence *float64          `json:"angle_of_incidence,omitempty" yaml:"angle_of_incidence,omitempty"`
	N1               *float64          `json:"n1,omitempty" yaml:"n1,omitempty"`
	N2               *float64          `json:"n2,omitempty" yaml:"n2,omitempty"`
	Rs               *float64          `json:"rs,omitempty" yaml:"rs,omitempty"`
	Rp               *float64          `json:"rp,omitempty" yaml:"rp,omitempty"`
	Ts               *float64          `json:"ts,omitempty" yaml:"ts,omitempty"`
	Tp               *float64          `json:"tp,omitempty" yaml:"tp,omitempty"`
	CoatingRs        *float64          `json:"coating_rs,omitempty" yaml:"coating_rs,omitempty"`
	CoatingRp        *float64          `json:"coating_rp,omitempty" yaml:"coating_rp,omitempty"`
	CoatingTs        *float64          `json:"coating_ts,omitempty" yaml:"coating_ts,omitempty"`
	CoatingTp        *float64          `json:"coating_tp,omitempty" yaml:"coating_tp,omitempty"`
	ErrorCode        string            `json:"error_code,omitempty" yaml:"error_code,omitempty"`
}

// raysListOutput is the structured (yaml/json) shape of `list rays`.
type raysListOutput struct {
	Summary []RaySummaryRow `json:"summary" yaml:"summary"`
	Details []RayDetailRow  `json:"details,omitempty" yaml:"details,omitempty"`
}

// ThicknessDiffRow is one (config, surface) record of the cross-config
// thickness comparison: a surface ID whose thickness differs between configs
// together with its value in one config. Structured output stores these
// self-describing records; table/csv render them as a matrix (rows = configs,
// columns = differing surfaces).
type ThicknessDiffRow struct {
	ConfigIndex int     `json:"config_index" yaml:"config_index"`
	ConfigName  string  `json:"config_name" yaml:"config_name"`
	SurfaceID   int     `json:"surface_id" yaml:"surface_id"`
	Thickness   float64 `json:"thickness" yaml:"thickness"`
}

// GlassIndex is one wavelength's refractive index of a glass.
type GlassIndex struct {
	WavelengthNM float64 `json:"wavelength_nm" yaml:"wavelength_nm"`
	Index        float64 `json:"index" yaml:"index"`
}

// GlassListRow is one row of the `list glasses` table. Type is the dispersion
// category (formula name for catalog glasses, "model", "constant", or
// "tabulated"); empty for an unresolved key. ND/VD are the d-line index and
// Abbe number (stored values when present, computed from the dispersion data
// otherwise; zero when unavailable — a constant-index glass has no Abbe
// number). Indices holds n at every collected wavelength (longest first).
type GlassListRow struct {
	Name         string       `json:"name" yaml:"name"`
	Manufacturer string       `json:"manufacturer,omitempty" yaml:"manufacturer,omitempty"`
	Type         string       `json:"type,omitempty" yaml:"type,omitempty"`
	ND           float64      `json:"nd,omitempty" yaml:"nd,omitempty"`
	VD           float64      `json:"vd,omitempty" yaml:"vd,omitempty"`
	Indices      []GlassIndex `json:"indices,omitempty" yaml:"indices,omitempty"`
}

// glassesListOutput is the structured (yaml/json) shape of `list glasses`.
type glassesListOutput struct {
	Glasses []GlassListRow `json:"glasses" yaml:"glasses"`
}

// FieldListRow is one row of the `list fields` table. Input* fields come from
// the YAML definition; Computed* fields come from the real-ray chief trace.
// Nil pointers mean the field is not applicable for that type.
type FieldListRow struct {
	Index int    `json:"index" yaml:"index"`
	Type  string `json:"type" yaml:"type"`
	// Input values (from YAML)
	InputAngle       *float64  `json:"input_angle,omitempty" yaml:"input_angle,omitempty"`
	InputImageHeight *float64  `json:"input_image_height,omitempty" yaml:"input_image_height,omitempty"`
	InputHeight      *float64  `json:"input_height,omitempty" yaml:"input_height,omitempty"`
	InputObjectZ     *float64  `json:"input_object_z,omitempty" yaml:"input_object_z,omitempty"`
	Direction        []float64 `json:"direction,omitempty" yaml:"direction,omitempty"`
	// Computed values (from chief ray trace)
	ComputedAngle       *float64 `json:"computed_angle,omitempty" yaml:"computed_angle,omitempty"`
	ComputedImageHeight *float64 `json:"computed_image_height,omitempty" yaml:"computed_image_height,omitempty"`
}

// fieldsListOutput is the structured (yaml/json) shape of `list fields`.
type fieldsListOutput struct {
	Fields []FieldListRow `json:"fields" yaml:"fields"`
}

// WavelengthListRow is one row of the `list wavelengths` table.
type WavelengthListRow struct {
	Config    string  `json:"config" yaml:"config"`
	Value     float64 `json:"value" yaml:"value"`
	Label     string  `json:"label,omitempty" yaml:"label,omitempty"`
	RawWeight float64 `json:"raw_weight" yaml:"raw_weight"`
	Weight    float64 `json:"weight" yaml:"weight"`
	Included  bool    `json:"included" yaml:"included"`
}

// WavelengthListOutput is the structured shape of `list wavelengths`.
type WavelengthListOutput struct {
	Reference float64 `json:"reference,omitempty" yaml:"reference,omitempty"`
	// Mode is the resolved chief.wavelength_set (design | spectral | custom).
	Mode string `json:"wavelength_set,omitempty" yaml:"wavelength_set,omitempty"`
	// Resolved is the actual set a per-wavelength command would evaluate.
	Resolved      []types.WavelengthSample `json:"resolved,omitempty" yaml:"resolved,omitempty"`
	ResolvedCount int                      `json:"resolved_count,omitempty" yaml:"resolved_count,omitempty"`
	Rows          []WavelengthListRow      `json:"wavelengths" yaml:"wavelengths"`
	Spectrum      *types.SpectralConfig    `json:"spectrum,omitempty" yaml:"spectrum,omitempty"`
}

// MeritTermRow is one row of the `list merit` terms table.
type MeritTermRow struct {
	Config               string  `json:"config" yaml:"config"`
	Kind                 string  `json:"kind" yaml:"kind"`
	Field                int     `json:"field,omitempty" yaml:"field,omitempty"`
	Wavelength           float64 `json:"wavelength,omitempty" yaml:"wavelength,omitempty"`
	WavelengthSet        string  `json:"wavelength_set,omitempty" yaml:"wavelength_set,omitempty"`
	ComparisonWavelength float64 `json:"comparison_wavelength,omitempty" yaml:"comparison_wavelength,omitempty"`
	Target               float64 `json:"target,omitempty" yaml:"target,omitempty"`
	Fraction             float64 `json:"fraction,omitempty" yaml:"fraction,omitempty"`
	SurfaceSet           []int   `json:"surface_set,omitempty" yaml:"surface_set,omitempty"`
	Weight               float64 `json:"weight" yaml:"weight"`
}

// MeritModeRow is one row of the `list merit` modes table.
type MeritModeRow struct {
	Config   string `json:"config" yaml:"config"`
	Name     string `json:"name" yaml:"name"`
	NumTerms int    `json:"num_terms" yaml:"num_terms"`
}

// MeritConstraintRow is one row of the `list merit` constraints table.
type MeritConstraintRow struct {
	Config    string  `json:"config" yaml:"config"`
	ID        string  `json:"id" yaml:"id"`
	Kind      string  `json:"kind" yaml:"kind"`
	Measure   string  `json:"measure" yaml:"measure"`
	Field     int     `json:"field,omitempty" yaml:"field,omitempty"`
	Target    float64 `json:"target,omitempty" yaml:"target,omitempty"`
	Lower     float64 `json:"lower,omitempty" yaml:"lower,omitempty"`
	Upper     float64 `json:"upper,omitempty" yaml:"upper,omitempty"`
	BandWidth float64 `json:"band_width,omitempty" yaml:"band_width,omitempty"`
	Weight    float64 `json:"weight" yaml:"weight"`
	Active    bool    `json:"active" yaml:"active"`
	// Inherited marks constraints resolved from optimization.constraints
	// because the config defines none of its own (matching the optimizer's
	// per-config constraint fallback).
	Inherited bool `json:"inherited,omitempty" yaml:"inherited,omitempty"`
}

// OptResultSummary is a lightweight summary of the optimization result.
type OptResultSummary struct {
	Status      string  `json:"status" yaml:"status"`
	Iterations  int     `json:"iterations" yaml:"iterations"`
	Merit       float64 `json:"merit,omitempty" yaml:"merit,omitempty"`
	Interrupted bool    `json:"interrupted,omitempty" yaml:"interrupted,omitempty"`
}

// meritListOutput is the structured (yaml/json) shape of `list merit`.
type meritListOutput struct {
	Terms       []MeritTermRow       `json:"terms,omitempty" yaml:"terms,omitempty"`
	Modes       []MeritModeRow       `json:"modes,omitempty" yaml:"modes,omitempty"`
	Constraints []MeritConstraintRow `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	OptResult   *OptResultSummary    `json:"opt_result,omitempty" yaml:"opt_result,omitempty"`
}

// propRow is a name/value pair used by `list optimization` for the solver
// settings and sub-config sections (same shape as paraxialProp).
type propRow struct {
	Name  string `json:"name" yaml:"name"`
	Value string `json:"value" yaml:"value"`
}

// OptVarRow is one row of the `list optimization` per-config variables table
// (optimization.variables[]).
type OptVarRow struct {
	Name    string  `json:"name" yaml:"name"`
	Config  string  `json:"config,omitempty" yaml:"config,omitempty"`
	Surface int     `json:"surface,omitempty" yaml:"surface,omitempty"`
	Param   string  `json:"param" yaml:"param"`
	Min     float64 `json:"min" yaml:"min"`
	Max     float64 `json:"max" yaml:"max"`
	Step    float64 `json:"step,omitempty" yaml:"step,omitempty"`
	Active  bool    `json:"active" yaml:"active"`
}

// SharedBindingRow is one binding of a shared variable.
type SharedBindingRow struct {
	Config string  `json:"config" yaml:"config"`
	ID     int     `json:"id" yaml:"id"`
	Param  string  `json:"param" yaml:"param"`
	Scale  float64 `json:"scale,omitempty" yaml:"scale,omitempty"`
	Offset float64 `json:"offset,omitempty" yaml:"offset,omitempty"`
}

// SharedVarRow is one row of the shared-variables table
// (optimization.shared_variables[]).
type SharedVarRow struct {
	Name     string             `json:"name" yaml:"name"`
	Min      float64            `json:"min" yaml:"min"`
	Max      float64            `json:"max" yaml:"max"`
	Active   bool               `json:"active" yaml:"active"`
	Bindings []SharedBindingRow `json:"bindings" yaml:"bindings"`
}

// LocalVarRow is one row of the local-variables table
// (optimization.local_variables[]).
type LocalVarRow struct {
	Name    string  `json:"name" yaml:"name"`
	Config  string  `json:"config" yaml:"config"`
	Surface int     `json:"surface,omitempty" yaml:"surface,omitempty"`
	Param   string  `json:"param" yaml:"param"`
	Min     float64 `json:"min" yaml:"min"`
	Max     float64 `json:"max" yaml:"max"`
	Active  bool    `json:"active" yaml:"active"`
}

// ScheduleModeRow is one mode of the merit-schedule table.
type ScheduleModeRow struct {
	Name       string  `json:"name" yaml:"name"`
	WeightFrom float64 `json:"weight_from" yaml:"weight_from"`
	WeightTo   float64 `json:"weight_to" yaml:"weight_to"`
}

// ScheduleSection is the structured shape of optimization.merit_schedule.
type ScheduleSection struct {
	Metric      string            `json:"metric,omitempty" yaml:"metric,omitempty"`
	Curve       string            `json:"curve,omitempty" yaml:"curve,omitempty"`
	AnchorFrom  float64           `json:"anchor_from,omitempty" yaml:"anchor_from,omitempty"`
	AnchorTo    float64           `json:"anchor_to,omitempty" yaml:"anchor_to,omitempty"`
	Aggregation string            `json:"metric_aggregation,omitempty" yaml:"metric_aggregation,omitempty"`
	Modes       []ScheduleModeRow `json:"modes" yaml:"modes"`
}

// SubConfigSection is one named sub-config of optimization (escape,
// glass_hull, degenerate, power_solve, region_active, adaptive_damping) as a
// key-value settings list.
type SubConfigSection struct {
	Name     string    `json:"name" yaml:"name"`
	Settings []propRow `json:"settings" yaml:"settings"`
}

// OptimizationResultSummary is the piped-output summary shown by
// `list optimization` (output.opt_results).
type OptimizationResultSummary struct {
	Status           string                       `json:"status" yaml:"status"`
	Iterations       int                          `json:"iterations" yaml:"iterations"`
	ActiveMode       string                       `json:"active_mode,omitempty" yaml:"active_mode,omitempty"`
	ModeWeights      map[string]float64           `json:"mode_weights,omitempty" yaml:"mode_weights,omitempty"`
	ModeChanges      int                          `json:"mode_changes,omitempty" yaml:"mode_changes,omitempty"`
	MetricValue      float64                      `json:"metric_value,omitempty" yaml:"metric_value,omitempty"`
	EffectiveNumRays int                          `json:"effective_num_rays,omitempty" yaml:"effective_num_rays,omitempty"`
	Interrupted      bool                         `json:"interrupted,omitempty" yaml:"interrupted,omitempty"`
	Reason           string                       `json:"reason,omitempty" yaml:"reason,omitempty"`
	GlassAttraction  *types.GlassAttractionResult `json:"glass_attraction,omitempty" yaml:"glass_attraction,omitempty"`
	Snap             *types.SnapResult            `json:"snap,omitempty" yaml:"snap,omitempty"`
	// SpotFlatness is the spot-slope diagnostic the spot_flatness /
	// spot_astigmatism terms produce (one row per config, field and
	// wavelength). It is a table, not a key-value list, so the renderer prints
	// it as its own section.
	SpotFlatness []types.SpotFlatnessRow `json:"spot_flatness,omitempty" yaml:"spot_flatness,omitempty"`
}

// optimizationListOutput is the structured (yaml/json) shape of
// `list optimization`. No constraints: the effective constraint display lives
// in `list merit` (per-config constraints, else inherited from
// optimization.constraints).
type optimizationListOutput struct {
	Solver          []propRow                  `json:"solver,omitempty" yaml:"solver,omitempty"`
	Variables       []OptVarRow                `json:"variables,omitempty" yaml:"variables,omitempty"`
	SharedVariables []SharedVarRow             `json:"shared_variables,omitempty" yaml:"shared_variables,omitempty"`
	LocalVariables  []LocalVarRow              `json:"local_variables,omitempty" yaml:"local_variables,omitempty"`
	MeritSchedule   *ScheduleSection           `json:"merit_schedule,omitempty" yaml:"merit_schedule,omitempty"`
	SubConfigs      []SubConfigSection         `json:"sub_configs,omitempty" yaml:"sub_configs,omitempty"`
	OptResult       *OptimizationResultSummary `json:"opt_result,omitempty" yaml:"opt_result,omitempty"`
}

// GlobalMinimumRow is one local minimum for `list global`. Reason carries the
// invalid reason of an infeasible basin (the feasible list never has one).
// Variables is the minimum's variable values (name/param/after, the nd/vd
// states a glass-changing run carries) and, like ElementPowers, exists only
// for a pipeline document — a run log records no design data. Both are
// structured (yaml/json) output only: the table and csv renderers build their
// cells explicitly and ignore them.
type GlobalMinimumRow struct {
	Index         int                    `json:"index" yaml:"index"`
	Merit         float64                `json:"merit" yaml:"merit"`
	Status        string                 `json:"status,omitempty" yaml:"status,omitempty"`
	Reason        string                 `json:"reason,omitempty" yaml:"reason,omitempty"`
	File          string                 `json:"file,omitempty" yaml:"file,omitempty"`
	ElementPowers [][]float64            `json:"element_powers,omitempty" yaml:"element_powers,omitempty"`
	Variables     []types.GlobalVarState `json:"variables,omitempty" yaml:"variables,omitempty"`
}

// globalRunInfo is the run-level aggregate recovered from an escape/pso JSONL
// run log (the `global_complete` / `done` events). It is absent for a pipeline
// document, which carries no record of how the run progressed.
type globalRunInfo struct {
	Workers      int     `json:"workers,omitempty" yaml:"workers,omitempty"`
	Cycles       int     `json:"cycles,omitempty" yaml:"cycles,omitempty"`
	Explorations int     `json:"explorations,omitempty" yaml:"explorations,omitempty"`
	Minima       int     `json:"minima_count,omitempty" yaml:"minima_count,omitempty"`
	Elapsed      float64 `json:"elapsed_s,omitempty" yaml:"elapsed_s,omitempty"`
}

// GlobalWorkerRow is one worker's lifecycle summary for `list global`. It is
// recovered only from an escape/pso JSONL run log: Status is the completion
// state (completed / timeout / interrupted / retired), Cycles the number of
// cycles the worker reached before finishing, Explored/Recorded its exploration and
// minimum-recording counts, BestMerit the best merit it reached, Elapsed the
// seconds from run start to its completion event, and Reason the
// resource-guard retirement reason.
type GlobalWorkerRow struct {
	Worker    int      `json:"worker" yaml:"worker"`
	Status    string   `json:"status" yaml:"status"`
	Cycles    int      `json:"cycles,omitempty" yaml:"cycles,omitempty"`
	Explored  int      `json:"explored,omitempty" yaml:"explored,omitempty"`
	Recorded  int      `json:"recorded,omitempty" yaml:"recorded,omitempty"`
	BestMerit *float64 `json:"best_merit,omitempty" yaml:"best_merit,omitempty"`
	Elapsed   float64  `json:"elapsed_s,omitempty" yaml:"elapsed_s,omitempty"`
	Reason    string   `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// globalListData is the render input of `list global`, shared by the pipeline
// (YAML) reader and the escape/pso JSONL run-log reader so both render through
// one code path. Run, Workers, Events and the file names of InfeasibleBasins
// are only populated from a run log; Found is false when the input carried no
// escape result at all.
type globalListData struct {
	Found bool
	// FromLog marks data recovered from an escape/pso JSONL run log. Such a
	// log may carry no --save file names, in which case the File column of the
	// minima tables is dropped for it.
	FromLog          bool
	Params           []propRow
	Minima           []GlobalMinimumRow
	InfeasibleBasins []GlobalMinimumRow
	BestIndex        int
	BestMerit        float64
	FileDir          string
	TimedOut         bool
	Interrupted      bool
	Run              *globalRunInfo
	Workers          []GlobalWorkerRow
	Events           []GlobalEventRow
}

// globalListOutput is the structured (yaml/json) shape of `list global`.
type globalListOutput struct {
	Params           []propRow          `json:"params,omitempty" yaml:"params,omitempty"`
	Minima           []GlobalMinimumRow `json:"minima,omitempty" yaml:"minima,omitempty"`
	InfeasibleBasins []GlobalMinimumRow `json:"infeasible_basins,omitempty" yaml:"infeasible_basins,omitempty"`
	BestIndex        int                `json:"best_index" yaml:"best_index"`
	BestMerit        float64            `json:"best_merit" yaml:"best_merit"`
	FileDir          string             `json:"file_directory,omitempty" yaml:"file_directory,omitempty"`
	TimedOut         bool               `json:"timed_out,omitempty" yaml:"timed_out,omitempty"`
	Interrupted      bool               `json:"interrupted,omitempty" yaml:"interrupted,omitempty"`
	Run              *globalRunInfo     `json:"run,omitempty" yaml:"run,omitempty"`
	Workers          []GlobalWorkerRow  `json:"workers,omitempty" yaml:"workers,omitempty"`
	Events           []GlobalEventRow   `json:"events,omitempty" yaml:"events,omitempty"`
}

// flagAndPositional holds the pre-split command line of the list subcommand.
type flagAndPositional struct {
	flags      []string
	positional []string
}

// tableColumn is one column of a rendered text table.
type tableColumn struct {
	header string
	right  bool
	cells  []string
}

// paraxialProp is one row of the paraxial key-value property table.
type paraxialProp struct {
	Name  string
	Value string
}

// focusListOutput is the structured (yaml/json) shape of `list focus`: the
// pipeline focus_comparison section echoed back under its own key so the output
// is self-describing.
type focusListOutput struct {
	FocusComparison *types.FocusComparison `json:"focus_comparison,omitempty" yaml:"focus_comparison,omitempty"`
}
