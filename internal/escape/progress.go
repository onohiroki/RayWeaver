package escape

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Progress is a thread-safe JSONL reporter for the escape search. Each event
// is marshalled to a single JSON line and written to every registered writer
// (stderr for --verbose, a file for --log) so the YAML pipeline on stdout
// stays intact. Multiple worker goroutines share one Progress; the mutex
// keeps individual lines from interleaving. Every event carries a wall-clock
// timestamp (time) and the seconds since the reporter was created (elapsed).
type Progress struct {
	mu      sync.Mutex
	writers []io.Writer
	start   time.Time
}

// NewProgress returns a reporter with no writers (all events are no-ops).
func NewProgress() *Progress {
	return &Progress{start: time.Now()}
}

// AddWriter registers an output stream. Events are written to all writers in
// registration order.
func (p *Progress) AddWriter(w io.Writer) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writers = append(p.writers, w)
}

// Event writes one structured JSONL line tagged with the event name. A nil
// receiver and an empty writer list are both no-ops.
func (p *Progress) Event(name string, fields map[string]any) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.writers) == 0 {
		return
	}
	m := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		m[k] = v
	}
	m["event"] = name
	m["time"] = time.Now()
	m["elapsed"] = time.Since(p.start).Seconds()
	data, err := json.Marshal(m)
	if err != nil {
		fmt.Fprintf(p.writers[0], "rayweave[escape]: marshal %q failed: %v\n", name, err)
		return
	}
	for _, w := range p.writers {
		fmt.Fprintln(w, string(data))
	}
}
