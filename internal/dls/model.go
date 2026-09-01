package dls

import "github.com/hiroki/rayweaver/internal/types"

const (
	MeritSpotRMS      = "spot_rms"
	MeritSpotRMST     = "spot_rms_t"
	MeritSpotRMSS     = "spot_rms_s"
	MeritSpotRMSWorst = "spot_rms_worst"
	MeritSpotWeighted = "spot_rms_weighted"
	MeritSpotEERadius = "spot_ee_radius"
)

// StatusInterrupted is the solver status returned when the Stop channel is
// closed mid-solve: the best point found so far is returned instead of a
// converged result.
const StatusInterrupted = "interrupted"

type VariableInfo struct {
	Name      string
	SurfaceID int
	GlassName string
	Param     string
	Min       float64
	Max       float64
}

type MeritTerm struct {
	FieldAngle  float64
	FieldDir    []float64
	FieldWeight float64
	Wavelength  float64
	WavWeight   float64
	Weight      float64
}

type Result struct {
	BeforeMerit float64
	AfterMerit  float64
	Iterations  int
	Status      string
	Variables   []VariableState
}

type VariableState struct {
	Name      string
	SurfaceID int
	GlassName string
	Param     string
	Before    float64
	After     float64
}

type Options struct {
	MaxIter            int
	Mu                 float64
	Tol                float64
	Epsilon            float64
	NumRays            int
	ApertureMargin     float64
	MuConMax           float64
	Workers            int
	DisableStallEscape bool
	EnableStallDone    bool
	// CentralDiff uses central-difference Jacobian (2nd-order accurate) instead
	// of forward-difference (1st-order). Doubles the number of residual
	// evaluations per iteration but improves accuracy for tightly-coupled
	// variables (e.g. high-order aspheres, multi-element lenses).
	CentralDiff bool
	// BFGS enables BFGS-augmented LM: the damping term μI is replaced by
	// μ·B⁻¹ where B is the damped-BFGS inverse Hessian approximation. This
	// improves convergence in well-conditioned valleys where the Hessian has
	// strong anisotropy (typical for lens optimisation with many variables).
	// The inverse Hessian is updated after each accepted step using the
	// standard BFGS formula with damped update to maintain positive-
	// definiteness. O(n²) per update, O(n³) to invert via Cholesky — for
	// n ≤ 50 this is negligible relative to the Jacobian cost.
	BFGS bool
	// AdaptiveDamping configures per-variable adaptive damping. When non-nil,
	// the solver replaces the fixed μI damping with μD where D is a diagonal
	// matrix derived from Jacobian sensitivity, variable class, and accept/reject
	// history. nil preserves the legacy μI behaviour.
	AdaptiveDamping *types.AdaptiveDampingConfig
	// StallWindowFrac is the fraction of MaxIter used as the stalled-early-stop
	// window (0 = default 20%, i.e. MaxIter/5, floor 50). Only consulted when
	// EnableStallDone is true.
	StallWindowFrac float64
	// StallRelTol is the relative best-merit improvement that must occur over
	// the stall window to keep the solver running (0 = default 1e-4). Only
	// consulted when EnableStallDone is true.
	StallRelTol float64
	Logger      Logger
	// Stop is an optional channel that, when closed, asks the solver to abort
	// as soon as possible (checked at the top of every iteration and inside
	// the Jacobian sweep and line search). On stop the solver returns the
	// best point found so far with Status "interrupted" instead of a converged
	// result. nil disables interruption.
	Stop <-chan struct{}
}

type ConstraintState struct {
	Residual float64
}

type Logger interface {
	LogIter(iter int, merit, improvement, stepNorm float64, variables []float64, constraints []ConstraintState)
	LogFinal(iter int, status string, merit float64, stepNorm float64, variables []float64, constraints []ConstraintState)
}

// ModeLogger is an optional Logger capability: report the per-iteration
// merit-blend weights of a conditional merit schedule.
type ModeLogger interface {
	LogModeWeights(iter int, weights map[string]float64)
}

type Model interface {
	Variables() []VariableInfo
	InitialState() []float64
	EvaluateMerit(x []float64) float64
	ComputeResiduals(x []float64) []float64
	ComputeConstraints(x []float64) []float64
	Options() Options
}

// PupilUpdater is an optional Model capability: recompute the dynamic pupil
// (grid centring) at the current variable state before the next Jacobian
// column sweep. The solver calls it once per iteration at the current x, so the
// pupil moves between iterations while staying frozen within one iteration —
// the base-point and all finite-difference residual evaluations share the same
// pupil, keeping the Jacobian consistent. Models that do not implement it keep
// whatever grid centring they were constructed with.
type PupilUpdater interface {
	UpdatePupils(x []float64)
}

// MeritScheduleUpdater is an optional Model capability: recompute the smooth
// merit-blend weights at the current variable state before the next Jacobian
// column sweep. Like PupilUpdater, the solver calls it once per iteration at
// the current x, so the weights stay frozen within one iteration — the
// base-point and all finite-difference residual evaluations share the same
// weights, keeping the Jacobian consistent with the merit actually minimised.
// Models without a schedule keep the weights they were constructed with.
type MeritScheduleUpdater interface {
	UpdateMeritWeights(x []float64, iter int)
}

// RegionActiveUpdater is an optional Model capability: update the dynamic
// active set of inequality constraints at the current variable state before
// the next Jacobian column sweep. The solver calls it once per iteration at
// the current x, so the active set is frozen within one iteration — the
// base-point and all finite-difference residual evaluations share the same
// active constraint subset, keeping the Jacobian consistent. Models that do
// not implement it keep all constraints active (legacy behaviour).
type RegionActiveUpdater interface {
	UpdateRegionActiveSet(x []float64)
}

// ActiveConstraintIndices is an optional Model capability: return the indices
// of constraints that should be included in the augmented system. The solver
// calls this after UpdateRegionActiveSet to determine which constraints to
// treat as active in the normal equations. Models that do not implement it
// return nil, causing the solver to include all constraints (legacy behaviour).
type ActiveConstraintIndices interface {
	ActiveConstraintIndices() []int
}

// ConstraintMultipliers is an optional Model capability: read/write the
// Lagrange multipliers for the region-active constraints. The solver reads
// the current multipliers before building the augmented system and writes
// the updated multipliers after an accepted step. Models that do not
// implement it use the solver's internal lambda tracking (legacy behaviour).
type ConstraintMultipliers interface {
	ConstraintMultipliers() []float64
	SetConstraintMultipliers(lambdas []float64)
}

// DampingVarInfo carries per-variable diagnostic information for the
// adaptive damping logger.
type DampingVarInfo struct {
	Name        string  `json:"name"`
	Class       string  `json:"class"`
	HDiag       float64 `json:"hessian_diagonal"`
	Ratio       float64 `json:"sensitivity_ratio"`
	LocalFactor float64 `json:"local_factor"`
	Diagonal    float64 `json:"damping_diagonal"`
	Step        float64 `json:"normalized_step"`
}

// DampingLogger is an optional Logger capability: report the per-variable
// adaptive damping diagnostics each iteration.
type DampingLogger interface {
	LogDamping(iter int, mu float64, ref float64, vars []DampingVarInfo)
}
