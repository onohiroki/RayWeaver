package main

import (
	"context"
	"os"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/escape"
	"github.com/hiroki/rayweaver/internal/types"
)

// escapeTail is the second half of an escape/pso run, shared by the
// single-config and multi-config paths: streaming output, the search call,
// minima assembly, the best-solution write-back and the document output.
//
// The two paths differ in how they build the model (factory, fingerprint,
// validation) and in how the best point is applied to the document, so those
// arrive as callbacks. Everything after them — including the streamed and
// one-shot output forms, which must stay identical — is one implementation.
type escapeTail struct {
	input          *types.Input
	command        string
	settings       globalSettings
	progress       *escape.Progress
	factory        func() dls.Model
	fingerprint    func([]float64) []float64
	validateFn     func([]float64, float64, dls.Model) (escape.MinStatus, escape.InvalidReason)
	ctx            context.Context
	hardStop       <-chan struct{}
	gctx           glassPhaseCtx
	debug          bool
	newExplorer    func(*escape.Progress, int64) escape.Explorer
	onRecord       escape.RecordHandler
	saver          *escapeFileSaver
	keepInfeasible bool
	// streamChief reports whether the streamed header carries a chief tail
	// (the run optimises the virtual entrance pupil).
	streamChief     bool
	buildMinimum    func(position, storeIdx int, p escape.Point) types.GlobalMinimum
	buildInfeasible func(position int, p escape.Point) types.GlobalMinimum
	applyBest       func(input *types.Input, res escape.Result)
}

// runEscapeTail runs the search and writes the pipeline document.
func runEscapeTail(t escapeTail) {
	input, command, settings := t.input, t.command, t.settings
	progress, factory := t.progress, t.factory
	onRecord, saver := t.onRecord, t.saver
	buildMinimum := t.buildMinimum

	// Streaming output (default): the settled top-level part of the document is
	// written now and one global_result.minima entry follows each discovered
	// solution, so a run killed mid-way still leaves a readable document.
	// metadata is stamped here, which makes created_at the run start.
	var st *escapeStream
	if enabled, _ := escapeStreamEnabled(settings.config, command); enabled {
		st = newEscapeStream(os.Stdout, escapeStreamTails{chief: t.streamChief})
		inner := onRecord
		onRecord = func(idx int, p escape.Point, isNew bool, version int) {
			if inner != nil {
				inner(idx, p, isNew, version)
			}
			st.record(idx, p, isNew, buildMinimum)
		}
		withOutputMetadata(input, command, subcmdArgs())
		st.header(*input, escape.ReportParams(*settings.config, settings.tuning, factory().Variables()))
	}

	res := escape.ParallelEscape(factory, *settings.config, escape.RunOptions{
		Tuning:      settings.tuning,
		Progress:    progress,
		OnRecord:    onRecord,
		Fingerprint: t.fingerprint,
		ValidateFn:  t.validateFn,
		Context:     t.ctx,
		HardStop:    t.hardStop,
		GlassPhase:  t.gctx.enabled,
		Debug:       t.debug,
		NewPhaseLog: newPhaseLogIfDebug(progress, t.debug),
		NewExplorer: t.newExplorer,
	})
	progress.Event("done", map[string]any{
		"workers":      res.Workers,
		"cycles":       res.Cycles,
		"explorations": res.Explorations,
		"minima":       len(res.Minima),
		"best_merit":   safeF(res.BestMerit),
		"timed_out":    res.TimedOut,
		"interrupted":  res.Interrupted,
	})
	if saver != nil && saver.err != nil {
		progress.Event("error", map[string]any{"message": saver.err.Error()})
	}

	// The streamed records already carry the feasible minima; the one-shot
	// output builds them here, in merit order (buildMinimum is shared, so both
	// forms produce identical entries).
	var minima []types.GlobalMinimum
	if st == nil {
		minima = make([]types.GlobalMinimum, len(res.Minima))
		for i, p := range res.Minima {
			minima[i] = buildMinimum(i, res.MinimaIdx[i], p)
		}
	}

	// Build infeasible basins list.
	infeasibleMinima := make([]types.GlobalMinimum, len(res.InfeasibleBasins))
	for i, p := range res.InfeasibleBasins {
		infeasibleMinima[i] = t.buildInfeasible(i, p)
	}

	t.applyBest(input, res)

	// Discard infeasible basins from output unless --keep-infeasible is set.
	if !t.keepInfeasible {
		infeasibleMinima = nil
	}

	// Streamed run: the feasible minima are already in the document, so only
	// the run outcome, the improved points and the deferred top-level keys
	// remain to be written — configs carries the best solution and must be
	// written after it has been applied, not before the search started.
	if st != nil {
		reportEscape(res, progress)
		st.complete(streamCompletion{
			BestIndex:          bestStreamIndex(res, st),
			BestMerit:          res.BestMerit,
			TimedOut:           res.TimedOut,
			Interrupted:        res.Interrupted,
			InfeasibleBasins:   infeasibleMinima,
			MinimaImprovements: st.improvements(res, buildMinimum),
		}, streamTailKeys(*input, st))
		if err := st.Err(); err != nil {
			progress.Event("error", map[string]any{"message": err.Error()})
			errOut("Error writing streamed escape output: %v", err)
		}
		return
	}

	globalResult := assembleGlobalResult(res, minima, infeasibleMinima)
	reportEscape(res, progress)
	writeGlobalOutput(*input, globalResult, command)
}
