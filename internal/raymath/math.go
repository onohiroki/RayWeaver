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
	rad := DegToRad(angleDeg)
	return types.Vec3{X: 0, Y: math.Sin(rad), Z: math.Cos(rad)}.Normalize()
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
		if dir.Z == 0 {
			return 0, false
		}
		return -origin.Z / dir.Z, true
	}

	a := dir.Dot(dir)
	centerToOrigin := types.Vec3{X: origin.X, Y: origin.Y, Z: origin.Z - radius}
	b := 2.0 * dir.Dot(centerToOrigin)
	c := centerToOrigin.Dot(centerToOrigin) - radius*radius
	disc := b*b - 4*a*c

	if disc < 0 {
		return 0, false
	}

	sqrtDisc := math.Sqrt(disc)
	t1 := (-b - sqrtDisc) / (2 * a)
	t2 := (-b + sqrtDisc) / (2 * a)

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

func SphereNormal(p types.Vec3, radius float64) types.Vec3 {
	if radius == 0 {
		return types.Vec3{0, 0, 1}
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

func ZernikeAsphereSag(h, radius, conic float64, coefficients []float64, normRadius float64) float64 {
	sag2nd := PolynomialAsphereSag(h, radius, conic, nil)

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

func IntersectAsphere(origin, dir types.Vec3, sagFunc func(float64) float64, maxIter int, tol float64) (float64, bool) {
	t := 0.0
	for i := 0; i < maxIter; i++ {
		p := types.Vec3{
			X: origin.X + dir.X*t,
			Y: origin.Y + dir.Y*t,
			Z: origin.Z + dir.Z*t,
		}
		h := math.Sqrt(p.X*p.X + p.Y*p.Y)
		sag := sagFunc(h)

		f := p.Z - sag
		if math.Abs(f) < tol {
			return t, true
		}

		dh := 1e-6
		dsagDh := (sagFunc(h+dh) - sagFunc(h-dh)) / (2 * dh)
		var dhDt float64
		if h > 1e-12 {
			dhDt = (p.X*dir.X + p.Y*dir.Y) / h
		}
		df := dir.Z - dsagDh*dhDt
		if df == 0 {
			break
		}
		t -= f / df
	}
	return 0, false
}

func AsphereNormal(p types.Vec3, sagFunc func(float64) float64) types.Vec3 {
	h := math.Sqrt(p.X*p.X + p.Y*p.Y)
	eps := 1e-6

	dzdh := (sagFunc(h+eps) - sagFunc(h-eps)) / (2 * eps)

	if h == 0 {
		return types.Vec3{0, 0, 1}
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
		Rinv := 1.0 / radius + 2*coefficients[0]
		if Rinv == 0 {
			return 0
		}
		return 1.0 / Rinv
	}
	return radius
}
