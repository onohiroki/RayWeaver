package escape

import (
	"math"
	"sort"
	"sync"
)

// RecordHandler is invoked whenever the store records a new minimum or
// replaces a recorded one with a better (lower-merit) point. isNew is true for
// a first-time minimum; version is the number of times the minimum at idx has
// been improved (0 for a new point). The handler runs while the store lock is
// held, so invocations from parallel workers are serialised.
type RecordHandler func(idx int, p Point, isNew bool, version int)

// Store is a thread-safe collection of recorded local minima. Multiple worker
// goroutines share one Store; the escape strength of a minimum grows when DLS
// keeps returning to it, and its stored X/Merit is replaced by a better point
// found later.
type Store struct {
	mu          sync.RWMutex
	points      []Point
	versions    []int
	params      Params
	fingerprint func(x []float64) []float64 // optional design descriptor; nil disables the fingerprint criterion
	onRecord    RecordHandler
}

// NewStore creates an empty store.
func NewStore(params Params) *Store {
	return &Store{params: params}
}

// SetFingerprint registers the design-fingerprint function used by IsNew to
// distinguish structurally different solutions (nil disables it). The function
// maps a variable vector to a compact design descriptor such as the element
// powers; two points are the same minimum only when they are close in both
// variable space and fingerprint space.
func (s *Store) SetFingerprint(fn func(x []float64) []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fingerprint = fn
}

// Distance returns the normalised distance between x and a recorded point
// (same metric as the escape function, without weights applied to the scale).
func (s *Store) Distance(x []float64, p Point) float64 {
	n := len(s.params.Active)
	if n == 0 {
		return 0
	}
	sum := 0.0
	for _, j := range s.params.Active {
		scale := s.params.Scales[j]
		if scale <= 0 {
			scale = 1.0
		}
		wt := 1.0
		if j < len(s.params.Weights) && s.params.Weights[j] != 0 {
			wt = s.params.Weights[j]
		}
		du := (x[j] - p.X[j]) / scale
		sum += wt * du * du
	}
	return math.Sqrt(sum / float64(n))
}

// FindNearest returns the distance to the closest recorded point and its index,
// or (0, -1) if the store is empty.
func (s *Store) FindNearest(x []float64) (float64, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bestD := math.Inf(1)
	bestIdx := -1
	for i := range s.points {
		d := s.Distance(x, s.points[i])
		if d < bestD {
			bestD = d
			bestIdx = i
		}
	}
	return bestD, bestIdx
}

// FingerprintDistance returns the normalised (per-element mean-square) distance
// between x's fingerprint and a recorded point's fingerprint, in the raw
// fingerprint units. When the fingerprint criterion is disabled, or a stored
// point has no fingerprint, it returns 0 (never forces a new minimum). A
// mismatch in the number of elements (structural topology change) is treated as
// maximally far apart (+Inf), since the two designs cannot share a descriptor
// layout.
func (s *Store) FingerprintDistance(x []float64, p Point) float64 {
	if s.fingerprint == nil || s.params.DtFp <= 0 || len(p.Fingerprint) == 0 {
		return 0
	}
	fx := s.fingerprint(x)
	if len(fx) != len(p.Fingerprint) {
		return math.Inf(1)
	}
	n := len(fx)
	if n == 0 {
		return 0
	}
	sum := 0.0
	for i := 0; i < n; i++ {
		d := fx[i] - p.Fingerprint[i]
		sum += d * d
	}
	return math.Sqrt(sum / float64(n))
}

// sameAs reports whether x is close enough to the recorded point p to be
// treated as the same local minimum: close in variable space AND (when the
// fingerprint criterion is enabled) close in fingerprint space. A point that is
// numerically close but structurally different is therefore a distinct minimum.
func (s *Store) sameAs(x []float64, p Point) bool {
	if s.Distance(x, p) >= s.params.Dt {
		return false
	}
	if s.fingerprint != nil && s.params.DtFp > 0 {
		if s.FingerprintDistance(x, p) >= s.params.DtFp {
			return false
		}
	}
	return true
}

// Add appends a new point and returns its index. The caller is responsible
// for checking distance against Dt (and the fingerprint criterion) beforehand
// (see IsNew). The onRecord handler fires with isNew=true and version=0.
func (s *Store) Add(p Point) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fingerprint != nil {
		p.Fingerprint = s.fingerprint(p.X)
	}
	p.H = s.params.H
	p.W = s.params.W
	s.points = append(s.points, p)
	s.versions = append(s.versions, 0)
	idx := len(s.points) - 1
	if s.onRecord != nil {
		s.onRecord(idx, p, true, 0)
	}
	return idx
}

// Replace updates the point at idx in place when p.Merit is better than the
// stored merit. H and W (escape strength) are kept from the previous version;
// X, Merit and Fingerprint take the improved values. Returns the final stored
// point and whether a replacement happened. The onRecord handler fires with
// isNew=false and the new version count only when a replacement actually
// occurs.
func (s *Store) Replace(idx int, p Point) (Point, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 0 || idx >= len(s.points) {
		return Point{}, false
	}
	cur := &s.points[idx]
	if !(p.Merit < cur.Merit) {
		return *cur, false
	}
	cur.X = p.X
	cur.Merit = p.Merit
	if s.fingerprint != nil {
		cur.Fingerprint = s.fingerprint(p.X)
	}
	s.versions[idx]++
	if s.onRecord != nil {
		s.onRecord(idx, *cur, false, s.versions[idx])
	}
	return *cur, true
}

// Version returns how many times the point at idx has been improved (0 = never).
func (s *Store) Version(idx int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if idx < 0 || idx >= len(s.versions) {
		return 0
	}
	return s.versions[idx]
}

// SetOnRecord registers the record handler (nil disables it).
func (s *Store) SetOnRecord(h RecordHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onRecord = h
}

// IsNew reports whether x is far enough from every recorded point to be
// treated as a distinct local minimum: far in variable space, or — when the
// fingerprint criterion is enabled — structurally different (far in
// fingerprint space) even if numerically close.
func (s *Store) IsNew(x []float64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.points {
		if s.sameAs(x, s.points[i]) {
			return false
		}
	}
	return true
}

// Strengthen grows the escape height and width of the point at idx and
// returns the strengthened point (for reporting the updated parameters).
func (s *Store) Strengthen(idx int) Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 0 || idx >= len(s.points) {
		return Point{}
	}
	p := &s.points[idx]
	if s.params.HMult != 0 {
		p.H *= s.params.HMult
	}
	if s.params.WMult != 0 {
		p.W *= s.params.WMult
	}
	return *p
}

// All returns a snapshot of all recorded points.
func (s *Store) All() []Point {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Point, len(s.points))
	copy(out, s.points)
	return out
}

// Len returns the number of recorded points.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.points)
}

// Best returns a copy of the lowest-merit point and its index. Returns a zero
// point and -1 when empty.
func (s *Store) Best() (Point, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.points) == 0 {
		return Point{}, -1
	}
	idx := 0
	for i := 1; i < len(s.points); i++ {
		if s.points[i].Merit < s.points[idx].Merit {
			idx = i
		}
	}
	p := s.points[idx]
	return p, idx
}

// SortedByMerit returns all points ordered by merit (lowest first) with their
// original store indices.
type sortedPoint struct {
	p   Point
	idx int
}

// SortedByMerit returns points ordered by merit ascending, along with their
// store indices.
func (s *Store) SortedByMerit() ([]Point, []int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sp := make([]sortedPoint, len(s.points))
	for i, p := range s.points {
		sp[i] = sortedPoint{p: p, idx: i}
	}
	sort.Slice(sp, func(a, b int) bool { return sp[a].p.Merit < sp[b].p.Merit })
	points := make([]Point, len(sp))
	idx := make([]int, len(sp))
	for i := range sp {
		points[i] = sp[i].p
		idx[i] = sp[i].idx
	}
	return points, idx
}
