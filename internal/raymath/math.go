package raymath

import (
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

func Reflect(d, n types.Vec3) types.Vec3 {
	dot := d.Dot(n)
	return d.Subtract(n.Scale(2 * dot))
}

// DegToRad converts an angle from degrees to radians.
func DegToRad(deg float64) float64 {
	return deg * math.Pi / 180.0
}

// RadToDeg converts an angle from radians to degrees.
func RadToDeg(rad float64) float64 {
	return rad * 180.0 / math.Pi
}

// DirectionFromAngle returns a normalized direction in the XY plane at the
// given field angle (degrees), pointing toward +Z.
func DirectionFromAngle(angleDeg float64) types.Vec3 {
	return DirectionFromField(angleDeg, nil)
}

// FieldAzimuth returns the normalized in-plane (dx, dy) azimuth for a field
// direction, defaulting to +Y when the direction is absent or degenerate.
func FieldAzimuth(direction []float64) (dx, dy float64) {
	dx, dy = 0.0, 1.0
	if len(direction) >= 2 {
		norm := math.Hypot(direction[0], direction[1])
		if norm > 0 {
			dx = direction[0] / norm
			dy = direction[1] / norm
		}
	}
	return dx, dy
}

// DirectionFromField returns the object-space ray direction for an angle
// field: (sinθ·dx, sinθ·dy, cosθ) normalized. The in-plane azimuth direction
// is resolved by FieldAzimuth.
func DirectionFromField(angleDeg float64, direction []float64) types.Vec3 {
	rad := DegToRad(angleDeg)
	sinT := math.Sin(rad)
	cosT := math.Cos(rad)
	dx, dy := FieldAzimuth(direction)
	return types.Vec3{X: sinT * dx, Y: sinT * dy, Z: cosT}.Normalize()
}

// GridPoint is a pupil sample coordinate plus its pupil-cell area weight (the
// fraction of the entrance-pupil flux the ray represents).
type GridPoint struct {
	X, Y float64
	Area float64
}

// PupilGrid distributes sample coordinates within the disk of apertureRadius.
// Supported patterns: GridSquare (uniform n×n, rim-trimmed), GridHex (dense
// hex covering the full disk rim, as used by the clear-aperture/beam-envelope
// measurements) and GridPolar (n rings × n sectors, linearly spaced in radius,
// rotated by rotationOffset). Square/hex samples carry area 1; polar samples
// carry the rotational weight r/apertureRadius so area-weighted sums recover
// the disk area.
func PupilGrid(numRays int, apertureRadius float64, gridType types.GridType, rotationOffset float64) []GridPoint {
	numRays = int(math.Sqrt(float64(numRays)))
	if numRays < 2 {
		numRays = 2
	}

	switch gridType {
	case types.GridSquare:
		var out []GridPoint
		for i := 0; i < numRays; i++ {
			for j := 0; j < numRays; j++ {
				x := (float64(i)+0.5)/float64(numRays)*2 - 1
				y := (float64(j)+0.5)/float64(numRays)*2 - 1
				if x*x+y*y <= 1 {
					out = append(out, GridPoint{X: x * apertureRadius, Y: y * apertureRadius, Area: 1})
				}
			}
		}
		return out

	case types.GridHex:
		n := numRays + 1
		dy := apertureRadius * 2 / float64(n)
		dx := dy * math.Sqrt(3) / 2
		var out []GridPoint
		for i := 0; i < n; i++ {
			y := -apertureRadius + (float64(i)+0.5)*dy
			xOff := 0.0
			if i%2 == 1 {
				xOff = dx / 2
			}
			nx := int(float64(n) * apertureRadius / (dx * float64(n) / 2))
			if nx < 1 {
				nx = 1
			}
			for j := 0; j < nx; j++ {
				x := -apertureRadius + (float64(j)+0.5)*dx + xOff
				if x*x+y*y <= apertureRadius*apertureRadius {
					out = append(out, GridPoint{X: x, Y: y, Area: 1})
				}
			}
		}
		return out

	default: // GridPolar
		var out []GridPoint
		for i := 0; i < numRays; i++ {
			r := (float64(i) + 0.5) / float64(numRays) * apertureRadius
			area := r / apertureRadius
			for j := 0; j < numRays; j++ {
				theta := 2*math.Pi*(float64(j)+0.5)/float64(numRays) + rotationOffset
				out = append(out, GridPoint{X: r * math.Cos(theta), Y: r * math.Sin(theta), Area: area})
			}
		}
		return out
	}
}

// ProjectOntoWavefront projects the launch point p onto the wavefront plane
// that passes through the reference point c and is perpendicular to the
// propagation direction dir. For a parallel angle-field bundle this moves each
// ray's OPL reference onto the common wavefront, removing the launch-geometry
// tilt (a linear OPL ramp across the pupil from launching off-axis rays from a
// plane perpendicular to the optical axis rather than to the ray direction).
//
// The shift is along dir, so the ray line (and therefore every surface
// intersection and the pupil footprint) is unchanged — only the OPL baseline
// moves, by exactly (c - p)·dir. dir must be unit length. The projection uses
// only the direction vector, so it stays finite at any angle including 90°
// incidence (dir.Z → 0): no tanθ appears.
func ProjectOntoWavefront(p, c, dir types.Vec3) types.Vec3 {
	return p.Add(dir.Scale(c.Subtract(p).Dot(dir)))
}

// WavefrontGridCenter returns the grid centre for a parallel angle-field
// bundle. The grid must be centred so its rays (direction dir) pass through the
// entrance-pupil centre c. When the rays are not parallel to the launch plane
// (the plane z = zStart, perpendicular to the optical axis) the centre is the
// point on the zStart plane whose ray crosses c — the classic
// -(pupilZ-zStart)·tanθ offset, expressed with direction vectors only (no tanθ,
// so it is well-behaved as θ grows). When the ray is nearly parallel to the
// launch plane (θ → 90°, dir.Z ≈ 0) that crossing is at infinity; the centre
// then falls back to c itself, placing the grid on the wavefront plane through
// the entrance pupil, which is the correct grazing-incidence limit.
func WavefrontGridCenter(c, dir types.Vec3, zStart float64) types.Vec3 {
	if math.Abs(dir.Z) < 1e-9 {
		return c
	}
	t := (c.Z - zStart) / dir.Z
	return c.Subtract(dir.Scale(t))
}

func Refract(d, n types.Vec3, n1, n2 float64) (types.Vec3, bool) {
	cosTheta1 := -d.Dot(n)
	eta := n1 / n2
	sinTheta1Sq := 1.0 - cosTheta1*cosTheta1
	cosTheta2Sq := 1.0 - eta*eta*sinTheta1Sq

	if cosTheta2Sq < 0 {
		return types.Vec3{}, false
	}

	cosTheta2 := math.Sqrt(cosTheta2Sq)
	term1 := d.Scale(eta)
	term2 := n.Scale(eta*cosTheta1 - cosTheta2)
	return term1.Add(term2).Normalize(), true
}

func FresnelAmplitude(n1, n2, cosTheta1, cosTheta2 float64) (rs, rp, ts, tp float64) {
	eta1s := n1 * cosTheta1
	eta1p := n1 / cosTheta1
	eta2s := n2 * cosTheta2
	eta2p := n2 / cosTheta2

	rs = (eta1s - eta2s) / (eta1s + eta2s)
	rp = (eta1p - eta2p) / (eta1p + eta2p)
	ts = 2 * eta1s / (eta1s + eta2s)
	tp = 2 * eta1p / (eta1p + eta2p)
	return
}

func ComputeDecenterTransform(decenter []types.DecenterStep) types.Mat4 {
	m := types.NewIdentity()
	for _, step := range decenter {
		// A scope:frame step bends the beam frame only; it does not move the
		// surface itself, so it is excluded from the surface's local transform.
		if !step.Scope.MovesSurface() {
			continue
		}
		t := types.NewTranslation(step.Shift)
		rx := types.NewRotationX(DegToRad(step.Tilt.X))
		ry := types.NewRotationY(DegToRad(step.Tilt.Y))
		rz := types.NewRotationZ(DegToRad(step.Tilt.Z))
		local := t.Multiply(rx).Multiply(ry).Multiply(rz)
		m = m.Multiply(local)
	}
	return m
}

func IntersectSphere(origin, dir types.Vec3, radius float64) (float64, bool) {
	if radius == 0 {
		// A plane: the ray always crosses it (any sign of t), matching the
		// original behaviour before the two-root refactor.
		if dir.Z == 0 {
			return 0, false
		}
		return -origin.Z / dir.Z, true
	}

	t1, t2, ok := IntersectSphereBoth(origin, dir, radius)
	if !ok {
		return 0, false
	}
	if t1 > 1e-12 {
		return t1, true
	}
	// A ray arriving at a zero-thickness (coincident) surface is already on
	// the next sphere at its vertex: the near intersection is t1 ≈ 0 for a
	// convex surface and t2 ≈ 0 for a concave one. Accept that hit so the
	// trace continues instead of reporting a missed surface. TraceRay rejects
	// t ≈ 0 on the first surface of a path, so this only relaxes later
	// surfaces.
	if math.Abs(t1) < 1e-9 {
		return t1, true
	}
	if t2 > 1e-12 {
		return t2, true
	}
	if math.Abs(t2) < 1e-9 {
		return t2, true
	}
	return 0, false
}

// IntersectSphereBoth returns both intersection parameters of the ray with the
// sphere (t1 <= t2), or ok=false when the ray misses. Unlike IntersectSphere it
// does not discard negative roots, letting a backward ray select the vertex-side
// hit.
func IntersectSphereBoth(origin, dir types.Vec3, radius float64) (t1, t2 float64, ok bool) {
	if radius == 0 {
		if dir.Z == 0 {
			return 0, 0, false
		}
		t := -origin.Z / dir.Z
		return t, t, true
	}

	a := dir.Dot(dir)
	centerToOrigin := types.Vec3{X: origin.X, Y: origin.Y, Z: origin.Z - radius}
	b := 2.0 * dir.Dot(centerToOrigin)
	c := centerToOrigin.Dot(centerToOrigin) - radius*radius
	disc := b*b - 4*a*c

	if disc < 0 {
		return 0, 0, false
	}

	sqrtDisc := math.Sqrt(disc)
	t1 = (-b - sqrtDisc) / (2 * a)
	t2 = (-b + sqrtDisc) / (2 * a)
	if t1 > t2 {
		t1, t2 = t2, t1
	}
	return t1, t2, true
}

func SphereNormal(p types.Vec3, radius float64) types.Vec3 {
	if radius == 0 {
		return types.Vec3{Z: 1}
	}
	center := types.Vec3{X: 0, Y: 0, Z: radius}
	n := p.Subtract(center)
	return n.Normalize()
}

func PolynomialAsphereSag(h, radius, conic float64, coefficients []float64) float64 {
	h2 := h * h
	R2 := radius * radius

	var sag2nd float64
	if radius == 0 {
		sag2nd = 0
	} else {
		disc := 1.0 - (1.0+conic)*h2/R2
		if disc < 0 {
			return math.NaN()
		}
		sag2nd = h2 / (radius * (1.0 + math.Sqrt(disc)))
	}

	sagAsphere := 0.0
	for i, coef := range coefficients {
		power := 2*i + 4
		sagAsphere += coef * math.Pow(h, float64(power))
	}
	return sag2nd + sagAsphere
}

// ZernikeAsphereSag evaluates the sag of an asphere whose departure from the
// base sphere is expressed by the first Zernike coefficients (piston, defocus
// and primary spherical, coefficient slots 0..2). Higher coefficient slots are
// not honoured and must not be set on a surface using this type. A zero or
// negative normRadius makes the Zernike terms ill-defined, so the base sphere
// is returned unchanged.
func ZernikeAsphereSag(h, radius, conic float64, coefficients []float64, normRadius float64) float64 {
	sag2nd := PolynomialAsphereSag(h, radius, conic, nil)

	if normRadius <= 0 {
		return sag2nd
	}

	rho := h / normRadius
	rho2 := rho * rho

	sagZernike := 0.0
	for i, coef := range coefficients {
		switch i {
		case 0:
			sagZernike += coef * 1.0
		case 1:
			sagZernike += coef * rho2
		case 2:
			sagZernike += coef * (2*rho2*rho2 - rho2)
		}
	}
	return sag2nd + sagZernike
}

// IntersectAsphere finds the ray parameter t at which the ray hits the surface
// whose sag is given by sagFunc. Newton is seeded from the analytic base-sphere
// intersection (radius), so an off-axis ray whose root lies far along the ray
// still converges: the sphere seed is within the asphere's sag perturbation.
// Returns false on a miss (no forward root or NaN sag).
func IntersectAsphere(origin, dir types.Vec3, sagFunc func(float64) float64, radius float64, maxIter int, tol float64) (float64, bool) {
	f := func(t float64) float64 {
		p := types.Vec3{
			X: origin.X + dir.X*t,
			Y: origin.Y + dir.Y*t,
			Z: origin.Z + dir.Z*t,
		}
		h := math.Sqrt(p.X*p.X + p.Y*p.Y)
		sag := sagFunc(h)
		if math.IsNaN(sag) {
			return math.NaN()
		}
		return p.Z - sag
	}

	// Seed from the base sphere when a radius is known; otherwise from t=0.
	t := 0.0
	if radius != 0 {
		if s1, _, ok := IntersectSphereBoth(origin, dir, radius); ok && s1 > 1e-12 {
			t = s1
		}
	}

	// Walk forward if the seed is before the root (f < 0), guarding NaN sag.
	if fv := f(t); !math.IsNaN(fv) && fv < 0 {
		step := 1.0
		lo := t
		hi := 0.0
		for i := 0; i < 200; i++ {
			cand := t + step
			fc := f(cand)
			if math.IsNaN(fc) {
				step *= 0.5
				if step < 1e-9 {
					return 0, false
				}
				continue
			}
			if fc >= 0 {
				hi = cand
				break
			}
			lo = cand
			step *= 2
		}
		if hi > lo {
			// Bisect [lo, hi] to 1e-9.
			for i := 0; i < 80; i++ {
				mid := (lo + hi) / 2
				fm := f(mid)
				if math.IsNaN(fm) {
					hi = mid
					continue
				}
				if fm >= 0 {
					hi = mid
				} else {
					lo = mid
				}
				if hi-lo < 1e-9 {
					break
				}
			}
			t = (lo + hi) / 2
		}
	}

	// Newton polish.
	for i := 0; i < maxIter; i++ {
		fv := f(t)
		if math.IsNaN(fv) {
			return 0, false
		}
		if math.Abs(fv) < tol {
			return t, true
		}
		dh := 1e-6
		df := (f(t+dh) - f(t-dh)) / (2 * dh)
		if math.IsNaN(df) || df == 0 {
			break
		}
		nt := t - fv/df
		if math.IsNaN(nt) || nt <= 0 {
			break
		}
		t = nt
	}
	if math.Abs(f(t)) < tol {
		return t, true
	}
	// Fall back to the bisected point when Newton did not fully converge.
	if math.Abs(f(t)) < 1e-6 {
		return t, true
	}
	return 0, false
}

func AsphereNormal(p types.Vec3, sagFunc func(float64) float64) types.Vec3 {
	h := math.Sqrt(p.X*p.X + p.Y*p.Y)
	eps := 1e-6

	dzdh := (sagFunc(h+eps) - sagFunc(h-eps)) / (2 * eps)

	if h == 0 {
		return types.Vec3{Z: 1}
	}

	return types.Vec3{
		X: -p.X / h * dzdh,
		Y: -p.Y / h * dzdh,
		Z: 1,
	}.Normalize()
}

func DetermineInteraction(prev, current, next int) types.InteractionType {
	d1 := current - prev
	d2 := next - current
	if (d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0) {
		return types.Reflect
	}
	return types.Transmit
}

func ComputeParaxialCurvature(sagFunc func(float64) float64) float64 {
	h := 1e-6
	zMinus := sagFunc(-h)
	zZero := sagFunc(0)
	zPlus := sagFunc(h)
	return (zPlus - 2*zZero + zMinus) / (h * h)
}

func SphereParaxialRadius(radius float64, coefficients []float64) float64 {
	if len(coefficients) >= 1 {
		Rinv := 1.0/radius + 2*coefficients[0]
		if Rinv == 0 {
			return 0
		}
		return 1.0 / Rinv
	}
	return radius
}
