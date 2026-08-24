package importer

import (
	"math"
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
	hdr := &zemaxHeader{result: result}

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
				parseZemaxHeader(hdr, keyword, args)
				continue
			}
			parseZemaxSurfaceParam(currentSurface, keyword, args)
		} else {
			parseZemaxHeader(hdr, keyword, args)
		}
	}

	if currentSurface != nil {
		surfParams = append(surfParams, *currentSurface)
	}

	// Object distance (surface 0 thickness) for finite-conjugate (FTYP 1,
	// object-height) fields. The object plane sits at z = -T before surface 1.
	var objectZ float64
	for _, sp := range surfParams {
		if sp.ID == 0 {
			objectZ = -sp.Thickness
			break
		}
	}
	buildZemaxFields(result, hdr, objectZ)
	buildZemaxWavelengths(result, hdr)

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

// zemaxHeader accumulates the header-level system data whose meaning depends on
// other keywords (FTYP for the field values, the wavefront count for WAVM).
// Field values, weights and vignetting factors are collected slot-aligned and
// resolved into FieldItems once parsing completes.
type zemaxHeader struct {
	result *ParseResult

	// Slot-aligned field data from XFLD/XFLN (x) and YFLD/YFLN (y), FWGT/FWGN
	// (weights) and VDXN/VDYN/VCXN/VCYN/VANN (vignetting factors).
	fieldX []float64
	fieldY []float64
	weight []float64
	vig    []zemaxVignette

	// WAVM rows: [value µm, weight]. The ZMX format pads unused wavelength
	// slots to 24 with a constant fill value; the trailing fill run is removed
	// once parsing completes.
	wavmRows [][2]float64
}

// zemaxVignette holds one field's five ZEMAX vignetting factors (VDXN/VDYN/
// VCXN/VCYN/VANN), interpreted against the entrance-pupil radius.
type zemaxVignette struct {
	vdx, vdy, vcx, vcy, van float64
}

func (v zemaxVignette) IsZero() bool {
	return v.vdx == 0 && v.vdy == 0 && v.vcx == 0 && v.vcy == 0 && v.van == 0
}

func parseZemaxHeader(hdr *zemaxHeader, keyword string, args []string) {
	result := hdr.result
	switch keyword {
	case "STOP":
		if len(args) > 0 {
			result.StopSurface = int(parseFloat(args[0]))
		}
	case "WAVL":
		for _, arg := range args {
			result.Wavelengths = append(result.Wavelengths, types.WavelengthItem{
				ID:     len(result.Wavelengths),
				Value:  parseFloat(arg) / 1000.0,
				Weight: 1.0,
			})
		}
	case "YFLD", "YFLN":
		// YFLD/YFLN <f0> <f1> ... — y field values, slot-aligned with XFLD/XFLN
		// and the FWGT/FWGN weights and vignetting rows. The meaning of the
		// values is given by the system field type (FTYP[0]).
		for _, a := range args {
			hdr.fieldY = append(hdr.fieldY, parseFloat(a))
		}
	case "XFLD", "XFLN":
		// XFLD/XFLN <x0> <x1> ... — x field values; nearly always zero. Non-zero
		// entries create skew fields (non-default direction).
		for _, a := range args {
			hdr.fieldX = append(hdr.fieldX, parseFloat(a))
		}
	case "FWGT", "FWGN":
		// Field weights, slot-aligned with the field values.
		for _, a := range args {
			hdr.weight = append(hdr.weight, parseFloat(a))
		}
	case "VDXN":
		for _, a := range args {
			hdr.vig = append(hdr.vig, zemaxVignette{vdx: parseFloat(a)})
		}
	case "VDYN":
		appendVignetteField(&hdr.vig, len(args), func(v *zemaxVignette, f float64) { v.vdy = f }, args)
	case "VCXN":
		appendVignetteField(&hdr.vig, len(args), func(v *zemaxVignette, f float64) { v.vcx = f }, args)
	case "VCYN":
		appendVignetteField(&hdr.vig, len(args), func(v *zemaxVignette, f float64) { v.vcy = f }, args)
	case "VANN":
		appendVignetteField(&hdr.vig, len(args), func(v *zemaxVignette, f float64) { v.van = f }, args)
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
				// Legacy FIELD image-height type; matches the modern
				// FTYP 2/3 -> ImageHeight mapping in buildZemaxFields.
				f.ImageHeight = parseFloat(args[2])
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
		// WAVM <index> <value µm> <weight>. Collected and truncated in
		// buildZemaxWavelengths: the ZMX format pads unused slots to 24 with a
		// constant fill value that must not be imported as real wavelengths.
		if len(args) >= 2 {
			w := 1.0
			if len(args) >= 3 {
				w = parseFloat(args[2])
			}
			hdr.wavmRows = append(hdr.wavmRows, [2]float64{parseFloat(args[1]), w})
		}
	case "WWGT":
		for i := range result.Wavelengths {
			if i < len(args) {
				result.Wavelengths[i].Weight = parseFloat(args[i])
			}
		}
	case "PWAV":
		// PWAV <n> — the 1-based index of the primary (reference) wavelength.
		// Applied once the WAVL/WAVM tables are built.
		if len(args) > 0 {
			result.ReferenceWavelengthIdx = int(parseFloat(args[0])) - 1
		}
	case "FTYP":
		// FTYP <global-type> <flags...> — system field type. Only the first
		// value is used: 0 = angle (deg), 1 = object height, 2 = paraxial image
		// height, 3 = real image height. The trailing values are internal
		// compatibility flags, not per-field codes, and are ignored.
		if len(args) > 0 {
			result.FieldType = int(parseFloat(args[0]))
		}
	case "UNIT":
	case "VERS":
	case "MODE":
	case "NAME":
	case "NOTE":
	case "FNUM":
		// FNUM <f-number> <flag...> — system F-number for aperture sizing when
		// the file carries no per-surface diameters.
		if len(args) > 0 {
			result.FNO = parseFloat(args[0])
		}
	case "ENPD", "ENVD":
		// Entrance-pupil diameter (ENPD) / envelope diameter (ENVD) header,
		// applied to the stop surface when the file carries no diameters.
		if len(args) > 0 {
			result.EntrancePupilDiameter = parseFloat(args[0])
		}
	case "APER":
	}
}

// appendVignetteField assigns one slot-aligned vignetting-factor row to the
// parallel vignette slice, extending it with zero rows as needed.
func appendVignetteField(vig *[]zemaxVignette, n int, set func(*zemaxVignette, float64), args []string) {
	for len(*vig) < n {
		*vig = append(*vig, zemaxVignette{})
	}
	for i := 0; i < n && i < len(args); i++ {
		set(&(*vig)[i], parseFloat(args[i]))
	}
}

// buildZemaxFields converts the slot-aligned X/Y field values, weights and
// vignetting factors into FieldItems. The meaning of the values is given by
// the global field type (FTYP[0]): 0 = half-angle in degrees, 1 = object
// height (finite conjugate, object distance objectZ), 2/3 = paraxial/real
// image height. Trailing all-zero padding slots are dropped; slot 0 (the
// on-axis field) is always kept. Non-zero X values create a skew field via
// Direction (the unnormalized X/Y azimuth).
func buildZemaxFields(result *ParseResult, hdr *zemaxHeader, objectZ float64) {
	x, y, weight, vig := hdr.fieldX, hdr.fieldY, hdr.weight, hdr.vig
	n := len(y)
	if len(x) > n {
		n = len(x)
	}
	if n == 0 {
		return
	}
	at := func(s []float64, i int) float64 {
		if i < len(s) {
			return s[i]
		}
		return 0
	}
	for n > 1 {
		w := at(weight, n-1)
		if w == 0 {
			w = 1
		}
		v := zemaxVignette{}
		if n-1 < len(vig) {
			v = vig[n-1]
		}
		if at(x, n-1) == 0 && at(y, n-1) == 0 && w == 1 && v.IsZero() {
			n--
			continue
		}
		break
	}

	for i := 0; i < n; i++ {
		vx, vy := at(x, i), at(y, i)
		mag := math.Hypot(vx, vy)
		var dir []float64
		if vx != 0 {
			dir = []float64{vx, vy}
		}
		var vigd *types.VignettingDef
		if i < len(vig) && !vig[i].IsZero() {
			vigd = &types.VignettingDef{
				DecenterX:    vig[i].vdx,
				DecenterY:    vig[i].vdy,
				CompressionX: vig[i].vcx,
				CompressionY: vig[i].vcy,
				Tangent:      vig[i].van,
			}
		}
		f := types.FieldItem{
			ID:         len(result.Fields),
			Weight:     at(weight, i),
			Direction:  dir,
			Vignetting: vigd,
		}
		if f.Weight == 0 {
			f.Weight = 1.0
		}
		switch result.FieldType {
		case 1:
			// Object height: finite-conjugate field at z = objectZ.
			f.Height = mag
			f.ObjectZ = objectZ
		case 2, 3:
			// Paraxial (2) / real (3) image height. RayWeaver resolves the
			// chief-ray angle that lands at this image height; the paraxial
			// vs real distinction is not modelled separately.
			f.ImageHeight = mag
		default: // 0 = angle, degrees
			f.AngleDeg = mag
		}
		result.Fields = append(result.Fields, f)
	}
}

// buildZemaxWavelengths appends the effective WAVM rows (trailing fill run
// removed) to the already-parsed WAVL/WWGT wavelengths.
func buildZemaxWavelengths(result *ParseResult, hdr *zemaxHeader) {
	n := effectiveWAVMCount(hdr.wavmRows)
	for i := 0; i < n; i++ {
		row := hdr.wavmRows[i]
		result.Wavelengths = append(result.Wavelengths, types.WavelengthItem{
			ID:     len(result.Wavelengths),
			Value:  row[0] / 1000.0,
			Weight: row[1],
		})
	}
}

// effectiveWAVMCount trims the ZMX wavelength-table padding. Old ZMX files
// always write 24 WAVM rows, filling unused slots with a constant placeholder
// value; the effective set is the leading rows before the trailing constant
// run. A single all-placeholder table collapses to one wavelength.
func effectiveWAVMCount(rows [][2]float64) int {
	if len(rows) <= 1 {
		return len(rows)
	}
	last := rows[len(rows)-1][0]
	run := 1
	for i := len(rows) - 2; i >= 0 && rows[i][0] == last; i-- {
		run++
	}
	if run == len(rows) {
		return 1
	}
	if run >= 2 {
		return len(rows) - run
	}
	return len(rows)
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
