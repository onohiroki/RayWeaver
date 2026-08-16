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
// shift delegates to psf.BestFocusShift so the wavefront best-focus reference
// and psf --best-focus evaluate the same spot-RMS objective.
func FitSphereShift(samples []psf.WavefrontSample, planeZ float64) (Sphere, error) {
	if len(samples) < 4 {
		return Sphere{}, fmt.Errorf("sphere fit needs >= 4 samples, got %d", len(samples))
	}
	delta := psf.BestFocusShift(samples, planeZ)
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
	cx, cy, rms := psf.ImagePlaneSpot(samples, planeZ+delta)
	return types.Vec3{X: cx, Y: cy, Z: planeZ + delta}, rms
}

// lineDist returns |P - (F0 + δ·dir)|.
func lineDist(p, F0, dir types.Vec3, delta float64) float64 {
	dx := p.X - F0.X - delta*dir.X
	dy := p.Y - F0.Y - delta*dir.Y
	dz := p.Z - F0.Z - delta*dir.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}
