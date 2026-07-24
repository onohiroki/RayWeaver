package glass

import (
	"fmt"
	"math"
	"sort"

	"github.com/hiroki/rayweaver/internal/types"
)

type Catalog struct {
	ByName map[string]*types.Glass
}

func NewCatalog() *Catalog {
	return &Catalog{
		ByName: make(map[string]*types.Glass),
	}
}

func (c *Catalog) Add(glass types.Glass) {
	g := glass
	c.ByName[glass.Name] = &g
	for _, alias := range glass.Aliases {
		c.ByName[alias] = &g
	}
}

func (c *Catalog) Lookup(name string) (*types.Glass, bool) {
	g, ok := c.ByName[name]
	return g, ok
}

func (c *Catalog) RefractiveIndex(material string, wavelength float64) (float64, error) {
	if material == "AIR" || material == "" {
		return 1.0, nil
	}

	g, ok := c.Lookup(material)
	if !ok {
		return 0, fmt.Errorf("glass not found: %s", material)
	}

	return CalcRefractiveIndex(g, wavelength)
}

func CalcRefractiveIndex(g *types.Glass, wavelength float64) (float64, error) {
	if len(g.RefractiveIndices) > 0 {
		return interpolateRefractiveIndex(g.RefractiveIndices, wavelength)
	}

	switch g.DispersionFormula {
	case types.Sellmeier1:
		return sellmeier1(g.Coefficients, wavelength)
	case types.Constant:
		return g.ND, nil
	default:
		if g.ND != 0 && g.VD != 0 {
			return RefractiveIndexFromNDVD(g.ND, g.VD, wavelength)
		}
		return 0, fmt.Errorf("cannot compute refractive index for %s", g.Name)
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

func interpolateRefractiveIndex(entries []types.RefractiveIndexEntry, wavelength float64) (float64, error) {
	if len(entries) == 0 {
		return 0, fmt.Errorf("no refractive index entries")
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Wavelength < entries[j].Wavelength
	})

	if wavelength <= entries[0].Wavelength {
		return entries[0].Value, nil
	}
	if wavelength >= entries[len(entries)-1].Wavelength {
		return entries[len(entries)-1].Value, nil
	}

	for i := 0; i < len(entries)-1; i++ {
		if wavelength >= entries[i].Wavelength && wavelength <= entries[i+1].Wavelength {
			t := (wavelength - entries[i].Wavelength) / (entries[i+1].Wavelength - entries[i].Wavelength)
			return entries[i].Value + t*(entries[i+1].Value-entries[i].Value), nil
		}
	}

	return entries[len(entries)-1].Value, nil
}
