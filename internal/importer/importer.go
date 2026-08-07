package importer

import (
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

type ParseResult struct {
	Surfaces     []types.Surface
	Wavelengths  []types.WavelengthItem
	Fields       []types.FieldItem
	StopSurface  int
	ImageSurface int
	GlassEntries []types.Glass
	FNO          float64

	// EntrancePupilDiameter is the CODE V EPD (entrance pupil diameter) header
	// value, applied to the stop surface aperture when the file carries no
	// per-surface diameters.
	EntrancePupilDiameter float64

	// FieldType is the ZEMAX system field type (FTYP code) controlling how the
	// YFLN/XFLN values are interpreted: 0 = angle (deg), 1 = object height,
	// 2 = paraxial image height, 3 = real image height. Defaults to 0 (angle)
	// when no FTYP line is present.
	FieldType int

	// Multi-config support (ZEMAX): surfaces hold the base (config-0)
	// geometry; the per-config thickness/diameter overrides by config index
	// and surface ID are reported here. Non-nil when a lens declares at
	// least one config overrides. Fields/wavelengths are shared across
	// configs.
	ConfigIndexes   []int
	ConfigThickness map[int]map[int]float64 // [config][surfaceID]
	ConfigDiameter  map[int]map[int]float64 // [config][surfaceID]
}

// commonGlass is the built-in fallback glass dictionary. Entries added below
// were sourced from lens data obtained from https://www.lens-designs.com/.
var commonGlass = map[string]struct {
	ND float64
	VD float64
}{
	"BK7":     {1.5168, 64.17},
	"N-BK7":   {1.5168, 64.17},
	"F2":      {1.62004, 36.37},
	"SF12":    {1.64831, 33.84},
	"S-LAH66": {1.77250, 49.60},
	"SK18":    {1.63854, 55.42},
	"SF5":     {1.67270, 32.21},
	"SF11":    {1.78470, 25.76},
	"LAKN22":  {1.65113, 55.89},
	"K10":     {1.50137, 56.41},
	// Hoya glasses (H_ prefix used in patent shorthand).
	"H_LAF2":   {1.74320, 49.31},
	"H-LAK52":  {1.72916, 54.68},
	"H-LAK53":  {1.72151, 50.79},
	"H-ZF3":    {1.71736, 29.51},
	"H-F1":     {1.62588, 35.70},
	"H-ZLAF56": {1.77377, 47.25},
	// Optical plastics and moulding resins.
	"E48R":     {1.53016, 55.99},
	"480R":     {1.52500, 56.00},
	"COC":      {1.53000, 56.00},
	"POLYCARB": {1.58547, 30.16},
	"POLYSTYR": {1.59030, 30.90},
	// Fused silica / fluorides.
	"CAF2":     {1.43380, 95.30},
	"QUARTZ":   {1.45846, 67.82},
	"SUPRASIL": {1.45846, 67.82},
	"PYREX":    {1.47340, 67.50},
	// Common crown/flint alias spellings.
	"SKN18":  {1.63854, 55.42},
	"LAKN16": {1.73400, 51.49},
	// Optical plastics and resins.
	"PMMA":    {1.49180, 57.40},
	"ACRYLIC": {1.49180, 57.40},
	"OKP4":   {1.52500, 56.00},
	"OKP4HT": {1.52500, 56.00},
	"330R":   {1.50940, 56.20},
	"F52R":   {1.53530, 56.00},
	"K26R":   {1.53530, 56.00},
	"WATER":  {1.33300, 55.50},
	// Hoya legacy glasses (H- naming used in patents; discontinued from HOYA's
	// current catalog). Values from the 湖北新华光 (NHG) / CDGM / OHARA AGF
	// equivalents of the same glasses.
	"H-ZF72":    {1.92286, 18.90},
	"H-ZLAF70":  {1.90366, 31.32},
	"H-LAK51":   {1.69680, 55.44},
	"H-ZF4":     {1.72825, 28.32},
	"H-QK3":     {1.48749, 70.44},
	"H-ZLAF54":  {1.81600, 46.54},
	"H-ZLAF55":  {1.83480, 42.73},
	"H-ZLAF53":  {1.83400, 37.32},
	"H-LAK2":    {1.69099, 54.75},
	"H-ZLAF50B": {1.80400, 46.56},
	"H-ZLAF55F": {1.83480, 42.73},
	"H-LAF6L":   {1.75699, 47.70},
	"H-LAF50A":  {1.77250, 49.60},
	"H-LAF3":    {1.74400, 44.89},
	"H-FK70":    {1.56907, 71.30},
	"H-ZPK2":    {1.60300, 65.44},
	"H-ZLAF55A": {1.83480, 42.73},
	"H-LAK50":   {1.65160, 58.39},
	"H-ZF75":    {1.94595, 17.99},
	"H-ZLAF68":  {1.88299, 40.79},
	"H-ZLAF80":  {2.00069, 25.47},
}

func LookupGlass(name string) (nd, vd float64, ok bool) {
	g, ok := commonGlass[name]
	if ok {
		return g.ND, g.VD, true
	}
	// Accept the same glass under the alternative H- spelling ("H-LAK51" vs
	// "H_LAK51"), which lens data mixes.
	for _, alt := range []string{strings.ReplaceAll(name, "_", "-"), strings.ReplaceAll(name, "-", "_")} {
		if alt == name {
			continue
		}
		if g, ok := commonGlass[alt]; ok {
			return g.ND, g.VD, true
		}
	}
	return 0, 0, false
}

// resolveCommonGlass resolves a glass label against the built-in catalog,
// additionally falling back to the moulding-grade "_MOLD" suffix and to the
// parenthesised resin of compound names such as "AL-6263-(OKP4HT)".
func resolveCommonGlass(name string) (nd, vd float64, ok bool) {
	if nd, vd, ok := LookupGlass(name); ok {
		return nd, vd, true
	}
	if strings.HasSuffix(name, "_MOLD") {
		if nd, vd, ok := LookupGlass(name[:len(name)-len("_MOLD")]); ok {
			return nd, vd, true
		}
	}
	if i := strings.IndexByte(name, '('); i >= 0 && strings.HasSuffix(name, ")") {
		if nd, vd, ok := LookupGlass(name[i+1 : len(name)-1]); ok {
			return nd, vd, true
		}
	}
	return 0, 0, false
}

func isAir(name string) bool {
	return name == "" || name == "AIR" || name == "air" || name == "Air"
}
