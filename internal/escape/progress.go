package escape

import (
	"fmt"
	"os"
	"sync"
)

// Progress is a thread-safe verbose reporter for the escape search. When
// enabled it writes progress lines to stderr so the YAML pipeline on stdout
// stays intact. Multiple worker goroutines share one Progress; the mutex
// keeps individual lines from interleaving.
type Progress struct {
	mu      sync.Mutex
	enabled bool
}

// NewProgress returns a disabled reporter.
func NewProgress() *Progress {
	return &Progress{}
}

// SetEnabled toggles verbose output on or off.
func (p *Progress) SetEnabled(enabled bool) {
	if p == nil {
		return
	}
	p.enabled = enabled
}

// Logf writes a formatted progress line to stderr when enabled. A nil
// receiver and a disabled reporter are both no-ops.
func (p *Progress) Logf(format string, args ...any) {
	if p == nil || !p.enabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(os.Stderr, "rayweave[escape]: "+format+"\n", args...)
}
