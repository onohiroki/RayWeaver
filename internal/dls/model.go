package dls

const MeritSpotRMS = "spot_rms"

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
	Logger             Logger
}

type ConstraintState struct {
	Residual float64
}

type Logger interface {
	LogIter(iter int, merit, improvement, stepNorm float64, variables []float64, constraints []ConstraintState)
	LogFinal(iter int, status string, merit float64, stepNorm float64, variables []float64, constraints []ConstraintState)
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
