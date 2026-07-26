package importer

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/hiroki/rayweaver/internal/types"
)

func ParseCodeV(input string) (*ParseResult, error) {
	input = decodeCodeVContent(input)
	lines := strings.Split(input, "\n")

	result := &ParseResult{StopSurface: 0}
	seenGlasses := make(map[string]bool)

	surfMap := make(map[int]*codeVSurf)
	stopSurface := 0
	imageSurface := 0

	beforeLens := true

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "//") {
			continue
		}

		upper := strings.ToUpper(line)

		if upper == "SEQ" || strings.HasPrefix(upper, "SEQ ") {
			beforeLens = false
			continue
		}
		if upper == "END" || strings.HasPrefix(upper, "END ") {
			break
		}

		if beforeLens {
			if strings.HasPrefix(upper, "WVL ") || strings.HasPrefix(upper, "WL ") {
				parts := strings.Fields(line)
				for _, p := range parts[1:] {
					val := parseCodeVFloat(p)
					if val > 0 {
						result.Wavelengths = append(result.Wavelengths, types.WavelengthItem{
							ID:     len(result.Wavelengths),
							Value:  val / 1000.0,
							Weight: 1.0,
						})
					}
				}
			}
			fld := parseCodeVField(line)
			if fld != nil {
				fld.ID = len(result.Fields)
				result.Fields = append(result.Fields, *fld)
			}
			continue
		}

		if strings.HasPrefix(upper, "WVL ") || strings.HasPrefix(upper, "WL ") {
			parts := strings.Fields(line)
			for _, p := range parts[1:] {
				val := parseCodeVFloat(p)
				if val > 0 {
					mm := val / 1000.0
					result.Wavelengths = append(result.Wavelengths, types.WavelengthItem{
						ID:     len(result.Wavelengths),
						Value:  mm,
						Weight: 1.0,
					})
				}
			}
			continue
		}

		fld := parseCodeVField(line)
		if fld != nil {
			fld.ID = len(result.Fields)
			result.Fields = append(result.Fields, *fld)
			continue
		}

		tokens := strings.Fields(line)
		if len(tokens) < 2 {
			continue
		}

		surfNum := parseCodeVSurfNum(tokens[1])
		if surfNum < 0 {
			continue
		}

		surf := getOrCreateCodeVSurf(surfMap, surfNum)

		switch tokens[0] {
		case "RDM", "RDY", "RD":
			if len(tokens) >= 3 {
				r := parseCodeVFloat(tokens[2])
				if r != 0 {
					surf.Curvature = 1.0 / r
				} else {
					surf.Curvature = 0
				}
			}
		case "THI", "TH":
			if len(tokens) >= 3 {
				surf.Thickness = parseCodeVThick(parseCodeVFloat(tokens[2]))
			}
		case "GLA":
			if len(tokens) >= 3 {
				raw := tokens[2]
				raw = strings.Trim(raw, "'\"")
				surf.Material = raw
			}
		case "CCY":
			if len(tokens) >= 3 {
				surf.Conic = parseCodeVFloat(tokens[2])
			}
		case "DIA":
			if len(tokens) >= 3 {
				surf.Diameter = parseCodeVFloat(tokens[2]) * 2
			}
		case "SDI":
			if len(tokens) >= 3 {
				surf.Diameter = parseCodeVFloat(tokens[2]) * 2
			}
		case "STO":
			stopSurface = surfNum
			surf.isStop = true
		case "SPS", "SPC", "SI":
			if len(tokens) >= 3 {
				surf.SurfType = tokens[2]
			}
		case "K":
			if len(tokens) >= 3 {
				surf.Conic = parseCodeVFloat(tokens[2])
			}
		case "ASP":
			surf.SurfType = "ASPHERICAL"
		case "AD":
			if len(tokens) >= 3 {
				surf.Coeff4 = parseCodeVFloat(tokens[2])
			}
		case "AE":
			if len(tokens) >= 3 {
				surf.Coeff6 = parseCodeVFloat(tokens[2])
			}
		case "AF":
			if len(tokens) >= 3 {
				surf.Coeff8 = parseCodeVFloat(tokens[2])
			}
		case "AG":
			if len(tokens) >= 3 {
				surf.Coeff10 = parseCodeVFloat(tokens[2])
			}
		}

		if surfNum > imageSurface {
			imageSurface = surfNum
		}
	}

	lastID := 0
	for id := range surfMap {
		if id > lastID {
			lastID = id
		}
	}
	if imageSurface > lastID {
		lastID = imageSurface
	}

	if stopSurface == 0 {
		stopSurface = 1
	}
	result.StopSurface = stopSurface

	for id := 1; id <= lastID; id++ {
		s, ok := surfMap[id]
		if !ok {
			continue
		}

		mat := s.Material
		if mat == "" {
			mat = "AIR"
		}

		if mat != "" && !isAir(mat) && !seenGlasses[mat] {
			seenGlasses[mat] = true
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

		surfType := types.Sphere
		if s.SurfType == "ASPHERICAL" || s.SurfType == "ASP" {
			surfType = types.AspherePolynomial
		}

		t := types.Surface{
			ID:        id,
			Type:      surfType,
			Curvature: s.Curvature,
			Thickness: s.Thickness,
			Material:  mat,
			Diameter:  s.Diameter,
			Conic:     s.Conic,
		}

		var coeffs []float64
		if s.Coeff4 != 0 || s.Coeff6 != 0 || s.Coeff8 != 0 || s.Coeff10 != 0 {
			maxOrder := 0
			if s.Coeff10 != 0 {
				maxOrder = 10
			} else if s.Coeff8 != 0 {
				maxOrder = 8
			} else if s.Coeff6 != 0 {
				maxOrder = 6
			} else if s.Coeff4 != 0 {
				maxOrder = 4
			}
			size := maxOrder / 2
			coeffs = make([]float64, size+1)
			if s.Coeff4 != 0 {
				coeffs[1] = s.Coeff4
			}
			if s.Coeff6 != 0 {
				coeffs[2] = s.Coeff6
			}
			if s.Coeff8 != 0 {
				coeffs[3] = s.Coeff8
			}
			if s.Coeff10 != 0 {
				coeffs[4] = s.Coeff10
			}
		}
		if len(coeffs) > 0 {
			t.Coefficients = coeffs
		}

		result.Surfaces = append(result.Surfaces, t)
	}

	if len(result.Surfaces) > 0 {
		last := result.Surfaces[len(result.Surfaces)-1]
		result.ImageSurface = last.ID
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

type codeVSurf struct {
	Curvature float64
	Thickness float64
	Material  string
	Diameter  float64
	Conic     float64
	isStop    bool
	SurfType  string
	Coeff4    float64
	Coeff6    float64
	Coeff8    float64
	Coeff10   float64
}

func decodeCodeVContent(input string) string {
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

func getOrCreateCodeVSurf(m map[int]*codeVSurf, id int) *codeVSurf {
	if s, ok := m[id]; ok {
		return s
	}
	s := &codeVSurf{}
	m[id] = s
	return s
}

func parseCodeVSurfNum(s string) int {
	s = strings.TrimSpace(s)
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

func parseCodeVFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	upper := strings.ToUpper(s)
	if upper == "INF" || upper == "INFINITY" || upper == "INFINITE" {
		return math.Inf(1)
	}
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return val
}

func parseCodeVThick(v float64) float64 {
	if math.IsInf(v, 1) {
		return 0
	}
	return v
}

func parseCodeVField(line string) *types.FieldItem {
	upper := strings.ToUpper(strings.TrimSpace(line))
	var rest string
	if strings.HasPrefix(upper, "FIELD ") {
		rest = strings.TrimSpace(line[6:])
	} else if strings.HasPrefix(upper, "ANG ") {
		rest = strings.TrimSpace(line[4:])
	} else if strings.HasPrefix(upper, "F ") {
		rest = strings.TrimSpace(line[2:])
	} else {
		return nil
	}
	if rest == "" {
		return nil
	}
	parts := strings.Fields(rest)
	if len(parts) < 1 {
		return nil
	}
	angle := parseCodeVFloat(parts[0])
	w := 1.0
	if len(parts) >= 2 {
		w = parseCodeVFloat(parts[1])
	}
	if w == 0 {
		w = 1.0
	}
	return &types.FieldItem{
		AngleDeg: angle,
		Weight:   w,
	}
}
