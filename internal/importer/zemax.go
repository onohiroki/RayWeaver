package importer

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/hiroki/rayweaver/internal/types"
)

func decodeZMXContent(input string) string {
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

func ParseZemax(input string) (*ParseResult, error) {
	input = decodeZMXContent(input)
	lines := strings.Split(input, "\n")

	result := &ParseResult{
		Surfaces:    nil,
		Wavelengths: nil,
		Fields:      nil,
		StopSurface: 0,
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
			parseZemaxSurfaceParam(currentSurface, keyword, args)
		} else {
			parseZemaxHeader(result, keyword, args)
		}
	}

	if currentSurface != nil {
		surfParams = append(surfParams, *currentSurface)
	}

	seenIDs := make(map[int]bool)
	for _, sp := range surfParams {
		if seenIDs[sp.ID] {
			continue
		}
		seenIDs[sp.ID] = true

		s := types.Surface{
			ID:        sp.ID,
			Type:      types.Sphere,
			Curvature: sp.Curvature,
			Thickness: sp.Thickness,
			Material:  sp.Material,
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
				size := maxN/2 + 1
				s.Coefficients = make([]float64, size)
				for n, val := range sp.Parms {
					idx := n/2 - 1
					if idx >= 0 && idx < size {
						s.Coefficients[idx+1] = val
					}
				}
			}
		}

		mat := strings.TrimSpace(sp.Material)
		if mat != "" && !isAir(mat) {
			found := false
			for _, g := range result.GlassEntries {
				if strings.EqualFold(g.Label, mat) {
					found = true
					break
				}
			}
			if !found {
				nd, vd, ok := LookupGlass(mat)
				entry := types.Glass{
					Type:  types.GlassTypeModel,
					Label: mat,
				}
				if ok {
					entry.ND = nd
					entry.VD = vd
				}
				result.GlassEntries = append(result.GlassEntries, entry)
			}
		}

		if sp.ID != 0 {
			result.Surfaces = append(result.Surfaces, s)
		}
	}

	if len(result.Surfaces) > 0 {
		last := result.Surfaces[len(result.Surfaces)-1]
		result.ImageSurface = last.ID
	}

	if result.StopSurface == 0 && len(result.Surfaces) > 0 {
		result.StopSurface = result.Surfaces[0].ID
	}

	if len(result.Wavelengths) == 0 {
		result.Wavelengths = []types.WavelengthItem{
			{ID: 0, Value: 0.00058756, Weight: 1.0},
		}
	}

	if len(result.Fields) == 0 {
		result.Fields = []types.FieldItem{
			{ID: 0, AngleDeg: 0, Weight: 1.0},
		}
	}

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
	case "THIC":
		if len(args) > 0 {
			s.Thickness = parseThickness(args[0])
		}
	case "GLAS":
		if len(args) > 0 {
			s.Material = args[0]
		}
	case "DIAM":
		if len(args) > 0 {
			s.Diameter = parseFloat(args[0])
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
				Value: parseFloat(args[0]),
			}
			if len(args) >= 2 {
				wl.Weight = parseFloat(args[1])
			} else {
				wl.Weight = 1.0
			}
			result.Wavelengths = append(result.Wavelengths, wl)
		}
	case "FIELD":
		if len(args) >= 3 {
			fieldType := int(parseFloat(args[1]))
			if fieldType == 0 {
				f := types.FieldItem{
					ID:       len(result.Fields),
					AngleDeg: parseFloat(args[2]),
				}
				if len(args) >= 5 {
					f.Weight = parseFloat(args[4])
				} else {
					f.Weight = 1.0
				}
				result.Fields = append(result.Fields, f)
			}
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
				Value: parseFloat(args[1]),
			}
			if len(args) >= 3 {
				wl.Weight = parseFloat(args[2])
			} else {
				wl.Weight = 1.0
			}
			result.Wavelengths = append(result.Wavelengths, wl)
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

func splitLine(line string) []string {
	return strings.Fields(line)
}

func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	upper := strings.ToUpper(s)
	if upper == "INFINITY" || upper == "INF" {
		return math.Inf(1)
	}
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return val
}

func parseThickness(s string) float64 {
	v := parseFloat(s)
	if math.IsInf(v, 1) {
		return 0
	}
	return v
}


