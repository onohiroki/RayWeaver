package escape

import (
	"sync"
	"testing"
)

// TestStoreRecordConcurrentKeepsOneBestMinimum pins the atomic record decision:
// several workers converging on the same minimum must end with one entry
// carrying the best merit, no matter which of them arrives first.
func TestStoreRecordConcurrentKeepsOneBestMinimum(t *testing.T) {
	s := NewStore(testParams())
	const workers = 64

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// All within Dt (0.1) of each other, so they are one minimum.
			x := []float64{0.50 + float64(i)*1e-6}
			s.Record(Point{X: x, Merit: float64(workers - i)})
		}(i)
	}
	wg.Wait()

	if got := s.Len(); got != 1 {
		t.Fatalf("stored minima = %d, want 1 (concurrent Record inserted duplicates)", got)
	}
	p, _ := s.Best()
	if p.Merit != 1 {
		t.Fatalf("stored merit = %v, want the best (1)", p.Merit)
	}
}

// TestStoreRecordKinds checks the three outcomes and that a repeat still
// strengthens the escape bump, while a new point starts a fresh entry.
func TestStoreRecordKinds(t *testing.T) {
	s := NewStore(testParams())

	idx, kind, improved := s.Record(Point{X: []float64{0.5}, Merit: 5})
	if idx != 0 || kind != "new" || improved {
		t.Fatalf("first Record = (%d, %q, %v), want (0, new, false)", idx, kind, improved)
	}
	before := s.All()[0].H

	idx, kind, improved = s.Record(Point{X: []float64{0.50001}, Merit: 4})
	if idx != 0 || kind != "improved" || !improved {
		t.Fatalf("better repeat = (%d, %q, %v), want (0, improved, true)", idx, kind, improved)
	}
	if p, _ := s.Best(); p.Merit != 4 {
		t.Fatalf("stored merit = %v, want 4", p.Merit)
	}
	if got := s.All()[0].H; got <= before {
		t.Fatalf("repeat did not strengthen the bump: H %v -> %v", before, got)
	}

	idx, kind, improved = s.Record(Point{X: []float64{0.50002}, Merit: 9})
	if idx != 0 || kind != "repeat" || improved {
		t.Fatalf("worse repeat = (%d, %q, %v), want (0, repeat, false)", idx, kind, improved)
	}
	if p, _ := s.Best(); p.Merit != 4 {
		t.Fatalf("worse repeat changed the stored merit to %v", p.Merit)
	}

	idx, kind, improved = s.Record(Point{X: []float64{1.5}, Merit: 1})
	if idx != 1 || kind != "new" || improved {
		t.Fatalf("distant point = (%d, %q, %v), want a second entry", idx, kind, improved)
	}
	if got := s.Len(); got != 2 {
		t.Fatalf("stored minima = %d, want 2", got)
	}
}
