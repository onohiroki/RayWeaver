package types

import (
	"bytes"
	"strings"
	"math"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestVec3Length(t *testing.T) {
	v := Vec3{X: 3, Y: 4, Z: 0}
	if got := v.Length(); math.Abs(got-5) > 1e-12 {
		t.Errorf("Length() = %v, want 5", got)
	}
}

func TestVec3LengthSq(t *testing.T) {
	v := Vec3{X: 1, Y: 2, Z: 3}
	want := 1.0 + 4.0 + 9.0
	if got := v.LengthSq(); got != want {
		t.Errorf("LengthSq() = %v, want %v", got, want)
	}
}

func TestVec3Normalize(t *testing.T) {
	v := Vec3{X: 3, Y: 4, Z: 0}.Normalize()
	want := Vec3{X: 0.6, Y: 0.8, Z: 0}
	if math.Abs(v.X-want.X) > 1e-12 || math.Abs(v.Y-want.Y) > 1e-12 || math.Abs(v.Z-want.Z) > 1e-12 {
		t.Errorf("Normalize() = %v, want %v", v, want)
	}
}

func TestVec3Dot(t *testing.T) {
	a := Vec3{X: 1, Y: 0, Z: 0}
	b := Vec3{X: 0, Y: 1, Z: 0}
	if got := a.Dot(b); got != 0 {
		t.Errorf("Dot(orthogonal) = %v, want 0", got)
	}
	if got := a.Dot(a); got != 1 {
		t.Errorf("Dot(unit self) = %v, want 1", got)
	}
}

func TestVec3Cross(t *testing.T) {
	a := Vec3{X: 1, Y: 0, Z: 0}
	b := Vec3{X: 0, Y: 1, Z: 0}
	c := a.Cross(b)
	want := Vec3{X: 0, Y: 0, Z: 1}
	if c != want {
		t.Errorf("Cross() = %v, want %v", c, want)
	}
}

func TestVec3AddSubScaleNegate(t *testing.T) {
	a := Vec3{X: 1, Y: 2, Z: 3}
	b := Vec3{X: 4, Y: 5, Z: 6}
	if got := a.Add(b); got != (Vec3{X: 5, Y: 7, Z: 9}) {
		t.Errorf("Add() = %v", got)
	}
	if got := a.Subtract(b); got != (Vec3{X: -3, Y: -3, Z: -3}) {
		t.Errorf("Subtract() = %v", got)
	}
	if got := a.Scale(2); got != (Vec3{X: 2, Y: 4, Z: 6}) {
		t.Errorf("Scale() = %v", got)
	}
	if got := a.Negate(); got != (Vec3{X: -1, Y: -2, Z: -3}) {
		t.Errorf("Negate() = %v", got)
	}
}

func TestMat4Identity(t *testing.T) {
	m := NewIdentity()
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			want := 0.0
			if i == j {
				want = 1.0
			}
			if m[i][j] != want {
				t.Errorf("Identity[%d][%d] = %v, want %v", i, j, m[i][j], want)
			}
		}
	}
}

func TestMat4Translation(t *testing.T) {
	m := NewTranslation(Vec3{X: 1, Y: 2, Z: 3})
	p := m.MultiplyPoint(Vec3{X: 0, Y: 0, Z: 0})
	want := Vec3{X: 1, Y: 2, Z: 3}
	if p != want {
		t.Errorf("Translate point = %v, want %v", p, want)
	}
	v := m.MultiplyVector(Vec3{X: 1, Y: 0, Z: 0})
	if v != (Vec3{X: 1, Y: 0, Z: 0}) {
		t.Errorf("Translate vector (should be unchanged) = %v", v)
	}
}

func TestMat4Rotation(t *testing.T) {
	m := NewRotationZ(math.Pi / 2)
	p := m.MultiplyPoint(Vec3{X: 1, Y: 0, Z: 0})
	if math.Abs(p.X) > 1e-12 || math.Abs(p.Y-1) > 1e-12 {
		t.Errorf("RotZ(90) (1,0,0) = %v, want approx (0,1,0)", p)
	}
}

func TestMat4Inverse(t *testing.T) {
	trans := NewTranslation(Vec3{X: 5, Y: 10, Z: 15})
	rot := NewRotationX(0.5)
	m := trans.Multiply(rot)
	inv := m.Inverse()
	p := Vec3{X: 1, Y: 2, Z: 3}
	got := m.MultiplyPoint(inv.MultiplyPoint(p))
	if math.Abs(got.X-p.X) > 1e-10 || math.Abs(got.Y-p.Y) > 1e-10 || math.Abs(got.Z-p.Z) > 1e-10 {
		t.Errorf("M * inv(M) * p = %v, want %v", got, p)
	}
}

func TestVec3YAMLRoundTrip(t *testing.T) {
	v := Vec3{X: 1.5, Y: -2.5, Z: 3.0}
	encoded, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	var got Vec3
	if err := yaml.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if got != v {
		t.Errorf("round-trip = %v, want %v", got, v)
	}
}

func TestJonesVectorYAMLRoundTrip(t *testing.T) {
	j := JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	encoded, err := yaml.Marshal(j)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	var got JonesVector
	if err := yaml.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if got.Ex != j.Ex || got.Ey != j.Ey {
		t.Errorf("round-trip = %v, want %v", got, j)
	}
}

func TestVec3YAMLSequence(t *testing.T) {
	data := []byte("[1.0, 2.0, 3.0]")
	var v Vec3
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatalf("yaml.Unmarshal [1,2,3]: %v", err)
	}
	if v != (Vec3{X: 1, Y: 2, Z: 3}) {
		t.Errorf("Unmarshal = %v, want (1,2,3)", v)
	}
}

func TestJonesVectorYAMLSequence(t *testing.T) {
	data := []byte("[1.0, 0.0, 0.0, 1.0]")
	var j JonesVector
	if err := yaml.Unmarshal(data, &j); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	want := JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	if j != want {
		t.Errorf("Unmarshal = %v, want %v", j, want)
	}
}

// TestFieldItemZeroAngleMarshals is a regression test for the improvement
// report (3.11): angle_deg: 0 must not be dropped by omitempty, otherwise the
// on-axis (0°) field disappears from optimized output.
func TestFieldItemZeroAngleMarshals(t *testing.T) {
	f := FieldItem{ID: 0, AngleDeg: 0.0, Weight: 1.0}
	out, err := yaml.Marshal(f)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if !bytes.Contains(out, []byte("angle_deg: 0")) {
		t.Errorf("angle_deg: 0 missing from marshaled FieldItem: %s", out)
	}
}

// TestSurfaceNegativeThicknessError: the fold model requires positive
// thicknesses; a negative thickness must be rejected at parse time.
func TestSurfaceNegativeThicknessError(t *testing.T) {
	data := []byte("id: 2\nradius: -800.0\nthickness: -340.0\nmaterial: AIR")
	var s Surface
	err := yaml.Unmarshal(data, &s)
	if err == nil {
		t.Fatal("expected error for negative thickness, got nil")
	}
	if !strings.Contains(err.Error(), "negative thickness") {
		t.Errorf("error = %v, want mention of negative thickness", err)
	}
}

// TestSurfaceTopLevelReflect: the surface-level `reflect` flag marks a mirror
// surface (the per-decenter `reflect` was replaced by decenter `scope`).
func TestSurfaceTopLevelReflect(t *testing.T) {
	data := []byte("id: 3\nradius: -800.0\nthickness: 340.0\nmaterial: AIR\nreflect: true")
	var s Surface
	err := yaml.Unmarshal(data, &s)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Reflect {
		t.Error("reflect: true not decoded")
	}
	out, err := yaml.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("reflect: true")) {
		t.Errorf("marshal output = %s, want reflect: true", out)
	}
}
