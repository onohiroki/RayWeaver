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

// ndvdLines returns the d/F/C line wavelengths in the unit expected by g's
// dispersion model. Catalog dispersion formulas (sellmeier/schott/extended)
// are fitted for micrometres; model and tabulated glasses use millimetres,
// the internal wavelength unit of the ray trace.
func ndvdLines(g *types.Glass) (d, f, c float64) {
	switch {
	case g.Type == types.GlassTypeCatalog &&
		(g.DispersionFormula == types.Sellmeier1 || g.DispersionFormula == types.Schott ||
			g.DispersionFormula == types.Extended2 || g.DispersionFormula == types.Extended3):
		return 0.587562, 0.486133, 0.656273
	default:
		return 0.000587562, 0.000486133, 0.000656273
	}
}

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

	wlD, wlF, wlC := ndvdLines(g)
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
		if g, ok := c.Lookup(prefix); ok && strings.ToUpper(g.Manufacturer) == wantMfr {
			return g, true
		}
	}
	return nil, false
}

func (c *Catalog) RefractiveIndex(material string, wavelength float64) (float64, error) {
	if material == "AIR" || material == "" {
		return 1.0, nil
	}

	g, ok := c.Lookup(material)
	if !ok {
		return 0, fmt.Errorf("glass not found: %s", material)
	}

	// Cache the computed index per (glass, nd/vd, wavelength). Model glasses
	// optimised by nd/vd change values between evaluations, so those are part
	// of the key; catalog/tabulated glasses use zero nd/vd markers that are
	// stable across calls.
	key := c.cacheKey(g, material, wavelength)
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

func CalcRefractiveIndex(g *types.Glass, wavelength float64) (float64, error) {
	switch g.Type {
	case types.GlassTypeCatalog:
		switch g.DispersionFormula {
		case types.Sellmeier1:
			return sellmeier1(g.Coefficients, wavelength)
		case types.Schott:
			return schott(g.Coefficients, wavelength)
		case types.Extended3:
			return extended3(g.Coefficients, wavelength)
		case types.Extended2:
			return extended2(g.Coefficients, wavelength)
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
		splineMin = 0.000365
		splineMax = 0.002058
		cauchyMin = 0.000320
		cauchyMax = 0.005000
	)

	if wavelength >= splineMin && wavelength <= splineMax {
		indices := IndecesFromNdVd(nd, vd)
		knots := sortedKnots(indices)
		return SplineInterpolate(knots, wavelength)
	}

	if wavelength > cauchyMin && wavelength < splineMin ||
		wavelength > splineMax && wavelength < cauchyMax {
		indices := IndecesFromNdVd(nd, vd)
		knots := sortedKnots(indices)
		ca := FitCauchy(knots, 3)
		return ca.Eval(wavelength), nil
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

	if wavelength <= sorted[0].Wavelength {
		return sorted[0].Value, nil
	}
	if wavelength >= sorted[len(sorted)-1].Wavelength {
		return sorted[len(sorted)-1].Value, nil
	}

	for i := 0; i < len(sorted)-1; i++ {
		if wavelength >= sorted[i].Wavelength && wavelength <= sorted[i+1].Wavelength {
			t := (wavelength - sorted[i].Wavelength) / (sorted[i+1].Wavelength - sorted[i].Wavelength)
			return sorted[i].Value + t*(sorted[i+1].Value-sorted[i].Value), nil
		}
	}

	return sorted[len(sorted)-1].Value, nil
}
