package importer

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/hiroki/rayweaver/internal/types"
)

func ParseOslo(input string) (*ParseResult, error) {
	input = decodeBOM(input)

	if usesNXTFormat(input) {
		return parseOsloNXT(input)
	}

	return parseOsloSRF(input)
}

func usesNXTFormat(input string) bool {
	lines := strings.Split(input, "\n")
	hasNXT := false
	hasSRF := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		if upper == "NXT" {
			hasNXT = true
		}
		if strings.HasPrefix(upper, "SRF") || strings.HasPrefix(upper, "SURF") {
			hasSRF = true
		}
	}
	return hasNXT && !hasSRF
}

func parseOsloNXT(input string) (*ParseResult, error) {
	lines := strings.Split(input, "\n")

	result := &ParseResult{StopSurface: 0}
	seenGlasses := make(map[string]bool)

	inLens := false
	var surfaces []nxtSurface
	var current nxtSurface
	var wvValues []float64

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "//") {
			continue
		}

		upper := strings.ToUpper(line)

		if strings.HasPrefix(upper, "LEN NEW") {
			inLens = true
			continue
		}
		if upper == "END" || strings.HasPrefix(upper, "END ") {
			inLens = false
			break
		}
		if !inLens {
			wv := parseOsloWVLine(line)
			if wv != nil {
				result.Wavelengths = append(result.Wavelengths, wv...)
			}
			fld := parseOsloAngleField(line)
			if fld != nil {
				fld.ID = len(result.Fields)
				result.Fields = append(result.Fields, *fld)
			}
			continue
		}

		if upper == "NXT" || strings.HasPrefix(upper, "NXT ") {
			surfaces = append(surfaces, current)
			current = nxtSurface{}
			continue
		}

		if upper == "AIR" || upper == "AIR " {
			current.Material = "AIR"
			continue
		}
		if strings.HasPrefix(upper, "GLA ") || strings.HasPrefix(upper, "GLASS ") {
			current.Material = strings.TrimSpace(line[4:])
			continue
		}
		if strings.HasPrefix(upper, "RD ") {
			current.Radius = parseOsloFloat(line[3:])
			continue
		}
		if strings.HasPrefix(upper, "TH ") {
			current.Thickness = parseOsloFloat(line[3:])
			continue
		}
		if strings.HasPrefix(upper, "AP ") {
			current.ApertureRadius = parseOsloFloat(line[3:])
			continue
		}
		if strings.HasPrefix(upper, "PY ") {
			continue
		}
		if strings.HasPrefix(upper, "WV ") || strings.HasPrefix(upper, "WV\t") {
			parts := strings.Fields(line[2:])
			wvValues = nil
			for _, p := range parts {
				val := parseOsloFloat(p)
				if val > 0 {
					wvValues = append(wvValues, val)
				}
			}
			continue
		}
		if strings.HasPrefix(upper, "WW ") || strings.HasPrefix(upper, "WW\t") {
			parts := strings.Fields(line[2:])
			for i, p := range parts {
				weight := parseOsloFloat(p)
				if weight <= 0 {
					weight = 1.0
				}
				if i < len(wvValues) {
					result.Wavelengths = append(result.Wavelengths, types.WavelengthItem{
						ID:     len(result.Wavelengths),
						Value:  wvValues[i],
						Weight: weight,
					})
				}
			}
			continue
		}
	}

	if inLens && current.Material != "" {
		surfaces = append(surfaces, current)
	}

	if len(surfaces) <= 1 {
		return result, nil
	}

	var surfList []types.Surface
	stopFound := false

	for i := 1; i < len(surfaces); i++ {
		s := surfaces[i]
		id := i

		mat := s.Material
		if mat == "" {
			mat = "AIR"
		}

		curv := 0.0
		if s.Radius != 0 {
			curv = 1.0 / s.Radius
		}

		diam := 0.0
		if s.ApertureRadius > 0 {
			diam = s.ApertureRadius * 2
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

		surf := types.Surface{
			ID:        id,
			Type:      types.Sphere,
			Curvature: curv,
			Thickness: parseOsloThickness(s.Thickness),
			Material:  mat,
			Diameter:  diam,
		}
		surfList = append(surfList, surf)
	}

	if len(surfList) > 0 {
		last := surfList[len(surfList)-1]
		result.ImageSurface = last.ID
	}

	result.Surfaces = surfList

	if !stopFound && len(surfList) > 0 {
		result.StopSurface = surfList[0].ID
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

type nxtSurface struct {
	Material       string
	Radius         float64
	Thickness      float64
	ApertureRadius float64
}

func parseOsloWVLine(line string) []types.WavelengthItem {
	upper := strings.ToUpper(strings.TrimSpace(line))
	if !strings.HasPrefix(upper, "WV") {
		return nil
	}
	rest := strings.TrimSpace(line[2:])
	parts := strings.Fields(rest)
	var out []types.WavelengthItem
	for i, p := range parts {
		val := parseOsloFloat(p)
		if val > 0 {
			out = append(out, types.WavelengthItem{
				ID:    i,
				Value: val,
				Weight: 1.0,
			})
		}
	}
	return out
}

func parseOsloSRF(input string) (*ParseResult, error) {
	lines := strings.Split(input, "\n")

	result := &ParseResult{
		StopSurface: 0,
	}
	seenGlasses := make(map[string]bool)

	inLens := false
	surfMap := make(map[int]*osloSurface)
	stopID := 0
	nextID := 1
	curSurf := -1

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "*") {
			continue
		}

		upper := strings.ToUpper(line)

		if strings.HasPrefix(upper, "LEN NEW") || strings.HasPrefix(upper, "LENS NEW") {
			inLens = true
			nextID = 1
			curSurf = -1
			continue
		}
		if upper == "END" {
			inLens = false
			continue
		}
		if !inLens {
			piField := parseOsloPILine(line)
			if piField != nil {
				piField.ID = len(result.Wavelengths)
				result.Wavelengths = append(result.Wavelengths, *piField)
			}
			fld := parseOsloAngleField(line)
			if fld != nil {
				fld.ID = len(result.Fields)
				result.Fields = append(result.Fields, *fld)
			}
			continue
		}

		line = stripComment(line)
		if line == "" {
			continue
		}

		upper = strings.ToUpper(line)

		if strings.HasPrefix(upper, "SRF") || strings.HasPrefix(upper, "SURF") {
			tokens := strings.Fields(line)
			if len(tokens) >= 2 {
				label := strings.ToUpper(strings.TrimSpace(tokens[1]))
				switch {
				case label == "OBJ":
					curSurf = -1
				case label == "IMS" || label == "IMG":
					curSurf = nextID
					s := getOrCreateSurface(surfMap, curSurf)
					if len(tokens) >= 3 {
						parseOsloKV(s, tokens[2:])
					}
					nextID++
				case label == "AST" || label == "STOP" || label == "STO" || label == "A":
					curSurf = nextID
					stopID = curSurf
					s := getOrCreateSurface(surfMap, curSurf)
					s.isAST = true
					if len(tokens) >= 3 {
						parseOsloKV(s, tokens[2:])
					}
					nextID++
				default:
					n, err := strconv.Atoi(label)
					if err == nil && n >= 0 {
						curSurf = n
						if n >= nextID {
							nextID = n + 1
						}
						s := getOrCreateSurface(surfMap, n)
						if len(tokens) >= 3 {
							parseOsloKV(s, tokens[2:])
						}
					}
				}
			}
			continue
		}

		if curSurf >= 0 {
			val := parseOsloBlockParam(line, curSurf)
			if val != nil {
				surf := getOrCreateSurface(surfMap, val.surface)
				switch val.name {
				case "RD", "RADIUS":
					if val.value != 0 {
						surf.Curvature = 1.0 / val.value
					} else {
						surf.Curvature = 0
					}
				case "CV", "CURV", "CURVATURE":
					surf.Curvature = val.value
				case "TH", "THICKNESS":
					surf.Thickness = parseOsloThickness(val.value)
				case "GL", "GLASS":
					surf.Material = val.strValue
				case "AP", "APERTURE_RADIUS", "APERTURE":
					surf.Diameter = val.value * 2
				case "CONI", "CONIC":
					surf.Conic = val.value
				}
			}
		}

		if cmd := tryCommandFormat(line); cmd != nil {
			surf := getOrCreateSurface(surfMap, cmd.surface)
			switch cmd.name {
			case "RD", "RADIUS":
				if cmd.value != 0 {
					surf.Curvature = 1.0 / cmd.value
				} else {
					surf.Curvature = 0
				}
			case "CV", "CURV", "CURVATURE":
				surf.Curvature = cmd.value
			case "TH", "THICKNESS":
				surf.Thickness = parseOsloThickness(cmd.value)
			case "GL", "GLASS":
				surf.Material = cmd.strValue
			case "AP", "APERTURE_RADIUS", "APERTURE":
				surf.Diameter = cmd.value * 2
			case "CONI", "CONIC":
				surf.Conic = cmd.value
			}
			if cmd.surface > nextID {
				nextID = cmd.surface + 1
			}
			continue
		}
	}

	lastID := 0
	for id := range surfMap {
		if id > lastID {
			lastID = id
		}
	}

	for _, s := range surfMap {
		if s.isAST {
			stopID = s.ID
		}
	}

	if stopID == 0 && len(surfMap) > 0 {
		stopID = 1
	}
	result.StopSurface = stopID

	for id := 1; id <= lastID; id++ {
		s, ok := surfMap[id]
		if !ok {
			continue
		}

		mat := s.Material
		if mat != "" && !isAir(mat) {
			if !seenGlasses[mat] {
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
		}

		surf := types.Surface{
			ID:        id,
			Type:      types.Sphere,
			Curvature: s.Curvature,
			Thickness: s.Thickness,
			Material:  s.Material,
			Diameter:  s.Diameter,
			Conic:     s.Conic,
		}
		result.Surfaces = append(result.Surfaces, surf)
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

type osloSurface struct {
	ID        int
	Curvature float64
	Thickness float64
	Material  string
	Diameter  float64
	Conic     float64
	isAST     bool
}

type osloCommand struct {
	name     string
	surface  int
	value    float64
	strValue string
}

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

func getOrCreateSurface(m map[int]*osloSurface, id int) *osloSurface {
	if s, ok := m[id]; ok {
		return s
	}
	s := &osloSurface{ID: id}
	m[id] = s
	return s
}

func parseOsloSurfName(s string) int {
	u := strings.ToUpper(strings.TrimSpace(s))
	if u == "OBJ" {
		return 0
	}
	if u == "IMS" || u == "IMG" {
		return -1
	}
	if u == "AST" || u == "STOP" || u == "STO" || u == "A" {
		return -2
	}
	n, err := strconv.Atoi(u)
	if err != nil {
		return -1
	}
	return n
}

func parseOsloASTNumber(s string) int {
	u := strings.ToUpper(strings.TrimSpace(s))
	u = strings.TrimLeft(u, "AST")
	u = strings.TrimSpace(u)
	n, err := strconv.Atoi(u)
	if err != nil {
		return 1
	}
	return n
}

func parseOsloKV(s *osloSurface, tokens []string) {
	if len(tokens) < 2 {
		return
	}
	key := strings.ToUpper(tokens[0])
	val := tokens[1]
	switch key {
	case "RADIUS", "RD":
		r := parseOsloFloat(val)
		if r != 0 {
			s.Curvature = 1.0 / r
		} else {
			s.Curvature = 0
		}
	case "CURVATURE", "CV", "CURV":
		s.Curvature = parseOsloFloat(val)
	case "THICKNESS", "TH":
		s.Thickness = parseOsloThickness(parseOsloFloat(val))
	case "GLASS", "GL":
		s.Material = val
	case "APERTURE_RADIUS", "APERTURE", "AP":
		s.Diameter = parseOsloFloat(val) * 2
	case "CONIC", "CONI":
		s.Conic = parseOsloFloat(val)
	case "DIAMETER":
		s.Diameter = parseOsloFloat(val)
	}
}

func tryCommandFormat(line string) *osloCommand {
	upper := strings.ToUpper(strings.TrimSpace(line))

	prefixes := []struct {
		key string
		hasValue bool
	}{
		{"RD ", false}, {"RADIUS ", false},
		{"CV ", false}, {"CURVATURE ", false},
		{"TH ", false}, {"THICKNESS ", false},
		{"GL ", true}, {"GLASS ", true},
		{"AP ", false}, {"APERTURE_RADIUS ", false}, {"APERTURE ", false},
		{"CONI ", false}, {"CONIC ", false},
	}

	for _, p := range prefixes {
		if !strings.HasPrefix(upper, p.key) {
			continue
		}
		rest := strings.TrimSpace(line[len(p.key):])
		fields := strings.Fields(rest)
		if len(fields) < 2 {
			continue
		}
		surf := parseOsloSurfName(fields[0])
		if surf < 0 {
			continue
		}
		cmd := &osloCommand{
			name:    strings.TrimSpace(p.key),
			surface: surf,
		}
		if p.hasValue {
			cmd.strValue = fields[1]
			cmd.value = 0
		} else {
			cmd.value = parseOsloFloat(fields[1])
		}
		return cmd
	}

	if idx := strings.Index(line, "("); idx > 0 && idx < 10 {
		cmdName := strings.ToUpper(strings.TrimSpace(line[:idx]))
		rest := strings.TrimRight(strings.TrimSpace(line[idx+1:]), ")")
		args := strings.Split(rest, ",")
		if len(args) < 2 {
			return nil
		}
		surf := parseOsloSurfName(strings.TrimSpace(args[0]))
		if surf < 0 {
			return nil
		}
		cmd := &osloCommand{
			name:    cmdName,
			surface: surf,
		}
		valStr := strings.TrimSpace(args[1])
		valStr = strings.Trim(valStr, "\"")
		cmd.value = parseOsloFloat(valStr)
		if cmdName == "GL" || cmdName == "GLASS" || strings.HasPrefix(cmdName, "GL") {
			cmd.strValue = valStr
		}
		return cmd
	}

	return nil
}

func parseOsloPILine(line string) *types.WavelengthItem {
	upper := strings.ToUpper(strings.TrimSpace(line))
	if !strings.HasPrefix(upper, "PI ") && upper != "PI" {
		return nil
	}
	rest := strings.TrimSpace(line[3:])
	if rest == "" {
		return nil
	}
	parts := strings.Fields(rest)
	if len(parts) < 1 {
		return nil
	}
	val := parseOsloFloat(parts[0])
	if val <= 0 {
		return nil
	}
	return &types.WavelengthItem{
		Value:  val,
		Weight: 1.0,
	}
}

func parseOsloAngleField(line string) *types.FieldItem {
	upper := strings.ToUpper(strings.TrimSpace(line))
	rest := ""
	if strings.HasPrefix(upper, "FIELD ") {
		rest = strings.TrimSpace(line[6:])
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
	angle := parseOsloFloat(parts[0])
	w := 1.0
	if len(parts) >= 2 {
		w = parseOsloFloat(parts[1])
	}
	return &types.FieldItem{
		AngleDeg: angle,
		Weight:   w,
	}
}

func parseOsloBlockParam(line string, surfID int) *osloCommand {
	upper := strings.ToUpper(strings.TrimSpace(line))
	prefixes := []struct {
		key      string
		hasValue bool
	}{
		{"RD ", false}, {"RADIUS ", false},
		{"CV ", false}, {"CURVATURE ", false},
		{"TH ", false}, {"THICKNESS ", false},
		{"GL ", true}, {"GLASS ", true},
		{"AP ", false}, {"APERTURE_RADIUS ", false}, {"APERTURE ", false},
		{"CONI ", false}, {"CONIC ", false},
	}
	for _, p := range prefixes {
		if !strings.HasPrefix(upper, p.key) {
			continue
		}
		rest := strings.TrimSpace(line[len(p.key):])
		fields := strings.Fields(rest)
		if len(fields) < 1 {
			continue
		}
		cmd := &osloCommand{
			name:    strings.TrimSpace(p.key),
			surface: surfID,
		}
		if p.hasValue {
			cmd.strValue = fields[0]
		} else {
			cmd.value = parseOsloFloat(fields[0])
		}
		return cmd
	}
	return nil
}

func parseOsloFloat(s string) float64 {
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

func parseOsloThickness(v float64) float64 {
	if math.IsInf(v, 1) {
		return 0
	}
	return v
}

func stripComment(line string) string {
	if idx := strings.Index(line, "!"); idx >= 0 {
		return strings.TrimSpace(line[:idx])
	}
	return line
}
