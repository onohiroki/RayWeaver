package dls

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestComputeSpotRMS(t *testing.T) {
	points := []IPoint{
		{X: 1.0, Y: 0.0, OK: true},
		{X: -1.0, Y: 0.0, OK: true},
		{X: 0.0, Y: 1.0, OK: true},
		{X: 0.0, Y: -1.0, OK: true},
	}
	rms := ComputeSpotRMS(points)
	expected := 1.0
	if math.Abs(rms-expected) > 1e-10 {
		t.Errorf("RMS = %v, want %v", rms, expected)
	}
}

func TestComputeSpotRMSAllFailed(t *testing.T) {
	points := []IPoint{
		{OK: false},
		{OK: false},
	}
	rms := ComputeSpotRMS(points)
	if rms != 1e6 {
		t.Errorf("RMS = %v, want 1e6 for all-failed points", rms)
	}
}

func TestBuildPath(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 0},
		{ID: 1},
		{ID: 2},
	}
	path := BuildPath(surfaces)
	if len(path) != 3 || path[0] != 0 || path[1] != 1 || path[2] != 2 {
		t.Errorf("BuildPath = %v, want [0, 1, 2]", path)
	}
}

func TestSurfaceIndex(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1},
		{ID: 2},
		{ID: 3},
	}
	if SurfaceIndex(surfaces, 2) != 1 {
		t.Error("SurfaceIndex(surfaces, 2) != 1")
	}
	if SurfaceIndex(surfaces, 99) != -1 {
		t.Error("SurfaceIndex(surfaces, 99) should be -1")
	}
}

func TestSolveLinearSystem(t *testing.T) {
	H := [][]float64{
		{2, 0},
		{0, 2},
	}
	g := []float64{1, 1}
	x := solveLinearSystem(H, g)
	if x == nil {
		t.Fatal("solveLinearSystem returned nil")
	}
	if math.Abs(x[0]-0.5) > 1e-10 || math.Abs(x[1]-0.5) > 1e-10 {
		t.Errorf("solveLinearSystem = %v, want [0.5, 0.5]", x)
	}
}

func TestProjectOntoBox(t *testing.T) {
	variables := []VariableInfo{
		{Min: 0.0, Max: 1.0},
		{Min: -5.0, Max: 5.0},
	}
	x := []float64{-1.0, 10.0}
	projectOntoBox(x, variables)
	if x[0] != 0.0 {
		t.Errorf("x[0] = %v, want 0.0 (clamped to min)", x[0])
	}
	if x[1] != 5.0 {
		t.Errorf("x[1] = %v, want 5.0 (clamped to max)", x[1])
	}
}

func TestSanitize(t *testing.T) {
	if sanitize(math.NaN()) != 0 {
		t.Error("sanitize(NaN) should be 0")
	}
	if sanitize(math.Inf(1)) != 0 {
		t.Error("sanitize(+Inf) should be 0")
	}
	if sanitize(3.14) != 3.14 {
		t.Error("sanitize(3.14) should be 3.14")
	}
}

// polyModel is an n-dimensional Model whose residuals are nonlinear functions
// of every variable, so every Jacobian column is nontrivial.
type polyModel struct {
	n int
}

func (m polyModel) Variables() []VariableInfo {
	vs := make([]VariableInfo, m.n)
	for i := range vs {
		vs[i] = VariableInfo{Name: "x", Min: -2, Max: 2}
	}
	return vs
}

func (m polyModel) InitialState() []float64 {
	x := make([]float64, m.n)
	for i := range x {
		x[i] = 0.3
	}
	return x
}

func (m polyModel) Options() Options { return Options{MaxIter: 10} }

func (m polyModel) EvaluateMerit(x []float64) float64 {
	r := m.ComputeResiduals(x)
	s := 0.0
	for _, v := range r {
		s += v * v
	}
	return s
}

func (m polyModel) ComputeResiduals(x []float64) []float64 {
	// One residual per variable plus one coupled cross-term.
	r := make([]float64, m.n+1)
	for i := 0; i < m.n; i++ {
		r[i] = x[i]*x[i]*x[i] - 2*x[i]
	}
	cross := 0.0
	for i := 0; i < m.n; i++ {
		cross += x[i]
	}
	r[m.n] = cross * cross * 0.5
	return r
}

func (m polyModel) ComputeConstraints(x []float64) []float64 {
	c := make([]float64, m.n)
	for i := 0; i < m.n; i++ {
		c[i] = x[i] - 0.25
	}
	return c
}

func TestComputeJacobiansParallelDeterminism(t *testing.T) {
	m := polyModel{n: 6}
	xNorm := []float64{0.1, 0.4, 0.7, 0.9, 0.2, 0.5}
	variables := m.Variables()
	scales := make([]float64, len(variables))
	for i, v := range variables {
		scales[i] = v.Max - v.Min
	}

	serialJ, serialR, serialJC, serialC := computeJacobians(m, xNorm, variables, scales, 1e-6, 1)
	parJ, parR, parJC, parC := computeJacobians(m, xNorm, variables, scales, 1e-6, 4)

	if !equalFloats(serialR, parR) {
		t.Errorf("residual baseline mismatch: %v vs %v", serialR, parR)
	}
	if !equalFloats(serialC, parC) {
		t.Errorf("constraint baseline mismatch: %v vs %v", serialC, parC)
	}
	if !equalMatrices(serialJ, parJ) {
		t.Errorf("residual Jacobian mismatch:\nserial=%v\nparallel=%v", serialJ, parJ)
	}
	if !equalMatrices(serialJC, parJC) {
		t.Errorf("constraint Jacobian mismatch:\nserial=%v\nparallel=%v", serialJC, parJC)
	}
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalMatrices(a, b [][]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalFloats(a[i], b[i]) {
			return false
		}
	}
	return true
}