package glass

import (
	"math"
	"testing"
)

func TestNewDefaultConvexHull(t *testing.T) {
	h := NewDefaultConvexHull()
	if len(h.Vertices) != 13 {
		t.Errorf("expected 13 vertices, got %d", len(h.Vertices))
	}
	if len(h.normVertices) != 13 {
		t.Errorf("expected 13 normalized vertices, got %d", len(h.normVertices))
	}
	if h.NDSpan <= 0 || h.VDSpan <= 0 {
		t.Errorf("invalid span: nd=%f vd=%f", h.NDSpan, h.VDSpan)
	}
}

func TestSignedDistance_InsidePoint(t *testing.T) {
	h := NewDefaultConvexHull()
	tests := []struct {
		nd, vd  float64
		name    string
		wantMin float64
	}{
		{1.437, 95.1, "FCD100 (HOYA)", 0},
		{1.5168, 64.17, "N-BK7 (SCHOTT)", 0},
		{1.5, 60, "typical mid", 0},
		{1.9, 30, "high nd", 0},
		{1.41268, 100.7, "hull vertex 0", 0},
		{1.4139, 101.0, "hull vertex 12", -1e-12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := h.SignedDistance(tt.nd, tt.vd)
			if d < tt.wantMin {
				t.Errorf("SignedDistance(%f, %f) = %f, want >= %f (outside hull but should be inside)", tt.nd, tt.vd, d, tt.wantMin)
			}
		})
	}
}

func TestSignedDistance_OutsidePoint(t *testing.T) {
	h := NewDefaultConvexHull()
	tests := []struct {
		nd, vd  float64
		name    string
		wantMax float64
	}{
		{1.35, 105, "far upper-left", -0.001},
		{2.3, 15, "far lower-right", -0.001},
		{1.5, 10, "vd too low", -0.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := h.SignedDistance(tt.nd, tt.vd)
			if d > tt.wantMax {
				t.Errorf("SignedDistance(%f, %f) = %f, want <= %f (inside hull but should be outside)", tt.nd, tt.vd, d, tt.wantMax)
			}
		})
	}
}

func TestPenalty_InsideWithMargin(t *testing.T) {
	h := NewDefaultConvexHull()
	pen := h.Penalty(1.5, 60, 0.02, 1.0)
	if pen != 0 {
		t.Errorf("Penalty for inside point = %f, want 0", pen)
	}
}

func TestResidual_Outside(t *testing.T) {
	h := NewDefaultConvexHull()
	res := h.Residual(1.5, 10, 0.02, 1.0)
	if res <= 0 {
		t.Errorf("Residual for outside point = %f, want >0", res)
	}
}

func TestPenaltyResidualConsistency(t *testing.T) {
	h := NewDefaultConvexHull()
	pts := []struct{ nd, vd float64 }{
		{1.5, 60},
		{1.437, 95.1},
		{1.35, 105},
		{2.3, 15},
	}
	margin := 0.02
	weight := 1.0
	for _, p := range pts {
		pen := h.Penalty(p.nd, p.vd, margin, weight)
		res := h.Residual(p.nd, p.vd, margin, weight)
		expectedPen := res * res
		if math.Abs(pen-expectedPen) > 1e-12 {
			t.Errorf("(%f,%f): penalty=%f residual^2=%f mismatch", p.nd, p.vd, pen, expectedPen)
		}
	}
}

func TestNewConvexHull_Normalization(t *testing.T) {
	verts := []Point2D{
		{1.4, 100},
		{2.2, 15},
	}
	h := NewConvexHull(verts, 1.4, 2.2, 15, 100)
	if math.Abs(h.NDSpan-0.8) > 1e-12 {
		t.Errorf("NDSpan = %.15f, want 0.8", h.NDSpan)
	}
	if math.Abs(h.VDSpan-85) > 1e-12 {
		t.Errorf("VDSpan = %.15f, want 85", h.VDSpan)
	}
	if h.normVertices[0].ND != 0 || h.normVertices[0].VD != 1 {
		t.Errorf("norm[0] = (%f,%f), want (0,1)", h.normVertices[0].ND, h.normVertices[0].VD)
	}
	if h.normVertices[1].ND != 1 || h.normVertices[1].VD != 0 {
		t.Errorf("norm[1] = (%f,%f), want (1,0)", h.normVertices[1].ND, h.normVertices[1].VD)
	}
}
