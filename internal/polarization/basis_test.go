package polarization

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestSPBasisOrthonormal(t *testing.T) {
	d := types.Vec3{X: 0, Y: 0.3, Z: 0.8}.Normalize()
	n := types.Vec3{X: 0, Y: 0, Z: -1}
	b := ComputeSPBasis(d, n)

	if math.Abs(b.S.Dot(b.S)-1) > 1e-9 {
		t.Errorf("|s| = %v, want 1", b.S.Length())
	}
	if math.Abs(b.P.Dot(b.P)-1) > 1e-9 {
		t.Errorf("|p| = %v, want 1", b.P.Length())
	}
	if math.Abs(b.S.Dot(b.P)) > 1e-9 {
		t.Errorf("s·p = %v, want 0", b.S.Dot(b.P))
	}
	if math.Abs(b.S.Dot(d)) > 1e-9 {
		t.Errorf("s·d = %v, want 0", b.S.Dot(d))
	}
	if math.Abs(b.P.Dot(d)) > 1e-9 {
		t.Errorf("p·d = %v, want 0", b.P.Dot(d))
	}
}

func TestSPBasisInvariantAcrossRefraction(t *testing.T) {
	d := types.Vec3{Y: 0.5, Z: 0.866}
	n := types.Vec3{Z: -1}
	b := ComputeSPBasis(d, n)
	// Refracted direction (n1=1 → n2=1.5): Snell.
	cos2 := math.Sqrt(1 - (1.0/1.5)*(1.0/1.5)*0.25)
	dir2 := types.Vec3{Y: (1.0 / 1.5) * 0.5, Z: cos2}.Normalize()
	b2 := ComputeSPBasis(dir2, n)
	// s is invariant within the plane of incidence.
	if math.Abs(b.S.Dot(b2.S)) < 1-1e-9 {
		t.Errorf("s changed across refraction: s·s' = %v", b.S.Dot(b2.S))
	}
}

func TestTransmissionPreservesEnergy(t *testing.T) {
	// A transverse field passing through a surface with Fresnel ts/tp keeps
	// its direction under the s/p decomposition for a round trip.
	d := types.Vec3{Y: 0.3, Z: 0.9539}.Normalize()
	n := types.Vec3{Z: -1}
	b := ComputeSPBasis(d, n)
	ts, tp := 0.95, 0.98

	// x-polarized transverse field
	u, v := TransverseFrame(d)
	E := TransverseField(u, v, types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 0)})
	es, ep := b.Project(E)
	// The transmitted field keeps the same s/p components scaled.
	E2 := FromSP(b.S, b.OutgoingP(d), complex(ts, 0)*es, complex(tp, 0)*ep)
	want := ts*ts*absSqC(es) + tp*tp*absSqC(ep)
	if math.Abs(E2.AbsSq()-want) > 1e-9 {
		t.Errorf("transmission reconstruction: |E2|² = %v, want %v", E2.AbsSq(), want)
	}
}

func absSqC(c complex128) float64 {
	return real(c)*real(c) + imag(c)*imag(c)
}

func TestMirrorReflectPreservesMagnitude(t *testing.T) {
	// Any input field reflected by an ideal mirror keeps |E|.
	for _, E := range []types.Vec3C{
		{X: complex(1, 0), Y: complex(0, 0)},
		{X: complex(0, 1), Y: complex(1, 0)},
		{X: complex(0.7, 0.1), Y: complex(0.3, -0.2), Z: complex(0.5, 0)},
	} {
		n := types.Vec3{Y: 0.1, Z: -0.9}.Normalize()
		out := MirrorReflect(E, n)
		if math.Abs(out.AbsSq()-E.AbsSq()) > 1e-9 {
			t.Errorf("mirror reflect changed |E|: %v → %v", E, out)
		}
	}
}

func TestMirrorReflectTransverseToOutgoing(t *testing.T) {
	// The reflected field must be perpendicular to the reflected ray when the
	// input field is transverse to the incident ray.
	d := types.Vec3{Y: 0.5, Z: 0.866}
	n := types.Vec3{Z: -1}
	u, v := TransverseFrame(d)
	E := TransverseField(u, v, types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)})
	if math.Abs(real(E.DotVec(d))) > 1e-9 || math.Abs(imag(E.DotVec(d))) > 1e-9 {
		t.Fatalf("input field not transverse: E·d = %v", E.DotVec(d))
	}
	out := MirrorReflect(E, n)
	dout := d.Subtract(n.Scale(2 * d.Dot(n)))
	if math.Abs(real(out.DotVec(dout))) > 1e-6 || math.Abs(imag(out.DotVec(dout))) > 1e-6 {
		t.Errorf("reflected field not transverse to reflected ray: E·d_out = %v", out.DotVec(dout))
	}
}

func TestTransverseFrameOnAxis(t *testing.T) {
	u, v := TransverseFrame(types.Vec3{Z: 1})
	if math.Abs(u.X-1) > 1e-9 || math.Abs(v.Y-1) > 1e-9 {
		t.Errorf("on-axis frame = (%v, %v), want ((1,0,0), (0,1,0))", u, v)
	}
}

func TestTransverseFrameOffAxis(t *testing.T) {
	d := types.Vec3{Y: 0.5, Z: 0.866}
	u, v := TransverseFrame(d)
	if math.Abs(u.Dot(d)) > 1e-9 || math.Abs(v.Dot(d)) > 1e-9 || math.Abs(u.Dot(v)) > 1e-9 {
		t.Errorf("frame not orthonormal to direction")
	}
}

func TestJonesMatrixOps(t *testing.T) {
	// diag(ts, tp) applied to a Jones vector.
	m := FresnelTransmissionMatrix(0.9, 0.8)
	v := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	out := m.Apply(v)
	if math.Abs(real(out.Ex)-0.9) > 1e-9 || math.Abs(real(out.Ey)) > 1e-9 {
		t.Errorf("Jones apply wrong: %v", out)
	}
	// diag(a) · diag(b) = diag(a*b)
	prod := FresnelTransmissionMatrix(0.9, 0.8).Multiply(FresnelReflectionMatrix(0.5, 0.4))
	if math.Abs(real(prod[0][0])-0.45) > 1e-9 || math.Abs(real(prod[1][1])-0.32) > 1e-9 {
		t.Errorf("Jones multiply wrong: %v", prod)
	}
}
