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
	Weights []float64 // optional per-variable weight (nil or 0 -> 1.0)
	Active  []int     // indices of variables where Min != Max (excluded from distance)
	Scales  []float64 // Max - Min for each variable (normalisation scale)
}

// DefaultParams returns the literature-recommended starting values adapted to
// the normalised variable space.
func DefaultParams() Params {
	return Params{
		H:     0.1,
		W:     0.5,
		HMult: 2.0,
		WMult: 1.3,
		Dt:    0.1,
	}
}

// Point is one recorded local minimum together with the escape parameters
// (H, W) currently associated with it. These grow when DLS keeps returning to
// the same minimum.
type Point struct {
	X     []float64
	Merit float64
	H     float64
	W     float64
}

// Wrapper implements dls.Model by delegating to an inner model and adding a
// smooth escape residual for every recorded local minimum. Passing a nil or
// empty escape list makes the wrapper behave exactly like the inner model
// (used for the clean re-optimisation step of the cycle).
type Wrapper struct {
	inner   dls.Model
	escapes []Point
	params  Params
	startX  []float64
}

// NewWrapper wraps an inner dls.Model with escape-function support.
func NewWrapper(inner dls.Model, params Params) *Wrapper {
	return &Wrapper{inner: inner, params: params}
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
	return opts
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
