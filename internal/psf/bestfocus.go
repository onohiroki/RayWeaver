package psf

import (
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

// BestFocusShift returns the image-plane shift δ (mm) that maximizes the
// coherent PSF peak — i.e. the plane at which the peak-ratio Strehl is largest.
//
// A geometric spot-RMS best focus (the historic definition) is a good proxy
// only while the wavefront is close to diffraction limited. For strongly
// aberrated fields it picks the medial plane (circle of least confusion) that
// balances the transverse blur, whereas the coherent peak sits elsewhere, so
// the spot-RMS plane can have a *lower* Strehl than the input plane. This
// objective instead evaluates the on-axis vector Huygens sum (the same Jones-
// tracked integral ComputeField performs, at the trial plane's intensity
// centroid) and returns the plane that maximizes it. A geometric spot-RMS focus
// seeds the search because the through-focus coherent peak is oscillatory
// (depth-of-focus lobes) and its global maximum must be bracketed by a coarse
// scan before a local refinement.
//
// nImage is the image-space refractive index and wavelength the (mm)
// wavelength. nImage <= 0 or wavelength <= 0 falls back to the geometric
// spot-RMS shift (the coherent objective is undefined without a phase
// reference).
func BestFocusShift(samples []WavefrontSample, planeZ, nImage, wavelength float64) float64 {
	if len(samples) < 4 {
		return 0
	}
	seed := spotRMSBestFocus(samples, planeZ)
	if nImage <= 0 || wavelength <= 0 {
		return seed
	}
	dof := depthOfFocus(samples, planeZ, nImage, wavelength)
	if dof <= 0 || math.IsInf(dof, 0) || math.IsNaN(dof) {
		return seed
	}

	// Coarse global scan: the through-focus coherent peak oscillates on the
	// depth-of-focus scale, so scan a wide window around the geometric seed at
	// a step that resolves one lobe, then refine within one step.
	const lobes = 16.0
	step := dof / 8.0
	span := lobes * dof
	bestD := seed
	bestV := coherentFocusPeak(samples, planeZ, seed, nImage, wavelength)
	steps := int(math.Ceil(span / step))
	for i := -steps; i <= steps; i++ {
		d := seed + float64(i)*step
		if v := coherentFocusPeak(samples, planeZ, d, nImage, wavelength); v > bestV {
			bestV, bestD = v, d
		}
	}
	// Local golden-section refinement (maximize = minimize the negated peak).
	return minimize1D(func(d float64) float64 {
		return -coherentFocusPeak(samples, planeZ, d, nImage, wavelength)
	}, bestD-step, bestD+step)
}

// spotRMSBestFocus is the geometric spot-RMS minimum: the shift that makes the
// intensity-weighted transverse RMS radius about the centroid smallest. It
// seeds the coherent search and is the fallback when the coherent objective is
// undefined.
func spotRMSBestFocus(samples []WavefrontSample, planeZ float64) float64 {
	// Reference surface Z: all samples sit on the reference surface.
	b := math.Max(2*math.Abs(planeZ), 1.0)
	refZ := samples[0].Position.Z
	lo := refZ - planeZ + 1e-3
	if lo < -b {
		lo = -b
	}
	return minimize1D(func(d float64) float64 {
		_, _, rms := ImagePlaneSpot(samples, planeZ+d)
		return rms
	}, lo, b)
}

// depthOfFocus returns the diffraction depth of focus λ/(n·NA²) (mm) at the
// given image plane. It is the natural scale of the through-focus coherent lobe
// and therefore the scan step unit.
func depthOfFocus(samples []WavefrontSample, planeZ, nImage, wavelength float64) float64 {
	cx, cy, _ := ImagePlaneSpot(samples, planeZ)
	na := ComputeImageNA(samples, types.Vec3{X: cx, Y: cy, Z: planeZ}, nImage)
	if na <= 1e-6 {
		return 0
	}
	return wavelength / (nImage * na * na)
}

// coherentFocusPeak evaluates the on-axis vector Huygens sum at the trial image
// plane z = planeZ + delta, at the plane's intensity centroid:
//
//	| Σ_j (K_j·A_j / R_j) · E_j · e^{i k (OPL_j + n·R_j)} |²
//
// with R_j = |F − q_j| and the obliquity K_j = (1 + s_j·R̂_j)/2. It is exactly
// the field ComputeField places at the point F (up to the common 1/λ factor),
// so its maximizer over the plane is the plane of greatest PSF peak. A perfect
// converging wave has all phases equal there and peaks at its focus.
func coherentFocusPeak(samples []WavefrontSample, planeZ, delta, nImage, wavelength float64) float64 {
	z := planeZ + delta
	cx, cy, _ := ImagePlaneSpot(samples, z)
	focus := types.Vec3{X: cx, Y: cy, Z: z}
	k := 2 * math.Pi / wavelength
	var ex, ey, ez complex128
	for _, s := range samples {
		Rvec := focus.Subtract(s.Position)
		R := Rvec.Length()
		if R < 1e-9 {
			continue
		}
		Rhat := Rvec.Scale(1 / R)
		ob := 0.5 * (1 + s.Direction.Dot(Rhat))
		if ob < 0 {
			ob = 0
		}
		phase := k * (s.OPL + nImage*R)
		w := ob * s.Area / R
		cf := complex(w*math.Cos(phase), w*math.Sin(phase))
		ex += cf * s.Field.X
		ey += cf * s.Field.Y
		ez += cf * s.Field.Z
	}
	return absSq(ex) + absSq(ey) + absSq(ez)
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
