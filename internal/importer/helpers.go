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

// surfaceValue converts a CODE V surface first value into a curvature,
// honouring the RDM entry mode: in radius mode the value is a radius of
// curvature, in curvature mode it is already a curvature.
func surfaceValue(v float64, radiusMode bool) float64 {
	if radiusMode {
		return radiusToCurvature(v)
	}
	return v
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
	shiftNegativeDummyThickness(result)
}

// mirrorFoldStep is the decenter step that folds a mirror in the beam frame: a
// 180-degree Y tilt with scope: both reflects the ray off the surface and, at
// the fold walk, reverses the optical axis for the following surfaces. The
// surface itself carries `reflect: true` (set in convertFoldMirrors).
var mirrorFoldStep = types.DecenterStep{
	Tilt:  types.Vec3{X: 0, Y: 180, Z: 0},
	Scope: types.ScopeBoth,
}

// convertFoldMirrors converts CODE V / ZEMAX folded-mirror systems into
// rayweave's fold model. Mirror surfaces (material REFL/MIRROR) gain the fold
// decenter step and revert to the surrounding medium (AIR); surfaces in the
// reflected frame (after an odd number of mirrors) have their curvature sign
// negated and their (negative) thickness made positive, matching the reversed
// Z axis of the fold. The mirror's own curvature is part of the reflected
// frame, so it is sign-flipped too. Returns the number of mirrors converted.
func convertFoldMirrors(result *ParseResult) int {
	if result == nil {
		return 0
	}
	reflected := make([]bool, len(result.Surfaces))
	reflectCount := 0
	converted := 0
	for i := range result.Surfaces {
		s := &result.Surfaces[i]
		if isMirrorMaterial(s.Material.Key) {
			reflectCount++
			s.Decenter = append(s.Decenter, mirrorFoldStep)
			s.Reflect = true
			s.Material = types.Material{}
			converted++
		}
		if reflectCount%2 == 1 {
			reflected[i] = true
			s.Curvature = -s.Curvature
			if s.Thickness < 0 {
				s.Thickness = -s.Thickness
			}
		}
	}
	// Per-config thickness overrides for surfaces in the reflected frame carry
	// the same reversed-axis spacing; normalise them the same way so that
	// ConfigSurfaceSet never reintroduces a negative thickness downstream.
	if reflectCount > 0 {
		for cfg := range result.ConfigThickness {
			for surfID, t := range result.ConfigThickness[cfg] {
				idx := surfaceIndexByID(result.Surfaces, surfID)
				if idx >= 0 && idx < len(reflected) && reflected[idx] && t < 0 {
					result.ConfigThickness[cfg][surfID] = -t
				}
			}
		}
	}
	return converted
}

// surfaceIndexByID returns the index of the surface with the given ID, or -1.
func surfaceIndexByID(surfaces []types.Surface, id int) int {
	for i := range surfaces {
		if surfaces[i].ID == id {
			return i
		}
	}
	return -1
}

// shiftNegativeDummyThickness normalises the negative-spacing dummy-surface
// idiom (a zero-power reference plane with a negative spacing) into rayweave's
// all-positive, monotonic-Z model. A surface is a dummy only when it carries no
// optical power (Curvature == 0) and is not a mirror (material REFL/MIRROR);
// folded mirrors and powered surfaces with negative spacings encode genuine
// return paths and must not be collapsed.
//
// The negative spacing is not discarded: it is reproduced as a scope-surface
// decenter shift on the dummy itself, so the plane's global vertex keeps the
// originally-mapped position while its thickness is set to 0 so the beam frame
// (and the trace) continues past the plane unchanged. When the dummy held the
// aperture stop, the stop is dropped (StopSurface = 0) and the pipeline falls
// back to the dynamic pupil. The per-config thickness overrides are normalised
// the same way so that ConfigSurfaceSet never reintroduces a negative thickness
// downstream. Returns the number of surfaces converted.
func shiftNegativeDummyThickness(result *ParseResult) int {
	if result == nil {
		return 0
	}
	shifted := 0

	shiftedIDs := map[int]bool{}
	for i := range result.Surfaces {
		s := &result.Surfaces[i]
		if s.Thickness < 0 && isDummySurface(s) {
			s.Decenter = append(s.Decenter, types.DecenterStep{
				Shift: types.Vec3{Z: s.Thickness},
			})
			s.Thickness = 0
			shifted++
			shiftedIDs[s.ID] = true
		}
	}

	for cfg := range result.ConfigThickness {
		for s, t := range result.ConfigThickness[cfg] {
			if t < 0 && isDummySurfaceID(result, s) {
				result.ConfigThickness[cfg][s] = 0
				shifted++
				shiftedIDs[s] = true
			}
		}
	}

	if result.StopSurface > 0 && shiftedIDs[result.StopSurface] {
		result.StopSurface = 0
	}

	return shifted
}

// isDummySurface reports whether a surface is a zero-power non-mirror reference
// plane (the CODE V/ZEMAX "dummy" convention).
func isDummySurface(s *types.Surface) bool {
	return s.Curvature == 0 && !isMirrorMaterial(s.Material.Key)
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
	if mat == "" || isAir(mat) || isMirrorMaterial(mat) {
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

// decodeDispersionCode decodes a glass code into nd and vd.
//
// The bare ZEMAX/OSLO 6-digit form is "nnnvvv" (e.g. 748523 -> 1.748/52.3):
// nd = 1 + nnn/1000, vd = vvv/10.
//
// CODE V uses a separator dot: the part before the dot encodes the fractional
// digits of nd (nd = 1 + value/10^len), and the part after the dot encodes the
// Abbe number with a fixed 2-digit integer part (vd = value/10^(len-2)).
// Each half may be any length; trailing digits beyond the first 3 (nd) or
// 2+decimals (vd) are significant.  Examples:
//
//	"506.47"    -> nd = 1 + 506/10^3 = 1.506,  vd = 47/10^0    = 47.0
//	"5.654321"  -> nd = 1 + 5/10^1   = 1.5,    vd = 654321/10^4 = 65.4321
//	"50.647"    -> nd = 1 + 50/10^2  = 1.50,   vd = 647/10^1   = 64.7
//	"500.700"   -> nd = 1 + 500/10^3 = 1.500,  vd = 700/10^1   = 70.0
func decodeDispersionCode(code string) (nd, vd float64, ok bool) {
	ndStr, vdStr := "", ""
	if i := strings.IndexByte(code, '.'); i >= 0 {
		ndStr, vdStr = code[:i], code[i+1:]
		ndVal, ndLen, ok1 := parseLeadingDigits(ndStr)
		vdVal, vdLen, ok2 := parseLeadingDigits(vdStr)
		if !ok1 || !ok2 {
			return 0, 0, false
		}
		nd = 1 + float64(ndVal)/math.Pow(10, float64(ndLen))
		if vdLen >= 2 {
			vd = float64(vdVal) / math.Pow(10, float64(vdLen-2))
		} else {
			vd = float64(vdVal) * math.Pow(10, float64(2-vdLen))
		}
		return nd, vd, true
	}
	if len(code) != 6 {
		return 0, 0, false
	}
	ndStr, vdStr = code[:3], code[3:]
	nd3, ok1 := leadingDigits(ndStr)
	vd3, ok2 := leadingDigits(vdStr)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return 1 + nd3/1000.0, vd3/10.0, true
}

// parseLeadingDigits returns the integer value of the leading run of digits,
// the count of digits consumed, and whether at least one digit was found.
// Unlike leadingDigits it has no upper bound on the digit count.
func parseLeadingDigits(s string) (int, int, bool) {
	val := 0
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		val = val*10 + int(r-'0')
		n++
	}
	return val, n, n > 0
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
// unchanged. CODE V zoom positions override the base through prebuilt
// ConfigSurfaces (see ParseCodeV); when present they take precedence over the
// ZEMAX-style thickness/diameter overlay.
func ConfigSurfaceSet(result *ParseResult, config int) []types.Surface {
	if s, ok := result.ConfigSurfaces[config]; ok {
		return s
	}
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

// ConfigFields returns the fields for a given config index: the shared base
// fields with that config's per-field vignetting overrides applied (CODE V
// zoom vignetting). Config 0 (and configs without overrides) return the base
// fields unchanged.
func ConfigFields(result *ParseResult, config int) []types.FieldItem {
	if len(result.ConfigFieldVignetting) == 0 {
		return result.Fields
	}
	over, ok := result.ConfigFieldVignetting[config]
	if !ok {
		return result.Fields
	}
	out := make([]types.FieldItem, len(result.Fields))
	copy(out, result.Fields)
	for i := range out {
		if v, ok := over[i]; ok && !v.IsZero() {
			out[i].Vignetting = &v
		}
	}
	return out
}

// ConfigIndexes returns the distinct config indices declared by the file
// (from THIC/SDIA overrides or CODE V zoom positions). Empty when the lens has
// no per-config overrides (a single-config lens) — the caller then uses the
// base surfaces directly as config 0.
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
	for c := range result.ConfigSurfaces {
		if !seen[c] {
			seen[c] = true
			idx = append(idx, c)
		}
	}
	sort.Ints(idx)
	return idx
}
