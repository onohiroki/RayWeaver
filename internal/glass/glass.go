package glass

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/hiroki/rayweaver/internal/types"
)

// d/F/C spectral lines in mm (the internal wavelength unit).
const (
	wlD = 0.000587562
	wlF = 0.000486133
	wlC = 0.000656273
)

// NDVD returns the d-line refractive index and Abbe number for g. When nd/vd
// are not stored directly (coefficient-only catalog glasses or tabulated
// glasses), it computes n(d), n(F), n(C) via CalcRefractiveIndex and derives
// vd = (nd-1)/(nF-nC). ok is false when neither source yields valid values.
func NDVD(g *types.Glass) (nd, vd float64, ok bool) {
	if g == nil {
		return 0, 0, false
	}
	if g.ND > 0 && g.VD > 0 {
		return g.ND, g.VD, true
	}

	nD, errD := CalcRefractiveIndex(g, wlD)
	nF, errF := CalcRefractiveIndex(g, wlF)
	nC, errC := CalcRefractiveIndex(g, wlC)

	if g.ND > 0 {
		nd = g.ND
	} else if errD == nil {
		nd = nD
	} else {
		return 0, 0, false
	}

	if errF != nil || errC != nil {
		return nd, 0, true
	}
	if nd <= 1 || nF-nC == 0 {
		return nd, 0, true
	}
	return nd, (nd - 1) / (nF - nC), true
}

type Catalog struct {
	ByName map[string]*types.Glass

	indexCache sync.Map
}

func NewCatalog() *Catalog {
	return &Catalog{
		ByName: make(map[string]*types.Glass),
	}
}

// Has reports whether the catalog contains an entry for the given key (including
// normalised variants).
func (c *Catalog) Has(key string) bool {
	if _, ok := c.ByName[key]; ok {
		return true
	}
	if norm := NormalizeName(key); norm != key {
		if _, ok := c.ByName[norm]; ok {
			return true
		}
	}
	return false
}

// NormalizeName normalizes a glass name for lookup: hyphens and underscores
// are removed and the result is uppercased. CODE V references glasses without
// the separators used in AGF names (e.g. "LLAL12" for "L-LAL12").
func NormalizeName(name string) string {
	s := strings.ReplaceAll(name, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return strings.ToUpper(s)
}

func (c *Catalog) Add(glass types.Glass) {
	key := types.ResolveGlassKey(glass)
	g := glass
	g.Key = key
	c.addKey(key, &g)
	if g.Label != "" && g.Label != key {
		c.addKey(g.Label, &g)
	}
	for _, alias := range glass.Aliases {
		c.addKey(alias, &g)
	}
}

// addKey registers g under key and its normalized (hyphen/underscore-stripped,
// uppercased) variant so lookups work with either the AGF or CODE V spelling.
func (c *Catalog) addKey(key string, g *types.Glass) {
	c.ByName[key] = g
	norm := NormalizeName(key)
	if norm != key {
		c.ByName[norm] = g
	}
}

func (c *Catalog) Lookup(key string) (*types.Glass, bool) {
	if g, ok := c.ByName[key]; ok {
		return g, true
	}
	for _, g := range c.ByName {
		if g.Name == key {
			return g, true
		}
	}
	if norm := NormalizeName(key); norm != key {
		if g, ok := c.ByName[norm]; ok {
			return g, true
		}
	}
	// CODE V "MFR_GLASS" convention: material names may carry a manufacturer
	// suffix (e.g. "LLAL12_OHARA"). Match the prefix and confirm the
	// manufacturer, mirroring the import-time lookup.
	if i := strings.IndexByte(key, '_'); i > 0 {
		prefix := key[:i]
		wantMfr := strings.ToUpper(key[i+1:])
		if wantMfr != "" {
			if g, ok := c.Lookup(prefix); ok && strings.ToUpper(g.Manufacturer) == wantMfr {
				return g, true
			}
		}
	}
	// Moulding-grade suffix: "N-BK7_MOLD" is the pressing blank of the same
	// glass, so strip it and resolve the base name.
	if strings.HasSuffix(key, "_MOLD") {
		if g, ok := c.Lookup(key[:len(key)-len("_MOLD")]); ok {
			return g, true
		}
	}
	// Moulding-grade trailing "M" (Ohara "S-BAL42M", CDGM "D-LAK6M", Sumita
	// "K-VC79M"): the base name is the same glass. Only applies when the base
	// actually resolves, so a legitimate name ending in M is unaffected.
	if strings.HasSuffix(key, "M") && len(key) > 2 {
		if g, ok := c.Lookup(key[:len(key)-1]); ok {
			return g, true
		}
	}
	// Resin notation: "AL-6263-(OKP4HT)" names a moulding compound by the
	// parenthesised resin, which is the actual optical material.
	if i := strings.IndexByte(key, '('); i >= 0 && strings.HasSuffix(key, ")") {
		if g, ok := c.Lookup(key[i+1 : len(key)-1]); ok {
			return g, true
		}
	}
	return nil, false
}

func (c *Catalog) RefractiveIndex(mat types.Material, wavelength float64) (float64, error) {
	if mat.IsAir() {
		return 1.0, nil
	}

	// Catalog reference (key takes precedence over an inline nd/vd): the entry
	// may be a catalog (dispersion formula), model (nd/vd) or tabulated glass.
	if mat.HasKey() {
		g, ok := c.Lookup(mat.Key)
		if !ok {
			return 0, fmt.Errorf("glass not found: %s", mat.Key)
		}
		key := c.cacheKey(g, mat.String(), wavelength)
		if v, ok := c.indexCache.Load(key); ok {
			return v.(float64), nil
		}
		n, err := CalcRefractiveIndex(g, wavelength)
		if err != nil {
			return 0, err
		}
		c.indexCache.Store(key, n)
		return n, nil
	}

	// Self-contained model glass: nd/vd live directly on the material.
	if mat.HasModel() {
		key := "model|" + strconv.FormatFloat(mat.ND, 'g', -1, 64) + ":" + strconv.FormatFloat(mat.VD, 'g', -1, 64) + "|" + strconv.FormatFloat(wavelength, 'g', -1, 64)
		if v, ok := c.indexCache.Load(key); ok {
			return v.(float64), nil
		}
		n, err := RefractiveIndexFromNDVD(mat.ND, mat.VD, wavelength)
		if err != nil {
			return 0, err
		}
		c.indexCache.Store(key, n)
		return n, nil
	}

	return 1.0, nil
}

func (c *Catalog) cacheKey(g *types.Glass, material string, wavelength float64) string {
	var sb strings.Builder
	sb.WriteString(material)
	sb.WriteByte('|')
	sb.WriteString(strconv.FormatFloat(g.ND, 'g', -1, 64))
	sb.WriteByte('|')
	sb.WriteString(strconv.FormatFloat(g.VD, 'g', -1, 64))
	sb.WriteByte('|')
	sb.WriteString(strconv.FormatFloat(wavelength, 'g', -1, 64))
	return sb.String()
}

// CalcRefractiveIndex computes the refractive index of g at the given
// wavelength in mm (the internal ray-trace unit). Catalog dispersion formulas
// (sellmeier/schott/extended) are fitted for micrometre wavelengths per the
// AGF convention, so the wavelength is converted to µm before evaluation;
// model and tabulated glasses consume mm directly.
func CalcRefractiveIndex(g *types.Glass, wavelength float64) (float64, error) {
	switch g.Type {
	case types.GlassTypeCatalog:
		switch g.DispersionFormula {
		case types.Sellmeier1:
			return sellmeier1(g.Coefficients, wavelength*1000)
		case types.Schott:
			return schott(g.Coefficients, wavelength*1000)
		case types.Extended3:
			return extended3(g.Coefficients, wavelength*1000)
		case types.Extended2:
			return extended2(g.Coefficients, wavelength*1000)
		case types.Laurent:
			return laurent(g.Coefficients, wavelength*1000)
		case types.Cauchy:
			return cauchy(g.Coefficients, wavelength*1000)
		case types.Hartmann:
			return hartmann(g.Coefficients, wavelength*1000)
		case types.Constant:
			return g.ND, nil
		default:
			if g.ND > 0 && g.VD > 0 {
				return RefractiveIndexFromNDVD(g.ND, g.VD, wavelength)
			}
			return 0, fmt.Errorf("catalog glass %q has unknown dispersion formula", g.Key)
		}
	case types.GlassTypeModel:
		if g.ND == 0 || g.VD == 0 {
			return 0, fmt.Errorf("model glass %q missing nd/vd", g.Key)
		}
		return RefractiveIndexFromNDVD(g.ND, g.VD, wavelength)
	case types.GlassTypeTabulated:
		if len(g.RefractiveIndices) == 0 {
			return 0, fmt.Errorf("tabulated glass %q has no index data", g.Key)
		}
		return interpolateRefractiveIndex(g.RefractiveIndices, wavelength)
	default:
		return 0, fmt.Errorf("cannot compute refractive index for %s", g.Key)
	}
}

// sellmeier1 evaluates the Sellmeier 1 formula with coefficients in µm
// wavelength units (AGF convention).
func sellmeier1(coeffs []float64, lambda float64) (float64, error) {
	if len(coeffs) < 6 {
		return 0, fmt.Errorf("sellmeier_1 requires 6 coefficients")
	}
	lsq := lambda * lambda
	n2 := 1.0
	for i := 0; i < 3; i++ {
		b := coeffs[2*i]
		c := coeffs[2*i+1]
		n2 += b * lsq / (lsq - c)
	}
	if n2 <= 0 {
		return 0, fmt.Errorf("invalid sellmeier result")
	}
	return math.Sqrt(n2), nil
}

// schott evaluates the Schott formula with coefficients in µm wavelength
// units (AGF convention).
func schott(coeffs []float64, lambda float64) (float64, error) {
	if len(coeffs) < 6 {
		return 0, fmt.Errorf("schott requires 6 coefficients")
	}
	lsq := lambda * lambda
	l2 := 1.0 / lsq
	l4 := l2 * l2
	l6 := l4 * l2
	l8 := l6 * l2
	n2 := coeffs[0] + coeffs[1]*lsq + coeffs[2]*l2 + coeffs[3]*l4 + coeffs[4]*l6 + coeffs[5]*l8
	if n2 <= 0 {
		return 0, fmt.Errorf("invalid schott result")
	}
	return math.Sqrt(n2), nil
}

// laurent evaluates the Laurent dispersion formula (CODE V "LAU", also the
// classic Schott polynomial) with coefficients in µm wavelength units:
//
//	n² = A₀ + A₁λ² + A₂/λ² + A₃/λ⁴ + A₄/λ⁶ + A₅/λ⁸
func laurent(coeffs []float64, lambda float64) (float64, error) {
	if len(coeffs) < 6 {
		return 0, fmt.Errorf("laurent requires 6 coefficients")
	}
	lsq := lambda * lambda
	l2 := 1.0 / lsq
	l4 := l2 * l2
	l6 := l4 * l2
	l8 := l6 * l2
	n2 := coeffs[0] + coeffs[1]*lsq + coeffs[2]*l2 + coeffs[3]*l4 + coeffs[4]*l6 + coeffs[5]*l8
	if n2 <= 0 {
		return 0, fmt.Errorf("invalid laurent result")
	}
	return math.Sqrt(n2), nil
}

// cauchy evaluates the Cauchy dispersion formula (CODE V "CAU"), returning n
// directly (not n²). Coefficients are in µm wavelength units:
//
//	n(λ) = A₀ + A₁/λ² + A₂/λ⁴ + A₃/λ⁶ + …
//
// The number of terms is taken from the coefficient count (at least one).
func cauchy(coeffs []float64, lambda float64) (float64, error) {
	if len(coeffs) < 1 {
		return 0, fmt.Errorf("cauchy requires at least 1 coefficient")
	}
	x := 1.0 / (lambda * lambda)
	n := coeffs[0]
	xi := x
	for i := 1; i < len(coeffs); i++ {
		n += coeffs[i] * xi
		xi *= x
	}
	return n, nil
}

// hartmann evaluates the Hartmann dispersion formula (CODE V "HAR") with
// coefficients in µm wavelength units, returning n directly:
//
//	n(λ) = A₀ + A₁/(λ − A₂)
func hartmann(coeffs []float64, lambda float64) (float64, error) {
	if len(coeffs) < 3 {
		return 0, fmt.Errorf("hartmann requires 3 coefficients")
	}
	if lambda == coeffs[2] {
		return 0, fmt.Errorf("hartmann: wavelength at resonance (%g)", lambda)
	}
	return coeffs[0] + coeffs[1]/(lambda-coeffs[2]), nil
}

// extended3 evaluates the extended 3 formula with coefficients in µm
// wavelength units (AGF convention).
func extended3(coeffs []float64, lambda float64) (float64, error) {
	if len(coeffs) < 9 {
		return 0, fmt.Errorf("extended_3 requires 9 coefficients")
	}
	lsq := lambda * lambda
	l4 := lsq * lsq
	l2 := 1.0 / lsq
	l4i := l2 * l2
	l6i := l4i * l2
	l8i := l6i * l2
	l10i := l8i * l2
	l12i := l10i * l2
	n2 := coeffs[0] + coeffs[1]*lsq + coeffs[2]*l4 + coeffs[3]*l2 + coeffs[4]*l4i + coeffs[5]*l6i + coeffs[6]*l8i + coeffs[7]*l10i + coeffs[8]*l12i
	if n2 <= 0 {
		return 0, fmt.Errorf("invalid extended_3 result")
	}
	return math.Sqrt(n2), nil
}

// extended2 evaluates the extended 2 formula with coefficients in µm
// wavelength units (AGF convention).
func extended2(coeffs []float64, lambda float64) (float64, error) {
	if len(coeffs) < 10 {
		return 0, fmt.Errorf("extended_2 requires 10 coefficients")
	}
	lsq := lambda * lambda
	n2 := 1.0
	for i := 0; i < 5; i++ {
		b := coeffs[2*i]
		c := coeffs[2*i+1]
		n2 += b * lsq / (lsq - c)
	}
	if n2 <= 0 {
		return 0, fmt.Errorf("invalid extended_2 result")
	}
	return math.Sqrt(n2), nil
}

func RefractiveIndexFromNDVD(nd, vd, wavelength float64) (float64, error) {
	const (
		cauchyMin = 0.000320
		cauchyMax = 0.005000
	)

	indices := IndecesFromNdVd(nd, vd)
	knots := sortedKnots(indices)
	lo := knots[0].Wavelength
	hi := knots[len(knots)-1].Wavelength

	if wavelength >= lo && wavelength <= hi {
		return SplineInterpolate(knots, wavelength)
	}

	if wavelength > cauchyMin && wavelength < cauchyMax {
		ca := FitCauchy(knots, 3)
		if wavelength < lo {
			ca = ConnectedCauchy(ca, lo, knots[0].Index)
		} else {
			ca = ConnectedCauchy(ca, hi, knots[len(knots)-1].Index)
		}
		n := ca.Eval(wavelength)
		if n < 1.0 {
			n = 1.0
		}
		return n, nil
	}

	return nd, nil
}

func sortedKnots(indices map[string]IndexEntry) []IndexEntry {
	knots := make([]IndexEntry, 0, len(indices))
	for _, e := range indices {
		knots = append(knots, e)
	}
	for i := 0; i < len(knots); i++ {
		for j := i + 1; j < len(knots); j++ {
			if knots[j].Wavelength < knots[i].Wavelength {
				knots[i], knots[j] = knots[j], knots[i]
			}
		}
	}
	return knots
}

func interpolateRefractiveIndex(entries types.RefractiveIndexTable, wavelength float64) (float64, error) {
	if len(entries) == 0 {
		return 0, fmt.Errorf("no refractive index entries")
	}

	sorted := make(types.RefractiveIndexTable, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Wavelength < sorted[j].Wavelength
	})

	// Spline interpolation needs at least 3 distinct knots; smaller tables stay
	// linear (constant for a single entry, clamped beyond the endpoints).
	if len(sorted) < 3 {
		return linearInterpolateTable(sorted, wavelength), nil
	}

	knots := make([]IndexEntry, len(sorted))
	for i, e := range sorted {
		knots[i] = IndexEntry{Wavelength: e.Wavelength, Index: e.Value}
	}

	s, err := BuildSpline(knots)
	if err != nil {
		// Duplicate or non-increasing wavelengths: fall back to linear/clamped.
		return linearInterpolateTable(sorted, wavelength), nil
	}

	// Outside the table, extrapolate with a Cauchy fit connected C0-continuously
	// to the spline at the table edge, clamped to a physical n >= 1.
	if wavelength < knots[0].Wavelength {
		ca := ConnectedCauchy(FitCauchy(knots, 3), knots[0].Wavelength, knots[0].Index)
		return clampIndex(ca.Eval(wavelength)), nil
	}
	if wavelength > knots[len(knots)-1].Wavelength {
		last := len(knots) - 1
		ca := ConnectedCauchy(FitCauchy(knots, 3), knots[last].Wavelength, knots[last].Index)
		return clampIndex(ca.Eval(wavelength)), nil
	}

	return EvalSpline(knots, s, wavelength), nil
}

func linearInterpolateTable(sorted types.RefractiveIndexTable, wavelength float64) float64 {
	if wavelength <= sorted[0].Wavelength {
		return sorted[0].Value
	}
	if wavelength >= sorted[len(sorted)-1].Wavelength {
		return sorted[len(sorted)-1].Value
	}

	for i := 0; i < len(sorted)-1; i++ {
		if wavelength >= sorted[i].Wavelength && wavelength <= sorted[i+1].Wavelength {
			t := (wavelength - sorted[i].Wavelength) / (sorted[i+1].Wavelength - sorted[i].Wavelength)
			return sorted[i].Value + t*(sorted[i+1].Value-sorted[i].Value)
		}
	}

	return sorted[len(sorted)-1].Value
}

func clampIndex(n float64) float64 {
	if n < 1.0 {
		return 1.0
	}
	return n
}
