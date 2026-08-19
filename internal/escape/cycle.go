package escape

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/hiroki/rayweaver/internal/dls"
)

// Cycle runs the two-step escape optimisation loop for one worker:
//
//  1. DLS with escape functions active (pushes away from known minima)
//  2. DLS with escapes cleared (converges to the true local minimum)
//
// The true minimum is recorded or, if it matches a known one, that minimum's
// escape is strengthened (and its stored X/Merit replaced when the new point
// is better) and the restart offset grows so the next escape run starts
// farther from the trap.
type Cycle struct {
	wrapper   *Wrapper
	store     *Store
	params    Params
	maxCycles int
	maxFail   int
	seed      int64
	workerID  int
	progress  *Progress
	deadline  time.Time
	ctx       context.Context
	hardStop  <-chan struct{}
	escaped   int
	recorded  int
	stopped   bool
}

// NewCycle creates an escape cycle bound to a wrapper and shared store.
// progress may be nil to disable verbose reporting. A zero deadline disables
// the time budget (unlimited runtime); a nil context and a nil hardStop are
// ignored.
func NewCycle(wrapper *Wrapper, store *Store, params Params, maxCycles int, seed int64, progress *Progress, deadline time.Time, ctx context.Context, hardStop <-chan struct{}) *Cycle {
	return &Cycle{
		wrapper:   wrapper,
		store:     store,
		params:    params,
		maxCycles: maxCycles,
		maxFail:   3,
		seed:      seed,
		workerID:  int(seed),
		progress:  progress,
		deadline:  deadline,
		ctx:       ctx,
		hardStop:  hardStop,
	}
}

// Escaped reports how many successful escape steps this cycle performed.
func (c *Cycle) Escaped() int { return c.escaped }

// Recorded reports how many distinct minima this cycle recorded.
func (c *Cycle) Recorded() int { return c.recorded }

// StoppedByTime reports whether the cycle was cut short by the time budget
// (not by an external interrupt or a hard stop).
func (c *Cycle) StoppedByTime() bool { return c.stopped && !c.ctxDone() && !hardStopped(c.hardStop) }

// Interrupted reports whether the shared context was cancelled or the hard
// stop fired while the cycle ran (SIGINT/SIGTERM handled at the command layer).
func (c *Cycle) Interrupted() bool { return c.ctxDone() || hardStopped(c.hardStop) }

// ctxDone reports whether the shared stop context has been cancelled.
func (c *Cycle) ctxDone() bool {
	if c.ctx == nil {
		return false
	}
	select {
	case <-c.ctx.Done():
		return true
	default:
		return false
	}
}

// hardStopped reports whether the mid-solve interrupt channel has been closed.
func hardStopped(hardStop <-chan struct{}) bool {
	if hardStop == nil {
		return false
	}
	select {
	case <-hardStop:
		return true
	default:
		return false
	}
}

// timeUp reports whether the shared wall-clock budget has been exhausted or
// the shared stop context has been cancelled.
func (c *Cycle) timeUp() bool {
	return (!c.deadline.IsZero() && time.Now().After(c.deadline)) || c.ctxDone()
}

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
		if c.timeUp() {
			event := "timeout"
			if c.ctxDone() {
				event = "interrupted"
			}
			c.progress.Event(event, map[string]any{
				"worker":     c.workerID,
				"cycle":      cyc,
				"max_cycles": c.maxCycles,
			})
			c.stopped = true
			break
		}
		// Step 1: escape DLS. Push away from every recorded minimum.
		c.wrapper.SetEscapes(c.store.All())
		c.wrapper.SetStartX(currentX)
		c.wrapper.SetPhase(PhaseEscape)
		c.wrapper.SetStop(c.hardStop)
		escRes := dls.Solve(c.wrapper)
		escapedX := extractX(escRes)
		if escRes.Status == dls.StatusInterrupted {
			c.recordInterrupted(escRes, cyc, "escape_dls")
			c.stopped = true
			break
		}
		if !c.acceptable(escRes.Status, escapedX) {
			failures++
			c.progress.Event("cycle", map[string]any{
				"cycle":      cyc,
				"worker":     c.workerID,
				"phase":      "escape_dls",
				"status":     "rejected",
				"dls_status": escRes.Status,
			})
			if failures >= c.maxFail {
				break
			}
			currentX = c.perturb(currentX, cyc, restartAmp)
			continue
		}
		c.progress.Event("cycle", map[string]any{
			"cycle":      cyc,
			"worker":     c.workerID,
			"phase":      "escape_dls",
			"status":     "accepted",
			"dls_status": escRes.Status,
			"merit":      c.wrapper.innerMerit(escapedX),
		})

		// Step 1.5 (optional): power-preserving glass phase. Lock every variable
		// except the glass dispersions (via the inner glassPhaseable), switch to
		// the colour-only merit, and hold the element powers fixed, so the glass
		// is rebalanced while the layout stays put. This runs between the escape
		// (layout exploration) and the clean DLS (full refinement). The result
		// feeds the clean DLS start; the clean solution is what the store
		// records, so the escape distance/store stay in the full variable-space
		// dimension regardless of the phase's reduced active set.
		cleanStart := escapedX
		if c.wrapper.GlassPhaseEnabled() {
			c.wrapper.SetEscapes(nil)
			c.wrapper.SetStartX(escapedX)
			c.wrapper.SetPhase(PhaseGlassSolve)
			c.wrapper.SetStop(c.hardStop)
			glassRes := dls.Solve(c.wrapper)
			glassX := extractX(glassRes)
			if glassRes.Status == dls.StatusInterrupted {
				c.recordInterrupted(glassRes, cyc, "glass_dls")
				c.stopped = true
				break
			}
			if c.acceptable(glassRes.Status, glassX) {
				cleanStart = glassX
				c.progress.Event("cycle", map[string]any{
					"cycle":      cyc,
					"worker":     c.workerID,
					"phase":      "glass_dls",
					"status":     "accepted",
					"dls_status": glassRes.Status,
					"merit":      c.wrapper.innerMerit(glassX),
				})
			} else {
				c.progress.Event("cycle", map[string]any{
					"cycle":      cyc,
					"worker":     c.workerID,
					"phase":      "glass_dls",
					"status":     "rejected",
					"dls_status": glassRes.Status,
				})
			}
		}

		// Step 2: clean DLS. Remove escapes and converge to the true minimum.
		c.wrapper.SetEscapes(nil)
		cleanStartCopy := make([]float64, len(cleanStart))
		copy(cleanStartCopy, cleanStart)
		c.wrapper.SetStartX(cleanStartCopy)
		c.wrapper.SetPhase(PhaseClean) // leaves the glass phase (restores the variables)
		c.wrapper.SetStop(c.hardStop)
		cleanRes := dls.Solve(c.wrapper)
		trueX := extractX(cleanRes)
		if cleanRes.Status == dls.StatusInterrupted {
			c.recordInterrupted(cleanRes, cyc, "clean_dls")
			c.stopped = true
			break
		}
		if !c.acceptable(cleanRes.Status, trueX) {
			failures++
			c.progress.Event("cycle", map[string]any{
				"cycle":      cyc,
				"worker":     c.workerID,
				"phase":      "clean_dls",
				"status":     "rejected",
				"dls_status": cleanRes.Status,
			})
			if failures >= c.maxFail {
				break
			}
			currentX = c.perturb(cleanStart, cyc, restartAmp)
			continue
		}
		c.progress.Event("cycle", map[string]any{
			"cycle":      cyc,
			"worker":     c.workerID,
			"phase":      "clean_dls",
			"status":     "accepted",
			"dls_status": cleanRes.Status,
			"merit":      c.wrapper.innerMerit(trueX),
		})

		failures = 0
		c.escaped++

		trueMerit := c.wrapper.innerMerit(trueX)

		if c.store.IsNew(trueX) {
			idx := c.store.Add(Point{X: trueX, Merit: trueMerit})
			c.recorded++
			c.progress.Event("minimum", map[string]any{
				"cycle":  cyc,
				"worker": c.workerID,
				"kind":   "new",
				"index":  idx,
				"merit":  trueMerit,
			})
			repeatStreak = 0
			restartAmp = restartPerturb
		} else {
			_, nearest := c.store.FindNearest(trueX)
			// Strengthen the escape bump first so the recorded H/W are the
			// grown values, then replace the stored X/Merit when the new point
			// is better.
			c.store.Strengthen(nearest)
			if _, improved := c.store.Replace(nearest, Point{X: trueX, Merit: trueMerit}); improved {
				c.progress.Event("minimum", map[string]any{
					"cycle":  cyc,
					"worker": c.workerID,
					"kind":   "improved",
					"index":  nearest,
					"merit":  trueMerit,
				})
			} else {
				c.progress.Event("minimum", map[string]any{
					"cycle":  cyc,
					"worker": c.workerID,
					"kind":   "repeat",
					"index":  nearest,
					"merit":  trueMerit,
				})
			}
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

// recordInterrupted records the best point found so far by a DLS solve that
// was aborted mid-iteration by the hard stop. The point is a partial solution,
// not a converged local minimum, but preserving it means an interrupted run
// never loses its best work. A solve stopped before completing any iteration
// (or yielding a finite merit) has nothing worth keeping and is skipped.
func (c *Cycle) recordInterrupted(res dls.Result, cyc int, phase string) {
	if res.Iterations <= 0 {
		c.progress.Event("cycle", map[string]any{
			"cycle":      cyc,
			"worker":     c.workerID,
			"phase":      phase,
			"status":     "interrupted",
			"dls_status": res.Status,
		})
		return
	}
	x := extractX(res)
	m := c.wrapper.innerMerit(x)
	if math.IsNaN(m) || math.IsInf(m, 0) || m >= 1e12 {
		return
	}
	c.progress.Event("cycle", map[string]any{
		"cycle":      cyc,
		"worker":     c.workerID,
		"phase":      phase,
		"status":     "interrupted",
		"dls_status": res.Status,
		"merit":      m,
	})
	if c.store.IsNew(x) {
		idx := c.store.Add(Point{X: x, Merit: m})
		c.recorded++
		c.progress.Event("minimum", map[string]any{
			"cycle":  cyc,
			"worker": c.workerID,
			"kind":   "interrupted",
			"index":  idx,
			"merit":  m,
		})
		return
	}
	_, nearest := c.store.FindNearest(x)
	c.store.Strengthen(nearest)
	if _, improved := c.store.Replace(nearest, Point{X: x, Merit: m}); improved {
		c.progress.Event("minimum", map[string]any{
			"cycle":  cyc,
			"worker": c.workerID,
			"kind":   "improved",
			"index":  nearest,
			"merit":  m,
		})
	} else {
		c.progress.Event("minimum", map[string]any{
			"cycle":  cyc,
			"worker": c.workerID,
			"kind":   "repeat",
			"index":  nearest,
			"merit":  m,
		})
	}
}
