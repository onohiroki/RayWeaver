package escape

import (
	"math"
	"strings"

	"github.com/hiroki/rayweaver/internal/dls"
)

// Cycle runs the two-step escape optimisation loop for one worker:
//
//  1. DLS with escape functions active (pushes away from known minima)
//  2. DLS with escapes cleared (converges to the true local minimum)
//
// The true minimum is recorded or, if it matches a known one, that minimum's
// escape is strengthened and the restart offset grows so the next escape run
// starts farther from the trap.
type Cycle struct {
	wrapper   *Wrapper
	store     *Store
	params    Params
	maxCycles int
	maxFail   int
	seed      int64
	workerID  int
	progress  *Progress
	escaped   int
	recorded  int
}

// NewCycle creates an escape cycle bound to a wrapper and shared store.
// progress may be nil to disable verbose reporting.
func NewCycle(wrapper *Wrapper, store *Store, params Params, maxCycles int, seed int64, progress *Progress) *Cycle {
	return &Cycle{
		wrapper:   wrapper,
		store:     store,
		params:    params,
		maxCycles: maxCycles,
		maxFail:   3,
		seed:      seed,
		workerID:  int(seed),
		progress:  progress,
	}
}

// Escaped reports how many successful escape steps this cycle performed.
func (c *Cycle) Escaped() int { return c.escaped }

// Recorded reports how many distinct minima this cycle recorded.
func (c *Cycle) Recorded() int { return c.recorded }

func isConverged(status string) bool {
	return strings.HasPrefix(status, "converged")
}

// acceptable reports whether the DLS run reached a usable stopping point: it
// converged, or ran out of iterations while producing a finite, sane merit.
// A truly broken solve (all rays missing, degenerate evaluation) shows up as
// an absurdly large merit and is rejected.
func (c *Cycle) acceptable(status string, x []float64) bool {
	if !(isConverged(status) || status == "max_iterations") {
		return false
	}
	m := c.wrapper.innerMerit(x)
	return !math.IsNaN(m) && !math.IsInf(m, 0) && m < 1e12
}

// extractX pulls the final (After) variable vector out of a DLS result.
func extractX(r dls.Result) []float64 {
	x := make([]float64, len(r.Variables))
	for i, vs := range r.Variables {
		x[i] = vs.After
	}
	return x
}

// perturb applies a deterministic perturbation of ±amplitude of the variable
// range, derived from the seed and a counter, so each worker explores a
// distinct neighbourhood while remaining reproducible.
func (c *Cycle) perturb(x []float64, counter int, amplitude float64) []float64 {
	out := make([]float64, len(x))
	copy(out, x)
	for j, vi := range c.wrapper.inner.Variables() {
		scale := vi.Max - vi.Min
		if scale <= 0 {
			scale = 1.0
		}
		r := float64((c.seed+int64(counter)+1)*(int64(j)+1)%97) / 97.0
		out[j] += (r - 0.5) * 2 * amplitude * scale
		// Clamp to the variable bounds.
		if out[j] < vi.Min {
			out[j] = vi.Min
		} else if out[j] > vi.Max {
			out[j] = vi.Max
		}
	}
	return out
}

// restartPerturb is the normalised-amplitude nudge applied to the starting
// point of the next escape DLS. Starting exactly on a recorded minimum gives a
// zero gradient (the escape bump is flat there), so a small offset is needed
// for the escape to exert a push.
const restartPerturb = 0.1

// initialPerturb is the normalised-amplitude spread applied to each worker's
// initial state so parallel workers explore different neighbourhoods.
const initialPerturb = 0.01

// Run performs the escape loop starting from x0. It returns the final point
// (the last converged minimum) and its unescaped merit.
func (c *Cycle) Run(x0 []float64) ([]float64, float64) {
	currentX := make([]float64, len(x0))
	copy(currentX, x0)
	bestX := make([]float64, len(x0))
	copy(bestX, x0)
	bestMerit := c.wrapper.innerMerit(x0)

	failures := 0
	repeatStreak := 0
	restartAmp := restartPerturb
	for cyc := 0; cyc < c.maxCycles; cyc++ {
		// Step 1: escape DLS. Push away from every recorded minimum.
		c.wrapper.SetEscapes(c.store.All())
		c.wrapper.SetStartX(currentX)
		escRes := dls.Solve(c.wrapper)
		escapedX := extractX(escRes)
		if !c.acceptable(escRes.Status, escapedX) {
			failures++
			c.progress.Logf("cycle %d worker %d  escape-DLS rejected (%s)", cyc, c.workerID, escRes.Status)
			if failures >= c.maxFail {
				break
			}
			currentX = c.perturb(currentX, cyc, restartAmp)
			continue
		}
		c.progress.Logf("cycle %d worker %d  escape-DLS %s merit=%.6e", cyc, c.workerID, escRes.Status, c.wrapper.innerMerit(escapedX))

		// Step 2: clean DLS. Remove escapes and converge to the true minimum.
		c.wrapper.SetEscapes(nil)
		c.wrapper.SetStartX(escapedX)
		cleanRes := dls.Solve(c.wrapper)
		trueX := extractX(cleanRes)
		if !c.acceptable(cleanRes.Status, trueX) {
			failures++
			c.progress.Logf("cycle %d worker %d  clean-DLS rejected (%s)", cyc, c.workerID, cleanRes.Status)
			if failures >= c.maxFail {
				break
			}
			currentX = c.perturb(escapedX, cyc, restartAmp)
			continue
		}
		c.progress.Logf("cycle %d worker %d  clean-DLS  %s merit=%.6e", cyc, c.workerID, cleanRes.Status, c.wrapper.innerMerit(trueX))

		failures = 0
		c.escaped++

		trueMerit := c.wrapper.innerMerit(trueX)

		if c.store.IsNew(trueX) {
			idx := c.store.Add(Point{X: trueX, Merit: trueMerit})
			c.recorded++
			c.progress.Logf("cycle %d worker %d  NEW minimum [%d] merit=%.6e", cyc, c.workerID, idx, trueMerit)
			repeatStreak = 0
			restartAmp = restartPerturb
		} else {
			_, nearest := c.store.FindNearest(trueX)
			p := c.store.Strengthen(nearest)
			c.progress.Logf("cycle %d worker %d  repeat -> minimum %d strengthened h=%.4g w=%.4g", cyc, c.workerID, nearest, p.H, p.W)
			// Repeatedly returning to the same minimum: strengthen the escape
			// (done above) and push the next restart farther out so the
			// escape DLS starts on a steeper part of the bump.
			repeatStreak++
			if repeatStreak > 1 {
				restartAmp *= 2
				if restartAmp > 0.5 {
					restartAmp = 0.5
				}
			}
		}

		if trueMerit < bestMerit {
			bestMerit = trueMerit
			copy(bestX, trueX)
		}

		// Nudge away from the freshly recorded minimum: the escape bump is
		// flat at its own centre, so restarting exactly there would stall.
		currentX = c.perturb(trueX, cyc, restartAmp)
	}

	return bestX, bestMerit
}
