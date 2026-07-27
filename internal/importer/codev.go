package importer

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/hiroki/rayweaver/internal/types"
)

type codeVSurf struct {
	Curvature float64
	Thickness float64
	Material  string
	Diameter  float64
	Conic     float64
	isStop    bool
	isPIM     bool
	SurfType  string
	Coeffs    map[int]float64
}

func ParseCodeV(input string) (*ParseResult, error) {
	input = decodeCodeVContent(input)
	rawLines := strings.Split(input, "\n")

	lines := joinContinuationLines(rawLines)

	result := &ParseResult{StopSurface: 0}
	seenGlasses := make(map[string]bool)
	surfMap := make(map[int]*codeVSurf)

	beforeLens := true
	inchMode := false
	stopSurface := 0
	imageSurface := 0
	compactMode := false
	compactCounter := 0
	lastSurfNum := 0
	inAspBlock := false

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "//") {
			inAspBlock = false
			continue
		}

		upper := strings.ToUpper(line)
		tokens := strings.Fields(line)

		if len(tokens) == 0 {
			continue
		}

	if beforeLens {
		if upper == "SEQ" || strings.HasPrefix(upper, "SEQ ") {
			beforeLens = false
			continue
		}

		if strings.HasPrefix(upper, "WVL ") || strings.HasPrefix(upper, "WL ") {
			parseCodeVWavelengths(tokens[1:], result)
			continue
		}
		if strings.HasPrefix(upper, "WTW ") {
			parseCodeVWeights(tokens[1:], result)
			continue
		}
		if strings.HasPrefix(upper, "YAN ") {
			for _, v := range tokens[1:] {
				val := parseCodeVFloat(v)
				result.Fields = append(result.Fields, types.FieldItem{
					ID:       0,
					AngleDeg: val,
					Weight:   1.0,
				})
			}
			continue
		}
		if strings.HasPrefix(upper, "XAN ") {
			continue
		}
		if strings.HasPrefix(upper, "WTF ") {
			ws := make([]float64, 0, len(tokens)-1)
			for _, v := range tokens[1:] {
				ws = append(ws, parseCodeVFloat(v))
			}
			for i := range result.Fields {
				if i < len(ws) {
					result.Fields[i].Weight = ws[i]
				}
			}
			continue
		}
		if strings.HasPrefix(upper, "DIM ") && len(tokens) >= 2 {
			if strings.ToUpper(tokens[1]) == "I" {
				inchMode = true
			}
			continue
		}
		// Some SEQ files omit the SEQ keyword entirely.
		// SO/S/SI lines signal start of surface data.
		if len(tokens) > 0 && (tokens[0] == "SO" || tokens[0] == "S" || tokens[0] == "SI") {
			beforeLens = false
		} else {
			continue
		}
	}

		if upper == "END" || strings.HasPrefix(upper, "END ") {
			break
		}

		if strings.HasPrefix(upper, "WVL ") || strings.HasPrefix(upper, "WL ") {
			parseCodeVWavelengths(tokens[1:], result)
			continue
		}
		if strings.HasPrefix(upper, "WTW ") {
			parseCodeVWeights(tokens[1:], result)
			continue
		}
		if strings.HasPrefix(upper, "YAN ") {
			for _, v := range tokens[1:] {
				val := parseCodeVFloat(v)
				result.Fields = append(result.Fields, types.FieldItem{
					ID:       0,
					AngleDeg: val,
					Weight:   1.0,
				})
			}
			continue
		}
		if strings.HasPrefix(upper, "XAN ") {
			continue
		}
		if strings.HasPrefix(upper, "WTF ") {
			ws := make([]float64, 0, len(tokens)-1)
			for _, v := range tokens[1:] {
				ws = append(ws, parseCodeVFloat(v))
			}
			for i := range result.Fields {
				if i < len(ws) {
					result.Fields[i].Weight = ws[i]
				}
			}
			continue
		}

		if strings.HasPrefix(upper, "DIM ") && len(tokens) >= 2 {
			if strings.ToUpper(tokens[1]) == "I" {
				inchMode = true
			}
			continue
		}

		first := tokens[0]

		if first == "ASP" {
			if len(tokens) >= 2 {
				surfNum := parseCodeVSurfNum(tokens[1])
				if surfNum >= 0 {
					surf := getOrCreateCodeVSurf(surfMap, surfNum)
					surf.SurfType = "ASPHERICAL"
					lastSurfNum = surfNum
					inAspBlock = false
					continue
				}
			}
			inAspBlock = true
			if lastSurfNum > 0 {
				s := getOrCreateCodeVSurf(surfMap, lastSurfNum)
				s.SurfType = "ASPHERICAL"
			}
			continue
		}

		if inAspBlock && lastSurfNum > 0 {
			surf := surfMap[lastSurfNum]
			if first == "K" && len(tokens) >= 2 {
				surf.Conic = parseCodeVFloat(tokens[1])
				continue
			}
			if first == "CUF" {
				continue
			}
			if isAsphereLetter(first) && len(tokens) >= 2 {
				order := asphereOrder(first)
				val := parseCodeVFloat(tokens[1])
				if surf.Coeffs == nil {
					surf.Coeffs = make(map[int]float64)
				}
				surf.Coeffs[order] = val

				for j := 2; j+1 < len(tokens); j += 2 {
					letter := tokens[j]
					if !isAsphereLetter(letter) {
						break
					}
					val := parseCodeVFloat(tokens[j+1])
					surf.Coeffs[asphereOrder(letter)] = val
				}
				continue
			}
		}

		if first == "SO" && len(tokens) >= 3 {
			compactMode = true
			compactCounter = 0
			lastSurfNum = 0
			continue
		}

		if first == "S" && len(tokens) >= 3 {
			compactMode = true
			compactCounter++
			surf := getOrCreateCodeVSurf(surfMap, compactCounter)
			surf.Curvature = radiusToCurvature(parseCodeVFloat(tokens[1]))
			surf.Thickness = parseCodeVThick(parseCodeVFloat(tokens[2]))

			if len(tokens) >= 4 {
				raw := tokens[3]
				raw = strings.Trim(raw, "'\"")
				if strings.Contains(raw, ":") {
					surf.Material = raw
				} else {
					surf.Material = raw
				}
			}

			lastSurfNum = compactCounter
			if compactCounter > imageSurface {
				imageSurface = compactCounter
			}
			inAspBlock = false
			continue
		}

		if first == "SI" && len(tokens) >= 3 {
			compactMode = true
			compactCounter++
			surf := getOrCreateCodeVSurf(surfMap, compactCounter)
			surf.Curvature = radiusToCurvature(parseCodeVFloat(tokens[1]))
			surf.Thickness = parseCodeVThick(parseCodeVFloat(tokens[2]))
			lastSurfNum = compactCounter
			imageSurface = compactCounter
			inAspBlock = false
			continue
		}

		if first == "CIR" && len(tokens) >= 2 {
			d := parseCodeVFloat(tokens[1]) * 2
			if lastSurfNum > 0 {
				surf := getOrCreateCodeVSurf(surfMap, lastSurfNum)
				surf.Diameter = d
			}
			inAspBlock = false
			continue
		}

		if first == "STO" && compactMode {
			if lastSurfNum > 0 {
				surf := getOrCreateCodeVSurf(surfMap, lastSurfNum)
				surf.isStop = true
				stopSurface = lastSurfNum
			}
			inAspBlock = false
			continue
		}

		if first == "PIM" && compactMode {
			if lastSurfNum > 0 {
				surf := getOrCreateCodeVSurf(surfMap, lastSurfNum)
				surf.isPIM = true
			}
			inAspBlock = false
			continue
		}

		switch first {
		case "RDM", "RDY", "RD", "THI", "TH", "GLA", "CCY", "K",
			"DIA", "SDI", "SPS", "SPC", "SI":
			if len(tokens) < 2 {
				break
			}
			surfNum := parseCodeVSurfNum(tokens[1])
			if surfNum < 0 {
				break
			}
			surf := getOrCreateCodeVSurf(surfMap, surfNum)
			lastSurfNum = surfNum
			processCodeVKeyword(surf, first, tokens)
			if surfNum > imageSurface {
				imageSurface = surfNum
			}
			continue
		case "STO":
			if len(tokens) >= 2 {
				surfNum := parseCodeVSurfNum(tokens[1])
				if surfNum >= 0 {
					surf := getOrCreateCodeVSurf(surfMap, surfNum)
					surf.isStop = true
					stopSurface = surfNum
					lastSurfNum = surfNum
					if surfNum > imageSurface {
						imageSurface = surfNum
					}
				}
			}
			continue
		}

		inAspBlock = false
	}

	if inchMode {
		for _, s := range surfMap {
			if s.Curvature != 0 {
				s.Curvature = s.Curvature / 25.4
			}
			if s.Thickness != 0 {
				s.Thickness *= 25.4
			}
			if s.Diameter != 0 {
				s.Diameter *= 25.4
			}
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
			entry := types.Glass{
				Type:  types.GlassTypeModel,
				Label: mat,
			}
			if strings.Contains(mat, ":") {
				parts := strings.SplitN(mat, ":", 2)
				nd := parseCodeVFloat(parts[0])
				vd := parseCodeVFloat(parts[1])
				if nd > 0 {
					entry.ND = nd
					entry.VD = vd
				}
			} else {
				if nd, vd, ok := LookupGlass(mat); ok {
					entry.ND = nd
					entry.VD = vd
				}
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

		if len(s.Coeffs) > 0 {
			hasNonZero := false
			maxOrder := 0
			for k, v := range s.Coeffs {
				if v != 0 {
					hasNonZero = true
				}
				if k > maxOrder {
					maxOrder = k
				}
			}
			if hasNonZero {
				size := maxOrder / 2
				coeffs := make([]float64, size+1)
				for order, val := range s.Coeffs {
					idx := order / 2
					if idx >= 0 && idx <= size {
						coeffs[idx] = val
					}
				}
				t.Coefficients = coeffs
			}
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

func joinContinuationLines(rawLines []string) []string {
	var out []string
	buf := ""
	for _, line := range rawLines {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.HasSuffix(trimmed, "&") {
			buf += strings.TrimSuffix(trimmed, "&")
			continue
		}
		if buf != "" {
			out = append(out, buf+trimmed)
			buf = ""
		} else {
			out = append(out, trimmed)
		}
	}
	if buf != "" {
		out = append(out, buf)
	}
	return out
}

func parseCodeVWavelengths(args []string, result *ParseResult) {
	for _, p := range args {
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

func parseCodeVWeights(args []string, result *ParseResult) {
	ws := make([]float64, 0, len(args))
	for _, p := range args {
		ws = append(ws, parseCodeVFloat(p))
	}
	for i := range result.Wavelengths {
		if i < len(ws) {
			result.Wavelengths[i].Weight = ws[i]
		}
	}
}

func processCodeVKeyword(surf *codeVSurf, keyword string, tokens []string) {
	switch keyword {
	case "RDM", "RDY", "RD":
		if len(tokens) >= 3 {
			surf.Curvature = radiusToCurvature(parseCodeVFloat(tokens[2]))
		}
	case "THI", "TH":
		if len(tokens) >= 3 {
			surf.Thickness = parseCodeVThick(parseCodeVFloat(tokens[2]))
		}
	case "GLA":
		if len(tokens) >= 3 {
			raw := strings.Trim(tokens[2], "'\"")
			surf.Material = raw
		}
	case "CCY":
		if len(tokens) >= 3 {
			surf.Conic = parseCodeVFloat(tokens[2])
		}
	case "K":
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
	case "SPS", "SPC":
		if len(tokens) >= 3 {
			surf.SurfType = tokens[2]
		}
	case "SI":
		if len(tokens) >= 3 {
			surf.SurfType = tokens[2]
		}
	}
}

func radiusToCurvature(r float64) float64 {
	if r == 0 {
		return 0
	}
	return 1.0 / r
}

func asphereOrder(letter string) int {
	switch strings.ToUpper(letter) {
	case "A":
		return 4
	case "B":
		return 6
	case "C":
		return 8
	case "D":
		return 10
	case "E":
		return 12
	case "F":
		return 14
	case "G":
		return 16
	case "H":
		return 18
	case "J":
		return 20
	default:
		return 0
	}
}

func isAsphereLetter(s string) bool {
	switch s {
	case "A", "B", "C", "D", "E", "F", "G", "H", "J":
		return true
	}
	return false
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
	s := &codeVSurf{Coeffs: make(map[int]float64)}
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
	s = strings.TrimRight(s, ";")
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
