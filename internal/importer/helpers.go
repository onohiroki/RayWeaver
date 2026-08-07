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
	zeroNegativeDummyThickness(result)
}

// zeroNegativeDummyThickness normalises the negative-spacing dummy-surface idiom
// (a zero-power reference plane with a negative spacing) into rayweave's
// all-positive, monotonic-Z model. A surface is a dummy only when it carries no
// optical power (Curvature == 0) and is not a mirror (material REFL/MIRROR);
// folded mirrors and powered surfaces with negative spacings encode genuine
// return paths and must not be collapsed.
//
// Each dummy's thickness is set to 0 so the trace continues. When the dummy held
// the aperture stop, the stop is dropped (StopSurface = 0) and the pipeline falls
// back to the dynamic pupil. The base surfaces and the per-config thickness
// overrides are both normalised so that ConfigSurfaceSet never reintroduces a
// negative thickness downstream. Returns the number of surfaces zeroed.
func zeroNegativeDummyThickness(result *ParseResult) int {
	if result == nil {
		return 0
	}
	zeroed := 0

	zeroedIDs := map[int]bool{}
	for i := range result.Surfaces {
		s := &result.Surfaces[i]
		if s.Thickness < 0 && isDummySurface(s) {
			s.Thickness = 0
			zeroed++
			zeroedIDs[s.ID] = true
		}
	}

	for cfg := range result.ConfigThickness {
		for s, t := range result.ConfigThickness[cfg] {
			if t < 0 && isDummySurfaceID(result, s) {
				result.ConfigThickness[cfg][s] = 0
				zeroed++
				zeroedIDs[s] = true
			}
		}
	}

	if result.StopSurface > 0 && zeroedIDs[result.StopSurface] {
		result.StopSurface = 0
	}

	return zeroed
}

// isDummySurface reports whether a surface is a zero-power non-mirror reference
// plane (the CODE V/ZEMAX "dummy" convention).
func isDummySurface(s *types.Surface) bool {
	return s.Curvature == 0 && !isMirrorMaterial(s.Material)
}

// isDummySurfaceID looks a surface up by ID and reports whether it is a dummy.
func isDummySurfaceID(result *ParseResult, id int) bool {
	for i := range result.Surfaces {
		if result.Surfaces[i].ID == id {
			return isDummySurface(&result.Surfaces[i])
		}
	}
	return false
}

// isMirrorMaterial reports whether a material label denotes a mirror surface.
func isMirrorMaterial(mat string) bool {
	m := strings.ToUpper(strings.TrimSpace(mat))
	return m == "REFL" || m == "MIRROR"
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
	} else if nd, vd, ok := decodeDispersionCode(mat); ok {
		entry.ND = nd
		entry.VD = vd
	} else if nd, vd, ok := resolveCommonGlass(mat); ok {
		entry.ND = nd
		entry.VD = vd
	}
	result.GlassEntries = append(result.GlassEntries, entry)
}

// decodeDispersionCode decodes a glass code into nd = 1.nnn and vd = vv.v.
// The bare ZEMAX/OSLO 6-digit form is "nnnvvv" (e.g. 748523 -> 1.748/52.3);
// CODE V writes the same digits with a separator dot, padding each half with
// zeros (e.g. "500.700" -> 1.500/70.0, "517000.520000" -> 1.517/52.0).
func decodeDispersionCode(code string) (nd, vd float64, ok bool) {
	ndStr, vdStr := "", ""
	if i := strings.IndexByte(code, '.'); i >= 0 {
		ndStr, vdStr = code[:i], code[i+1:]
	} else {
		if len(code) != 6 {
			return 0, 0, false
		}
		ndStr, vdStr = code[:3], code[3:]
	}
	nd3, ok1 := leadingDigits(ndStr)
	vd3, ok2 := leadingDigits(vdStr)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return 1 + nd3/1000.0, vd3 / 10.0, true
}

// leadingDigits returns the value of the leading run of up to three digits.
func leadingDigits(s string) (float64, bool) {
	var d float64
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		if n >= 3 {
			break
		}
		d = d*10 + float64(r-'0')
		n++
	}
	return d, n > 0
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
