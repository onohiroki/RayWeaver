package importer

import (
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

// codeVFieldVignette collects the CODE V field-spec vignetting rows
// (VUX/VLX/VUY/VLY), slot-aligned per field like YAN/WTF. Each row holds one
// value per field: the fractional reduction of the axial ray height at the
// entrance pupil for the +X (VUX), -X (VLX), +Y (VUY) and -Y (VLY) marginal
// rays. A zero value means no vignetting (the CODE V baseline). See
// VignettingDef for the ZEMAX-style pupil-ellipse interpretation.
type codeVFieldVignette struct {
	vux, vlx, vuy, vly []float64
}

func (v *codeVFieldVignette) add(keyword string, args []string) {
	vals := tokenFloats(args)
	switch keyword {
	case "VUX":
		v.vux = append(v.vux, vals...)
	case "VLX":
		v.vlx = append(v.vlx, vals...)
	case "VUY":
		v.vuy = append(v.vuy, vals...)
	case "VLY":
		v.vly = append(v.vly, vals...)
	}
}

func (v *codeVFieldVignette) empty() bool {
	return len(v.vux)+len(v.vlx)+len(v.vuy)+len(v.vly) == 0
}

// at converts the CODE V vignetting factors of field i (0-based slot) into the
// ZEMAX-convention VignettingDef. From the pupil bounds
//
//	upper = (1 - VUY)·R, lower = -(1 - VLY)·R
//
// the ZEMAX decenter/compression (fractions of the pupil radius R) follow as
//
//	decenterY = (VLY - VUY)/2, compressionY = (VUY + VLY)/2
//
// and likewise for X. Missing values (short rows) read as 0.
func (v *codeVFieldVignette) at(i int) types.VignettingDef {
	at := func(s []float64) float64 {
		if i < len(s) {
			return s[i]
		}
		return 0
	}
	vux, vlx, vuy, vly := at(v.vux), at(v.vlx), at(v.vuy), at(v.vly)
	return types.VignettingDef{
		DecenterX:    (vlx - vux) / 2,
		CompressionX: (vux + vlx) / 2,
		DecenterY:    (vly - vuy) / 2,
		CompressionY: (vuy + vly) / 2,
	}
}

// applyCodeVFieldVignetting sets the per-field VignettingDef for the base
// (position-1) fields from the CODE V field-spec vignetting rows. Fields whose
// four factors are all zero keep no vignetting.
func applyCodeVFieldVignetting(result *ParseResult, v *codeVFieldVignette) {
	if v == nil || v.empty() {
		return
	}
	for i := range result.Fields {
		if i >= len(v.vux) && i >= len(v.vlx) && i >= len(v.vuy) && i >= len(v.vly) {
			break
		}
		vv := v.at(i)
		if vv.IsZero() {
			continue
		}
		result.Fields[i].Vignetting = &vv
	}
}

// codeVZoomOverlay collects the CODE V zoom declarations: the "ZOOM n"
// position count and the "ZOO <code> S<n>|F<n> <values per position>" rows.
// Each ZOO row carries one value per zoom position; position 1 is the base
// (surface data), so positions 2..n become separate configs.
type codeVZoomOverlay struct {
	count  int
	thick  map[int][]float64            // [surf] per-position thickness
	radius map[int][]float64            // [surf] per-position radius of curvature (RDY/RD)
	curv   map[int][]float64            // [surf] per-position curvature (CV)
	conic  map[int][]float64            // [surf] per-position conic constant (K)
	diam   map[int][]float64            // [surf] per-position clear-aperture semi-diameter (CIR)
	asp    map[int]map[int][]float64    // [surf][order] per-position asphere coefficient (A..J)
	vig    map[string]map[int][]float64 // [VUY|VLY|VUX|VLX][field(1-based)] per-position
}

// addRow parses one "ZOO <code> S<n>|F<n> <values...>" line. Surface-qualified
// rows vary surface data; field-qualified (F<n>) rows vary the per-field
// vignetting factors.
func (z *codeVZoomOverlay) addRow(tokens []string) {
	if len(tokens) < 3 {
		return
	}
	code := strings.ToUpper(tokens[1])
	qual := tokens[2]
	nums := tokenFloats(tokens[3:])
	if len(nums) == 0 {
		return
	}
	prefix, num := splitQualifier(qual)
	switch prefix {
	case 'S':
		switch code {
		case "THI", "TH":
			z.set(&z.thick, num, nums)
		case "RDY", "RD", "RDM":
			z.set(&z.radius, num, nums)
		case "CV":
			z.set(&z.curv, num, nums)
		case "K":
			z.set(&z.conic, num, nums)
		case "CIR", "DIA":
			z.set(&z.diam, num, nums)
		default:
			if isAsphereLetter(code) {
				if z.asp[num] == nil {
					z.asp[num] = map[int][]float64{}
				}
				z.asp[num][asphereOrder(code)] = nums
			}
		}
	case 'F':
		switch code {
		case "VUY", "VLY", "VUX", "VLX":
			if z.vig == nil {
				z.vig = map[string]map[int][]float64{}
			}
			if z.vig[code] == nil {
				z.vig[code] = map[int][]float64{}
			}
			z.vig[code][num] = nums
		}
	}
}

func (z *codeVZoomOverlay) set(m *map[int][]float64, n int, vals []float64) {
	if *m == nil {
		*m = map[int][]float64{}
	}
	(*m)[n] = vals
}

// countFromRows infers the position count from the longest ZOO value row when
// no "ZOOM n" declaration was present.
func (z *codeVZoomOverlay) countFromRows() int {
	n := 0
	max := func(vals []float64) {
		if len(vals) > n {
			n = len(vals)
		}
	}
	for _, v := range z.thick {
		max(v)
	}
	for _, v := range z.radius {
		max(v)
	}
	for _, v := range z.curv {
		max(v)
	}
	for _, v := range z.conic {
		max(v)
	}
	for _, v := range z.diam {
		max(v)
	}
	for _, m := range z.asp {
		for _, v := range m {
			max(v)
		}
	}
	for _, m := range z.vig {
		for _, v := range m {
			max(v)
		}
	}
	return n
}

// buildCodeVZoomConfigs constructs the per-position config surfaces and
// per-config field vignetting from the collected zoom overlays. Config index c
// = zoom position c (positions 2..n are the extra configs; position 1 is the
// base, config 0, whose surfaces stay the shared base geometry). Surfaces are
// built only for positions whose overlays change something.
func buildCodeVZoomConfigs(result *ParseResult, z *codeVZoomOverlay) {
	if z == nil {
		return
	}
	if z.count == 0 {
		z.count = z.countFromRows()
	}
	if z.count <= 1 {
		return
	}
	for pos := 2; pos <= z.count; pos++ {
		surfs := cloneSurfaces(result.Surfaces)
		changed := false
		for i := range surfs {
			id := surfs[i].ID
			if v, ok := zoomVal(z.thick, id, pos); ok {
				surfs[i].Thickness = v
				changed = true
			}
			if v, ok := zoomVal(z.radius, id, pos); ok {
				surfs[i].Curvature = radiusToCurvature(v)
				changed = true
			}
			if v, ok := zoomVal(z.curv, id, pos); ok {
				surfs[i].Curvature = v
				changed = true
			}
			if v, ok := zoomVal(z.conic, id, pos); ok {
				surfs[i].Conic = v
				changed = true
			}
			if v, ok := zoomVal(z.diam, id, pos); ok {
				surfs[i].Diameter = v * 2
				changed = true
			}
			if orders, ok := z.asp[id]; ok {
				hasCoeff := false
				for order, vals := range orders {
					v, ok := sliceVal(vals, pos)
					if !ok {
						continue
					}
					idx := order/2 - 2
					for len(surfs[i].Coefficients) <= idx {
						surfs[i].Coefficients = append(surfs[i].Coefficients, 0)
					}
					surfs[i].Coefficients[idx] = v
					hasCoeff = true
				}
				if hasCoeff {
					surfs[i].Type = types.AspherePolynomial
					changed = true
				}
			}
		}
		// Negative dummy-spacing zoom positions (the CODE V dummy-plane idiom)
		// are normalised the same way the base surfaces are, so a per-position
		// negative thickness never survives into the downstream pipeline.
		normalizeNegativeDummy(surfs)
		if changed {
			if result.ConfigSurfaces == nil {
				result.ConfigSurfaces = map[int][]types.Surface{}
			}
			result.ConfigSurfaces[pos] = surfs
		}
		if fv := buildZoomFieldVignetting(z, pos, result); len(fv) > 0 {
			if result.ConfigFieldVignetting == nil {
				result.ConfigFieldVignetting = map[int]map[int]types.VignettingDef{}
			}
			result.ConfigFieldVignetting[pos] = fv
		}
	}
}

// zoomVal returns the value of a ZOO parameter row for surface id at zoom
// position pos (1-based).
func zoomVal(m map[int][]float64, id, pos int) (float64, bool) {
	vals, ok := m[id]
	if !ok {
		return 0, false
	}
	return sliceVal(vals, pos)
}

// sliceVal returns the value of a per-position value row at position pos
// (1-based).
func sliceVal(vals []float64, pos int) (float64, bool) {
	if pos < 1 || pos > len(vals) {
		return 0, false
	}
	return vals[pos-1], true
}

// buildZoomFieldVignetting converts the per-field ZOO vignetting rows of one
// zoom position into per-field VignettingDefs (keyed by field index).
func buildZoomFieldVignetting(z *codeVZoomOverlay, pos int, result *ParseResult) map[int]types.VignettingDef {
	if len(z.vig) == 0 {
		return nil
	}
	out := map[int]types.VignettingDef{}
	for fi := range result.Fields {
		f := fi + 1
		vuy, okUY := zoomVal(z.vig["VUY"], f, pos)
		vly, okLY := zoomVal(z.vig["VLY"], f, pos)
		vux, okUX := zoomVal(z.vig["VUX"], f, pos)
		vlx, okLX := zoomVal(z.vig["VLX"], f, pos)
		if !okUY && !okLY && !okUX && !okLX {
			continue
		}
		v := types.VignettingDef{
			DecenterX:    (vlx - vux) / 2,
			CompressionX: (vux + vlx) / 2,
			DecenterY:    (vly - vuy) / 2,
			CompressionY: (vuy + vly) / 2,
		}
		if !v.IsZero() {
			out[fi] = v
		}
	}
	return out
}

// normalizeNegativeDummy converts the negative-spacing dummy-surface idiom
// (a zero-power reference plane with a negative thickness) into rayweave's
// all-positive model for one surface list: the plane keeps its global vertex
// through a scope-surface decenter shift and its thickness becomes 0. Mirrors
// and powered surfaces with negative spacings are left untouched (they encode
// genuine return paths handled by the fold model).
func normalizeNegativeDummy(surfaces []types.Surface) {
	for i := range surfaces {
		s := &surfaces[i]
		if s.Thickness < 0 && isDummySurface(s) {
			s.Decenter = append(s.Decenter, types.DecenterStep{
				Shift: types.Vec3{Z: s.Thickness},
			})
			s.Thickness = 0
		}
	}
}

// splitQualifier splits a CODE V index qualifier like "S4" or "F1" into its
// letter and number. A bare number is treated as a surface qualifier.
func splitQualifier(s string) (byte, int) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, -1
	}
	prefix := trimmed[0]
	num := trimmed
	if prefix == 'S' || prefix == 's' || prefix == 'F' || prefix == 'f' {
		num = trimmed[1:]
	} else {
		prefix = 'S'
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 0 {
		return prefix, -1
	}
	return prefix, n
}

// tokenFloats converts string tokens to floats (parseFloat semantics).
func tokenFloats(args []string) []float64 {
	out := make([]float64, 0, len(args))
	for _, a := range args {
		out = append(out, parseFloat(a))
	}
	return out
}

func cloneSurfaces(in []types.Surface) []types.Surface {
	out := make([]types.Surface, len(in))
	copy(out, in)
	return out
}
