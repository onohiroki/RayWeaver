package importer

import (
	"math"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
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
			if current.Material != "" {
				surfaces = append(surfaces, current)
				current = nxtSurface{}
			}
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
			current.Material, current.GlassIndices = splitOsloGlassLine(line[4:])
			continue
		}
		if strings.HasPrefix(upper, "RD ") {
			current.Radius = parseFloat(line[3:])
			continue
		}
		if strings.HasPrefix(upper, "TH ") {
			current.Thickness = parseFloat(line[3:])
			continue
		}
		if strings.HasPrefix(upper, "AP ") {
			current.ApertureRadius = parseFloat(line[3:])
			continue
		}
		if strings.HasPrefix(upper, "PY ") {
			continue
		}
		if strings.HasPrefix(upper, "WRSP ") {
			current.WRSP = parseFloat(line[5:])
			continue
		}
		if strings.HasPrefix(upper, "WV ") || strings.HasPrefix(upper, "WV\t") {
			parts := strings.Fields(line[2:])
			wvValues = nil
			for _, p := range parts {
				val := parseFloat(p)
				if val > 0 {
					wvValues = append(wvValues, val)
				}
			}
			continue
		}
		if strings.HasPrefix(upper, "WW ") || strings.HasPrefix(upper, "WW\t") {
			parts := strings.Fields(line[2:])
			for i, p := range parts {
				weight := parseFloat(p)
				if weight <= 0 {
					weight = 1.0
				}
				if i < len(wvValues) {
					result.Wavelengths = append(result.Wavelengths, types.WavelengthItem{
						ID:     len(result.Wavelengths),
						Value:  wvValues[i] / 1000.0,
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

	for i := 1; i < len(surfaces); i++ {
		s := surfaces[i]
		id := i

		mat := s.Material
		if mat == "" {
			mat = "AIR"
		}

		curv := radiusToCurvature(s.Radius)

		diam := 0.0
		if s.ApertureRadius > 0 {
			diam = s.ApertureRadius * 2
		}

		if len(s.GlassIndices) > 0 {
			addOsloModelGlass(result, mat, s.GlassIndices)
		} else {
			addGlassEntry(result, mat)
		}

		surf := types.Surface{
			ID:        id,
			Type:      types.Sphere,
			Curvature: curv,
			Thickness: parseThickness(s.Thickness),
			Diameter:  diam,
		}
		if isAir(mat) {
			surf.Material = types.Material{}
		} else {
			surf.Material = types.Material{Key: mat}
		}
		surfList = append(surfList, surf)
	}

	if len(surfList) > 0 {
		last := surfList[len(surfList)-1]
		result.ImageSurface = last.ID
	}

	// WRSP Inf on the image surface signals an auto-image-distance solve
	// (paraxial marginal ray height): the distance from the last optical
	// surface to the image plane is determined by paraxial analysis, not
	// by an explicit TH value. Record the flag so the caller can compute
	// the right distance.
	if len(surfaces) > 0 && math.IsInf(surfaces[len(surfaces)-1].WRSP, 0) {
		result.NeedsImageDistance = true
	}

	result.Surfaces = surfList

	fillDefaults(result)

	return result, nil
}

type nxtSurface struct {
	Material       string
	GlassIndices   []float64
	Radius         float64
	Thickness      float64
	ApertureRadius float64
	WRSP           float64
}

// splitOsloGlassLine splits the value of an OSLO GLA/GLASS line into the glass
// name and any trailing model-glass refractive indices. OSLO writes model
// glasses as "GLA <name> <nd> <nF> <nC>" (indices at the d/F/C lines); catalog
// references are just "GLA <name>". Model glasses exported from ZEMAX may use a
// numeric name carrying its own index, e.g. "GLA 1.506000 1.506 1.506".
func splitOsloGlassLine(rest string) (name string, indices []float64) {
	tokens := strings.Fields(rest)
	if len(tokens) == 0 {
		return "", nil
	}
	n := len(tokens)
	for n > 0 && parseFloat(tokens[n-1]) > 0 {
		n--
	}
	if n > 0 {
		return strings.Join(tokens[:n], " "), tokensToFloats(tokens[n:])
	}
	if len(tokens) == 1 {
		return tokens[0], nil
	}
	return tokens[0], tokensToFloats(tokens[1:])
}

func tokensToFloats(tokens []string) []float64 {
	out := make([]float64, len(tokens))
	for i, t := range tokens {
		out[i] = parseFloat(t)
	}
	return out
}

// osloStandardWavelengths are the OSLO default WV1..WV3 (d, F, C lines) in mm,
// the wavelengths at which GLA model-glass indices are given.
var osloStandardWavelengths = [...]float64{0.000587562, 0.000486133, 0.000656273}

// addOsloModelGlass registers an OSLO model glass (a name plus refractive
// indices at the d/F/C lines) as a tabulated glass entry, deduplicating by
// label like addGlassEntry. The tabulated form preserves the three indices
// exactly and works at any trace wavelength.
func addOsloModelGlass(result *ParseResult, name string, indices []float64) {
	if name == "" || isAir(name) {
		return
	}
	for _, g := range result.GlassEntries {
		if strings.EqualFold(g.Label, name) {
			return
		}
	}
	table := make(types.RefractiveIndexTable, len(indices))
	for i, idx := range indices {
		wl := types.DefaultWavelength
		if i < len(osloStandardWavelengths) {
			wl = osloStandardWavelengths[i]
		}
		table[i] = types.RefractiveIndexEntry{Wavelength: wl, Value: idx}
	}
	result.GlassEntries = append(result.GlassEntries, types.Glass{
		Type:              types.GlassTypeTabulated,
		Label:             name,
		RefractiveIndices: table,
	})
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
		val := parseFloat(p)
		if val > 0 {
			out = append(out, types.WavelengthItem{
				ID:     i,
				Value:  val / 1000.0,
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
					s := getOrCreate(surfMap, curSurf)
					if len(tokens) >= 3 {
						parseOsloKV(s, tokens[2:])
					}
					nextID++
				case label == "AST" || label == "STOP" || label == "STO" || label == "A":
					curSurf = nextID
					stopID = curSurf
					s := getOrCreate(surfMap, curSurf)
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
						s := getOrCreate(surfMap, n)
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
				surf := getOrCreate(surfMap, val.surface)
				applyOsloCommand(surf, val)
			}
		}

		if cmd := tryCommandFormat(line); cmd != nil {
			surf := getOrCreate(surfMap, cmd.surface)
			applyOsloCommand(surf, cmd)
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

	for id, s := range surfMap {
		if s.isAST {
			stopID = id
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
		if mat == "" {
			mat = "AIR"
		}
		addGlassEntry(result, mat)

		surf := types.Surface{
			ID:        id,
			Type:      types.Sphere,
			Curvature: s.Curvature,
			Thickness: s.Thickness,
			Diameter:  s.Diameter,
			Conic:     s.Conic,
		}
		if isAir(mat) {
			surf.Material = types.Material{}
		} else {
			surf.Material = types.Material{Key: mat}
		}
		result.Surfaces = append(result.Surfaces, surf)
	}

	if len(result.Surfaces) > 0 {
		last := result.Surfaces[len(result.Surfaces)-1]
		result.ImageSurface = last.ID
	}

	fillDefaults(result)

	return result, nil
}

type osloSurface struct {
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
	if key == "DIAMETER" {
		s.Diameter = parseFloat(val)
		return
	}
	cmd := &osloCommand{name: key}
	if key == "GLASS" || key == "GL" {
		cmd.strValue = val
	} else {
		cmd.value = parseFloat(val)
	}
	applyOsloCommand(s, cmd)
}

// osloPrefixes maps keyword prefixes to whether they carry a string value.
var osloPrefixes = []struct {
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

// matchOsloCommand scans a line for a keyword prefix. When surfID >= 0 the
// value is the first field (block format); otherwise the surface number is
// parsed from the line (command format).
func matchOsloCommand(line string, surfID int) *osloCommand {
	upper := strings.ToUpper(strings.TrimSpace(line))
	for _, p := range osloPrefixes {
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
		if surfID >= 0 {
			if p.hasValue {
				cmd.strValue = fields[0]
			} else {
				cmd.value = parseFloat(fields[0])
			}
			return cmd
		}
		if len(fields) < 2 {
			continue
		}
		surf := parseOsloSurfName(fields[0])
		if surf < 0 {
			continue
		}
		cmd.surface = surf
		if p.hasValue {
			cmd.strValue = fields[1]
		} else {
			cmd.value = parseFloat(fields[1])
		}
		return cmd
	}
	return nil
}

// applyOsloCommand writes a parsed command onto a surface.
func applyOsloCommand(s *osloSurface, cmd *osloCommand) {
	switch cmd.name {
	case "RD", "RADIUS":
		s.Curvature = radiusToCurvature(cmd.value)
	case "CV", "CURV", "CURVATURE":
		s.Curvature = cmd.value
	case "TH", "THICKNESS":
		s.Thickness = parseThickness(cmd.value)
	case "GL", "GLASS":
		s.Material = cmd.strValue
	case "AP", "APERTURE_RADIUS", "APERTURE":
		s.Diameter = cmd.value * 2
	case "CONI", "CONIC":
		s.Conic = cmd.value
	}
}

func tryCommandFormat(line string) *osloCommand {
	if cmd := matchOsloCommand(line, -1); cmd != nil {
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
		cmd.value = parseFloat(valStr)
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
	val := parseFloat(parts[0])
	if val <= 0 {
		return nil
	}
	return &types.WavelengthItem{
		Value:  val / 1000.0,
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
	} else if strings.HasPrefix(upper, "ANG ") {
		rest = strings.TrimSpace(line[4:])
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
	angle := parseFloat(parts[0])
	w := 1.0
	if len(parts) >= 2 {
		w = parseFloat(parts[1])
	}
	return &types.FieldItem{
		AngleDeg: angle,
		Weight:   w,
	}
}

func parseOsloBlockParam(line string, surfID int) *osloCommand {
	return matchOsloCommand(line, surfID)
}

func stripComment(line string) string {
	if idx := strings.Index(line, "!"); idx >= 0 {
		return strings.TrimSpace(line[:idx])
	}
	return line
}

// ApplyImageDistance computes the paraxial image distance and sets the
// thickness of the last optical surface (second-to-last surface in the result)
// to the paraxial back focal length. This implements the OSLO WRSP Inf
// auto-image-distance solve (paraxial marginal ray height). The function is a
// no-op when NeedsImageDistance is false or when there are fewer than two
// surfaces. It should be called after the glass catalog is populated and
// surface precomputation has been done so PhysicalZ values are available.
func ApplyImageDistance(result *ParseResult, gc *glass.Catalog, wavelength float64) {
	if !result.NeedsImageDistance || len(result.Surfaces) < 2 {
		return
	}
	sys := types.System{Surfaces: result.Surfaces}
	pr := paraxial.Compute(sys, wavelength, gc, 0, nil)
	if pr.SecondPrincipalFocus <= 0 {
		return
	}
	result.Surfaces[len(result.Surfaces)-2].Thickness = pr.SecondPrincipalFocus
}
