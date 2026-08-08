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

	// Decenter-and-return (DAR) state: the surface's decenter components
	// accumulated from YDE/XDE/ZDE (shifts) and ADE/BDE/CDE (tilts). Only
	// present when a DAR keyword preceded them on this surface. The DAR
	// "return" is implicit: the decenter applies to the surface itself and
	// the axis then continues (scope scope surface), matching CODE V's
	// decenter-and-return (inverse transform after the surface). REX/REY and
	// ADY in the same block are rectangular-aperture half-widths and aperture
	// offsets, not return decenters.
	Decenter  types.DecenterStep
	decActive bool
}

func ParseCodeV(input string) (*ParseResult, error) {
	input = decodeBOM(input)
	rawLines := strings.Split(input, "\n")

	lines := joinContinuationLines(rawLines)

	result := &ParseResult{StopSurface: 0, ReferenceWavelengthIdx: -1}
	surfMap := make(map[int]*codeVSurf)

	beforeLens := true
	inchMode := false
	stopSurface := 0
	imageSurface := 0
	compactMode := false
	compactCounter := 0
	lastSurfNum := 0
	inAspBlock := false
	inPrv := false
	var prvWavelengths []float64

	// Radius/curvature entry mode (CODE V "RDM"). Default is curvature mode
	// (RDM absent or RDM N): surface first values are curvatures. RDM Y / RDM
	// select radius mode: values are radii of curvature and converted to
	// curvature at parse time.
	radiusMode := false

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

		// The RDM directive selects whether the surface first values are radii
		// of curvature (RDM / RDM Y, or absent-but-leaf RDM; CODE V writes
		// "RDM;..." for radius) or curvatures (RDM N / RDM NO). The default
		// when RDM is absent is curvature mode. Apply the mode before any
		// keyword dispatch so every following surface reads the right units.
		// Only treat a line as a directive when it carries a mode word; look
		// for the mode either fused into the token after "RDM" (e.g. ";LEN ")
		// or as a Y/N/YES/NO keyword.
		if parseCodeVRDMDirective(upper, tokens, &radiusMode) {
			continue
		}

		// CODE V is case-insensitive; dispatch on the uppercased keyword while
		// keeping the raw tokens for values and material labels.
		first := strings.ToUpper(tokens[0])

		// A partial-wavelength (PRV) block may precede or follow the lens
		// data; handle it before any keyword dispatch so PWL/glass rows and
		// the block-closing END are never parsed as surfaces or terminators.
		if parseCodeVPRVLine(upper, tokens, result, &inPrv, &prvWavelengths) {
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
			if len(tokens) > 0 && (first == "SO" || first == "S" || first == "SI") {
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
			surf.Curvature = surfaceValue(parseFloat(tokens[1]), radiusMode)
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
			surf.Curvature = surfaceValue(parseFloat(tokens[1]), radiusMode)
			surf.Thickness = parseThickness(parseFloat(tokens[2]))
			lastSurfNum = compactCounter
			imageSurface = compactCounter
			inAspBlock = false
			continue
		}

		if first == "CIR" && len(tokens) >= 2 {
			if lastSurfNum > 0 {
				surf := getOrCreate(surfMap, lastSurfNum)
				if strings.EqualFold(tokens[1], "EDG") {
					// Edge aperture: a mechanical edge spec, never the optical
					// clear aperture. Use it only as a fallback so it cannot
					// clobber a preceding clear CIR with a zero.
					if surf.Diameter == 0 && len(tokens) >= 3 {
						surf.Diameter = parseFloat(tokens[2]) * 2
					}
				} else {
					surf.Diameter = parseFloat(tokens[1]) * 2
				}
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
			case "CCY", "CON", "K":
				// Compact-mode conic statements. "CON" declares a conic type
				// (the value may follow on the same line or in the next "K"
				// line); "K <k>" / "CCY <k>" set the conic value directly. A
				// line may join several statements with ';' (e.g.
				// "K 0.226106; A 0.368950E-10"), so delegate to the statement
				// walker. The 3-token "K <surf> <k>" keyword form falls
				// through to the surface-prefixed handler below.
				if first != "K" || len(tokens) != 3 {
					applyCodeVCompactOps(getOrCreate(surfMap, lastSurfNum), tokens)
				}
				inAspBlock = false
				continue
			case "THC":
				inAspBlock = false
				continue
			case "DAR":
				// Decenter-and-return: the current surface is shifted/tilted
				// locally and the axis returns after it. rayweave models this
				// with a per-surface DecenterStep; the decenter components are
				// supplied by following YDE/XDE/ZDE/ADE/BDE/CDE statements.
				getOrCreate(surfMap, lastSurfNum).decActive = true
				inAspBlock = false
				continue
			case "YDE", "XDE", "ZDE", "ADE", "BDE", "CDE":
				surf := getOrCreate(surfMap, lastSurfNum)
				surf.decActive = true
				applyCodeVDecenterOps(surf, tokens)
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
		if result.EntrancePupilDiameter > 0 {
			result.EntrancePupilDiameter *= 25.4
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

		// CODE V decenter-and-return (DAR) components become a per-surface
		// DecenterStep with scope surface (default): the surface is shifted and
		// tilted while the beam frame continues, which is exactly what a DAR
		// does in CODE V (apply the decenter, then return the axis).
		if s.decActive && (s.Decenter != types.DecenterStep{}) {
			t.Decenter = []types.DecenterStep{s.Decenter}
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

	// Convert folded mirror systems (REFL material, negative spacings) into
	// rayweave's fold model before any EPD sizing or default filling so the
	// downstream pipeline sees all-positive thicknesses.
	convertFoldMirrors(result)

	// CODE V EPD specifies the entrance pupil diameter; when the file carries
	// no per-surface apertures, size the stop surface to it so the chief grid
	// radius resolves. (The stop surface defaults to 1 for codev.)
	if result.EntrancePupilDiameter > 0 {
		for i := range result.Surfaces {
			if result.Surfaces[i].ID == result.StopSurface {
				if result.Surfaces[i].Diameter == 0 {
					result.Surfaces[i].Diameter = result.EntrancePupilDiameter
				}
				break
			}
		}
	}

	// CODE V REF selects the primary (reference) wavelength by 1-based index
	// in the WL list; mark it so downstream selection (FNO sizing, chief ray
	// wavelength, merit term) uses it. REF may appear before or after WL, so
	// apply once all wavelengths are known.
	if result.ReferenceWavelengthIdx >= 0 && result.ReferenceWavelengthIdx < len(result.Wavelengths) {
		result.Wavelengths[result.ReferenceWavelengthIdx].Primary = true
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

// parseCodeVRDMDirective handles the CODE V "RDM" radius/curvature mode
// directive and reports whether the line was consumed as one.
//
// CODE V semantics: RDM selects the units used for the first value of surface
// data rows. "RDM" alone or "RDM Y" selects radii of curvature; "RDM N"
// selects curvatures. When the directive is absent, CODE V defaults to
// curvature mode. The directive commonly appears fused with other header text
// on the same line, e.g. "RDM N;LEN \"VERSION...\"" or "RDM;LEN \"VERSION...\"",
// where the mode word is the token following "RDM" (possibly including a ";"
// terminator that the surface-row parser would otherwise choke on).
func parseCodeVRDMDirective(upper string, tokens []string, radiusMode *bool) bool {
	if len(tokens) == 0 || !strings.HasPrefix(strings.ToUpper(tokens[0]), "RDM") {
		return false
	}
	// The mode word may be fused into the first token ("RDM;LEN ..." or
	// "RDM N;LEN ...") or appear as a separate token ("RDM Y", "rdm n").
	firstUpper := strings.ToUpper(tokens[0])
	var mode string
	if i := strings.Index(firstUpper, ";"); i >= 0 {
		// Fused form: the directive is complete within the first token.
		mode = firstUpper[len("RDM"):i]
	} else if len(tokens) > 1 {
		// Separate form: take the mode word from the next token, dropping a
		// trailing ";" that a surface row parser would otherwise choke on.
		mode = strings.ToUpper(strings.Trim(tokens[1], ";"))
	}
	switch mode {
	case "", "Y", "YES":
		*radiusMode = true
	case "N", "NO":
		*radiusMode = false
	default:
		// Not a mode directive — e.g. "RDM <surf> <radius>" surface row.
		return false
	}
	return true
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
	if strings.HasPrefix(upper, "EPD ") && len(tokens) >= 2 {
		result.EntrancePupilDiameter = parseFloat(tokens[1])
		return true
	}
	if strings.HasPrefix(upper, "REF ") && len(tokens) >= 2 {
		// REF selects the primary (reference) wavelength by its 1-based
		// position in the WL list. Stored 0-based; applied to the
		// WavelengthItem Primary flag once all wavelengths are known.
		if n, err := strconv.Atoi(tokens[1]); err == nil && n > 0 {
			result.ReferenceWavelengthIdx = n - 1
		}
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

// applyCodeVCompactOps applies compact-mode conic and asphere statements to a
// surface. A line may join several statements with ';' (e.g. "K 0.226106; A
// 0.368950E-10" or "CCY 0; THC 0"); tokens are walked in "keyword value" pairs
// where a trailing ';' is absorbed by parseFloat. CCY/CON/K set the conic
// constant, asphere letters set polynomial coefficients, and any other
// statement (e.g. THC) is ignored.
func applyCodeVCompactOps(surf *codeVSurf, tokens []string) {
	for i := 0; i+1 < len(tokens); i += 2 {
		switch tokens[i] {
		case "CCY", "CON", "K":
			surf.Conic = parseFloat(tokens[i+1])
		default:
			if isAsphereLetter(tokens[i]) {
				if surf.Coeffs == nil {
					surf.Coeffs = make(map[int]float64)
				}
				surf.Coeffs[asphereOrder(tokens[i])] = parseFloat(tokens[i+1])
			}
		}
	}
}

// applyCodeVDecenterOps accumulates decenter components from a CODE V decenter
// statement line. A line joins several statements with ';' (e.g.
// "YDE 48.695345; ADE 8.438645"), so walk the tokens as keyword/value pairs
// (parseFloat ignores a trailing ';').
func applyCodeVDecenterOps(surf *codeVSurf, tokens []string) {
	for i := 0; i+1 < len(tokens); i += 2 {
		switch strings.ToUpper(tokens[i]) {
		case "YDE":
			surf.Decenter.Shift.Y = parseFloat(tokens[i+1])
		case "XDE":
			surf.Decenter.Shift.X = parseFloat(tokens[i+1])
		case "ZDE":
			surf.Decenter.Shift.Z = parseFloat(tokens[i+1])
		case "ADE":
			surf.Decenter.Tilt.X = parseFloat(tokens[i+1])
		case "BDE":
			surf.Decenter.Tilt.Y = parseFloat(tokens[i+1])
		case "CDE":
			surf.Decenter.Tilt.Z = parseFloat(tokens[i+1])
		}
	}
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

// parseCodeVPRVLine handles one line of a CODE V partial-wavelength (PRV)
// block: "PRV" enters the block, "PWL <wavelengths>" records the reference
// wavelengths (nm), each following "<glass> <index per wavelength>" row
// registers a tabulated glass, and "END" leaves the block. Returns handled=true
// for any consumed line so the caller skips normal keyword parsing (including
// the block-closing END, which must not terminate the lens).
func parseCodeVPRVLine(upper string, tokens []string, result *ParseResult, inPrv *bool, prvWavelengths *[]float64) bool {
	if *inPrv {
		if strings.HasPrefix(upper, "PWL ") {
			*prvWavelengths = parseCodeVPRVWavelengths(tokens[1:])
		} else if upper == "END" || strings.HasPrefix(upper, "END ") {
			*inPrv = false
		} else if len(*prvWavelengths) > 0 && len(tokens) >= 2 {
			// A row is either "<glass> <index per wavelength>" (table form) or
			// "<glass> <formula-type> <coeffs...>" (dispersion-formula form,
			// e.g. "NOA61 LAU 2.36390625 ..."). A non-numeric second token
			// selects the formula form.
			name := strings.Trim(strings.TrimSpace(tokens[0]), "'\"")
			if formula, ok := codeVFormulaType(tokens[1]); ok {
				coeffs := make([]float64, 0, len(tokens)-2)
				for _, tok := range tokens[2:] {
					coeffs = append(coeffs, parseFloat(tok))
				}
				addFormulaGlass(result, name, formula, coeffs)
			} else if parseFloat(tokens[1]) > 1 {
				indices := make([]float64, 0, len(tokens)-1)
				for _, tok := range tokens[1:] {
					indices = append(indices, parseFloat(tok))
				}
				if len(indices) == len(*prvWavelengths) {
					addTabulatedGlass(result, name, *prvWavelengths, indices)
				}
			}
		}
		return true
	}
	if upper == "PRV" || strings.HasPrefix(upper, "PRV ") {
		*inPrv = true
		return true
	}
	return false
}

// parseCodeVPRVWavelengths converts a CODE V PRV "PWL" row (wavelengths in nm)
// into millimetres, the internal ray-trace wavelength unit.
func parseCodeVPRVWavelengths(tokens []string) []float64 {
	out := make([]float64, 0, len(tokens))
	for _, tok := range tokens {
		nm := parseFloat(tok)
		if nm > 0 {
			out = append(out, nm/1e6)
		}
	}
	return out
}

// addTabulatedGlass registers a partial-wavelength glass (CODE V PRV rows) as a
// tabulated dispersion entry, deduplicating by case-insensitive label.
func addTabulatedGlass(result *ParseResult, name string, wavelengths []float64, indices []float64) {
	if name == "" {
		return
	}
	for _, g := range result.GlassEntries {
		if strings.EqualFold(g.Label, name) {
			return
		}
	}
	table := make(types.RefractiveIndexTable, len(wavelengths))
	for i := range wavelengths {
		table[i] = types.RefractiveIndexEntry{Wavelength: wavelengths[i], Value: indices[i]}
	}
	result.GlassEntries = append(result.GlassEntries, types.Glass{
		Type:              types.GlassTypeTabulated,
		Key:               name,
		Label:             name,
		RefractiveIndices: table,
	})
}

// codeVFormulaType maps a CODE V PRV dispersion-formula keyword to the internal
// DispersionFormula, reporting ok=false for unrecognised types.
//
//   - LAU / GML: Laurent n² = A₀ + A₁λ² + A₂/λ² + A₃/λ⁴ + A₄/λ⁶ + A₅/λ⁸
//   - SLM / GMS: standard Sellmeier n² = 1 + Σ Bᵢλ²/(λ² − Cᵢ), 6 coefficients
//     in B₁ C₁ B₂ C₂ B₃ C₃ order
//   - CAU: Cauchy n = A₀ + A₁/λ² + A₂/λ⁴ + … (returns n directly)
//   - HAR: Hartmann n = A₀ + A₁/(λ − A₂) (returns n directly)
func codeVFormulaType(kw string) (types.DispersionFormula, bool) {
	switch strings.ToUpper(kw) {
	case "LAU", "GML":
		return types.Laurent, true
	case "SLM", "GMS":
		return types.Sellmeier1, true
	case "CAU":
		return types.Cauchy, true
	case "HAR":
		return types.Hartmann, true
	default:
		return "", false
	}
}

// addFormulaGlass registers a CODE V PRV dispersion-formula glass (e.g.
// "NOA61 LAU <coeffs>"), deduplicating by case-insensitive label.
func addFormulaGlass(result *ParseResult, name string, formula types.DispersionFormula, coeffs []float64) {
	if name == "" || len(coeffs) == 0 {
		return
	}
	for _, g := range result.GlassEntries {
		if strings.EqualFold(g.Label, name) {
			return
		}
	}
	result.GlassEntries = append(result.GlassEntries, types.Glass{
		Type:              types.GlassTypeCatalog,
		Key:               name,
		Label:             name,
		DispersionFormula: formula,
		Coefficients:      coeffs,
	})
}
