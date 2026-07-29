package dls

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
	MaxIter        int
	Mu             float64
	Tol            float64
	Epsilon        float64
	NumRays        int
	ApertureMargin float64
	Logger         Logger
}

type Logger interface {
	LogIter(iter int, merit, improvement, stepNorm float64, variables []float64)
	LogFinal(iter int, status string, merit float64, stepNorm float64, variables []float64)
}

type Model interface {
	Variables() []VariableInfo
	InitialState() []float64
	EvaluateMerit(x []float64) float64
	ComputeResiduals(x []float64) []float64
	ComputeConstraints(x []float64) []float64
	Options() Options
}
