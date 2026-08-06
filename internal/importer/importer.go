package importer

import "github.com/hiroki/rayweaver/internal/types"

type ParseResult struct {
	Surfaces     []types.Surface
	Wavelengths  []types.WavelengthItem
	Fields       []types.FieldItem
	StopSurface  int
	ImageSurface int
	GlassEntries []types.Glass
	FNO          float64

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
}

func LookupGlass(name string) (nd, vd float64, ok bool) {
	g, ok := commonGlass[name]
	if ok {
		return g.ND, g.VD, true
	}
	return 0, 0, false
}

func isAir(name string) bool {
	return name == "" || name == "AIR" || name == "air" || name == "Air"
}
