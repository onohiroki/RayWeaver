package glass

// DefaultHullVertices is the built-in full real-glass convex hull in (nd, vd)
// space, used as a fallback when the loaded catalogue is too small to form a
// meaningful hull (e.g. an input that declares only a few inline model glasses
// and no AGF directory). It spans the commercially realizable glass region so
// the hull constraint never silently disappears. When a richer catalogue is
// loaded, the hull is built from that catalogue instead (see
// NewConvexHull), which can be tighter but is always contained in — or very
// close to — this default region.
//
// The list is the convex hull of the reference glass map (nd 1.41268..2.154,
// vd 16.48..101.0). It is hand-maintained: the former generator
// (cmd/hullgen) was removed when the hull became catalogue-driven at runtime.
var DefaultHullVertices = []Point2D{
	{1.41268000, 100.700000},
	{1.45844000, 67.830000},
	{1.46450200, 65.767612},
	{1.51118000, 50.900000},
	{1.57583000, 35.200000},
	{1.62423000, 30.050000},
	{1.66382000, 27.346974},
	{1.86966000, 20.020000},
	{1.98612000, 16.480000},
	{2.10194900, 16.785097},
	{2.15400000, 17.200000},
	{2.10200000, 23.390000},
	{1.41390000, 101.000000},
}

// DefaultHullRange is the (nd, vd) bounding box of DefaultHullVertices.
const (
	DefaultHullNDMin = 1.41268000
	DefaultHullNDMax = 2.15400000
	DefaultHullVDMin = 16.48000000
	DefaultHullVDMax = 101.00000000
)

// newDefaultHullVertices returns DefaultHullVertices as internal ndvdPoints.
func newDefaultHullVertices() []ndvdPoint {
	out := make([]ndvdPoint, len(DefaultHullVertices))
	for i, p := range DefaultHullVertices {
		out[i] = ndvdPoint{nd: p.ND, vd: p.VD, label: "default"}
	}
	return out
}
