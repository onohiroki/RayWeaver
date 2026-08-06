package importer

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

// decodeBOM strips a UTF-8/UTF-16 BOM and decodes UTF-16LE content.
func decodeBOM(input string) string {
	return string(types.DecodeBOM([]byte(input)))
}

// parseFloat parses a numeric token, treating INF/INFINITY/INFINITE as +Inf
// and ignoring a trailing semicolon.
func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	upper := strings.ToUpper(s)
	if upper == "INF" || upper == "INFINITY" || upper == "INFINITE" {
		return math.Inf(1)
	}
	s = strings.TrimRight(s, ";")
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return val
}

// parseThickness converts an infinite thickness to zero.
func parseThickness(v float64) float64 {
	if math.IsInf(v, 1) {
		return 0
	}
	return v
}

// radiusToCurvature converts a surface radius to curvature.
func radiusToCurvature(r float64) float64 {
	if r == 0 {
		return 0
	}
	return 1.0 / r
}

// getOrCreate returns the map entry, creating it if absent.
func getOrCreate[T any](m map[int]*T, id int) *T {
	if s, ok := m[id]; ok {
		return s
	}
	var s T
	m[id] = &s
	return &s
}

func defaultWavelength() types.WavelengthItem {
	return types.WavelengthItem{ID: 0, Value: types.DefaultWavelength, Weight: 1.0}
}

func defaultField() types.FieldItem {
	return types.FieldItem{ID: 0, AngleDeg: 0, Weight: 1.0}
}

// fillDefaults supplies the fallback wavelength/field if none were parsed.
func fillDefaults(result *ParseResult) {
	if len(result.Wavelengths) == 0 {
		result.Wavelengths = []types.WavelengthItem{defaultWavelength()}
	}
	if len(result.Fields) == 0 {
		result.Fields = []types.FieldItem{defaultField()}
	}
	flattenNegativeThickness(result)
}

// maxFlattenThickness bounds the negative thicknesses that flattenNegativeThickness
// will normalise. Single isolated negatives at or below this magnitude are the
// ZEMAX "stop reference plane" idiom (a zero-power iris plane a small distance in
// front of a surface); larger negatives encode telecentric/afocal return paths or
// genuine folds, which must not be flipped to positive.
const maxFlattenThickness = 10.0

// flattenNegativeThickness normalises the negative-thickness ZEMAX stop-reference
// idiom into rayweave's all-positive, monotonic-Z fold model. A surface with a
// single isolated negative thickness t (|t| <= maxFlattenThickness) is flipped to
// +|t|, which preserves every inter-surface air gap while shifting only the
// surfaces after it by 2|t|. Files with several negatives (folded/return paths)
// or a single large negative (telecentric layouts) are left untouched.
//
// The base surfaces and the per-config thickness overrides are both normalised so
// that ConfigSurfaceSet never reintroduces a negative thickness downstream.
// StopSurface (the aperture position and its diameter) is unchanged. Returns the
// number of surfaces flattened.
func flattenNegativeThickness(result *ParseResult) int {
	if result == nil {
		return 0
	}
	flattened := 0

	neg := -1
	for i := range result.Surfaces {
		if result.Surfaces[i].Thickness < 0 {
			if neg >= 0 {
				return 0
			}
			neg = i
		}
	}
	if neg >= 0 && math.Abs(result.Surfaces[neg].Thickness) <= maxFlattenThickness {
		result.Surfaces[neg].Thickness = -result.Surfaces[neg].Thickness
		flattened++
	}

	for cfg := range result.ConfigThickness {
		negSurf := -1
		for s, t := range result.ConfigThickness[cfg] {
			if t < 0 {
				if negSurf >= 0 {
					negSurf = -2
					break
				}
				negSurf = s
			}
		}
		if negSurf >= 0 && math.Abs(result.ConfigThickness[cfg][negSurf]) <= maxFlattenThickness {
			result.ConfigThickness[cfg][negSurf] = -result.ConfigThickness[cfg][negSurf]
			flattened++
		}
	}
	return flattened
}

// addGlassEntry registers a glass material, deduplicating by
// case-insensitive label. Inline "nd:vd" model glasses are expanded in place.
func addGlassEntry(result *ParseResult, mat string) {
	addGlassEntryNDV(result, mat, 0, 0)
}

// addGlassEntryNDV registers a glass material, deduplicating by
// case-insensitive label. When nd is positive the entry is a model glass
// carrying the supplied index/Abbe number (used for ZEMAX inline model
// glasses such as "___BLANK"); otherwise nd/vd are resolved from the
// "nd:vd" label convention or the built-in catalog.
func addGlassEntryNDV(result *ParseResult, mat string, nd, vd float64) {
	if mat == "" || isAir(mat) {
		return
	}
	for _, g := range result.GlassEntries {
		if strings.EqualFold(g.Label, mat) {
			return
		}
	}
	entry := types.Glass{
		Type:  types.GlassTypeModel,
		Label: mat,
	}
	if nd > 0 {
		entry.ND = nd
		entry.VD = vd
	} else if strings.Contains(mat, ":") {
		parts := strings.SplitN(mat, ":", 2)
		nd := parseFloat(parts[0])
		vd := parseFloat(parts[1])
		if nd > 0 {
			entry.ND = nd
			entry.VD = vd
		}
	} else if nd, vd, ok := LookupGlass(mat); ok {
		entry.ND = nd
		entry.VD = vd
	}
	result.GlassEntries = append(result.GlassEntries, entry)
}

// ConfigSurfaceSet returns the surfaces for a given config index: the base
// (config-0) geometry with that config's thickness/diameter overrides
// applied. When the config has no overrides the base surfaces are returned
// unchanged.
func ConfigSurfaceSet(result *ParseResult, config int) []types.Surface {
	thick, okThick := result.ConfigThickness[config]
	diam, okDiam := result.ConfigDiameter[config]
	if !okThick && !okDiam {
		return result.Surfaces
	}
	out := make([]types.Surface, len(result.Surfaces))
	copy(out, result.Surfaces)
	for i := range out {
		id := out[i].ID
		if t, ok := thick[id]; ok {
			out[i].Thickness = t
		}
		if d, ok := diam[id]; ok {
			out[i].Diameter = d
		}
	}
	return out
}

// ConfigIndexes returns the distinct config indices declared by the file
// (from THIC/SDIA overrides). Empty when the lens has no per-config
// overrides (a single-config lens) — the caller then uses the base surfaces
// directly as config 0.
func ConfigIndexes(result *ParseResult) []int {
	seen := map[int]bool{}
	var idx []int
	for c := range result.ConfigThickness {
		if !seen[c] {
			seen[c] = true
			idx = append(idx, c)
		}
	}
	for c := range result.ConfigDiameter {
		if !seen[c] {
			seen[c] = true
			idx = append(idx, c)
		}
	}
	sort.Ints(idx)
	return idx
}
