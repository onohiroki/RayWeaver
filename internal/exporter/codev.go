package exporter

import (
	"fmt"
	"strings"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

// WriteCodeV renders a system as a CODE V SEQ file (compact mode, millimetres,
// curvature entry). configs is the ordered list of config indices to write;
// the first is the base (zoom position 1). Extra configs are emitted as CODE V
// zoom positions: a "ZOOM n" header plus "ZOO <code> S<n>|<F<n>> <values per
// position>" rows for every parameter that differs across configs. Fields,
// wavelengths and the stop come from the first config.
//
// Glass labels follow the CODE V convention "NAME_MANUFACTURER" (uppercase,
// hyphens/underscores stripped from the glass name; the manufacturer comes
// from the glass catalog and is omitted when unknown). With inlineNDVD, every
// glass is written as its "nd:vd" model form instead.
func WriteCodeV(input *types.Input, configs []int, gc *glass.Catalog, warn Warn, inlineNDVD bool) ([]byte, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("no configs to export")
	}
	baseCfg := &input.Configs[configs[0]]
	base := baseCfg.Surfaces
	if len(base) == 0 {
		return nil, fmt.Errorf("config %q has no surfaces", baseCfg.ID)
	}

	var b strings.Builder
	stopIdx := resolveStop(baseCfg, input.Chief)
	ftyp := dominantFieldClass(baseCfg.Fields, warn)

	b.WriteString("RDM N;LEN \"RayWeaver export\"\n")
	if name := configName(input, baseCfg); name != "" {
		b.WriteString("TITLE '" + strings.ReplaceAll(name, "'", " ") + "'\n")
	}
	if len(configs) > 1 {
		fmt.Fprintf(&b, "ZOOM %d\n", len(configs))
	}
	writeCodeVWavelengths(&b, baseCfg)
	writeCodeVFields(&b, baseCfg.Fields, ftyp, warn)
	writeCodeVVignetting(&b, baseCfg.Fields)

	// Object surface: curvature 0 with a huge distance for infinite conjugate,
	// else the first field's object distance.
	objDist := "0.1E+15"
	if ftyp == fieldObjectHeight {
		oz := objectDistance(baseCfg.Fields)
		if oz > 0 {
			objDist = num(oz)
		} else {
			warnf(warn, "finite-conjugate object fields are not representable in compact CODE V; using infinite object distance")
		}
	}
	fmt.Fprintf(&b, "SO 0.0 %s\n", objDist)

	// Surface data: base geometry (zoom position 1).
	for i := range base {
		s := base[i]
		last := i == len(base)-1
		writeCodeVSurface(&b, &s, i+1, last, i == stopIdx, gc, inlineNDVD, warn)
	}

	// Zoom-position differences: one ZOO row per parameter that changes across
	// configs, values covering all positions (position 1 = base).
	if len(configs) > 1 {
		writeCodeVZoomRows(&b, input, configs, base, warn)
	}

	b.WriteString("GO\n")
	return []byte(b.String()), nil
}

func writeCodeVWavelengths(b *strings.Builder, cfg *types.Config) {
	if len(cfg.Wavelengths) == 0 {
		return
	}
	var wls, wws []string
	ref := 1
	for i, wl := range cfg.Wavelengths {
		wls = append(wls, num(wl.Value*1e6))
		wws = append(wws, num(wl.Weight))
		if wl.Primary {
			ref = i + 1
		}
	}
	codeVLine(b, "WL ", wls, 100)
	codeVLine(b, "WTW ", wws, 100)
	fmt.Fprintf(b, "REF %d\n", ref)
}

// writeCodeVFields writes the YAN/XAN or YIM + WTF field rows. The CODE V
// field spec is single-type (angles or image heights), so every field is
// written under one type: image height when any field is one, else angle. A
// field whose image height is zero (a "YIM 0" on-axis entry) classifies as an
// angle after the omitempty round trip, so it still lands in the image-height
// column with value 0 and is not dropped. Object-height (finite-conjugate)
// fields are skipped with a warning: compact CODE V field spec has no
// object-height type.
func writeCodeVFields(b *strings.Builder, fields []types.FieldItem, ftyp fieldClass, warn Warn) {
	if len(fields) == 0 {
		return
	}
	useYIM := false
	for i := range fields {
		if classifyField(&fields[i]) == fieldImageHeight {
			useYIM = true
			break
		}
	}
	if useYIM && ftyp == fieldAngle {
		warnf(warn, "mixed angle / image-height fields; exporting all as image height")
	}
	var vals, xan, wtf []string
	for i := range fields {
		f := &fields[i]
		if classifyField(f) == fieldObjectHeight {
			warnf(warn, "field %d: object-height (finite conjugate) not representable in CODE V; skipped", i)
			continue
		}
		if useYIM {
			if f.ImageHeight > 0 {
				vals = append(vals, num(f.ImageHeight))
			} else {
				// image-height 0 (or an angle in a mixed config): best-effort
				// value, kept so no field is dropped.
				vals = append(vals, num(f.AngleDeg))
			}
		} else {
			x, y := fieldXY(f)
			vals = append(vals, num(y))
			xan = append(xan, num(x))
		}
		wtf = append(wtf, num(f.Weight))
	}
	if useYIM {
		codeVLine(b, "YIM ", vals, 100)
	} else {
		codeVLine(b, "YAN ", vals, 100)
		codeVLine(b, "XAN ", xan, 100)
	}
	if len(wtf) > 0 {
		codeVLine(b, "WTF ", wtf, 100)
	}
}

// writeCodeVVignetting writes the base (position 1) per-field vignetting
// factors VUX/VLX/VUY/VLY, slot-aligned, converting from the ZEMAX-convention
// VignettingDef (decenter/compression fractions of the pupil radius):
//
//	VUY = compressionY - decenterY    VLY = compressionY + decenterY
//	VUX = compressionX - decenterX    VLX = compressionX + decenterX
func writeCodeVVignetting(b *strings.Builder, fields []types.FieldItem) {
	if !anyVignetting(fields) {
		return
	}
	var vux, vlx, vuy, vly []string
	for i := range fields {
		v := codeVVignette(fields[i].Vignetting)
		vux = append(vux, num(v.vux))
		vlx = append(vlx, num(v.vlx))
		vuy = append(vuy, num(v.vuy))
		vly = append(vly, num(v.vly))
	}
	codeVLine(b, "VUX ", vux, 100)
	codeVLine(b, "VLX ", vlx, 100)
	codeVLine(b, "VUY ", vuy, 100)
	codeVLine(b, "VLY ", vly, 100)
}

type codeVVig struct{ vux, vlx, vuy, vly float64 }

func codeVVignette(v *types.VignettingDef) codeVVig {
	if v == nil {
		return codeVVig{}
	}
	return codeVVig{
		vux: v.CompressionX - v.DecenterX,
		vlx: v.CompressionX + v.DecenterX,
		vuy: v.CompressionY - v.DecenterY,
		vly: v.CompressionY + v.DecenterY,
	}
}

func anyVignetting(fields []types.FieldItem) bool {
	for i := range fields {
		if !fields[i].Vignetting.IsZero() {
			return true
		}
	}
	return false
}

// writeCodeVSurface writes one compact-mode surface row plus its asphere,
// decenter, aperture and stop statements. The image plane is written as SI.
func writeCodeVSurface(b *strings.Builder, s *types.Surface, surfNum int, isImage, isStop bool, gc *glass.Catalog, inlineNDVD bool, warn Warn) {
	prefix := "S"
	if isImage {
		prefix = "SI"
	}
	line := fmt.Sprintf("%s %s %s", prefix, num(s.Curvature), num(s.Thickness))
	if label := codeVGlassLabel(gc, s.Material, inlineNDVD, warn); label != "" {
		line += " " + label
	}
	b.WriteString(line + "\n")

	if s.Reflect {
		warnf(warn, "surface %d: folded mirror not unfolded; exported as a transmit surface", s.ID)
	}
	if s.Type == types.AsphereZernike {
		warnf(warn, "surface %d: Zernike asphere not representable in CODE V; exported as sphere + conic", s.ID)
	}

	asphere := s.Type == types.AspherePolynomial
	if asphere && !hasAnyNonZero(s.Coefficients) && s.Conic == 0 {
		asphere = false
	}

	if asphere {
		b.WriteString("  ASP\n")
		fmt.Fprintf(b, "  K %s\n", num(s.Conic))
		b.WriteString("  CUF 0.0\n")
		writeCodeVAsphereCoeffs(b, s.Coefficients)
	} else if s.Conic != 0 {
		// A conic sphere: CON declares the conic surface, K its constant.
		fmt.Fprintf(b, "  CON\n  K %s\n", num(s.Conic))
	}

	steps := codeVDecenterSteps(s, warn)
	if len(steps) > 0 {
		b.WriteString("  DAR\n")
		for _, st := range steps {
			if st.Shift.Y != 0 {
				fmt.Fprintf(b, "  YDE %s\n", num(st.Shift.Y))
			}
			if st.Shift.X != 0 {
				fmt.Fprintf(b, "  XDE %s\n", num(st.Shift.X))
			}
			if st.Shift.Z != 0 {
				fmt.Fprintf(b, "  ZDE %s\n", num(st.Shift.Z))
			}
			if st.Tilt.X != 0 {
				fmt.Fprintf(b, "  ADE %s\n", num(st.Tilt.X))
			}
			if st.Tilt.Y != 0 {
				fmt.Fprintf(b, "  BDE %s\n", num(st.Tilt.Y))
			}
			if st.Tilt.Z != 0 {
				fmt.Fprintf(b, "  CDE %s\n", num(st.Tilt.Z))
			}
		}
	}

	if s.Diameter > 0 {
		fmt.Fprintf(b, "  CIR %s\n", num(semiDiameter(s.Diameter)))
	}
	if isStop {
		b.WriteString("  STO\n")
	}
}

func writeCodeVAsphereCoeffs(b *strings.Builder, coeffs []float64) {
	var parts []string
	for i, c := range coeffs {
		if c == 0 {
			continue
		}
		if letter := codeVLetter(2*i + 4); letter != "" {
			parts = append(parts, letter+" "+num(c))
		}
	}
	if len(parts) > 0 {
		codeVLine(b, "  ", parts, 120)
	}
}

// codeVDecenterSteps reduces a surface's decenter steps to the CODE V DAR
// representation (one block per surface), dropping the mirror fold step and
// warning on anything lossy.
func codeVDecenterSteps(s *types.Surface, warn Warn) []types.DecenterStep {
	var steps []types.DecenterStep
	for _, d := range s.Decenter {
		if d == (types.DecenterStep{Tilt: types.Vec3{Y: 180}, Scope: types.ScopeBoth}) {
			continue
		}
		steps = append(steps, d)
	}
	if len(steps) <= 1 {
		return steps
	}
	warnf(warn, "surface %d: %d decenter steps; CODE V DAR carries one block per surface, exporting the first", s.ID, len(steps))
	return steps[:1]
}

// codeVGlassLabel returns the CODE V glass label for a surface material:
//   - model glasses (no catalog key) use the inline "nd:vd" form;
//   - keyed glasses use the CODE V convention "NAME_MANUFACTURER" (uppercase,
//     hyphens/underscores stripped from the glass name; the manufacturer comes
//     from the glass catalog and is omitted when unknown);
//   - with inlineNDVD, keyed glasses resolve to the inline "nd:vd" form, which
//     is the --nd-vd CLI option; an unresolvable key falls back to the CODE V
//     name with a warning.
func codeVGlassLabel(gc *glass.Catalog, m types.Material, inlineNDVD bool, warn Warn) string {
	if m.HasModel() {
		return glassName(m, ":")
	}
	if !m.HasKey() {
		return ""
	}
	if inlineNDVD {
		if nd, vd, ok := ndvdOf(gc, m); ok {
			return fmt.Sprintf("%s:%s", num(nd), num(vd))
		}
		warnf(warn, "glass %q: nd/vd not resolvable; exporting the CODE V name", m.Key)
	}
	return codeVGlassName(gc, m.Key)
}

// ndvdOf resolves a material's nd/vd: inline for model glasses, through the
// catalog for keyed ones.
func ndvdOf(gc *glass.Catalog, m types.Material) (nd, vd float64, ok bool) {
	if m.HasModel() {
		if m.ND > 0 {
			return m.ND, m.VD, true
		}
		return 0, 0, false
	}
	if gc != nil {
		if g, found := gc.Lookup(m.Key); found {
			if nd, vd, ok := glass.NDVD(g); ok {
				return nd, vd, true
			}
		}
	}
	return 0, 0, false
}

// codeVGlassName converts a catalog glass key to the CODE V spelling:
// uppercase, hyphens/underscores removed from the glass name, with the
// manufacturer (from the catalog) appended after an underscore. When the
// manufacturer is unknown (no catalog entry or the entry carries none), an
// already-split "GLASS_MFR" key keeps its suffix so the round trip stays
// stable; otherwise the separators are stripped.
func codeVGlassName(gc *glass.Catalog, key string) string {
	name := key
	mfr := ""
	if gc != nil {
		if g, ok := gc.Lookup(key); ok {
			if g.Name != "" {
				name = g.Name
			}
			mfr = g.Manufacturer
		}
	}
	if mfr != "" {
		return glass.NormalizeName(name) + "_" + strings.ToUpper(mfr)
	}
	if i := strings.LastIndexByte(name, '_'); i > 0 {
		prefix, suffix := name[:i], name[i+1:]
		if suffix != "" && strings.ToUpper(suffix) == suffix {
			return glass.NormalizeName(prefix) + "_" + suffix
		}
	}
	return glass.NormalizeName(name)
}

// writeCodeVZoomRows emits the ZOO rows for the configs after the base: one
// row per surface parameter that differs across configs, and one per field
// vignetting factor. Values cover every zoom position (position 1 = base).
func writeCodeVZoomRows(b *strings.Builder, input *types.Input, configs []int, base []types.Surface, warn Warn) {
	baseCfg := &input.Configs[configs[0]]
	for k := 1; k < len(configs); k++ {
		if len(input.Configs[configs[k]].Surfaces) != len(base) {
			warnf(warn, "config %q surface count differs from the base; zoom overrides limited to the common range", input.Configs[configs[k]].ID)
		}
	}
	for j := range base {
		numS := j + 1
		// Per-config decenter/material/reflect differences cannot be expressed
		// in CODE V zoom rows; warn when they occur so the loss is visible.
		for k := 1; k < len(configs); k++ {
			cs := input.Configs[configs[k]].Surfaces
			if j < len(cs) {
				if !sameDecenters(cs[j].Decenter, base[j].Decenter) {
					warnf(warn, "config %q: surface %d decenter differs per config; CODE V zoom rows cannot express it, exported from the base", input.Configs[configs[k]].ID, base[j].ID)
				}
				if cs[j].Reflect != base[j].Reflect || cs[j].Material.String() != base[j].Material.String() {
					warnf(warn, "config %q: surface %d material/reflect differs per config; CODE V zoom rows cannot express it, exported from the base", input.Configs[configs[k]].ID, base[j].ID)
				}
			}
		}
		if t, ok := zoomThicknessValues(input, configs, j); ok {
			codeVLine(b, "ZOO THI S"+itoa(numS)+" ", t, 120)
		}
		if r, ok := zoomRadiusValues(input, configs, j); ok {
			codeVLine(b, "ZOO RDY S"+itoa(numS)+" ", r, 120)
		}
		if c, ok := zoomConicValues(input, configs, j); ok {
			codeVLine(b, "ZOO K S"+itoa(numS)+" ", c, 120)
		}
		if d, ok := zoomDiameterValues(input, configs, j); ok {
			codeVLine(b, "ZOO CIR S"+itoa(numS)+" ", d, 120)
		}
		for _, ord := range zoomAsphereOrders(input, configs, j) {
			if letter := codeVLetter(ord); letter != "" {
				if a, ok := zoomAsphereValues(input, configs, j, ord); ok {
					codeVLine(b, "ZOO "+letter+" S"+itoa(numS)+" ", a, 120)
				}
			}
		}
	}
	// Field vignetting differences.
	for fi := range baseCfg.Fields {
		if vu, vl, vx, vxx, ok := zoomVignetteValues(input, configs, fi); ok {
			codeVLine(b, "ZOO VUY F"+itoa(fi+1)+" ", vu, 120)
			codeVLine(b, "ZOO VLY F"+itoa(fi+1)+" ", vl, 120)
			codeVLine(b, "ZOO VUX F"+itoa(fi+1)+" ", vx, 120)
			codeVLine(b, "ZOO VLX F"+itoa(fi+1)+" ", vxx, 120)
		}
	}
}

func itoa(v int) string { return fmt.Sprintf("%d", v) }

// perConfigSurf returns the j-th surface of every config, as a slice indexed
// by the configs list.
func perConfigSurf(input *types.Input, configs []int, j int) []types.Surface {
	out := make([]types.Surface, len(configs))
	for k, ci := range configs {
		cs := input.Configs[ci].Surfaces
		if j < len(cs) {
			out[k] = cs[j]
		}
	}
	return out
}

// diffValues returns the per-config values for a scalar surface parameter,
// ok=false when all configs agree.
func diffValues(input *types.Input, configs []int, j int, get func(*types.Surface) float64) ([]string, bool) {
	surfs := perConfigSurf(input, configs, j)
	if len(surfs) == 0 {
		return nil, false
	}
	v0 := get(&surfs[0])
	diff := false
	for k := 1; k < len(surfs); k++ {
		if get(&surfs[k]) != v0 {
			diff = true
			break
		}
	}
	if !diff {
		return nil, false
	}
	out := make([]string, len(surfs))
	for k := range surfs {
		out[k] = num(get(&surfs[k]))
	}
	return out, true
}

func zoomThicknessValues(input *types.Input, configs []int, j int) ([]string, bool) {
	return diffValues(input, configs, j, func(s *types.Surface) float64 { return s.Thickness })
}

func zoomRadiusValues(input *types.Input, configs []int, j int) ([]string, bool) {
	return diffValues(input, configs, j, func(s *types.Surface) float64 {
		if s.Curvature == 0 {
			return 0
		}
		return 1.0 / s.Curvature
	})
}

func zoomConicValues(input *types.Input, configs []int, j int) ([]string, bool) {
	return diffValues(input, configs, j, func(s *types.Surface) float64 { return s.Conic })
}

func zoomDiameterValues(input *types.Input, configs []int, j int) ([]string, bool) {
	return diffValues(input, configs, j, func(s *types.Surface) float64 { return semiDiameter(s.Diameter) })
}

func zoomAsphereOrders(input *types.Input, configs []int, j int) []int {
	seen := map[int]bool{}
	var orders []int
	for _, s := range perConfigSurf(input, configs, j) {
		for _, o := range asphereOrders(&s) {
			if !seen[o] {
				seen[o] = true
				orders = append(orders, o)
			}
		}
	}
	return orders
}

func zoomAsphereValues(input *types.Input, configs []int, j, order int) ([]string, bool) {
	idx := order/2 - 2
	return diffValues(input, configs, j, func(s *types.Surface) float64 {
		if idx < len(s.Coefficients) {
			return s.Coefficients[idx]
		}
		return 0
	})
}

// zoomVignetteValues returns the per-config CODE V vignetting factors for one
// field, ok=false when all configs agree.
func zoomVignetteValues(input *types.Input, configs []int, fi int) (vuy, vly, vux, vlx []string, ok bool) {
	base := codeVVignette(nil)
	same := true
	vuy = make([]string, len(configs))
	vly = make([]string, len(configs))
	vux = make([]string, len(configs))
	vlx = make([]string, len(configs))
	for k, ci := range configs {
		cfg := &input.Configs[ci]
		var v codeVVig
		if fi < len(cfg.Fields) {
			v = codeVVignette(cfg.Fields[fi].Vignetting)
		}
		if k == 0 {
			base = v
		} else if v != base {
			same = false
		}
		vuy[k], vly[k], vux[k], vlx[k] = num(v.vuy), num(v.vly), num(v.vux), num(v.vlx)
	}
	if same {
		return nil, nil, nil, nil, false
	}
	return vuy, vly, vux, vlx, true
}

// codeVLine writes a wrapped line: prefix + space-joined parts, breaking at
// spaces with the CODE V '&' continuation when the line would exceed maxLen.
func codeVLine(b *strings.Builder, prefix string, parts []string, maxLen int) {
	if len(parts) == 0 {
		return
	}
	cur := prefix + strings.Join(parts, " ")
	if len(cur) <= maxLen {
		b.WriteString(cur + "\n")
		return
	}
	// Break at the last space before maxLen.
	idx := strings.LastIndex(cur[:maxLen], " ")
	if idx < len(prefix) {
		b.WriteString(cur + "\n")
		return
	}
	b.WriteString(cur[:idx] + " &\n " + cur[idx+1:] + "\n")
}

func hasAnyNonZero(v []float64) bool {
	for _, x := range v {
		if x != 0 {
			return true
		}
	}
	return false
}

// sameDecenters reports whether two decenter step lists are identical.
func sameDecenters(a, b []types.DecenterStep) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
