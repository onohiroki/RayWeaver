package glass

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// HullReference holds catalogue metadata for a hull reference point.
type HullReference struct {
	Name string
	Kind string
	ND   float64
	VD   float64
}

// HullPoint is an external-facing hull vertex with catalogue metadata.
type HullPoint struct {
	ND          float64
	VD          float64
	Label       string
	VertexIndex int
}

// DefaultGlassKindOrder is the preferred glass-kind priority for hull building.
var DefaultGlassKindOrder = []int{
	int(kindSchott),
	int(kindOhara),
	int(kindHoya),
	int(kindCDGM),
}

// ConvexHull restricts nd/vd glass variables to a convex combination of
// real-catalog glass points. The boundary penalty is a hard 1e6 when outside
// the hull and a smooth polynomial that approaches zero near the boundary
// from within. Near the exact real-catalog vertices (within 1e-3 nd/vd
// distance), the penalty vanishes so that snapped values are stable.
type ConvexHull struct {
	points     []ndvdPoint
	hull       []ndvdPoint // 2-D convex hull in CCW order
	halfSpaces []hullHalfSpace
	baryCache  *baryCache
	ndMin      float64
	ndMax      float64
	vdMin      float64
	vdMax      float64
	margin     float64 // nd/vd margin for penalty onset (default 1.0)
	enabled    bool
	weight     float64
	references []HullReference // grouped catalogue metadata for diagnostics
}

// hullHalfSpace is one facet of the hull in half-space form: a point p is
// inside when n·p <= d for every facet. This is the "C/d" representation used
// for the O(n_facets) containment test and for anchoring an outside point back
// onto the boundary (projection onto the violated facet).
type hullHalfSpace struct {
	nx, ny float64 // outward unit normal
	d      float64 // plane offset (n·v for a vertex v on the facet)
	// ax, ay hold one vertex of the facet so a projection can return a point.
	ax, ay float64
	bx, by float64 // second facet vertex (for segment clamping)
}

// hullBoundaryOffset controls how far inside the hull the penalty onset
// begins, expressed as a fraction of the hull radius from each facet.
const hullBoundaryOffset = 0.05

// NewConvexHull builds a hull from a glass catalogue restricted to the
// specified glass kinds. k is the index into DefaultGlassKindOrder (1-based)
// that selects the kinds; 0 means all kinds. Negative kinds select by
// absolute value with kinds in reverse order.
func NewConvexHull(cat *Catalog, kinds []int) *ConvexHull {
	p, refs := catalogPoints(cat, kinds)
	return buildConvexHull(p, refs)
}

// catalogPoints collects the (nd, vd) points and diagnostic references of a
// catalogue restricted to the given glass kinds.
func catalogPoints(cat *Catalog, kinds []int) ([]ndvdPoint, []HullReference) {
	p := make([]ndvdPoint, 0, len(cat.ByName))
	refs := make([]HullReference, 0)
	seen := make(map[string]bool)
	for key, g := range cat.ByName {
		if g == nil {
			continue
		}
		nd, vd, ok := NDVD(g)
		if !ok || nd <= 0 || vd <= 0 {
			continue
		}
		k := glassKind(key)
		if !matchKind(k, kinds) {
			continue
		}
		display := g.Name
		if display == "" {
			display = g.Label
		}
		if display == "" {
			display = key
		}
		p = append(p, ndvdPoint{nd: nd, vd: vd, label: display})
		if !seen[display] {
			seen[display] = true
			refs = append(refs, HullReference{
				Name: display,
				Kind: k.String(),
				ND:   nd,
				VD:   vd,
			})
		}
	}
	return p, refs
}

// buildConvexHull constructs a ConvexHull from the given points (unsorted, may
// contain duplicates). It always returns a non-nil hull; when fewer than 3
// distinct non-collinear points remain the hull is degenerate (len(h.hull) < 3)
// and the caller decides whether to fall back to the built-in real-glass hull.
func buildConvexHull(p []ndvdPoint, refs []HullReference) *ConvexHull {
	if len(p) < 3 {
		return &ConvexHull{
			points:     p,
			enabled:    len(p) > 0,
			ndMin:      math.Inf(-1),
			ndMax:      math.Inf(1),
			vdMin:      math.Inf(-1),
			vdMax:      math.Inf(1),
			margin:     1.0,
			weight:     1.0,
			references: refs,
		}
	}
	sort.Slice(p, func(i, j int) bool {
		if p[i].nd < p[j].nd {
			return true
		}
		if p[i].nd > p[j].nd {
			return false
		}
		return p[i].vd < p[j].vd
	})
	deduped := p[:1]
	for i := 1; i < len(p); i++ {
		if p[i].nd == deduped[len(deduped)-1].nd && p[i].vd == deduped[len(deduped)-1].vd {
			continue
		}
		deduped = append(deduped, p[i])
	}
	p = deduped

	if len(p) < 3 {
		return &ConvexHull{
			points:     p,
			enabled:    true,
			ndMin:      p[0].nd,
			ndMax:      p[len(p)-1].nd,
			vdMin:      math.Inf(-1),
			vdMax:      math.Inf(1),
			margin:     1.0,
			weight:     1.0,
			references: refs,
		}
	}

	hull := convexHull2D(p)
	cache := buildBaryCache(hull)
	halfSpaces := buildHalfSpaces(hull)

	ndMin, ndMax := p[0].nd, p[0].nd
	vdMin, vdMax := p[0].vd, p[0].vd
	for _, pt := range p[1:] {
		if pt.nd < ndMin {
			ndMin = pt.nd
		}
		if pt.nd > ndMax {
			ndMax = pt.nd
		}
		if pt.vd < vdMin {
			vdMin = pt.vd
		}
		if pt.vd > vdMax {
			vdMax = pt.vd
		}
	}
	return &ConvexHull{
		points:     p,
		hull:       hull,
		halfSpaces: halfSpaces,
		baryCache:  cache,
		ndMin:      ndMin,
		ndMax:      ndMax,
		vdMin:      vdMin,
		vdMax:      vdMax,
		margin:     1.0,
		enabled:    true,
		weight:     1.0,
		references: refs,
	}
}

// NewConvexHullFromPoints builds a hull directly from an explicit list of
// (nd, vd) points (the "explicit" glass_hull source). Points with nd <= 0 or
// vd <= 0 are dropped. The result is nil when fewer than 3 distinct
// non-collinear points remain, so the caller can fall back to the built-in
// real-glass hull.
func NewConvexHullFromPoints(pts []Point2D) *ConvexHull {
	p := make([]ndvdPoint, 0, len(pts))
	refs := make([]HullReference, 0, len(pts))
	for i, pt := range pts {
		if pt.ND <= 0 || pt.VD <= 0 {
			continue
		}
		label := fmt.Sprintf("EXPLICIT%d", i)
		p = append(p, ndvdPoint{nd: pt.ND, vd: pt.VD, label: label})
		refs = append(refs, HullReference{Name: label, Kind: "EXPLICIT", ND: pt.ND, VD: pt.VD})
	}
	h := buildConvexHull(p, refs)
	if len(h.hull) < 3 {
		return nil
	}
	return h
}

// NewUnionConvexHull builds the union of the built-in full real-glass hull and
// the loaded catalogue's glasses (the "union" glass_hull source, the default).
// The constraint is never tighter than the built-in real-glass region, while a
// catalogue glass lying outside it (an unusual inline entry or AGF glass loaded
// via --glass-dir) is admitted too.
func NewUnionConvexHull(cat *Catalog) *ConvexHull {
	pts := newDefaultHullVertices()
	refs := make([]HullReference, 0, len(pts))
	for _, p := range pts {
		refs = append(refs, HullReference{Name: p.label, Kind: "DEFAULT", ND: p.nd, VD: p.vd})
	}
	if cat != nil {
		for key, g := range cat.ByName {
			if g == nil {
				continue
			}
			nd, vd, ok := NDVD(g)
			if !ok || nd <= 0 || vd <= 0 {
				continue
			}
			display := g.Name
			if display == "" {
				display = g.Label
			}
			if display == "" {
				display = key
			}
			pts = append(pts, ndvdPoint{nd: nd, vd: vd, label: display})
			refs = append(refs, HullReference{Name: display, Kind: "CATALOG", ND: nd, VD: vd})
		}
	}
	return buildConvexHull(pts, refs)
}

// NewDefaultConvexHull builds a hull from the loaded catalogue using the
// catalogue's manufacturer kind order. It backs the "catalog" glass_hull
// source. When the catalogue is too small to form a real hull (fewer than 3
// vertices — e.g. an input that declares only a couple of inline model glasses
// and no AGF directory), it falls back to the built-in full real-glass hull
// (DefaultHullVertices) so the constraint never silently disappears.
func NewDefaultConvexHull(cat *Catalog) *ConvexHull {
	if cat == nil {
		return newDefaultConvexHull()
	}
	kinds := GlassKindOrderForCatalog(cat)
	if len(kinds) == 0 {
		kinds = DefaultGlassKindOrder[:]
	}
	h := NewConvexHull(cat, kinds)
	if h == nil || len(h.hull) < 3 {
		return newDefaultConvexHull()
	}
	return h
}

// NewBuiltinConvexHull returns the built-in full real-glass hull
// (DefaultHullVertices). It is the "builtin" glass_hull source and the fallback
// target for "catalog"/"explicit" when their point set is too small.
func NewBuiltinConvexHull() *ConvexHull {
	return newDefaultConvexHull()
}

// newDefaultConvexHull builds the hull from the built-in DefaultHullVertices.
func newDefaultConvexHull() *ConvexHull {
	pts := newDefaultHullVertices()
	hull := convexHull2D(pts)
	halfSpaces := buildHalfSpaces(hull)
	ndMin, ndMax := pts[0].nd, pts[0].nd
	vdMin, vdMax := pts[0].vd, pts[0].vd
	for _, p := range pts[1:] {
		if p.nd < ndMin {
			ndMin = p.nd
		}
		if p.nd > ndMax {
			ndMax = p.nd
		}
		if p.vd < vdMin {
			vdMin = p.vd
		}
		if p.vd > vdMax {
			vdMax = p.vd
		}
	}
	refs := make([]HullReference, len(hull))
	for i, p := range hull {
		refs[i] = HullReference{Name: p.label, Kind: "DEFAULT", ND: p.nd, VD: p.vd}
	}
	return &ConvexHull{
		points:     pts,
		hull:       hull,
		halfSpaces: halfSpaces,
		baryCache:  buildBaryCache(hull),
		ndMin:      ndMin,
		ndMax:      ndMax,
		vdMin:      vdMin,
		vdMax:      vdMax,
		margin:     1.0,
		enabled:    true,
		weight:     1.0,
		references: refs,
	}
}

// ndvdPoint is an internal point used for hull computation only.
// External code uses HullPoint with MaterialIndex.
type ndvdPoint struct {
	nd, vd float64
	label  string
}

// BarycentricCoordinates stores pre-computed barycentric coordinates for a
// point (with its signed distance to the hull boundary).
type barycentricCoordinates struct {
	coords     []float64 // barycentric weights relative to hull vertices
	dist       float64   // signed distance; negative inside the hull
	facetIndex int       // index of nearest facet
}

// baryCache caches barycentric coordinates for all hull points.
type baryCache struct {
	cache map[uint64]barycentricCoordinates
}

func makePointKey(nd, vd float64) uint64 {
	n := math.Float64bits(nd)
	v := math.Float64bits(vd)
	return n | (v << 32)
}

func (c *baryCache) get(nd, vd float64) (barycentricCoordinates, bool) {
	v, ok := c.cache[makePointKey(nd, vd)]
	return v, ok
}

func (c *baryCache) put(nd, vd float64, bary barycentricCoordinates) {
	c.cache[makePointKey(nd, vd)] = bary
}

func buildBaryCache(hull []ndvdPoint) *baryCache {
	cache := &baryCache{cache: make(map[uint64]barycentricCoordinates)}
	if len(hull) < 3 {
		return cache
	}
	n := len(hull)
	for i := 0; i < n; i++ {
		p := hull[i]
		coords, dist := barycentricCoords(hull, p.nd, p.vd)
		cache.put(p.nd, p.vd, barycentricCoordinates{
			coords:     coords,
			dist:       dist,
			facetIndex: 0,
		})
	}
	return cache
}

func (h *ConvexHull) ReferencePoints() []HullPoint {
	if len(h.hull) == 0 {
		out := make([]HullPoint, 0, len(h.points))
		for _, p := range h.points {
			out = append(out, HullPoint{ND: p.nd, VD: p.vd, Label: p.label})
		}
		return out
	}
	out := make([]HullPoint, len(h.hull))
	for i, p := range h.hull {
		out[i] = HullPoint{ND: p.nd, VD: p.vd, Label: p.label, VertexIndex: i}
	}
	return out
}

// Penalty returns a dimensionless penalty for the given nd/vd pair.
// Outside the hull (beyond the margin) returns a hard 1e6. Inside, returns
// a smooth polynomial scaled by factor (which multiplies the configured weight).
// At the exact real-catalog vertices the penalty vanishes.
func (h *ConvexHull) Penalty(nd, vd, margin, weight float64) float64 {
	if !h.enabled {
		return 0
	}
	if weight <= 0 {
		return 0
	}
	if !h.inBounds(nd, vd) {
		return 1e6
	}

	if len(h.hull) < 3 {
		return h.penaltyLegacy(nd, vd, weight)
	}

	scaledMargin := margin
	if scaledMargin <= 0 {
		scaledMargin = 1.0
	}

	n := len(h.hull)
	cx, cy := 0.0, 0.0
	for _, v := range h.hull {
		cx += v.nd
		cy += v.vd
	}
	cx /= float64(n)
	cy /= float64(n)
	radius := 0.0
	for _, v := range h.hull {
		d := math.Hypot(v.nd-cx, v.vd-cy)
		if d > radius {
			radius = d
		}
	}
	if radius < 1e-9 {
		radius = 1.0
	}

	dPlane := signedDistanceToHullBoundary(h.hull, nd, vd)
	_, dBaryDist, _ := barycentricAndDistance(h.hull, nd, vd)
	d := dPlane
	if dBaryDist < d {
		d = dBaryDist
	}

	offset := radius * hullBoundaryOffset * scaledMargin
	if d < -offset {
		return 1e6
	}

	// Smooth penalty: 0 well inside, 1 at boundary, hard 1e6 outside.
	t := 0.0
	if offset > 1e-12 && d < offset {
		t = 1.0 - (d+offset)/(2*offset)
		if t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
	}
	penalty := t * t * (3 - 2*t)

	for _, p := range h.points {
		if math.Abs(p.nd-nd) < 1e-3 && math.Abs(p.vd-vd) < 1e-3 {
			return 0
		}
	}

	return weight * penalty
}

// Residual returns sqrt(weight) * sqrt(Penalty) for the DLS sum-of-squares
// framework. When squared by the solver this produces exactly Penalty.
func (h *ConvexHull) Residual(nd, vd, margin, weight float64) float64 {
	pen := h.Penalty(nd, vd, margin, weight)
	if pen <= 0 {
		return 0
	}
	return math.Sqrt(pen)
}

func (h *ConvexHull) penaltyLegacy(nd, vd, weight float64) float64 {
	closest := math.MaxFloat64
	if len(h.points) == 0 {
		return 0
	}
	for _, p := range h.points {
		d2 := (p.nd-nd)*(p.nd-nd) + (p.vd-vd)*(p.vd-vd)
		if d2 < closest {
			closest = d2
		}
	}
	if weight <= 0 {
		weight = 1.0
	}
	if closest < 1e-6 {
		return 0
	}
	d := math.Sqrt(closest)
	if d < 1e-3 {
		return 0
	}
	// With legacy hull (no proper hull), return 0 for any reasonable point
	return 0
}

func (h *ConvexHull) inBounds(nd, vd float64) bool {
	if nd < h.ndMin-h.margin || nd > h.ndMax+h.margin {
		return false
	}
	if vd < h.vdMin-h.margin || vd > h.vdMax+h.margin {
		return false
	}
	return true
}

// signedDistanceToHullBoundary returns a signed distance: negative = inside the hull.
func signedDistanceToHullBoundary(hull []ndvdPoint, nd, vd float64) float64 {
	if len(hull) < 3 {
		return 1.0
	}
	n := len(hull)
	minDist := math.MaxFloat64
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		d := pointToSegmentDistSigned(hull[i].nd, hull[i].vd, hull[j].nd, hull[j].vd, nd, vd)
		if d < minDist {
			minDist = d
		}
	}
	return minDist
}

// barycentricAndDistance returns barycentric coords and signed distance for a point.
func barycentricAndDistance(hull []ndvdPoint, nd, vd float64) ([]float64, float64, int) {
	coords, dist := barycentricCoords(hull, nd, vd)
	return coords, dist, 0
}

func barycentricCoords(hull []ndvdPoint, nd, vd float64) ([]float64, float64) {
	n := len(hull)
	coords := make([]float64, n)

	cx, cy := 0.0, 0.0
	for _, p := range hull {
		cx += p.nd
		cy += p.vd
	}
	cx /= float64(n)
	cy /= float64(n)

	totalArea := 0.0
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		area := (hull[i].nd-cx)*(hull[j].vd-cy) - (hull[j].nd-cx)*(hull[i].vd-cy)
		totalArea += area
	}

	if math.Abs(totalArea) < 1e-12 {
		totalArea = 1.0
	}

	for i := 0; i < n; i++ {
		j := (i + 1) % n
		area := (hull[i].nd-nd)*(hull[j].vd-vd) - (hull[j].nd-nd)*(hull[i].vd-vd)
		coords[i] = area / totalArea
	}

	dist := math.MaxFloat64
	negCount := 0
	for _, c := range coords {
		if c < -1e-9 {
			negCount++
		}
		if c < dist {
			dist = c
		}
	}
	if negCount > 0 {
		dist = -math.Abs(dist)
	}

	return coords, dist
}

// pointToSegmentDistSigned returns the signed distance from point (px,py) to
// the line defined by segment (ax,ay)-(bx,by). Negative = inside the hull.
func pointToSegmentDistSigned(ax, ay, bx, by, px, py float64) float64 {
	dx := bx - ax
	dy := by - ay
	dLen := math.Hypot(dx, dy)
	if dLen < 1e-15 {
		return math.Hypot(px-ax, py-ay)
	}
	nx := -dy / dLen
	ny := dx / dLen
	d := (px-ax)*nx + (py-ay)*ny
	return d
}

// Weight returns the configured weight of this hull.
func (h *ConvexHull) Weight() float64 {
	return h.weight
}

// Enabled reports whether this hull is active.
func (h *ConvexHull) Enabled() bool {
	return h.enabled
}

// References returns the catalogue metadata for the reference points.
func (h *ConvexHull) References() []HullReference {
	return h.references
}

// Barycentric returns barycentric coordinates and signed distance for a point.
// Useful for diagnostics and sensitivity analysis.
func (h *ConvexHull) Barycentric(nd, vd float64) ([]float64, float64) {
	if len(h.hull) < 3 {
		return nil, 0
	}
	coords, dist := barycentricCoords(h.hull, nd, vd)
	return coords, dist
}

// Contains reports whether (nd, vd) lies inside the convex hull (including the
// boundary). It evaluates the hull's half-space (C/d) representation: a point
// is inside when n·p <= d for every facet. This is an O(n_facets) test with no
// allocation. When the hull has fewer than 3 vertices (degenerate), every point
// is considered contained so the constraint is a no-op.
func (h *ConvexHull) Contains(nd, vd float64) bool {
	if !h.enabled {
		return true
	}
	if len(h.halfSpaces) == 0 {
		return true
	}
	const tol = 1e-9
	for _, hs := range h.halfSpaces {
		if hs.nx*nd+hs.ny*vd > hs.d+tol {
			return false
		}
	}
	return true
}

// ProjectOntoHull returns the point on the hull boundary closest to (nd, vd) by
// projecting a violated point onto the most-violated facet (clamped to the
// facet segment). A point already inside is returned unchanged. This is the
// "anchor" step: an optimised glass outside the hull is pulled back onto a real
// boundary facet rather than to the hull centroid.
func (h *ConvexHull) ProjectOntoHull(nd, vd float64) (float64, float64) {
	if !h.enabled || len(h.halfSpaces) == 0 {
		return nd, vd
	}
	// Find the facet with the largest outward violation.
	worst := -1
	worstViolation := 0.0
	for i, hs := range h.halfSpaces {
		v := hs.nx*nd + hs.ny*vd - hs.d
		if v > worstViolation {
			worstViolation = v
			worst = i
		}
	}
	if worst < 0 {
		return nd, vd // already inside
	}
	hs := h.halfSpaces[worst]
	// Project onto the facet line, then clamp to the facet segment.
	px := nd - worstViolation*hs.nx
	py := vd - worstViolation*hs.ny
	return clampToSegment(px, py, hs.ax, hs.ay, hs.bx, hs.by)
}

// clampToSegment returns the point on segment (ax,ay)-(bx,by) closest to (px,py).
func clampToSegment(px, py, ax, ay, bx, by float64) (float64, float64) {
	dx := bx - ax
	dy := by - ay
	den := dx*dx + dy*dy
	if den < 1e-18 {
		return ax, ay
	}
	t := ((px-ax)*dx + (py-ay)*dy) / den
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return ax + t*dx, ay + t*dy
}

// buildHalfSpaces converts a CCW hull into its C/d half-space form: for each
// edge, the outward unit normal n and offset d = n·v (v on the edge). The
// interior satisfies n·p <= d. It also records both edge endpoints so an
// outside point can be anchored onto the facet segment.
func buildHalfSpaces(hull []ndvdPoint) []hullHalfSpace {
	n := len(hull)
	if n < 3 {
		return nil
	}
	out := make([]hullHalfSpace, 0, n)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		ax, ay := hull[i].nd, hull[i].vd
		bx, by := hull[j].nd, hull[j].vd
		dx := bx - ax
		dy := by - ay
		length := math.Hypot(dx, dy)
		if length < 1e-15 {
			continue
		}
		// CCW hull: interior is to the left of the edge, so the outward
		// normal points to the right: n = (dy, -dx) / |edge|.
		nx := dy / length
		ny := -dx / length
		out = append(out, hullHalfSpace{
			nx: nx, ny: ny,
			d:  nx*ax + ny*ay,
			ax: ax, ay: ay,
			bx: bx, by: by,
		})
	}
	return out
}

// A convex-hull implementation for 2D points (Andrew's monotone chain).

type convexPoint struct {
	x, y float64
}

func convexHull2D(points []ndvdPoint) []ndvdPoint {
	if len(points) <= 1 {
		out := make([]ndvdPoint, len(points))
		copy(out, points)
		return out
	}

	cp := make([]convexPoint, len(points))
	for i, p := range points {
		cp[i] = convexPoint{p.nd, p.vd}
	}
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].x < cp[j].x {
			return true
		}
		if cp[i].x > cp[j].x {
			return false
		}
		return cp[i].y < cp[j].y
	})

	unique := cp[:1]
	for i := 1; i < len(cp); i++ {
		if cp[i].x != unique[len(unique)-1].x || cp[i].y != unique[len(unique)-1].y {
			unique = append(unique, cp[i])
		}
	}
	cp = unique

	n := len(cp)
	if n <= 2 {
		out := make([]ndvdPoint, n)
		for i, p := range cp {
			out[i] = ndvdPoint{nd: p.x, vd: p.y}
		}
		return out
	}

	lower := make([]convexPoint, 0, n)
	for _, p := range cp {
		for len(lower) >= 2 && cross(lower[len(lower)-2], lower[len(lower)-1], p) <= 0 {
			lower = lower[:len(lower)-1]
		}
		lower = append(lower, p)
	}

	upper := make([]convexPoint, 0, n)
	for i := n - 1; i >= 0; i-- {
		p := cp[i]
		for len(upper) >= 2 && cross(upper[len(upper)-2], upper[len(upper)-1], p) <= 0 {
			upper = upper[:len(upper)-1]
		}
		upper = append(upper, p)
	}

	hullCp := lower[:len(lower)-1]
	hullCp = append(hullCp, upper[:len(upper)-1]...)

	out := make([]ndvdPoint, len(hullCp))
	for i, p := range hullCp {
		out[i] = ndvdPoint{nd: p.x, vd: p.y}
	}
	return out
}

func cross(o, a, b convexPoint) float64 {
	return (a.x-o.x)*(b.y-o.y) - (a.y-o.y)*(b.x-o.x)
}

// GlassKindOrderForCatalog returns the kind order for the given catalogue.
func GlassKindOrderForCatalog(cat *Catalog) []int {
	if len(cat.ByName) == 0 {
		return DefaultGlassKindOrder[:]
	}

	seen := make(map[glassFamily]bool)
	for key := range cat.ByName {
		seen[glassKind(key)] = true
	}
	if len(seen) == 0 {
		return nil
	}
	primary := primaryKind(seen)
	if primary == kindUnknown {
		return DefaultGlassKindOrder[:]
	}
	remainder := make([]int, 0, len(DefaultGlassKindOrder)-1)
	for _, k := range DefaultGlassKindOrder {
		if k == int(primary) {
			continue
		}
		if seen[glassFamily(k)] {
			remainder = append(remainder, k)
		}
	}
	out := make([]int, 0, 1+len(remainder))
	out = append(out, int(primary))
	out = append(out, remainder...)
	return out
}

func primaryKind(seen map[glassFamily]bool) glassFamily {
	for _, k := range DefaultGlassKindOrder {
		if seen[glassFamily(k)] {
			return glassFamily(k)
		}
	}
	return kindUnknown
}

func matchKind(k glassFamily, kinds []int) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, want := range kinds {
		if int(k) == want {
			return true
		}
	}
	return false
}

// EnforceBounds returns a copy of (nd, vd) pulled back onto the hull boundary
// when it lies outside the margin. Negative nd or vd are clamped to a small
// positive value. Points outside the hull are anchored onto the most-violated
// facet (via ProjectOntoHull), nudged slightly inside by the margin fraction so
// a clamped value is not exactly on the boundary (which would re-trigger the
// hull check under floating-point noise).
func (h *ConvexHull) EnforceBounds(nd, vd float64) (float64, float64) {
	if nd <= 0 {
		nd = 0.01
	}
	if vd <= 0 {
		vd = 0.01
	}
	if !h.enabled {
		return nd, vd
	}

	if len(h.halfSpaces) > 0 {
		scaledMargin := h.margin
		if scaledMargin <= 0 {
			scaledMargin = 1.0
		}
		if !h.Contains(nd, vd) {
			pn, pv := h.ProjectOntoHull(nd, vd)
			// Nudge a small amount toward the centroid so the anchored point
			// sits just inside the boundary.
			cx, cy := 0.0, 0.0
			for _, v := range h.hull {
				cx += v.nd
				cy += v.vd
			}
			n := float64(len(h.hull))
			if n > 0 {
				cx /= n
				cy /= n
				t := 0.01 * scaledMargin
				if t > 0.5 {
					t = 0.5
				}
				nd = pn + (cx-pn)*t
				vd = pv + (cy-pv)*t
			} else {
				nd, vd = pn, pv
			}
		}
	}

	if nd < h.ndMin-h.margin {
		nd = h.ndMin
	}
	if nd > h.ndMax+h.margin {
		nd = h.ndMax
	}
	if vd < h.vdMin-h.margin {
		vd = h.vdMin
	}
	if vd > h.vdMax+h.margin {
		vd = h.vdMax
	}
	return nd, vd
}

// glassFamily identifies the manufacturer family of a glass.
type glassFamily int

const (
	kindUnknown glassFamily = iota
	kindSchott              // N-*, BK*, BAK*, SK*, SF*, LAFN*, ...
	kindOhara               // S-*, BSM*, ISM*, PBM*, TAF*, ...
	kindHoya                // FCD*, E-FCD*, FAC*, FUM*, ...
	kindCDGM                // H-*, H-ZK*, H-LAF*, H-F*, ...
)

func (k glassFamily) String() string {
	switch k {
	case kindSchott:
		return "SCHOTT"
	case kindOhara:
		return "OHARA"
	case kindHoya:
		return "HOYA"
	case kindCDGM:
		return "CDGM"
	default:
		return "UNKNOWN"
	}
}

func glassKind(name string) glassFamily {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if strings.HasPrefix(upper, "N-") || strings.HasPrefix(upper, "N+") {
		return kindSchott
	}
	if strings.HasPrefix(upper, "D-") {
		return kindCDGM
	}
	if strings.HasPrefix(upper, "H-") {
		return kindCDGM
	}
	if len(upper) < 2 {
		return kindUnknown
	}
	prefix := upper[:2]
	switch prefix {
	case "BK", "BA", "SK", "SF", "LF", "LLF", "PSK", "SSK", "TSK", "LAF", "LAK", "LAS", "LBF", "LBH", "LITH":
		return kindSchott
	case "SM", "IS", "PB", "TA", "NB", "OA":
		return kindOhara
	case "FC", "FA", "FU":
		return kindHoya
	}
	return kindUnknown
}

func init() {
	if len(DefaultGlassKindOrder) == 0 {
		panic(fmt.Sprintf("glass: DefaultGlassKindOrder must not be empty: %v", DefaultGlassKindOrder))
	}
}
