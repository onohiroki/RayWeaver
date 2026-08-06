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
