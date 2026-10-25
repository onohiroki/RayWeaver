package main

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// stopFileEnv names the control file a detached launcher uses to request the
// same staged stop a signal triggers. internal/runctl sets it on Windows, where
// the OS has no SIGTERM; Unix launchers keep sending SIGTERM. Nothing in the
// CLI is written on Unix unless something sets the variable explicitly.
const stopFileEnv = "RAYWEAVE_STOP_FILE"

// stopFilePoll is a variable so the tests can shorten the wait. 200 ms keeps a
// stop request responsive without measurable I/O.
var stopFilePoll = 200 * time.Millisecond

// stageRunner applies each stop stage at most once, in ascending order. A
// signal and the stop file can both request the same stage (a user pressing
// Ctrl+C on a run that a launcher is also stopping), so requests must be
// idempotent: stage 2 is never applied twice, and a late stage-1 request cannot
// rewind the sequence.
type stageRunner struct {
	mu      sync.Mutex
	applied int
	stages  []func()
}

func newStageRunner(stages ...func()) *stageRunner {
	return &stageRunner{stages: stages}
}

// request applies every stage up to n that has not been applied yet and
// returns the range it covered (from is exclusive, to is inclusive), so the
// caller can emit one progress event per stage that actually ran.
func (r *stageRunner) request(n int) (from, to int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	from = r.applied
	for r.applied < n && r.applied < len(r.stages) {
		r.applied++
		if fn := r.stages[r.applied-1]; fn != nil {
			fn()
		}
	}
	return from, r.applied
}

// watchStopFile polls path for stage numbers, one per line, and calls request
// with each one. A missing or unreadable file is ignored: the run simply keeps
// going. The launcher creates the file (empty) before starting the child, so a
// stop requested during startup is not lost.
func watchStopFile(path string, request func(int)) (stop func()) {
	if path == "" {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(stopFilePoll)
		defer ticker.Stop()
		seen := 0
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				b, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				for _, field := range strings.Fields(string(b)) {
					n, err := strconv.Atoi(field)
					if err != nil || n <= seen {
						continue
					}
					seen = n
					request(n)
				}
			}
		}
	}()
	return func() { close(done) }
}
