package glass

import "math"

type Point2D struct {
	ND, VD float64
}

type ConvexHull struct {
	Vertices     []Point2D
	normVertices []Point2D
	NDMin, NDSpan   float64
	VDMin, VDSpan   float64
}

func NewConvexHull(vertices []Point2D, ndMin, ndMax, vdMin, vdMax float64) *ConvexHull {
	norm := make([]Point2D, len(vertices))
	ndSpan := ndMax - ndMin
	vdSpan := vdMax - vdMin
	for i, v := range vertices {
		norm[i] = Point2D{
			ND: (v.ND - ndMin) / ndSpan,
			VD: (v.VD - vdMin) / vdSpan,
		}
	}
	return &ConvexHull{
		Vertices:     vertices,
		normVertices: norm,
		NDMin:        ndMin,
		NDSpan:       ndSpan,
		VDMin:        vdMin,
		VDSpan:       vdSpan,
	}
}

func (h *ConvexHull) SignedDistance(nd, vd float64) float64 {
	nx := (nd - h.NDMin) / h.NDSpan
	ny := (vd - h.VDMin) / h.VDSpan
	p := Point2D{nx, ny}

	n := len(h.normVertices)
	if n < 3 {
		return 0
	}

	inside := true
	minPen := math.MaxFloat64

	for i := 0; i < n; i++ {
		j := (i + 1) % n
		cp := cross2D(h.normVertices[i], h.normVertices[j], p)
		if cp < -1e-12 {
			inside = false
			d := pointToSegmentDist(p, h.normVertices[i], h.normVertices[j])
			if d < minPen {
				minPen = d
			}
		}
	}

	if inside {
		minDist := math.MaxFloat64
		for i := 0; i < n; i++ {
			j := (i + 1) % n
			d := pointToSegmentDist(p, h.normVertices[i], h.normVertices[j])
			if d < minDist {
				minDist = d
			}
		}
		return minDist
	}
	return -minPen
}

func (h *ConvexHull) Penalty(nd, vd, margin, weight float64) float64 {
	sd := h.SignedDistance(nd, vd)
	if sd >= margin {
		return 0
	}
	d := margin - sd
	return weight * d * d
}

func (h *ConvexHull) Residual(nd, vd, margin, weight float64) float64 {
	sd := h.SignedDistance(nd, vd)
	if sd >= margin {
		return 0
	}
	d := margin - sd
	return math.Sqrt(weight) * d
}

func cross2D(a, b, c Point2D) float64 {
	return (b.ND-a.ND)*(c.VD-a.VD) - (b.VD-a.VD)*(c.ND-a.ND)
}

func pointToSegmentDist(p, a, b Point2D) float64 {
	lx := b.ND - a.ND
	ly := b.VD - a.VD
	l2 := lx*lx + ly*ly
	if l2 < 1e-20 {
		dx := p.ND - a.ND
		dy := p.VD - a.VD
		return math.Sqrt(dx*dx + dy*dy)
	}
	t := ((p.ND-a.ND)*lx + (p.VD-a.VD)*ly) / l2
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	px := a.ND + t*lx
	py := a.VD + t*ly
	dx := p.ND - px
	dy := p.VD - py
	return math.Sqrt(dx*dx + dy*dy)
}
