package escape

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Progress is a thread-safe reporter for the escape search. Each event is
// written to every registered writer: the full JSONL stream (AddWriter, used
// for --log FILE) carries the complete structured record, while compact
// writers (AddCompactWriter, used for --verbose on stderr) receive a shortened
// single-line form. Multiple worker goroutines share one Progress; the mutex
// keeps individual lines from interleaving. Every event carries a wall-clock
// timestamp and the seconds since the reporter was created (elapsed).
type Progress struct {
	mu             sync.Mutex
	writers        []io.Writer
	compactWriters []io.Writer
	start          time.Time
}

// NewProgress returns a reporter with no writers (all events are no-ops).
func NewProgress() *Progress {
	return &Progress{start: time.Now()}
}

// AddWriter registers a full JSONL output stream. Events are written to all
// writers in registration order.
func (p *Progress) AddWriter(w io.Writer) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writers = append(p.writers, w)
}

// AddCompactWriter registers a compact (abbreviated) output stream, typically
// the terminal for --verbose. Events are written to all compact writers in
// registration order.
func (p *Progress) AddCompactWriter(w io.Writer) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.compactWriters = append(p.compactWriters, w)
}

// Event writes one structured JSONL line tagged with the event name to every
// registered writer. A nil receiver and an empty writer list are both no-ops.
func (p *Progress) Event(name string, fields map[string]any) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.writers) == 0 && len(p.compactWriters) == 0 {
		return
	}
	m := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		m[k] = v
	}
	m["event"] = name
	m["time"] = time.Now()
	m["elapsed"] = time.Since(p.start).Seconds()
	if len(p.writers) > 0 {
		line := marshalEventLine(m, false)
		for _, w := range p.writers {
			fmt.Fprintln(w, line)
		}
	}
	if len(p.compactWriters) > 0 {
		line := marshalEventLine(m, true)
		for _, w := range p.compactWriters {
			fmt.Fprintln(w, line)
		}
	}
}

// eventKeyOrder is the fixed key presentation order shared by the full and
// compact streams.
var eventKeyOrder = []string{"cycle", "elapsed", "time", "event", "merit", "worker", "index", "kind", "dls_status", "phase", "distance_threshold", "h", "h_mult", "w", "w_mult", "max_cycles", "max_seconds", "workers", "escaped", "recorded", "best_merit", "cycles", "escapes", "minima"}

func inEventKeyOrder(k string) bool {
	for _, kk := range eventKeyOrder {
		if kk == k {
			return true
		}
	}
	return false
}

// marshalEventLine serialises an event map to a single JSON object whose keys
// follow eventKeyOrder. In compact mode that fixed order is the whole output
// (every other key — status, signal, timed_out, interrupted — is dropped),
// elapsed becomes whole minutes under e_min, the wall clock becomes HH:MM:SS
// under t, and floats use 6-significant-figure exponent notation. In full mode
// the original keys and values are kept (elapsed seconds, RFC3339 time,
// full-precision floats) and the remaining keys follow in alphabetical order.
func marshalEventLine(m map[string]any, compact bool) string {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	writeKV := func(key string, val any) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		kq, _ := json.Marshal(key)
		b.Write(kq)
		b.WriteByte(':')
		b.Write(valueJSON(val, compact))
	}
	for _, k := range eventKeyOrder {
		v, ok := m[k]
		if !ok {
			continue
		}
		if compact {
			switch k {
			case "elapsed":
				writeKV("e_min", int64(v.(float64)/60))
				continue
			case "time":
				writeKV("t", v.(time.Time).Format("15:04:05"))
				continue
			}
		}
		writeKV(k, v)
	}
	if !compact {
		var rest []string
		for k := range m {
			if !inEventKeyOrder(k) {
				rest = append(rest, k)
			}
		}
		sort.Strings(rest)
		for _, k := range rest {
			writeKV(k, m[k])
		}
	}
	b.WriteByte('}')
	return b.String()
}

// valueJSON renders a single JSON value. In compact mode floats use
// 6-significant-figure exponent notation; everything else (and all values in
// full mode) uses the standard encoding/json rendering.
func valueJSON(v any, compact bool) []byte {
	if compact {
		if f, ok := v.(float64); ok {
			return []byte(strconv.FormatFloat(f, 'e', 5, 64))
		}
	}
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return data
}
