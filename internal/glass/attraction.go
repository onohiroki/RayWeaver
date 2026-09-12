package glass

import "math"

// CatalogField holds the normalised nd/vd points of every real glass in a
// catalog. It is built once per optimisation run and queried many times.
type CatalogField struct {
	// Points are the raw (nd, vd) pairs of all valid catalog glasses.
	Points []Point2D
	// NormPoints are the same points normalised to [0,1] using the range.
	NormPoints []Point2D
	// Names are the catalog glass keys, parallel to Points/NormPoints.
	Names []string
	NDMin, NDSpan, VDMin, VDSpan float64
}

// DefaultGlassRange returns the normalisation bounding box shared with the
// convex hull (same constants as hull_data.go).
func DefaultGlassRange() (ndMin, ndMax, vdMin, vdMax float64) {
	return DefaultHullNDMin, DefaultHullNDMax, DefaultHullVDMin, DefaultHullVDMax
}

// BuildCatalogField collects every glass in the catalog with valid nd/vd and
// normalises them to the default glass range. Nil or empty catalog returns an
// empty field.
func BuildCatalogField(catalog *Catalog) *CatalogField {
	if catalog == nil {
		return &CatalogField{}
	}
	ndMin, ndMax, vdMin, vdMax := DefaultGlassRange()
	ndSpan := ndMax - ndMin
	vdSpan := vdMax - vdMin
	if ndSpan <= 0 || vdSpan <= 0 {
		return &CatalogField{}
	}

	seen := make(map[string]bool)
	var raw []Point2D
	var names []string
	for _, g := range catalog.ByName {
		nd, vd, ok := NDVD(g)
		if !ok || nd <= 0 || vd <= 0 {
			continue
		}
		key := g.Key
		if key == "" {
			key = g.Label
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		raw = append(raw, Point2D{ND: nd, VD: vd})
		names = append(names, key)
	}

	norm := make([]Point2D, len(raw))
	for i, p := range raw {
		norm[i] = Point2D{
			ND: (p.ND - ndMin) / ndSpan,
			VD: (p.VD - vdMin) / vdSpan,
		}
	}
	return &CatalogField{
		Points:     raw,
		NormPoints: norm,
		Names:      names,
		NDMin:      ndMin,
		NDSpan:     ndSpan,
		VDMin:      vdMin,
		VDSpan:     vdSpan,
	}
}

// CatalogFieldCount returns the number of glasses in the field.
func (f *CatalogField) CatalogFieldCount() int {
	if f == nil {
		return 0
	}
	return len(f.Points)
}

// SoftMinPotential returns the soft-min distance from (nd, vd) to the
// nearest catalog point in normalised space. The kernel selects the blending
// function: "distance" (φ=r², return min r²) or "gaussian" (φ=1−exp(−r²/2σ²),
// return soft-min of 1−exp(−r²/2σ²)). sigmaND/sigmaVD are the gaussian
// kernel widths in normalised units (ignored for distance kernel).
func SoftMinPotential(f *CatalogField, nd, vd, sigmaND, sigmaVD float64, kernel string) float64 {
	if f == nil || len(f.NormPoints) == 0 {
		return 0
	}
	nx := (nd - f.NDMin) / f.NDSpan
	ny := (vd - f.VDMin) / f.VDSpan

	switch kernel {
	case "gaussian":
		sigma2 := sigmaND*sigmaND + sigmaVD*sigmaVD
		if sigma2 < 1e-30 {
			sigma2 = 1e-6
		}
		sumExp := 0.0
		for _, p := range f.NormPoints {
			dx := nx - p.ND
			dy := ny - p.VD
			r2 := dx*dx + dy*dy
			sumExp += math.Exp(-r2 / (2 * sigma2))
		}
		if sumExp <= 0 {
			return 1.0
		}
		// soft-min potential: higher sumExp → closer → lower value
		return -math.Log(sumExp) * sigma2
	default: // "distance"
		minR2 := math.MaxFloat64
		for _, p := range f.NormPoints {
			dx := nx - p.ND
			dy := ny - p.VD
			r2 := dx*dx + dy*dy
			if r2 < minR2 {
				minR2 = r2
			}
		}
		return minR2
	}
}

// Penalty returns weight * Potential (for merit-function summation).
func Penalty(f *CatalogField, nd, vd, sigmaND, sigmaVD, margin, weight float64, kernel string) float64 {
	pot := SoftMinPotential(f, nd, vd, sigmaND, sigmaVD, kernel)
	if pot <= margin*margin {
		return 0
	}
	d := math.Sqrt(pot) - margin
	if d <= 0 {
		return 0
	}
	return weight * d * d
}

// Residual returns sqrt(weight) * sqrt(Potential) for the DLS sum-of-squares
// framework. When squared by the solver this produces exactly Penalty.
func Residual(f *CatalogField, nd, vd, sigmaND, sigmaVD, margin, weight float64, kernel string) float64 {
	pot := SoftMinPotential(f, nd, vd, sigmaND, sigmaVD, kernel)
	if pot <= margin*margin {
		return 0
	}
	d := math.Sqrt(pot) - margin
	if d <= 0 {
		return 0
	}
	return math.Sqrt(weight) * d
}

// Nearest returns the raw (nd, vd) of the nearest catalog glass and its
// normalised distance² to (nd, vd).
func Nearest(f *CatalogField, nd, vd float64) (bestND, bestVD, bestR2 float64) {
	if f == nil || len(f.Points) == 0 {
		return 0, 0, math.MaxFloat64
	}
	nx := (nd - f.NDMin) / f.NDSpan
	ny := (vd - f.VDMin) / f.VDSpan
	bestR2 = math.MaxFloat64
	for i, p := range f.NormPoints {
		dx := nx - p.ND
		dy := ny - p.VD
		r2 := dx*dx + dy*dy
		if r2 < bestR2 {
			bestR2 = r2
			bestND = f.Points[i].ND
			bestVD = f.Points[i].VD
		}
	}
	return bestND, bestVD, bestR2
}

// NearestNamed returns the catalog key and raw (nd, vd) of the nearest
// catalog glass, plus its normalised distance².
func NearestNamed(f *CatalogField, nd, vd float64) (key string, bestND, bestVD, bestR2 float64) {
	if f == nil || len(f.Points) == 0 {
		return "", 0, 0, math.MaxFloat64
	}
	nx := (nd - f.NDMin) / f.NDSpan
	ny := (vd - f.VDMin) / f.VDSpan
	bestR2 = math.MaxFloat64
	for i, p := range f.NormPoints {
		dx := nx - p.ND
		dy := ny - p.VD
		r2 := dx*dx + dy*dy
		if r2 < bestR2 {
			bestR2 = r2
			bestND = f.Points[i].ND
			bestVD = f.Points[i].VD
			if i < len(f.Names) {
				key = f.Names[i]
			}
		}
	}
	return key, bestND, bestVD, bestR2
}
