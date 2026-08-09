package importer

import (
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

func ParseZemax(input string) (*ParseResult, error) {
	input = decodeBOM(input)
	lines := strings.Split(input, "\n")

	result := &ParseResult{
		Surfaces:     nil,
		Wavelengths:  nil,
		Fields:       nil,
		StopSurface:  0,
		GlassEntries: nil,
	}

	var currentSurface *zemaxSurface
	var surfParams []zemaxSurface

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "#") {
			continue
		}

		parts := splitLine(line)
		if len(parts) == 0 {
			continue
		}

		keyword := strings.ToUpper(parts[0])
		args := parts[1:]

		if keyword == "SURF" {
			if currentSurface != nil {
				surfParams = append(surfParams, *currentSurface)
			}
			if len(args) >= 1 {
				id, err := strconv.Atoi(args[0])
				if err == nil {
					currentSurface = &zemaxSurface{ID: id, Parms: make(map[int]float64)}
				}
			}
			continue
		}

		if currentSurface != nil {
			// "STOP" appears as a standalone token at the top of a surface's
			// block (the standard ZEMAX layout) and marks that surface as the
			// aperture stop. It carries no surface-param value, so route it
			// here rather than routing it to parseZemaxSurfaceParam.
			if keyword == "STOP" {
				result.StopSurface = currentSurface.ID
				continue
			}
			// Per-config overrides ("THIC <surf> <config> <value>") trail the
			// surface blocks; they reference a surface other than the one being
			// read, so route them to header parsing rather than corrupting the
			// current surface.
			if (keyword == "THIC" || keyword == "SDIA") && isConfigOverrideLine(args) {
				parseZemaxHeader(result, keyword, args)
				continue
			}
			parseZemaxSurfaceParam(currentSurface, keyword, args)
		} else {
			parseZemaxHeader(result, keyword, args)
		}
	}

	if currentSurface != nil {
		surfParams = append(surfParams, *currentSurface)
	}

	seenIDs := make(map[int]bool)
	var pendingDecenter types.DecenterStep
	var pendingTiltFirst bool
	var pendingHas bool
	for _, sp := range surfParams {
		if seenIDs[sp.ID] {
			continue
		}
		seenIDs[sp.ID] = true

		// A COORDBRK surface is a zero-power dummy that carries a coordinate
		// break for the following surfaces. It is removed from the surface
		// list; its decenter/tilt transform is transferred to the next real
		// surface and its (usually zero) thickness is folded into the
		// preceding surface.
		if strings.EqualFold(sp.SurfaceType, "COORDBRK") {
			pendingDecenter = types.DecenterStep{
				Shift: types.Vec3{X: sp.Parms[1], Y: sp.Parms[2]},
				Tilt:  types.Vec3{X: sp.Parms[3], Y: sp.Parms[4], Z: sp.Parms[5]},
			}
			pendingTiltFirst = sp.coordBreakOrder()
			pendingHas = true
			if len(result.Surfaces) > 0 {
				last := &result.Surfaces[len(result.Surfaces)-1]
				if sp.Thickness != 0 {
					last.Thickness += sp.Thickness
				}
			}
			continue
		}

		s := types.Surface{
			ID:        sp.ID,
			Type:      types.Sphere,
			Curvature: sp.Curvature,
			Thickness: sp.Thickness,
			Diameter:  sp.Diameter,
			Conic:     sp.Conic,
		}

		if sp.SurfaceType == "EVENASPH" {
			s.Type = types.AspherePolynomial
			maxN := 0
			for n := range sp.Parms {
				if n > maxN {
					maxN = n
				}
			}
			if maxN >= 2 {
				s.Coefficients = make([]float64, maxN-1)
				for n, val := range sp.Parms {
					if n >= 2 && n-2 < len(s.Coefficients) {
						s.Coefficients[n-2] = val
					}
				}
			}
		}

		mat := strings.TrimSpace(sp.Material)
		switch {
		case mat == "" || isAir(mat):
			s.Material = types.Material{}
		case sp.InlineND > 0:
			// ZEMAX inline model glass (e.g. "___BLANK"): the nd/vd travel
			// with the surface; no glass_catalog entry is needed.
			s.Material = types.Material{ND: sp.InlineND, VD: sp.InlineVD}
		default:
			addGlassEntry(result, mat)
			s.Material = types.Material{Key: mat}
		}

		// A pending COORDBRK transform applies to this surface: tilts before
		// decenters (PARM 6 = 1) or decenters before tilts (default) map onto
		// the DecenterStep's internal Tilt/Shift ordering.
		if pendingHas {
			step := pendingDecenter
			if pendingTiltFirst {
				// Tilt-first: a single DecenterStep applies translation then
				// rotation, so express the reversed order as a tilt step
				// followed by a shift step.
				s.Decenter = []types.DecenterStep{
					{Tilt: step.Tilt},
					{Shift: step.Shift},
				}
			} else {
				s.Decenter = []types.DecenterStep{step}
			}
			pendingHas = false
		}

		if sp.ID != 0 {
			result.Surfaces = append(result.Surfaces, s)
		}
	}

	// Convert folded mirror systems (MIRROR material, negative spacings) into
	// rayweave's fold model before defaults are filled so the downstream
	// pipeline sees all-positive thicknesses.
	convertFoldMirrors(result)

	if len(result.Surfaces) > 0 {
		last := result.Surfaces[len(result.Surfaces)-1]
		result.ImageSurface = last.ID
	}

	fillDefaults(result)

	return result, nil
}

type zemaxSurface struct {
	ID          int
	SurfaceType string
	Curvature   float64
	Thickness   float64
	Material    string
	Diameter    float64
	Conic       float64
	Parms       map[int]float64
	InlineND    float64
	InlineVD    float64

	// Decenter holds the coordinate-break transform of a COORDBRK surface
	// (PARM 1..6: decenter X/Y, tilt about X/Y/Z, order). It is transferred
	// to the next real surface when the COORDBRK dummy is removed.
	Decenter types.DecenterStep
}

// coordBreakOrder reports whether a ZEMAX COORDBRK applies tilts before
// decenters (PARM 6 = 1) rather than the default decenters-then-tilts.
func (s *zemaxSurface) coordBreakOrder() bool {
	return s.Parms[6] == 1
}

func parseZemaxSurfaceParam(s *zemaxSurface, keyword string, args []string) {
	switch keyword {
	case "TYPE":
		if len(args) > 0 {
			s.SurfaceType = strings.ToUpper(args[0])
		}
	case "CURV":
		if len(args) > 0 {
			s.Curvature = parseFloat(args[0])
		}
	case "THIC", "DISZ":
		if len(args) > 0 {
			s.Thickness = parseThickness(parseFloat(args[0]))
		}
	case "GLAS":
		if len(args) > 0 {
			s.Material = args[0]
			// ZEMAX inline model glass: GLAS <name> <dispersion-flag> <0> <nd> <vd> ...
			// A dispersion flag of 1 means nd/vd follow inline (a real model
			// glass); flag 0 is a named catalog glass whose trailing nd/vd are
			// ZEMAX placeholder defaults, so they must not override the lookup.
			if len(args) >= 5 && parseFloat(args[1]) == 1 {
				s.InlineND = parseFloat(args[3])
				s.InlineVD = parseFloat(args[4])
			}
		}
	case "DIAM":
		if len(args) > 0 {
			s.Diameter = parseFloat(args[0]) * 2
		}
	case "CONI":
		if len(args) > 0 {
			s.Conic = parseFloat(args[0])
		}
	case "PARM", "CODN":
		if len(args) >= 2 {
			n := int(parseFloat(args[0]))
			val := parseFloat(args[1])
			s.Parms[n] = val
		}
	}
}

func parseZemaxHeader(result *ParseResult, keyword string, args []string) {
	switch keyword {
	case "STOP":
		if len(args) > 0 {
			result.StopSurface = int(parseFloat(args[0]))
		}
	case "WAVL":
		if len(args) > 0 {
			wl := types.WavelengthItem{
				ID:    len(result.Wavelengths),
				Value: parseFloat(args[0]) / 1000.0,
			}
			if len(args) >= 2 {
				wl.Weight = parseFloat(args[1])
			} else {
				wl.Weight = 1.0
			}
			result.Wavelengths = append(result.Wavelengths, wl)
		}
	case "YFLN":
		// YFLN <f0> <f1> ... — field values for the y direction. The meaning of
		// the values is given by the system field type (FTYP): 0 = half-angle in
		// degrees, otherwise an object/image height in mm. The first column is
		// the on-axis field (normally 0) and is skipped together with any other
		// unused (zero) slots.
		for _, a := range args[1:] {
			v := parseFloat(a)
			if v > 0 {
				result.Fields = append(result.Fields, newField(result, v))
			}
		}
	case "XFLN":
		// XFLN <x0> <x1> ... — x field values; nearly always zero.
		for _, a := range args[1:] {
			v := parseFloat(a)
			if v > 0 {
				result.Fields = append(result.Fields, newField(result, v))
			}
		}
	case "THIC", "SDIA":
		// Config-override rows: <surf> <config> <value> <flags...>. They appear
		// after the surface blocks and carry a leading surface ID plus a config
		// index, distinguishing them from the per-surface THIC (thickness) /
		// DIAM (diameter) parameters. They only apply to lenses declaring them.
		if len(args) >= 3 && isConfigOverrideLine(args) {
			surf := int(parseFloat(args[0]))
			cfg := int(parseFloat(args[1]))
			val := parseFloat(args[2])
			if keyword == "THIC" {
				if result.ConfigThickness == nil {
					result.ConfigThickness = map[int]map[int]float64{}
				}
				if result.ConfigThickness[cfg] == nil {
					result.ConfigThickness[cfg] = map[int]float64{}
				}
				result.ConfigThickness[cfg][surf] = val
			} else {
				if result.ConfigDiameter == nil {
					result.ConfigDiameter = map[int]map[int]float64{}
				}
				if result.ConfigDiameter[cfg] == nil {
					result.ConfigDiameter[cfg] = map[int]float64{}
				}
				result.ConfigDiameter[cfg][surf] = val
			}
			return
		}
	case "FIELD":
		if len(args) >= 3 {
			fieldType := int(parseFloat(args[1]))
			f := types.FieldItem{
				ID:     len(result.Fields),
				Weight: 1.0,
			}
			if len(args) >= 5 {
				f.Weight = parseFloat(args[4])
			}
			switch fieldType {
			case 0:
				f.AngleDeg = parseFloat(args[2])
			case 1:
				f.ImageHeight = parseFloat(args[2])
			case 2:
				f.AngleDeg = parseFloat(args[2])
			}
			result.Fields = append(result.Fields, f)
		} else if len(args) >= 1 {
			f := types.FieldItem{
				ID:       len(result.Fields),
				AngleDeg: parseFloat(args[0]),
				Weight:   1.0,
			}
			result.Fields = append(result.Fields, f)
		}
	case "WAVM":
		if len(args) >= 2 {
			wl := types.WavelengthItem{
				ID:    len(result.Wavelengths),
				Value: parseFloat(args[1]) / 1000.0,
			}
			if len(args) >= 3 {
				wl.Weight = parseFloat(args[2])
			} else {
				wl.Weight = 1.0
			}
			result.Wavelengths = append(result.Wavelengths, wl)
		}
	case "WWGT":
		for i := range result.Wavelengths {
			if i < len(args) {
				result.Wavelengths[i].Weight = parseFloat(args[i])
			}
		}
	case "PWAV":
	case "FTYP":
		// FTYP <global-type> <f1> <f2> ... — system field type. FTYP[0] is the
		// global (per-volume) type; the remaining entries give per-field codes
		// for ZEMAX field 1, 2, ... (1-indexed; field 0 is the on-axis field).
		if len(args) > 0 {
			result.FieldType = int(parseFloat(args[0]))
			if len(args) > 1 {
				result.FieldTypes = make([]int, 0, len(args)-1)
				for _, a := range args[1:] {
					result.FieldTypes = append(result.FieldTypes, int(parseFloat(a)))
				}
			}
		}
	case "UNIT":
	case "VERS":
	case "MODE":
	case "NAME":
	case "NOTE":
	case "ENPD":
	case "APER":
	}
}

// newField builds a FieldItem from a YFLN/XFLN value. The meaning of the value
// is given by the per-field FTYP code: type 0 is a half-angle in degrees,
// types 1..3 are object/image heights. The per-field code for ZEMAX field i+1
// (1-indexed) is FieldTypes[i]; when absent the global FieldType applies.
func newField(result *ParseResult, v float64) types.FieldItem {
	f := types.FieldItem{
		ID:     len(result.Fields),
		Weight: 1.0,
	}
	ft := result.FieldType
	if k := len(result.Fields); k < len(result.FieldTypes) {
		ft = result.FieldTypes[k]
	}
	switch ft {
	case 1, 2, 3:
		f.ImageHeight = v
	default:
		f.AngleDeg = v
	}
	return f
}

func splitLine(line string) []string {
	return strings.Fields(line)
}

// isConfigOverrideLine reports whether a THIC/SDIA token list is a ZEMAX
// per-config override ("<surf> <config> <value> <flags...>") rather than a
// per-surface parameter. Overrides carry a leading integer surface ID and an
// integer config index (>=1); an in-surface thickness/diameter has a plain
// numeric value as its first token.
func isConfigOverrideLine(args []string) bool {
	if len(args) < 2 {
		return false
	}
	surf, ok1 := parseIntToken(args[0])
	cfg, ok2 := parseIntToken(args[1])
	return ok1 && ok2 && surf >= 0 && cfg >= 1
}

func parseIntToken(s string) (int, bool) {
	orig := s
	if strings.HasPrefix(s, "-") {
		s = s[1:]
	}
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	return int(parseFloat(orig)), true
}
