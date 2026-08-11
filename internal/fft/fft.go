// Package fft provides a small self-contained radix-2 Cooley-Tukey FFT used
// for the OTF/MTF computation from a sampled PSF grid. No external dependency
// is required.
package fft

import "math"

// NextPow2 returns the smallest power of two greater than or equal to n.
func NextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// FFT2D computes the 2D discrete Fourier transform of a square N×N complex
// array in place (row-major, index = y*N + x) and returns it. N must be a
// power of two. sign selects the transform direction: -1 for the forward
// transform with kernel exp(-2πi·k·n/N), +1 for the inverse with kernel
// exp(+2πi·k·n/N). No normalisation is applied; a forward followed by an
// inverse yields the input scaled by N*N.
func FFT2D(data []complex128, N, sign int) []complex128 {
	if N <= 1 || len(data) < N*N {
		return data
	}
	for j := 0; j < N; j++ {
		fft1D(data[j*N:(j+1)*N], sign)
	}
	col := make([]complex128, N)
	for i := 0; i < N; i++ {
		for j := 0; j < N; j++ {
			col[j] = data[j*N+i]
		}
		fft1D(col, sign)
		for j := 0; j < N; j++ {
			data[j*N+i] = col[j]
		}
	}
	return data
}

// FFT1D computes the 1D discrete Fourier transform of a in place (length must
// be a power of two). sign selects the direction (see FFT2D).
func FFT1D(a []complex128, sign int) {
	fft1D(a, sign)
}

// FFTShift2D swaps quadrants of a square N×N array (row-major, index =
// y*N + x) so that the origin (index 0 in each axis) moves to the array
// centre. Applying it twice restores the original order. Used to bring the DC
// coefficient of an FFT result to the centre.
func FFTShift2D(data []complex128, N int) {
	shift := N / 2
	out := make([]complex128, N*N)
	for j := 0; j < N; j++ {
		sj := (j + shift) % N
		for i := 0; i < N; i++ {
			si := (i + shift) % N
			out[j*N+i] = data[sj*N+si]
		}
	}
	copy(data, out)
}

func fft1D(a []complex128, sign int) {
	n := len(a)
	if n <= 1 {
		return
	}
	bitReverse(a)
	for length := 2; length <= n; length <<= 1 {
		angle := float64(sign) * 2 * math.Pi / float64(length)
		wlen := complex(math.Cos(angle), math.Sin(angle))
		half := length >> 1
		for i := 0; i < n; i += length {
			w := complex(1, 0)
			for j := 0; j < half; j++ {
				u := a[i+j]
				v := a[i+j+half] * w
				a[i+j] = u + v
				a[i+j+half] = u - v
				w *= wlen
			}
		}
	}
}

// bitReverse permutes a into bit-reversed index order.
func bitReverse(a []complex128) {
	n := len(a)
	j := 0
	for i := 1; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j &^= bit
			bit >>= 1
		}
		j |= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
}