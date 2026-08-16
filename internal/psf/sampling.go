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

// DefaultImageGrid returns a square image-plane grid spec centred on (cx, cy)
// sized to cover both the diffraction core and the geometric spot. When
// halfWidth is > 0 it overrides the auto-sized half-extent. The pixel count
// starts from the caller's gridSize (default 64) and is raised as needed so the
// diffraction core stays resolved: over a half-width of ~4× the Airy radius the
// default grid already resolves it (res ≈ Airy/8), but when the geometric spot
// dominates the window (fast or aberrated systems) the requested grid would
// leave the Airy core smaller than a pixel — under-resolving the ideal
// (diffraction-limited) reference PSF core and making the peak-ratio Strehl
// unreliable (it can exceed 1). The grid is therefore auto-enlarged to enforce
// res ≤ Airy/2, so the ideal peak is measured correctly and Strehl ≤ 1.
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
	n := gridSize
	if airy > 0 && res > airy/2 {
		need := int(math.Ceil(2 * half / (airy / 2)))
		if need > n {
			n = need
		}
	}
	if n < 16 {
		n = 16
	}
	if n > 2048 {
		n = 2048
	}
	res = 2 * half / float64(n)
	return ImageGridSpec{
		NX: n, NY: n,
		X0: cx - half, Y0: cy - half,
		DX: res, DY: res,
	}
}
