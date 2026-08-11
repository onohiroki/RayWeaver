package fft

import (
	"math/cmplx"
	"testing"
)

func TestNextPow2(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{1, 1}, {2, 2}, {3, 4}, {4, 4}, {63, 64}, {64, 64}, {65, 128}, {100, 128}, {1000, 1024},
	}
	for _, c := range cases {
		if got := NextPow2(c.in); got != c.want {
			t.Errorf("NextPow2(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestFFT1DRoundTrip verifies a forward+inverse 1D transform returns the input
// scaled by N.
func TestFFT1DRoundTrip(t *testing.T) {
	for _, n := range []int{2, 8, 64, 256} {
		a := make([]complex128, n)
		for i := range a {
			a[i] = complex(float64(i)*0.5, float64(i%3)-1)
		}
		orig := append([]complex128{}, a...)
		FFT1D(a, -1)
		FFT1D(a, +1)
		for i := range a {
			want := orig[i] * complex(float64(n), 0)
			if cmplx.Abs(a[i]-want) > 1e-9*float64(n) {
				t.Errorf("n=%d x[%d] = %v, want %v", n, i, a[i], want)
			}
		}
	}
}

// TestFFT2DRoundTrip verifies a forward+inverse 2D transform returns the input
// scaled by N².
func TestFFT2DRoundTrip(t *testing.T) {
	for _, n := range []int{2, 16, 64} {
		g := make([]complex128, n*n)
		for j := 0; j < n; j++ {
			for i := 0; i < n; i++ {
				g[j*n+i] = complex(float64(j+i*2)*0.25, float64(j-i)*0.1)
			}
		}
		orig := append([]complex128{}, g...)
		FFT2D(g, n, -1)
		FFT2D(g, n, +1)
		scale := complex(float64(n*n), 0)
		for i := range g {
			want := orig[i] * scale
			if cmplx.Abs(g[i]-want) > 1e-9*float64(n*n) {
				t.Errorf("n=%d x[%d] = %v, want %v", n, i, g[i], want)
			}
		}
	}
}

// TestFFT2DImpulse verifies the transform of a delta at the origin is a
// constant: FFT{δ} = 1 for all frequencies.
func TestFFT2DImpulse(t *testing.T) {
	n := 32
	g := make([]complex128, n*n)
	g[0] = 1
	FFT2D(g, n, -1)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			if cmplx.Abs(g[j*n+i]-1) > 1e-12 {
				t.Fatalf("FFT{δ}(%d,%d) = %v, want 1", i, j, g[j*n+i])
			}
		}
	}
}

// TestFFT2DConstant verifies the transform of a constant array is a single DC
// impulse: FFT{1} = N² at frequency 0 and 0 elsewhere.
func TestFFT2DConstant(t *testing.T) {
	n := 16
	g := make([]complex128, n*n)
	for i := range g {
		g[i] = 1
	}
	FFT2D(g, n, -1)
	if cmplx.Abs(g[0]-complex(float64(n*n), 0)) > 1e-9 {
		t.Errorf("DC = %v, want %v", g[0], n*n)
	}
	for i := 1; i < len(g); i++ {
		if cmplx.Abs(g[i]) > 1e-9 {
			t.Errorf("non-DC coefficient %d = %v, want 0", i, g[i])
		}
	}
}