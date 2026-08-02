package importer

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/hiroki/rayweaver/internal/types"
)

// decodeBOM strips a UTF-8/UTF-16 BOM and decodes UTF-16LE content.
func decodeBOM(input string) string {
	raw := []byte(input)
	if len(raw) < 2 {
		return input
	}
	if raw[0] == 0xFF && raw[1] == 0xFE {
		u16 := make([]uint16, 0, len(raw)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
		}
		return string(utf16.Decode(u16))
	}
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		return string(raw[3:])
	}
	return input
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
}

// addGlassEntry registers a glass material, deduplicating by
// case-insensitive label. Inline "nd:vd" model glasses are expanded in place.
func addGlassEntry(result *ParseResult, mat string) {
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
	if strings.Contains(mat, ":") {
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
