package spectral

import (
	"math"
	"testing"
)

func TestD65KnownValues(t *testing.T) {
	c := D65()
	if c.MinNM() != 300 || c.MaxNM() != 830 {
		t.Errorf("D65 range = [%v, %v], want [300, 830]", c.MinNM(), c.MaxNM())
	}
	cases := []struct {
		nm   float64
		want float64
	}{
		{400, 60.5904},
		{550, 86.3027},
		{650, 85.7429},
		{700, 84.4136},
	}
	for _, cs := range cases {
		if got := c.Weight(cs.nm * 1e-6); math.Abs(got-cs.want) > 1e-9 {
			t.Errorf("D65(%v nm) = %v, want %v", cs.nm, got, cs.want)
		}
	}
}

func TestD65OutsideRange(t *testing.T) {
	c := D65()
	if got := c.Weight(200e-6); got != 0 {
		t.Errorf("D65(200nm) = %v, want 0 (below range)", got)
	}
	if got := c.Weight(900e-6); got != 0 {
		t.Errorf("D65(900nm) = %v, want 0 (above range)", got)
	}
}

func TestInterpolation(t *testing.T) {
	c := NewCurve([]float64{400, 500, 600}, []float64{0, 100, 50})
	if got := c.Weight(450e-6); math.Abs(got-50) > 1e-9 {
		t.Errorf("midpoint 450nm = %v, want 50 (linear interp)", got)
	}
	if got := c.Weight(550e-6); math.Abs(got-75) > 1e-9 {
		t.Errorf("midpoint 550nm = %v, want 75 (linear interp)", got)
	}
	if got := c.Weight(500e-6); got != 100 {
		t.Errorf("sample 500nm = %v, want 100", got)
	}
}

func TestUnsortedAndDuplicate(t *testing.T) {
	c := NewCurve([]float64{600, 400, 500, 400}, []float64{50, 10, 20, 99})
	if got := c.Weight(400e-6); got != 99 {
		t.Errorf("duplicate 400nm keeps last value: got %v, want 99", got)
	}
	if got := c.Weight(450e-6); math.Abs(got-59.5) > 1e-9 {
		t.Errorf("sorted 450nm interp = %v, want 59.5", got)
	}
}

func TestFlat(t *testing.T) {
	c := Flat(400, 700)
	for _, wl := range []float64{400, 550, 700} {
		if got := c.Weight(wl * 1e-6); got != 1 {
			t.Errorf("Flat(%v nm) = %v, want 1", wl, got)
		}
	}
	if got := c.Weight(350e-6); got != 0 {
		t.Errorf("Flat(350 nm) = %v, want 0 (outside range)", got)
	}
}