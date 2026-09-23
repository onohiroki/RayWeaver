package optimize

import "testing"

// TestDominantModeTieBreakDeterministic guards the P5 fix: dominantMode
// iterated a map and kept the first strict maximum, so equal (or crossing)
// weights picked an arbitrary winner and currentBackFocusType /
// opt_results.active_mode flipped between identical runs.
func TestDominantModeTieBreakDeterministic(t *testing.T) {
	weights := map[string]float64{"explore": 0.5, "local": 0.5}
	first := dominantMode(weights)
	for i := 0; i < 200; i++ {
		if got := dominantMode(weights); got != first {
			t.Fatalf("dominantMode is not deterministic: %q != %q", got, first)
		}
	}
	if first != "explore" {
		t.Errorf("tie-break = %q, want the lexicographically first mode \"explore\"", first)
	}
	// A strict maximum still wins over the tie-break.
	if got := dominantMode(map[string]float64{"explore": 0.4, "local": 0.6}); got != "local" {
		t.Errorf("dominantMode = %q, want \"local\"", got)
	}
}
