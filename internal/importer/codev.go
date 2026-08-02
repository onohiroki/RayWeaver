package importer

import (
	"strconv"
	"strings"

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
	input = decodeBOM(input)
	rawLines := strings.Split(input, "\n")

	lines := joinContinuationLines(rawLines)

	result := &ParseResult{StopSurface: 0}
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

			if parseCodeVHeader(upper, tokens, result, &inchMode) {
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

		if parseCodeVHeader(upper, tokens, result, &inchMode) {
			continue
		}

		first := tokens[0]

		if first == "ASP" {
			if len(tokens) >= 2 {
				surfNum := parseCodeVSurfNum(tokens[1])
				if surfNum >= 0 {
					surf := getOrCreate(surfMap, surfNum)
					surf.SurfType = "ASPHERICAL"
					lastSurfNum = surfNum
					inAspBlock = false
					continue
				}
			}
			inAspBlock = true
			if lastSurfNum > 0 {
				s := getOrCreate(surfMap, lastSurfNum)
				s.SurfType = "ASPHERICAL"
			}
			continue
		}

		if inAspBlock && lastSurfNum > 0 {
			surf := surfMap[lastSurfNum]
			if first == "K" && len(tokens) >= 2 {
				surf.Conic = parseFloat(tokens[1])
				continue
			}
			if first == "CUF" {
				continue
			}
			if isAsphereLetter(first) && len(tokens) >= 2 {
				order := asphereOrder(first)
				val := parseFloat(tokens[1])
				if surf.Coeffs == nil {
					surf.Coeffs = make(map[int]float64)
				}
				surf.Coeffs[order] = val

				for j := 2; j+1 < len(tokens); j += 2 {
					letter := tokens[j]
					if !isAsphereLetter(letter) {
						break
					}
					val := parseFloat(tokens[j+1])
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
			surf := getOrCreate(surfMap, compactCounter)
			surf.Curvature = radiusToCurvature(parseFloat(tokens[1]))
			surf.Thickness = parseThickness(parseFloat(tokens[2]))

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
			surf := getOrCreate(surfMap, compactCounter)
			surf.Curvature = radiusToCurvature(parseFloat(tokens[1]))
			surf.Thickness = parseThickness(parseFloat(tokens[2]))
			lastSurfNum = compactCounter
			imageSurface = compactCounter
			inAspBlock = false
			continue
		}

		if first == "CIR" && len(tokens) >= 2 {
			d := parseFloat(tokens[1]) * 2
			if lastSurfNum > 0 {
				surf := getOrCreate(surfMap, lastSurfNum)
				surf.Diameter = d
			}
			inAspBlock = false
			continue
		}

		if first == "STO" && compactMode {
			if lastSurfNum > 0 {
				surf := getOrCreate(surfMap, lastSurfNum)
				surf.isStop = true
				stopSurface = lastSurfNum
			}
			inAspBlock = false
			continue
		}

		if first == "PIM" && compactMode {
			if lastSurfNum > 0 {
				surf := getOrCreate(surfMap, lastSurfNum)
				surf.isPIM = true
			}
			inAspBlock = false
			continue
		}

		if compactMode && lastSurfNum > 0 {
			switch first {
			case "CCY":
				if len(tokens) >= 2 {
					surf := getOrCreate(surfMap, lastSurfNum)
					surf.Conic = parseFloat(tokens[1])
				}
				inAspBlock = false
				continue
			case "THC":
				inAspBlock = false
				continue
			}
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
			surf := getOrCreate(surfMap, surfNum)
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
					surf := getOrCreate(surfMap, surfNum)
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

		addGlassEntry(result, mat)

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
				size := maxOrder/2 - 2
				coeffs := make([]float64, size+1)
				for order, val := range s.Coeffs {
					idx := order/2 - 2
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

	fillDefaults(result)

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
		val := parseFloat(p)
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
		ws = append(ws, parseFloat(p))
	}
	for i := range result.Wavelengths {
		if i < len(ws) {
			result.Wavelengths[i].Weight = ws[i]
		}
	}
}

// parseCodeVHeader handles header-level keywords (wavelengths, fields, FNO,
// DIM) that may appear before or after the SEQ keyword. It reports whether
// the line was consumed.
func parseCodeVHeader(upper string, tokens []string, result *ParseResult, inchMode *bool) bool {
	if strings.HasPrefix(upper, "WVL ") || strings.HasPrefix(upper, "WL ") {
		parseCodeVWavelengths(tokens[1:], result)
		return true
	}
	if strings.HasPrefix(upper, "WTW ") {
		parseCodeVWeights(tokens[1:], result)
		return true
	}
	if strings.HasPrefix(upper, "YAN ") {
		for _, v := range tokens[1:] {
			val := parseFloat(v)
			result.Fields = append(result.Fields, types.FieldItem{
				ID:       0,
				AngleDeg: val,
				Weight:   1.0,
			})
		}
		return true
	}
	if strings.HasPrefix(upper, "XAN ") {
		return true
	}
	if strings.HasPrefix(upper, "YIM ") || strings.HasPrefix(upper, "YRI ") {
		for _, v := range tokens[1:] {
			val := parseFloat(v)
			result.Fields = append(result.Fields, types.FieldItem{
				ID:          0,
				ImageHeight: val,
				Weight:      1.0,
			})
		}
		return true
	}
	if strings.HasPrefix(upper, "XIM ") || strings.HasPrefix(upper, "XRI ") {
		return true
	}
	if strings.HasPrefix(upper, "WTF ") {
		ws := make([]float64, 0, len(tokens)-1)
		for _, v := range tokens[1:] {
			ws = append(ws, parseFloat(v))
		}
		for i := range result.Fields {
			if i < len(ws) {
				result.Fields[i].Weight = ws[i]
			}
		}
		return true
	}
	if strings.HasPrefix(upper, "FNO ") && len(tokens) >= 2 {
		result.FNO = parseFloat(tokens[1])
		return true
	}
	if strings.HasPrefix(upper, "DIM ") && len(tokens) >= 2 {
		if strings.ToUpper(tokens[1]) == "I" {
			*inchMode = true
		}
		return true
	}
	return false
}

func processCodeVKeyword(surf *codeVSurf, keyword string, tokens []string) {
	switch keyword {
	case "RDM", "RDY", "RD":
		if len(tokens) >= 3 {
			surf.Curvature = radiusToCurvature(parseFloat(tokens[2]))
		}
	case "THI", "TH":
		if len(tokens) >= 3 {
			surf.Thickness = parseThickness(parseFloat(tokens[2]))
		}
	case "GLA":
		if len(tokens) >= 3 {
			raw := strings.Trim(tokens[2], "'\"")
			surf.Material = raw
		}
	case "CCY":
		if len(tokens) >= 3 {
			surf.Conic = parseFloat(tokens[2])
		}
	case "K":
		if len(tokens) >= 3 {
			surf.Conic = parseFloat(tokens[2])
		}
	case "DIA":
		if len(tokens) >= 3 {
			surf.Diameter = parseFloat(tokens[2]) * 2
		}
	case "SDI":
		if len(tokens) >= 3 {
			surf.Diameter = parseFloat(tokens[2]) * 2
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

func parseCodeVSurfNum(s string) int {
	s = strings.TrimSpace(s)
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}
