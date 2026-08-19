// Package escape implements the Ishiki-Ono style escape-function global
// optimisation. After DLS converges to a local minimum, a smooth bump is
// added to the merit function around that minimum so the next DLS run moves
// away and discovers other local minima. The bump only deforms the merit
// landscape near the recorded minimum.
package escape

import (
	"math"

	"github.com/hiroki/rayweaver/internal/dls"
)

// Params holds the escape-function parameters. H controls the bump height
// (escape strength), W controls its width (locality), and per-variable
// weights normalise the physical scale differences between variable types.
type Params struct {
	H       float64
	W       float64
	HMult   float64
	WMult   float64
	Dt      float64
	DtFp    float64   // fingerprint-distance threshold: a candidate is a repeat only when it is close in variables AND close in the design fingerprint (<= 0 disables the fingerprint criterion)
	Weights []float64 // optional per-variable weight (nil or 0 -> 1.0)
	Active  []int     // indices of variables where Min != Max (excluded from distance)
	Scales  []float64 // Max - Min for each variable (normalisation scale)
	// Execution tuning (wiring of the DLS options; not part of the bump).
	EscapeIterFrac  float64 // escape-phase MaxIter as a fraction of the full budget (0 = 1/3)
	GlassIterFrac   float64 // glass-phase MaxIter as a fraction of the full budget (0 = 1/3)
	WSpan           float64 // worker W scaling span: W*(1 + i/(N-1)*(WSpan-1)); 0 = 2
	StallWindowFrac float64 // stalled-early-stop window as a fraction of MaxIter (0 = 0.2)
	StallRelTol     float64 // stalled-early-stop relative merit threshold (0 = 1e-4)
	StallEarlyStop  bool    // enable stalled-early-stop in the escape phase (clean phase never stalls)
	InitialPerturb  float64 // normalised amplitude spreading parallel workers (0 = 0.05)
}

// DefaultParams returns the literature-recommended starting values adapted to
// the normalised variable space.
func DefaultParams() Params {
	return Params{
		H:               0.1,
		W:               0.5,
		HMult:           2.0,
		WMult:           1.3,
		Dt:              0.1,
		DtFp:            0,
		EscapeIterFrac:  1.0 / 3.0,
		GlassIterFrac:   1.0 / 3.0,
		WSpan:           2.0,
		StallWindowFrac: 0.2,
		StallRelTol:     1e-4,
		StallEarlyStop:  true,
		InitialPerturb:  0.05,
	}
}

// Point is one recorded local minimum together with the escape parameters
// (H, W) currently associated with it. These grow when DLS keeps returning to
// the same minimum. Fingerprint is the optional design descriptor (e.g. the
// thin-lens element powers) used as an additional "distinct minimum" criterion.
type Point struct {
	X           []float64
	Merit       float64
	H           float64
	W           float64
	Fingerprint []float64
}

// Phase selects the current phase of the escape cycle for the Wrapper's
// Options() adaptation.
type Phase int

const (
	PhaseEscape Phase = iota
	PhaseClean
	PhaseGlassSolve
)

// glassPhaseable is the optional capability an inner dls.Model may implement
// to enter/exit the power-preserving glass phase: lock every variable except
// the glass dispersions, route the merit to the colour-only glass terms, and
// toggle the power-preserving solve. The escape package stays decoupled from
// the concrete Optimizer (it only depends on this interface).
type glassPhaseable interface {
	EnterGlassPhase(x []float64)
	ExitGlassPhase()
}

// Wrapper implements dls.Model by delegating to an inner model and adding a
// smooth escape residual for every recorded local minimum. Passing a nil or
// empty escape list makes the wrapper behave exactly like the inner model
// (used for the clean re-optimisation step of the cycle).
type Wrapper struct {
	inner         dls.Model
	escapes       []Point
	params        Params
	startX        []float64
	phase         Phase
	stop          <-chan struct{}
	glassPhase    bool // insert the power-preserving glass phase between escape and clean DLS
	inGlassPhase  bool // the inner model is currently in the glass phase
}

// NewWrapper wraps an inner dls.Model with escape-function support.
func NewWrapper(inner dls.Model, params Params) *Wrapper {
	return &Wrapper{inner: inner, params: params}
}

// SetStop sets the optional channel that aborts a running DLS solve mid-way
// (see dls.Options.Stop). nil disables mid-solve interruption.
func (w *Wrapper) SetStop(stop <-chan struct{}) {
	w.stop = stop
}

// SetEscapes replaces the active escape points. Empty list clears them.
func (w *Wrapper) SetEscapes(points []Point) {
	w.escapes = points
}

// SetStartX sets a custom initial state (returned by InitialState). nil falls
// back to the inner model's own initial state.
func (w *Wrapper) SetStartX(x []float64) {
	if x == nil {
		w.startX = nil
		return
	}
	w.startX = make([]float64, len(x))
	copy(w.startX, x)
}

// distanceSq returns the normalised mean-squared distance between x and the
// point p. Variables with Min == Max (fixed) are excluded.
func (w *Wrapper) distanceSq(x []float64, p Point) float64 {
	n := len(w.params.Active)
	if n == 0 {
		return 0
	}
	sum := 0.0
	for _, j := range w.params.Active {
		scale := w.params.Scales[j]
		if scale <= 0 {
			scale = 1.0
		}
		wt := 1.0
		if j < len(w.params.Weights) && w.params.Weights[j] != 0 {
			wt = w.params.Weights[j]
		}
		du := (x[j] - p.X[j]) / scale
		sum += wt * du * du
	}
	return sum / float64(n)
}

// EscapeMerit returns the escape-function value for all recorded minima at x.
func (w *Wrapper) EscapeMerit(x []float64) float64 {
	total := 0.0
	for _, p := range w.escapes {
		total += p.H * math.Exp(-w.distanceSq(x, p)/(p.W*p.W))
	}
	return total
}

func (w *Wrapper) Variables() []dls.VariableInfo {
	return w.inner.Variables()
}

func (w *Wrapper) InitialState() []float64 {
	if w.startX != nil {
		return w.startX
	}
	return w.inner.InitialState()
}

func (w *Wrapper) Options() dls.Options {
	opts := w.inner.Options()
	// The escape cycle provides its own escape mechanism; the DLS internal
	// stall perturbation would fight it.
	opts.DisableStallEscape = true
	// The mid-solve stop channel is set per-solve by the cycle from the shared
	// interrupt signal. Each worker owns its Wrapper and runs sequentially, so
	// per-wrapper state is race-free.
	opts.Stop = w.stop
	// Only the basin-escape DLS uses stalled early termination; the clean
	// re-optimisation keeps the full budget so a slow late-stage improvement
	// (e.g. a max_iterations clean run yielding the global best) is not cut
	// off prematurely.
	opts.EnableStallDone = w.phase == PhaseEscape && w.params.StallEarlyStop
	opts.StallWindowFrac = w.params.StallWindowFrac
	opts.StallRelTol = w.params.StallRelTol
	if w.phase == PhaseEscape && w.params.EscapeIterFrac > 0 {
		if opts.MaxIter > 3 {
			opts.MaxIter = max(50, int(float64(opts.MaxIter)*w.params.EscapeIterFrac))
		}
	}
	if w.phase == PhaseGlassSolve && w.params.GlassIterFrac > 0 {
		if opts.MaxIter > 3 {
			opts.MaxIter = max(50, int(float64(opts.MaxIter)*w.params.GlassIterFrac))
		}
	}
	// The power-preserving glass phase is a plain DLS (no escape bumps).
	opts.DisableStallEscape = true
	return opts
}

// SetPhase selects the escape-cycle phase so Options() can adapt MaxIter.
// When the glass phase is enabled, entering PhaseGlassSolve pushes the inner
// model into its glass phase (locking non-glass variables to the current
// x/startX and switching to the colour merit); leaving it (escape/clean)
// restores the inner model. The inner model only participates if it implements
// the glassPhaseable capability.
func (w *Wrapper) SetPhase(p Phase) {
	if w.glassPhase && w.phase == PhaseGlassSolve && p != PhaseGlassSolve && w.inGlassPhase {
		if gp, ok := w.inner.(glassPhaseable); ok {
			gp.ExitGlassPhase()
		}
		w.inGlassPhase = false
	}
	if w.glassPhase && p == PhaseGlassSolve && !w.inGlassPhase {
		if gp, ok := w.inner.(glassPhaseable); ok {
			gp.EnterGlassPhase(w.startX)
		}
		w.inGlassPhase = true
	}
	w.phase = p
}

// SetGlassPhase enables or disables the power-preserving glass phase between
// the escape and clean DLS of each cycle. The inner model must implement
// glassPhaseable for the phase to take effect.
func (w *Wrapper) SetGlassPhase(enabled bool) {
	w.glassPhase = enabled
}

// GlassPhaseEnabled reports whether the glass phase is enabled on this wrapper.
func (w *Wrapper) GlassPhaseEnabled() bool {
	return w.glassPhase
}

// EvaluateMerit returns the inner merit plus all escape terms.
func (w *Wrapper) EvaluateMerit(x []float64) float64 {
	return w.inner.EvaluateMerit(x) + w.EscapeMerit(x)
}

// ComputeResiduals returns the inner residuals plus one residual per escape
// point. Each residual is sqrt(H) * exp(-d² / (2 W²)) so that its square is
// exactly the escape term H * exp(-d² / W²). The finite-difference Jacobian
// in DLS therefore captures the escape gradient naturally.
func (w *Wrapper) ComputeResiduals(x []float64) []float64 {
	r := w.inner.ComputeResiduals(x)
	for _, p := range w.escapes {
		d2 := w.distanceSq(x, p)
		r = append(r, math.Sqrt(p.H)*math.Exp(-d2/(2*p.W*p.W)))
	}
	return r
}

func (w *Wrapper) ComputeConstraints(x []float64) []float64 {
	return w.inner.ComputeConstraints(x)
}

// innerMerit evaluates the real (unescaped) merit at x.
func (w *Wrapper) innerMerit(x []float64) float64 {
	return w.inner.EvaluateMerit(x)
}
