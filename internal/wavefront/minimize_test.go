package wavefront

import (
	"math"
	"testing"
)

func TestMinimize1D(t *testing.T) {
	// f(x) = (x-0.5)^2 : min at 0.5
	got := minimize1D(func(x float64) float64 {
		return (x - 0.5) * (x - 0.5)
	}, -10, 10)
	if math.Abs(got-0.5) > 1e-6 {
		t.Errorf("minimize1D quadratic = %v, want 0.5", got)
	}
	// f(x) = (x+3)^2 : min at -3
	got = minimize1D(func(x float64) float64 {
		return (x + 3) * (x + 3)
	}, -100, 100)
	if math.Abs(got+3) > 1e-4 {
		t.Errorf("minimize1D quadratic2 = %v, want -3", got)
	}
	// asymmetric flat-ish: min at 0
	got = minimize1D(func(x float64) float64 {
		return math.Abs(x) + 0.01*x*x
	}, -50, 50)
	if math.Abs(got) > 1e-4 {
		t.Errorf("minimize1D abs = %v, want ~0", got)
	}
}
