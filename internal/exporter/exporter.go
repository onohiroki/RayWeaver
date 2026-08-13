// Package exporter writes RayWeaver pipeline YAML back out as native lens
// files for other optical design tools: ZEMAX ZMX text, CODE V SEQ and OSLO
// LEN (NXT). It is the inverse of internal/importer.
//
// Supported surface features: spheres, conics, even-order polynomial
// aspheres, catalog/model glasses, fields, wavelengths, aperture stops,
// surface diameters and per-surface decenters (ZEMAX COORDBRK / CODE V DAR).
// Multi-config systems export as ZEMAX multi-config (MNUM/CONFIG + THIC/SDIA
// overrides) and CODE V zoom positions (ZOOM n + ZOO rows); OSLO carries a
// single config. Unrepresentable features (folded mirrors, Zernike aspheres,
// OSLO conics, coating assignments) are reported through the warn callback
// and exported best-effort.
package exporter

import (
	"fmt"
	"strconv"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

// Warn is the callback writers use to report unrepresentable or lossy
// features. It is optional (nil = discard).
type Warn func(format string, args ...any)

func warnf(w Warn, format string, args ...any) {
	if w == nil {
		return
	}
	w(format, args...)
}

// num formats a float for round-tripping through the importers: the shortest
// representation that parses back to the exact float64.
func num(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// materialGlass describes how a surface material must be written.
type materialGlass struct {
	keyed  bool
	name   string // catalog key for keyed glasses
	nd, vd float64
}

func glassOf(m types.Material) materialGlass {
	if m.HasKey() {
		return materialGlass{keyed: true, name: m.Key}
	}
	if m.HasModel() {
		return materialGlass{nd: m.ND, vd: m.VD}
	}
	return materialGlass{}
}

// fieldClass classifies a field item for the per-format field-type mapping.
type fieldClass int

const (
	fieldAngle fieldClass = iota
	fieldImageHeight
	fieldObjectHeight
)

func (c fieldClass) String() string {
	switch c {
	case fieldImageHeight:
		return "image height"
	case fieldObjectHeight:
		return "object height"
	default:
		return "angle"
	}
}

// zemaxFTYP maps a field class to the ZEMAX system field type (FTYP) code:
// 0 = angle (deg), 1 = object height, 2 = image height. The codes are not the
// same as the fieldClass enum values, so the mapping is explicit.
func zemaxFTYP(c fieldClass) int {
	switch c {
	case fieldObjectHeight:
		return 1
	case fieldImageHeight:
		return 2
	default:
		return 0
	}
}

func classifyField(f *types.FieldItem) fieldClass {
	switch {
	case f.ImageHeight > 0:
		return fieldImageHeight
	case f.Height > 0:
		return fieldObjectHeight
	default:
		return fieldAngle
	}
}

// fieldNeutral reports whether a field carries no value in any type (angle,
// image height and object height all zero). Such a field is the on-axis field
// of an image-height/object-height system that lost its type through the
// omitempty round trip (image_height: 0 is omitted from YAML and re-read as
// angle 0); it is written under the dominant type and never triggers a
// mixed-field-type warning.
func fieldNeutral(f *types.FieldItem) bool {
	return f.AngleDeg == 0 && f.ImageHeight == 0 && f.Height == 0
}

// dominantFieldClass returns the field type shared by most fields, warning
// when genuinely mixed types (non-zero fields of different classes) would
// otherwise be lossy. Neutral on-axis fields (all values zero) do not count:
// they are value-0 under any interpretation.
func dominantFieldClass(fields []types.FieldItem, w Warn) fieldClass {
	if len(fields) == 0 {
		return fieldAngle
	}
	counts := map[fieldClass]int{}
	best := fieldAngle
	for _, f := range fields {
		if fieldNeutral(&f) {
			continue
		}
		c := classifyField(&f)
		counts[c]++
		if counts[c] > counts[best] {
			best = c
		}
	}
	if len(counts) > 1 {
		warnf(w, "mixed field types (angle / image height / object height); exporting as %s", best)
	}
	return best
}

// resolveStop returns the stop surface index (into the surfaces slice) for a
// config, or -1 when no stop is declared. The chief section is optional.
func resolveStop(cfg *types.Config, chief *types.ChiefInput) int {
	if cfg != nil {
		for _, rp := range cfg.RayPaths {
			if rp.StopSurface > 0 {
				return surfaceIndex(cfg.Surfaces, rp.StopSurface)
			}
		}
	}
	if chief != nil && chief.StopSurface > 0 && cfg != nil {
		return surfaceIndex(cfg.Surfaces, chief.StopSurface)
	}
	return -1
}

func surfaceIndex(surfaces []types.Surface, id int) int {
	for i := range surfaces {
		if surfaces[i].ID == id {
			return i
		}
	}
	return -1
}

// glassIndexes returns the index of a material at the d, F and C lines (mm),
// the OSLO model-glass convention. A missing catalog falls back to nd for all
// three.
func glassIndexes(gc *glass.Catalog, m types.Material) (nd, nF, nC float64) {
	if m.IsAir() {
		return 1, 1, 1
	}
	if m.HasModel() {
		nd = m.ND
	}
	if gc != nil {
		if n, err := gc.RefractiveIndex(m, 0.000587562); err == nil {
			nd = n
		}
		if n, err := gc.RefractiveIndex(m, 0.000486133); err == nil {
			nF = n
		}
		if n, err := gc.RefractiveIndex(m, 0.000656273); err == nil {
			nC = n
		}
	}
	if nF == 0 {
		nF = nd
	}
	if nC == 0 {
		nC = nd
	}
	return nd, nF, nC
}

// glassName derives the text label written for a material: the catalog key
// for keyed glasses, else "nd:vd" (CODE V) or "nd,vd" (ZEMAX model) style.
func glassName(m types.Material, sep string) string {
	if m.HasKey() {
		return m.Key
	}
	if m.HasModel() {
		return fmt.Sprintf("%s%s%s", num(m.ND), sep, num(m.VD))
	}
	return ""
}

// asphereOrders returns the even orders (4, 6, ...) covered by the surface's
// polynomial coefficients.
func asphereOrders(s *types.Surface) []int {
	var orders []int
	for i := range s.Coefficients {
		if s.Coefficients[i] != 0 {
			orders = append(orders, 2*i+4)
		}
	}
	return orders
}

// codeVLetter returns the CODE V compact asphere letter for an even order
// (4 -> A, 6 -> B, ... 20 -> J).
func codeVLetter(order int) string {
	if order < 4 || order > 20 || order%2 != 0 {
		return ""
	}
	return string(rune('A' + (order-4)/2))
}

// semiDiameter converts a RayWeaver diameter to the semi-diameter the foreign
// formats use (ZEMAX DIAM, CODE V CIR, OSLO AP).
func semiDiameter(d float64) float64 {
	return d / 2
}
