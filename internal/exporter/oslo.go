package exporter

import (
	"fmt"
	"strings"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

// WriteOslo renders a single config of a system as an OSLO LEN (NXT) file.
// OSLO LEN carries one configuration; configIdx selects which. OSLO cannot
// represent conics, aspheres, decenters or multi-config zoom positions — those
// are reported through warn and exported as their closest spherical form.
func WriteOslo(input *types.Input, configIdx int, gc *glass.Catalog, warn Warn) ([]byte, error) {
	if configIdx < 0 || configIdx >= len(input.Configs) {
		return nil, fmt.Errorf("config index %d out of range", configIdx)
	}
	cfg := &input.Configs[configIdx]
	base := cfg.Surfaces
	if len(base) == 0 {
		return nil, fmt.Errorf("config %q has no surfaces", cfg.ID)
	}

	var b strings.Builder
	title := strings.ReplaceAll(configName(input, cfg), "\"", " ")
	if title == "" {
		title = "RayWeaver export"
	}

	b.WriteString("// OSLO 5.00  RayWeaver export\n")
	fmt.Fprintf(&b, "LEN NEW \"%s\" 1.0 %d\n", title, len(base)+1)
	fmt.Fprintf(&b, "UNI  1.0\n")
	fmt.Fprintf(&b, "SNO1 \"%s\"\n", title)

	// Object block: object height / distance for finite-conjugate fields, or a
	// far-distant plane for infinite conjugate.
	objDist := 1.0e10
	objHeight := -1.0e9
	fields := cfg.Fields
	if ftyp := dominantFieldClass(fields, warn); ftyp == fieldObjectHeight {
		if h := objectHeight(fields); h > 0 {
			objHeight = h
		}
		if d := objectDistance(fields); d > 0 {
			objDist = d
		}
	}
	fmt.Fprintf(&b, "OBH  %s\n", num(objHeight))
	b.WriteString("AIR\n")
	fmt.Fprintf(&b, "TH   %s\n", num(objDist))
	fmt.Fprintf(&b, "AP   %s\n", num(objDist/2))

	stopIdx := resolveStop(cfg, input.Chief)
	for i := range base {
		s := base[i]
		b.WriteString("NXT\n")
		writeOsloBlock(&b, &s, i == stopIdx, gc, warn)
	}

	// Fields (angle only) and wavelengths.
	for i := range fields {
		f := &fields[i]
		switch classifyField(f) {
		case fieldAngle:
			fmt.Fprintf(&b, "F %s %s\n", num(f.AngleDeg), num(f.Weight))
		default:
			warnf(warn, "field %d: only angle fields are representable in OSLO LEN; skipped", i)
		}
	}
	writeOsloWavelengths(&b, cfg)

	fmt.Fprintf(&b, "END  %d\n", len(base)+1)
	return []byte(b.String()), nil
}

// writeOsloBlock writes one surface block: the medium (AIR or GLA), the radius
// (OSLO stores radii), the thickness and the aperture radius.
func writeOsloBlock(b *strings.Builder, s *types.Surface, isStop bool, gc *glass.Catalog, warn Warn) {
	if s.Reflect {
		warnf(warn, "surface %d: folded mirror not unfolded; exported as a transmit surface", s.ID)
	}
	if s.Conic != 0 || s.Type == types.AspherePolynomial || s.Type == types.AsphereZernike {
		warnf(warn, "surface %d: conic/asphere not representable in OSLO LEN; exported as a sphere", s.ID)
	}
	if len(s.Decenter) > 0 {
		warnf(warn, "surface %d: decenter not representable in OSLO LEN; skipped", s.ID)
	}

	g := glassOf(s.Material)
	switch {
	case g.keyed:
		fmt.Fprintf(b, "GLA %s\n", g.name)
	case g.nd > 0:
		nd, nF, nC := glassIndexes(gc, s.Material)
		fmt.Fprintf(b, "GLA %s %s %s %s\n", glassName(s.Material, ":"), num(nd), num(nF), num(nC))
	default:
		b.WriteString("AIR\n")
	}
	if s.Curvature != 0 {
		fmt.Fprintf(b, "RD   %s\n", num(1.0/s.Curvature))
	} else {
		b.WriteString("RD   0.0\n")
	}
	fmt.Fprintf(b, "TH   %s\n", num(s.Thickness))
	if s.Diameter > 0 {
		fmt.Fprintf(b, "AP   %s\n", num(semiDiameter(s.Diameter)))
	}
	if isStop {
		b.WriteString("AST\n")
	}
}

func writeOsloWavelengths(b *strings.Builder, cfg *types.Config) {
	if len(cfg.Wavelengths) == 0 {
		return
	}
	var wls, wws []string
	for _, wl := range cfg.Wavelengths {
		wls = append(wls, num(wl.Value*1000))
		wws = append(wws, num(wl.Weight))
	}
	codeVLine(b, "WV ", wls, 100)
	codeVLine(b, "WW ", wws, 100)
}

func objectHeight(fields []types.FieldItem) float64 {
	for _, f := range fields {
		if f.Height > 0 {
			return f.Height
		}
	}
	return 0
}
