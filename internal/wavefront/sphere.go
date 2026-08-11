package wavefront

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/types"
)

// Sphere is the best-focus reference sphere described by its focus point. The
// best-focus shift δ minimizes the geometric spot RMS: the image plane is
// moved by δ along the image-plane normal so the beam's RMS spot radius about
// its intensity centroid is smallest. This is robust against the
// launch-geometry artifacts that affect angle-field OPL values.
type Sphere struct {
	// Center is the best-focus point in the samples' frame (the same frame as
	// the traced wavefront positions and directions).
	CenterX, CenterY, CenterZ float64
	// Radius is the axial distance from the reference-surface vertex to the
	// focus (CenterZ).
	Radius float64
	// ShiftMM is the image-plane shift δ from the current focus to the
	// best focus, along the image-plane normal (mm).
	ShiftMM float64
	// SpotRMS is the RMS spot radius at best focus (mm).
	SpotRMS float64
	// RMSResidual is the wavefront OPD residual RMS at best focus, in mm.
	RMSResidual float64
}

// Center returns the best-focus point.
func (s Sphere) Center() types.Vec3 {
	return types.Vec3{X: s.CenterX, Y: s.CenterY, Z: s.CenterZ}
}

// FitSphereShift finds the best-focus shift δ that minimizes the geometric
// RMS spot radius of the samples propagated to the flat image plane moved by δ
// along its normal. The samples carry their emergent Direction and Intensity;
// planeZ is the current image-plane position (all in the samples' frame). The
// one-dimensional minimization uses golden-section search.
func FitSphereShift(samples []psf.WavefrontSample, planeZ float64) (Sphere, error) {
	if len(samples) < 4 {
		return Sphere{}, fmt.Errorf("sphere fit needs >= 4 samples, got %d", len(samples))
	}
	b := math.Max(2*math.Abs(planeZ), 1.0)
	delta := minimize1D(func(d float64) float64 {
		_, rms := spotAtShift(samples, planeZ, d)
		return rms
	}, -b, b)
	centroid, spotRMS := spotAtShift(samples, planeZ, delta)

	return Sphere{
		CenterX: centroid.X,
		CenterY: centroid.Y,
		CenterZ: centroid.Z,
		Radius:  centroid.Z,
		ShiftMM: delta,
		SpotRMS: spotRMS,
	}, nil
}

// spotAtShift propagates the samples to the image plane at z = planeZ + delta
// and returns the intensity-weighted centroid and RMS spot radius about it.
func spotAtShift(samples []psf.WavefrontSample, planeZ, delta float64) (types.Vec3, float64) {
	var sw, wx, wy float64
	for _, s := range samples {
		if math.Abs(s.Direction.Z) < 1e-9 {
			continue
		}
		t := (planeZ + delta - s.Position.Z) / s.Direction.Z
		wx += (s.Position.X + s.Direction.X*t) * s.Intensity
		wy += (s.Position.Y + s.Direction.Y*t) * s.Intensity
		sw += s.Intensity
	}
	if sw <= 0 {
		return types.Vec3{}, 0
	}
	cx, cy := wx/sw, wy/sw
	var ss float64
	for _, s := range samples {
		if math.Abs(s.Direction.Z) < 1e-9 {
			continue
		}
		t := (planeZ + delta - s.Position.Z) / s.Direction.Z
		x := s.Position.X + s.Direction.X*t - cx
		y := s.Position.Y + s.Direction.Y*t - cy
		ss += (x*x + y*y) * s.Intensity
	}
	return types.Vec3{X: cx, Y: cy, Z: planeZ + delta}, math.Sqrt(ss / sw)
}

// lineDist returns |P - (F0 + δ·dir)|.
func lineDist(p, F0, dir types.Vec3, delta float64) float64 {
	dx := p.X - F0.X - delta*dir.X
	dy := p.Y - F0.Y - delta*dir.Y
	dz := p.Z - F0.Z - delta*dir.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// minimize1D finds the minimizer of f over [lo, hi] by golden-section search.
func minimize1D(f func(float64) float64, lo, hi float64) float64 {
	const resphi = 2 - 1.618033988749895
	a, b := lo, hi
	c := a + resphi*(b-a) // left interior point
	d := b - resphi*(b-a) // right interior point
	fc, fd := f(c), f(d)
	for iter := 0; iter < 120; iter++ {
		if math.Abs(b-a) < 1e-12*(1+math.Abs(b)+math.Abs(a)) {
			break
		}
		if fc < fd {
			// Minimum lies in [a, d].
			b, d = d, c
			fd = fc
			c = a + resphi*(b-a)
			fc = f(c)
		} else {
			// Minimum lies in [c, b].
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
