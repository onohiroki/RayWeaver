package exporter

import (
	"fmt"
	"math"
	"strings"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

// zemaxEntry is one surface in the ZEMAX output sequence: a COORDBRK carrying
// a decenter step, or a real surface.
type zemaxEntry struct {
	coordBrk  bool
	tiltFirst bool
	step      types.DecenterStep
	surf      types.Surface
	znum      int
}

// WriteZemax renders a system as a ZEMAX ZMX text file. configs is the ordered
// list of config indices to write; the first is the base geometry. Extra
// configs are emitted as ZEMAX multi-config overrides (MNUM/CONFIG +
// THIC/SDIA). Fields, wavelengths and the stop are taken from the first
// config; per-config differences in those are reported through warn.
func WriteZemax(input *types.Input, configs []int, gc *glass.Catalog, warn Warn) ([]byte, error) {
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

	// Build the ZEMAX surface sequence: a COORDBRK surface precedes every
	// decentered surface. Each entry gets a sequential ZEMAX surface number
	// (0 = object).
	var entries []*zemaxEntry
	baseEntry := make([]*zemaxEntry, len(base))
	var stopEntry *zemaxEntry
	for i := range base {
		s := base[i]
		for _, cb := range decenterCoordBrks(&s, warn) {
			entries = append(entries, &zemaxEntry{coordBrk: true, tiltFirst: cb.tiltFirst, step: cb.step})
		}
		e := &zemaxEntry{surf: s}
		entries = append(entries, e)
		baseEntry[i] = e
		if i == stopIdx {
			stopEntry = e
		}
	}
	for i, e := range entries {
		e.znum = i + 1
	}

	ftyp := dominantFieldClass(baseCfg.Fields, warn)

	b.WriteString("VERS 0\n")
	name := strings.TrimSpace(configName(input, baseCfg))
	if name == "" {
		name = "RayWeaver export"
	}
	b.WriteString("NAME " + name + "\n")
	if input.Metadata != nil && strings.TrimSpace(input.Metadata.Notes) != "" {
		b.WriteString("NOTE 0 " + strings.TrimSpace(input.Metadata.Notes) + "\n")
	}
	b.WriteString("UNIT MM\n")
	b.WriteString(fmt.Sprintf("FTYP %d\n", zemaxFTYP(ftyp)))

	// Fields: XFLD/YFLD are slot-aligned per field; the interpretation is
	// given by FTYP (0 = angle deg, 1 = object height, 2 = image height).
	var xf, yf, fw []string
	for i := range baseCfg.Fields {
		f := &baseCfg.Fields[i]
		x, y := fieldXY(f)
		if !fieldNeutral(f) && classifyField(f) != ftyp {
			warnf(warn, "field %d type does not match the dominant type; exported under FTYP %d (%s)", i, zemaxFTYP(ftyp), ftyp)
		}
		xf = append(xf, num(x))
		yf = append(yf, num(y))
		fw = append(fw, num(f.Weight))
	}
	if len(yf) > 0 {
		b.WriteString("XFLD " + strings.Join(xf, " ") + "\n")
		b.WriteString("YFLD " + strings.Join(yf, " ") + "\n")
		b.WriteString("FWGT " + strings.Join(fw, " ") + "\n")
	}
	writeZemaxVignetting(&b, baseCfg.Fields)

	// Wavelengths in µm with weights; PWAV marks the primary (1-based).
	if err := writeZemaxWavelengths(&b, input, baseCfg); err != nil {
		return nil, err
	}

	// Object surface: infinite distance, or the first field's object distance
	// for finite-conjugate (object-height) fields.
	objDist := "INFINITY"
	if ftyp == fieldObjectHeight {
		if oz := objectDistance(baseCfg.Fields); !math.IsInf(oz, 1) {
			objDist = num(oz)
		}
	}
	b.WriteString("SURF 0\n")
	b.WriteString(" TYPE STANDARD\n")
	b.WriteString(" CURV 0.000000e+00 0 0 0\n")
	b.WriteString(" DISZ " + objDist + "\n")

	// Optical surfaces.
	for _, e := range entries {
		if e.coordBrk {
			writeZemaxCoordBrk(&b, e.znum, e.tiltFirst, e.step)
			continue
		}
		s := e.surf
		if s.Reflect {
			warnf(warn, "surface %d: folded mirror not unfolded; exported as a transmit surface", s.ID)
		}
		if s.Type == types.AsphereZernike {
			warnf(warn, "surface %d: Zernike asphere not representable in ZMX; exported as sphere + conic", s.ID)
		}
		writeZemaxSurface(&b, e.znum, s, e == stopEntry)
	}

	// Multi-config: base geometry is config 1; further configs override via
	// THIC (thickness) / SDIA (diameter) rows.
	if len(configs) > 1 {
		writeZemaxMultiConfig(&b, input, configs, base, baseEntry, warn)
	}

	return []byte(b.String()), nil
}

func configName(input *types.Input, cfg *types.Config) string {
	if cfg.Name != "" {
		return cfg.Name
	}
	if cfg.ID != "" {
		return cfg.ID
	}
	if input.Metadata != nil && input.Metadata.Notes != "" {
		return input.Metadata.Notes
	}
	return ""
}

// fieldXY returns the X/Y field values for a field under any FTYP: angle
// fields use the direction vector components (magnitude = hypot), image- and
// object-height fields use the height in Y.
func fieldXY(f *types.FieldItem) (x, y float64) {
	switch classifyField(f) {
	case fieldImageHeight:
		return 0, f.ImageHeight
	case fieldObjectHeight:
		return 0, f.Height
	default:
		if len(f.Direction) == 2 {
			return f.Direction[0], f.Direction[1]
		}
		return 0, f.AngleDeg
	}
}

// objectDistance returns the object distance (mm) for finite-conjugate fields:
// the object plane sits at z = object_z, so the distance before surface 1 is
// -object_z.
func objectDistance(fields []types.FieldItem) float64 {
	for _, f := range fields {
		if f.ObjectZ != 0 {
			return -f.ObjectZ
		}
	}
	return 0
}

func writeZemaxVignetting(b *strings.Builder, fields []types.FieldItem) {
	anyVig := false
	for _, f := range fields {
		if !f.Vignetting.IsZero() {
			anyVig = true
			break
		}
	}
	if !anyVig {
		return
	}
	var vdx, vdy, vcx, vcy, van []string
	for i := range fields {
		v := fields[i].Vignetting
		if v == nil {
			vdx, vdy, vcx, vcy, van = append(vdx, "0"), append(vdy, "0"), append(vcx, "0"), append(vcy, "0"), append(van, "0")
			continue
		}
		vdx = append(vdx, num(v.DecenterX))
		vdy = append(vdy, num(v.DecenterY))
		vcx = append(vcx, num(v.CompressionX))
		vcy = append(vcy, num(v.CompressionY))
		van = append(van, num(v.Tangent))
	}
	b.WriteString("VDXN " + strings.Join(vdx, " ") + "\n")
	b.WriteString("VDYN " + strings.Join(vdy, " ") + "\n")
	b.WriteString("VCXN " + strings.Join(vcx, " ") + "\n")
	b.WriteString("VCYN " + strings.Join(vcy, " ") + "\n")
	b.WriteString("VANN " + strings.Join(van, " ") + "\n")
}

func writeZemaxWavelengths(b *strings.Builder, input *types.Input, cfg *types.Config) error {
	wavelengths, primary, err := exportWavelengths(input, cfg)
	if err != nil {
		return err
	}
	var wls, wws []string
	for _, wl := range wavelengths {
		wls = append(wls, fmt.Sprintf("%.6e", wl.Value*1000))
		wws = append(wws, fmt.Sprintf("%.6e", wl.Weight))
	}
	fmt.Fprintf(b, "WAVL %s\n", strings.Join(wls, " "))
	fmt.Fprintf(b, "WWGT %s\n", strings.Join(wws, " "))
	fmt.Fprintf(b, "PWAV %d\n", primary)
	return nil
}

// writeZemaxSurface writes one ZEMAX surface block. isStop emits the STOP
// marker before the TYPE row.
func writeZemaxSurface(b *strings.Builder, znum int, s types.Surface, isStop bool) {
	fmt.Fprintf(b, "SURF %d\n", znum)
	if isStop {
		b.WriteString(" STOP\n")
	}
	if s.Type == types.AspherePolynomial {
		b.WriteString(" TYPE EVENASPH\n")
	} else {
		b.WriteString(" TYPE STANDARD\n")
	}
	fmt.Fprintf(b, " CURV %s 0 0 0\n", num(s.Curvature))
	if s.Conic != 0 {
		fmt.Fprintf(b, " CONI %s 0 0\n", num(s.Conic))
	}
	fmt.Fprintf(b, " DISZ %s\n", num(s.Thickness))
	g := glassOf(s.Material)
	if g.keyed {
		fmt.Fprintf(b, " GLAS %s 0 0 0 0 0 0 0 0\n", g.name)
	} else if g.nd > 0 {
		label := fmt.Sprintf("%s,%s", num(g.nd), num(g.vd))
		fmt.Fprintf(b, " GLAS %s 1 0 %s %s 0 0 0 0\n", label, num(g.nd), num(g.vd))
	}
	if s.Diameter > 0 {
		fmt.Fprintf(b, " DIAM %s 1 0\n", num(semiDiameter(s.Diameter)))
	}
	if s.Type == types.AspherePolynomial {
		for i, c := range s.Coefficients {
			if c != 0 {
				fmt.Fprintf(b, " PARM %d %s\n", i+2, num(c))
			}
		}
	}
}

// writeZemaxCoordBrk writes a COORDBRK surface carrying a decenter step
// (PARM 1..5 = decenter X/Y, tilt X/Y/Z; PARM 6 = tilt-before-decenter order).
func writeZemaxCoordBrk(b *strings.Builder, znum int, tiltFirst bool, step types.DecenterStep) {
	order := 0
	if tiltFirst {
		order = 1
	}
	fmt.Fprintf(b, "SURF %d\n", znum)
	b.WriteString(" TYPE COORDBRK\n")
	fmt.Fprintf(b, " PARM 1 %s\n", num(step.Shift.X))
	fmt.Fprintf(b, " PARM 2 %s\n", num(step.Shift.Y))
	fmt.Fprintf(b, " PARM 3 %s\n", num(step.Tilt.X))
	fmt.Fprintf(b, " PARM 4 %s\n", num(step.Tilt.Y))
	fmt.Fprintf(b, " PARM 5 %s\n", num(step.Tilt.Z))
	fmt.Fprintf(b, " PARM 6 %d\n", order)
	fmt.Fprintf(b, " DISZ 0\n")
}

// writeZemaxMultiConfig emits the MNUM/CONFIG declarations and the THIC/SDIA
// override rows for the configs after the base (ZEMAX config numbers 2..N; the
// base geometry is ZEMAX config 1, the surface blocks). SDIA carries the full
// diameter, matching the importer's reading.
func writeZemaxMultiConfig(b *strings.Builder, input *types.Input, configs []int, base []types.Surface, baseEntry []*zemaxEntry, warn Warn) {
	fmt.Fprintf(b, "MNUM %d\n", len(configs))
	for k := range configs {
		fmt.Fprintf(b, "CONFIG %d\n", k+1)
	}
	for k := 1; k < len(configs); k++ {
		cfg := &input.Configs[configs[k]]
		cs := cfg.Surfaces
		if len(cs) != len(base) {
			warnf(warn, "config %q surface count differs from the base; overrides limited to the common range", cfg.ID)
		}
		wroteRow := false
		for j := range base {
			if j >= len(cs) {
				break
			}
			if baseEntry[j] == nil || baseEntry[j].coordBrk {
				continue
			}
			znum := baseEntry[j].znum
			if cs[j].Thickness != base[j].Thickness {
				fmt.Fprintf(b, "THIC %d %d %s\n", znum, k+1, num(cs[j].Thickness))
				wroteRow = true
			}
			if cs[j].Diameter != base[j].Diameter {
				fmt.Fprintf(b, "SDIA %d %d %s\n", znum, k+1, num(cs[j].Diameter))
				wroteRow = true
			}
			if cs[j].Curvature != base[j].Curvature || cs[j].Conic != base[j].Conic ||
				!sameCoeffs(cs[j].Coefficients, base[j].Coefficients) {
				warnf(warn, "config %q: surface %d curvature/conic/asphere differs per config; not representable in ZEMAX THIC/SDIA", cfg.ID, base[j].ID)
			}
		}
		// A config whose surfaces all match the base would otherwise carry no
		// override rows and disappear on re-import; emit a redundant row for
		// the first surface so every config survives the round trip.
		if !wroteRow {
			for j := range base {
				if j >= len(cs) || baseEntry[j] == nil || baseEntry[j].coordBrk {
					continue
				}
				fmt.Fprintf(b, "THIC %d %d %s\n", baseEntry[j].znum, k+1, num(cs[j].Thickness))
				break
			}
		}
	}
}

// zemaxCoordBrk describes one representable ZEMAX coordinate break.
type zemaxCoordBrk struct {
	step      types.DecenterStep
	tiltFirst bool
}

// decenterCoordBrks reduces a surface's decenter steps to the ZEMAX COORDBRK
// representation, warning on anything lossy. The mirror fold step is dropped
// (folded mirrors are not unfolded on export).
func decenterCoordBrks(s *types.Surface, warn Warn) []zemaxCoordBrk {
	var steps []types.DecenterStep
	for _, d := range s.Decenter {
		if d == (types.DecenterStep{Tilt: types.Vec3{Y: 180}, Scope: types.ScopeBoth}) {
			continue // mirror fold — handled at the surface level
		}
		steps = append(steps, d)
	}
	if len(steps) == 0 {
		return nil
	}
	// A single step maps to one COORDBRK (decenter then tilt).
	if len(steps) == 1 {
		if steps[0].Scope.Bends() {
			warnf(warn, "surface %d: frame-bending decenter exported as ZEMAX COORDBRK (frame returns)", s.ID)
		}
		if steps[0].Shift.Z != 0 {
			warnf(warn, "surface %d: Z decenter not representable in ZEMAX COORDBRK; dropped", s.ID)
		}
		return []zemaxCoordBrk{{step: steps[0]}}
	}
	// Two steps in tilt-then-shift order (the importer's tilt-first COORDBRK
	// decomposition) collapse back into one COORDBRK with PARM 6 = 1.
	if len(steps) == 2 && steps[0].Shift == (types.Vec3{}) && steps[1].Tilt == (types.Vec3{}) {
		return []zemaxCoordBrk{{
			step:      types.DecenterStep{Shift: steps[1].Shift, Tilt: steps[0].Tilt},
			tiltFirst: true,
		}}
	}
	warnf(warn, "surface %d: %d decenter steps not representable in a single ZEMAX COORDBRK; exporting the first", s.ID, len(steps))
	return []zemaxCoordBrk{{step: steps[0]}}
}

func sameCoeffs(a, b []float64) bool {
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
