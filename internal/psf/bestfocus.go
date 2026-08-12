package psf

import "math"

// BestFocusShift returns the image-plane shift δ (mm) that minimizes the RMS
// geometric spot radius of the samples evaluated at z = planeZ + δ. When the
// image plane is not at the field's best focus (field curvature, defocus), a
// fixed-plane PSF is defocus-dominated and its peak-ratio Strehl is not a
// wavefront-quality number; shifting the evaluation plane by δ before the
// Huygens integral recovers it. The one-dimensional minimization uses
// golden-section search over the same intensity-weighted spot-RMS objective as
// the wavefront best-fit-sphere fit (ImagePlaneSpot), so psf --best-focus and
// the wavefront command's best focus agree by construction.
func BestFocusShift(samples []WavefrontSample, planeZ float64) float64 {
	if len(samples) < 4 {
		return 0
	}
	b := math.Max(2*math.Abs(planeZ), 1.0)
	return minimize1D(func(d float64) float64 {
		_, _, rms := ImagePlaneSpot(samples, planeZ+d)
		return rms
	}, -b, b)
}

// minimize1D finds the minimizer of f over [lo, hi] by golden-section search.
func minimize1D(f func(float64) float64, lo, hi float64) float64 {
	const resphi = 2 - 1.618033988749895
	a, b := lo, hi
	c := a + resphi*(b-a)
	d := b - resphi*(b-a)
	fc, fd := f(c), f(d)
	for iter := 0; iter < 120; iter++ {
		if math.Abs(b-a) < 1e-12*(1+math.Abs(b)+math.Abs(a)) {
			break
		}
		if fc < fd {
			b, d = d, c
			fd = fc
			c = a + resphi*(b-a)
			fc = f(c)
		} else {
			a, c = c, d
			fc = fd
			d = b - resphi*(b-a)
			fd = f(d)
		}
	}
	if fc < fd {
		return c
	}
	return d
}
