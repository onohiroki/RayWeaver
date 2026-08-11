package psf

import (
	"math"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

// imagePlaneZ returns the global Z of the image plane (the last surface).
func imagePlaneZ(surfaces []types.Surface) float64 {
	if len(surfaces) == 0 {
		return 0
	}
	return surfaces[len(surfaces)-1].PhysicalZ
}

// imageSpaceIndex returns the refractive index of the medium immediately
// following the reference surface (the region the wavefront propagates
// through to reach the image plane).
func imageSpaceIndex(surfaces []types.Surface, refSurfaceID int, wavelength float64, gc *glass.Catalog) float64 {
	idx := dls.SurfaceIndex(surfaces, refSurfaceID)
	if idx < 0 || idx >= len(surfaces) {
		return 1
	}
	n, _ := gc.RefractiveIndex(surfaces[idx].Material, wavelength)
	if n <= 0 {
		return 1
	}
	return n
}

// ComputeImageNA returns the image-space numerical aperture: n·sin(half-angle)
// of the cone subtended by the reference-surface footprint at the focus point.
// The cone axis is the line from the focus to the footprint centroid. This
// position-based measure matches the diffraction physics better than the
// emergent-ray cone when the marginal rays overshoot the focus (spherical
// aberration).
func ComputeImageNA(samples []WavefrontSample, focus types.Vec3, nImage float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	cx, cy, cz := 0.0, 0.0, 0.0
	for _, s := range samples {
		cx += s.Position.X
		cy += s.Position.Y
		cz += s.Position.Z
	}
	n := float64(len(samples))
	centroid := types.Vec3{X: cx / n, Y: cy / n, Z: cz / n}
	axis := centroid.Subtract(focus)
	la := axis.Length()
	if la < 1e-12 {
		return 0
	}
	axis = axis.Scale(1 / la)
	maxSin := 0.0
	for _, s := range samples {
		u := s.Position.Subtract(focus)
		lu := u.Length()
		if lu < 1e-12 {
			continue
		}
		cos := u.X/lu*axis.X + u.Y/lu*axis.Y + u.Z/lu*axis.Z
		if cos < -1 {
			cos = -1
		}
		if cos > 1 {
			cos = 1
		}
		sin := math.Sqrt(math.Max(0, 1-cos*cos))
		if sin > maxSin {
			maxSin = sin
		}
	}
	return nImage * maxSin
}

// ImagePlaneSpot computes the intensity-weighted centroid and RMS radius of
// the samples' ray intersections with the flat image plane (the geometric
// spot, useful for sizing the evaluation window).
func ImagePlaneSpot(samples []WavefrontSample, planeZ float64) (cx, cy, rms float64) {
	var sw, wx, wy float64
	for _, s := range samples {
		if math.Abs(s.Direction.Z) < 1e-9 {
			continue
		}
		t := (planeZ - s.Position.Z) / s.Direction.Z
		wx += (s.Position.X + s.Direction.X*t) * s.Intensity
		wy += (s.Position.Y + s.Direction.Y*t) * s.Intensity
		sw += s.Intensity
	}
	if sw <= 0 {
		return 0, 0, 0
	}
	cx, cy = wx/sw, wy/sw
	var ss float64
	for _, s := range samples {
		if math.Abs(s.Direction.Z) < 1e-9 {
			continue
		}
		t := (planeZ - s.Position.Z) / s.Direction.Z
		x := s.Position.X + s.Direction.X*t - cx
		y := s.Position.Y + s.Direction.Y*t - cy
		ss += (x*x + y*y) * s.Intensity
	}
	return cx, cy, math.Sqrt(ss / sw)
}

// AiryRadius returns the first dark ring radius 0.61λ/NA.
func AiryRadius(wavelength, na float64) float64 {
	if na <= 0 {
		return 0
	}
	return 0.61 * wavelength / na
}

// ChiefImagePoint intersects the chief ray with the flat image plane.
func ChiefImagePoint(chiefDir, chiefOrigin types.Vec3, planeZ float64) types.Vec3 {
	if math.Abs(chiefDir.Z) < 1e-9 {
		return chiefOrigin
	}
	t := (planeZ - chiefOrigin.Z) / chiefDir.Z
	return types.Vec3{
		X: chiefOrigin.X + chiefDir.X*t,
		Y: chiefOrigin.Y + chiefDir.Y*t,
		Z: planeZ,
	}
}

// DefaultImageGrid returns a square image-plane grid spec centred on (cx, cy)
// sized to cover both the diffraction core and the geometric spot. When
// halfWidth is > 0 it overrides the auto-sized half-extent. The pixel count
// is the caller's gridSize (default 64): over a half-width of ~4× the Airy
// radius this resolves the diffraction core (res ≈ Airy/8).
func DefaultImageGrid(samples []WavefrontSample, focus types.Vec3, nImage, wavelength float64,
	planeZ, cx, cy, halfWidth float64, gridSize int) ImageGridSpec {
	na := ComputeImageNA(samples, focus, nImage)
	airy := AiryRadius(wavelength, na)
	half := halfWidth
	if half <= 0 {
		_, _, rms := ImagePlaneSpot(samples, planeZ)
		half = math.Max(4*airy, 3*rms)
	}
	if half < 5e-3 {
		half = 5e-3
	}
	res := 2 * half / float64(gridSize)
	return ImageGridSpec{
		NX: gridSize, NY: gridSize,
		X0: cx - half, Y0: cy - half,
		DX: res, DY: res,
	}
}
