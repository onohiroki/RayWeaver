// Package cam solves the first-order cam of a zoom lens: the airspaces between
// the moving groups, one set per zoom position, chosen so that every position
// reaches its target EFL while its image plane stays on one fixed detector and
// the paraxial focus lands on it.
//
// The solve runs on the paraxial trace alone — no ray tracing, no merit — so it
// costs a few hundred microseconds per position and is bit-deterministic. It is
// meant to lay a zoom out before optimize/escape touch it: `cam` first, then
// the aberrations.
//
// Per position the solver drives three residuals to zero:
//
//  1. the EFL error (paraxial EFL minus the position's target),
//  2. the defocus: the paraxial back focal distance from the last refracting
//     surface minus the airspace from it to the image plane (zero exactly when
//     the axial focus sits on the image plane),
//  3. the image-plane position error, when the solve holds the plane at a
//     fixed Z.
//
// The cam airspaces are the unknowns. A position with more unknowns than
// conditions (the usual case: four group airspaces against two or three
// conditions) leaves the extra freedom to the regulariser, which keeps the
// solved layout as close to the input as it can — the minimum-motion cam. A
// position with too few is reported as not converged with its residuals, so
// "this cam surface set cannot hold a fixed detector" is visible rather than
// hidden behind a lucky-looking answer.
package cam

import (
	"errors"
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// Config is one zoom position to solve. Surfaces is never mutated: the result
// carries the solved copy.
type Config struct {
	ID        string
	Surfaces  []types.Surface
	TargetEFL float64
}

// Options configures a solve. Surfaces lists the surface IDs whose *following*
// airspace (that surface's thickness) is a cam variable, in the order the
// result reports them. ImagePlane is the image-plane surface ID; it must be the
// last surface of every solved config, because the paraxial trace treats the
// last surface as the image plane.
type Options struct {
	Surfaces       []int
	ImagePlane     int
	ImagePlaneZ    float64
	HoldImagePlane bool
	StopSurface    int
	Wavelength     float64
	ObjectHeight   float64
	MaxIterations  int
	Tolerance      float64
	// MotionWeight is the Tikhonov weight that pulls the solved airspaces back
	// toward their input values, i.e. how strongly the extra freedom is spent
	// on the smallest possible layout change (0 disables the pull).
	MotionWeight float64
	// MinAirspace is the lower bound every cam airspace must stay at (mm).
	// Thicknesses are positive by definition, so the step is limited to keep
	// the airspaces above it instead of proposing a negative geometry.
	MinAirspace float64
	// Logf, when set, receives one line per accepted iteration (plus the
	// initial residual). It only observes the solve; it cannot change it.
	Logf func(format string, args ...any)
}

// Airspace is one solved cam variable: the airspace following Surface, from the
// input thickness to the solved one.
type Airspace struct {
	Surface int
	From    float64
	To      float64
	Delta   float64
}

// ConfigResult is one position's solved state and the residuals (mm) that were
// driven to zero.
type ConfigResult struct {
	ID                 string
	EFLTarget          float64
	EFL                float64
	ImagePlaneZ        float64
	FocusZ             float64
	ResidualEFL        float64
	ResidualFocus      float64
	ResidualImagePlane float64
	Iterations         int
	Converged          bool
	// HitFloor reports that an airspace ended at MinAirspace, so the geometry
	// blocked the solve rather than the residuals being unsatisfiable.
	HitFloor  bool
	Airspaces []Airspace
	Surfaces  []types.Surface
}

// Result is a whole solve. Converged is true only when every position did.
type Result struct {
	Configs   []ConfigResult
	Converged bool
}

// defaultMaxIterations and defaultTolerance are used when Options leaves the
// corresponding field at its zero value.
const (
	defaultMaxIterations = 50
	defaultTolerance     = 1e-9
)

// Solve solves every config's cam airspaces. It returns an error only for a
// malformed request (no surfaces, a cam surface or image plane missing from a
// config); a position that merely fails to converge is reported through its
// ConfigResult so the caller still gets the best layout found.
func Solve(configs []Config, gc *glass.Catalog, opts Options) (Result, error) {
	if len(configs) == 0 {
		return Result{}, errors.New("no config to solve")
	}
	if len(opts.Surfaces) == 0 {
		return Result{}, errors.New("no cam surfaces declared")
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = defaultMaxIterations
	}
	if opts.Tolerance <= 0 {
		opts.Tolerance = defaultTolerance
	}
	if opts.MinAirspace < 0 {
		opts.MinAirspace = 0
	}

	res := Result{Converged: true}
	for _, c := range configs {
		s := &solver{cfg: c, opts: opts, gc: gc}
		if err := s.prepare(); err != nil {
			return Result{}, fmt.Errorf("config %q: %w", c.ID, err)
		}
		cr, err := s.solve()
		if err != nil {
			return Result{}, fmt.Errorf("config %q: %w", c.ID, err)
		}
		res.Configs = append(res.Configs, cr)
		if !cr.Converged {
			res.Converged = false
		}
	}
	return res, nil
}

// solver holds one position's solve state.
type solver struct {
	cfg  Config
	opts Options
	gc   *glass.Catalog

	// idx is the surface-list index of each cam surface, in Options order, and
	// refIdx the image-plane index.
	idx    []int
	refIdx int
	t0     []float64
}

// prepare resolves the cam and image-plane surfaces and records the input
// airspaces.
func (s *solver) prepare() error {
	if len(s.cfg.Surfaces) == 0 {
		return errors.New("no surfaces")
	}
	index := make(map[int]int, len(s.cfg.Surfaces))
	for i := range s.cfg.Surfaces {
		index[s.cfg.Surfaces[i].ID] = i
	}
	s.idx = make([]int, len(s.opts.Surfaces))
	s.t0 = make([]float64, len(s.opts.Surfaces))
	for k, id := range s.opts.Surfaces {
		i, ok := index[id]
		if !ok {
			return fmt.Errorf("cam surface %d is not in the config", id)
		}
		s.idx[k] = i
		s.t0[k] = s.cfg.Surfaces[i].Thickness
	}
	ref, ok := index[s.opts.ImagePlane]
	if !ok {
		return fmt.Errorf("image plane %d is not in the config", s.opts.ImagePlane)
	}
	if ref != len(s.cfg.Surfaces)-1 {
		return fmt.Errorf("image plane %d must be the last surface (the paraxial trace reads the last surface as the image plane)", s.opts.ImagePlane)
	}
	s.refIdx = ref
	return nil
}

// materialise returns a copy of the config's surfaces with the cam airspaces
// set to t and the vertex data precomputed.
func (s *solver) materialise(t []float64) []types.Surface {
	out := make([]types.Surface, len(s.cfg.Surfaces))
	copy(out, s.cfg.Surfaces)
	for k, i := range s.idx {
		out[i].Thickness = t[k]
	}
	surface.Precompute(out)
	return out
}

// sample is the paraxial state a residual vector was built from.
type sample struct {
	efl     float64
	focusZ  float64
	defocus float64
	planeZ  float64
}

// evaluate returns the residuals at t and the values they were built from. The
// residual order is EFL, focus, image plane (the last only when the plane is
// held).
func (s *solver) evaluate(t []float64) ([]float64, sample) {
	surfaces := s.materialise(t)
	pres := paraxial.Compute(
		types.System{Surfaces: surfaces, StopSurface: s.opts.StopSurface},
		s.opts.Wavelength, s.gc, s.opts.ObjectHeight, nil,
	)
	defocus := pres.SecondPrincipalFocus - distanceToImagePlane(surfaces)
	planeZ := surfaces[s.refIdx].PhysicalZ

	var smp sample
	smp.efl = pres.FocalLength
	smp.defocus = defocus
	smp.planeZ = planeZ
	smp.focusZ = planeZ + defocus // focus − plane = BFL − airspace = defocus

	r := make([]float64, 0, 3)
	// The EFL is compared as a magnitude, the same way the focal_length
	// constraint does: a folded system (odd reflection count) reports a negative
	// EFL, and a positive target has to mean "28 mm, either sign".
	r = append(r, math.Abs(pres.FocalLength)-s.cfg.TargetEFL, defocus)
	if s.opts.HoldImagePlane {
		r = append(r, planeZ-s.opts.ImagePlaneZ)
	}
	return r, smp
}

// solve runs the regularised Gauss-Newton iteration for one position.
func (s *solver) solve() (ConfigResult, error) {
	t := append([]float64{}, s.t0...)
	r, smp := s.evaluate(t)
	obj := s.objective(r, t)
	converged := maxAbs(r) <= s.opts.Tolerance
	if s.opts.Logf != nil {
		s.opts.Logf("%s: start  EFL %+.6g  focus %+.6g  plane %+.6g  (tol %g)",
			s.cfg.ID, r[0], smp.defocus, planeResidual(r, s.opts.HoldImagePlane), s.opts.Tolerance)
	}

	iterations := 0
	mu := 1e-6
	for it := 1; it <= s.opts.MaxIterations && !converged; it++ {
		iterations = it
		J := s.jacobian(t, r)
		accepted := false
		for try := 0; try < 30 && !accepted; try++ {
			d, ok := leastSquares(J, r, t, s.t0, s.opts.MotionWeight, mu, make([]bool, len(t)))
			if !ok {
				mu = mu*10 + 1e-9
				continue
			}
			// Active set: an airspace already sitting on the floor cannot be
			// pushed below it, so it is held there and the step is re-solved for
			// the airspaces that can still move. Without this the tiny back
			// focus alone would cap the whole step and the solve would crawl.
			for pass := 0; pass < len(t); pass++ {
				fixed := s.floorBlocked(t, d)
				if !anyFixed(fixed) {
					break
				}
				d2, ok2 := leastSquares(J, r, t, s.t0, s.opts.MotionWeight, mu, fixed)
				if !ok2 {
					break
				}
				d = d2
			}
			// Trust region plus line search: the step is capped (no cam
			// airspace jumps by more than its own scale or crosses the floor)
			// and then halved until it actually lowers the objective. The
			// Newton direction alone can be far too long for the EFL residual,
			// and a single clipped step would otherwise stall against the floor.
			alpha := s.stepAlpha(t, d)
			for ; alpha > 1e-9; alpha /= 2 {
				nt := make([]float64, len(t))
				for k := range t {
					nt[k] = math.Max(t[k]+alpha*d[k], s.opts.MinAirspace)
				}
				nr, nsmp := s.evaluate(nt)
				nobj := s.objective(nr, nt)
				if nobj < obj {
					t, r, smp, obj = nt, nr, nsmp, nobj
					mu = math.Max(mu/3, 1e-12)
					accepted = true
					if s.opts.Logf != nil {
						s.opts.Logf("%s: iter %d  EFL %+.6g  focus %+.6g  plane %+.6g  (alpha %g, mu %g)",
							s.cfg.ID, it, r[0], smp.defocus, planeResidual(r, s.opts.HoldImagePlane), alpha, mu)
					}
					break
				}
			}
			if !accepted {
				mu = mu*10 + 1e-9
			}
		}
		if !accepted {
			break // a local minimum of the regularised objective
		}
		converged = maxAbs(r) <= s.opts.Tolerance
	}

	cr := ConfigResult{
		ID:                 s.cfg.ID,
		EFLTarget:          s.cfg.TargetEFL,
		EFL:                smp.efl,
		ImagePlaneZ:        smp.planeZ,
		FocusZ:             smp.focusZ,
		ResidualEFL:        r[0],
		ResidualFocus:      smp.defocus,
		ResidualImagePlane: planeResidual(r, s.opts.HoldImagePlane),
		Iterations:         iterations,
		Converged:          converged,
		Surfaces:           s.materialise(t),
	}
	for k, i := range s.idx {
		cr.Airspaces = append(cr.Airspaces, Airspace{
			Surface: s.cfg.Surfaces[i].ID,
			From:    s.t0[k],
			To:      t[k],
			Delta:   t[k] - s.t0[k],
		})
		if t[k] <= s.opts.MinAirspace+1e-9 {
			cr.HitFloor = true
		}
	}
	return cr, nil
}

// objective is the augmented residual the iteration minimises: the physical
// residuals plus the motion pull toward the input airspaces.
func (s *solver) objective(r, t []float64) float64 {
	sum := 0.0
	for _, v := range r {
		sum += v * v
	}
	lam := s.opts.MotionWeight
	if lam != 0 {
		for k := range t {
			d := t[k] - s.t0[k]
			sum += lam * lam * d * d
		}
	}
	return sum
}

// jacobian is the finite-difference Jacobian of the residual vector at t. The
// paraxial trace is smooth in the airspaces, so a central difference around
// 1e-6 mm is enough for the 1e-9 mm target; a cam airspace close to the floor
// falls back to a one-sided difference there.
func (s *solver) jacobian(t, r []float64) [][]float64 {
	n := len(t)
	J := make([][]float64, len(r))
	for i := range J {
		J[i] = make([]float64, n)
	}
	for k := 0; k < n; k++ {
		h := 1e-6
		if s.opts.MinAirspace > 0 && t[k]-h < s.opts.MinAirspace {
			h = (t[k] - s.opts.MinAirspace) / 2
			if h <= 0 {
				h = 1e-6
			}
		}
		oneSided := t[k]-h < s.opts.MinAirspace
		tp := append([]float64{}, t...)
		tp[k] += h
		rp, _ := s.evaluate(tp)
		var rm []float64
		if oneSided {
			rm = r
		} else {
			tm := append([]float64{}, t...)
			tm[k] -= h
			rm, _ = s.evaluate(tm)
		}
		den := 2 * h
		if oneSided {
			den = h
		}
		for i := range r {
			J[i][k] = (rp[i] - rm[i]) / den
		}
	}
	return J
}

// floorBlocked reports which airspaces a step would push below the thickness
// floor from a position already on it.
func (s *solver) floorBlocked(t, d []float64) []bool {
	blocked := make([]bool, len(t))
	for k := range t {
		blocked[k] = d[k] < 0 && t[k] <= s.opts.MinAirspace+1e-12
	}
	return blocked
}

// anyFixed reports whether an active set holds anything.
func anyFixed(fixed []bool) bool {
	for _, f := range fixed {
		if f {
			return true
		}
	}
	return false
}

// stepAlpha returns the largest alpha in (0, 1] that keeps the step inside the
// trust region and every cam airspace at or above the floor along t + alpha*d.
// The trust region is per variable: a step may not move an airspace by more
// than half its own magnitude (at least 1 mm), so a Newton step built on a
// badly extrapolated EFL linearisation cannot teleport the layout.
func (s *solver) stepAlpha(t, d []float64) float64 {
	alpha := 1.0
	for k := range t {
		if d[k] == 0 {
			continue
		}
		if trust := math.Max(0.5*math.Abs(t[k]), 1.0); trust/math.Abs(d[k]) < alpha {
			alpha = trust / math.Abs(d[k])
		}
		if d[k] < 0 {
			if room := (t[k] - s.opts.MinAirspace) / -d[k]; room < alpha {
				alpha = room
			}
		}
	}
	if alpha < 0 {
		return 0
	}
	return alpha
}

// leastSquares solves for the step d that reduces
//
//	||J·d + r||² + λ²‖t + d − t0‖²
//
// with an extra Levenberg-Marquardt factor mu on the diagonal. Variables marked
// in fixed are held at their current value (their step stays zero), which is how
// an airspace the thickness floor has stopped stops throttling the others. The
// motion rows keep the normal matrix positive definite even when J is rank
// deficient, so a position with more airspaces than conditions still has a
// unique minimum-motion step.
func leastSquares(J [][]float64, r, t, t0 []float64, lambda, mu float64, fixed []bool) ([]float64, bool) {
	n := len(t)
	var free []int
	for k := 0; k < n; k++ {
		if !fixed[k] {
			free = append(free, k)
		}
	}
	if len(free) == 0 {
		return make([]float64, n), false
	}

	m := len(free)
	A := make([][]float64, m)
	b := make([]float64, m)
	for a := range A {
		A[a] = make([]float64, m)
	}
	for a, ka := range free {
		for c, kc := range free {
			sum := 0.0
			for i := range r {
				sum += J[i][ka] * J[i][kc]
			}
			A[a][c] = sum
		}
		A[a][a] += lambda * lambda
		b[a] = lambda * lambda * (t0[ka] - t[ka])
	}
	for i := range r {
		for a, ka := range free {
			b[a] -= J[i][ka] * r[i]
		}
	}
	for a := 0; a < m; a++ {
		A[a][a] *= 1 + mu
	}
	x, ok := solveDense(A, b)
	if !ok {
		return nil, false
	}
	d := make([]float64, n)
	for a, ka := range free {
		d[ka] = x[a]
	}
	return d, true
}

// solveDense solves A·x = b by Gaussian elimination with partial pivoting. A is
// small (the cam variable count) and the normal matrix is symmetric positive
// definite, but pivoting keeps it honest when a cam surface has no leverage.
func solveDense(A [][]float64, b []float64) ([]float64, bool) {
	n := len(b)
	m := make([][]float64, n)
	for i := range m {
		m[i] = append([]float64{}, A[i]...)
	}
	x := append([]float64{}, b...)

	for col := 0; col < n; col++ {
		p := col
		for row := col + 1; row < n; row++ {
			if math.Abs(m[row][col]) > math.Abs(m[p][col]) {
				p = row
			}
		}
		if math.Abs(m[p][col]) < 1e-300 {
			return nil, false
		}
		m[col], m[p] = m[p], m[col]
		x[col], x[p] = x[p], x[col]
		for row := col + 1; row < n; row++ {
			f := m[row][col] / m[col][col]
			if f == 0 {
				continue
			}
			for c := col; c < n; c++ {
				m[row][c] -= f * m[col][c]
			}
			x[row] -= f * x[col]
		}
	}
	for i := n - 1; i >= 0; i-- {
		sum := x[i]
		for c := i + 1; c < n; c++ {
			sum -= m[i][c] * x[c]
		}
		x[i] = sum / m[i][i]
	}
	return x, true
}

// distanceToImagePlane is the path from the vertex paraxial's back focal
// distance is measured from — the last surface that is not a trailing
// zero-thickness plane — to the image plane (the last surface). The paraxial
// axial focus sits on the image plane exactly when the back focal distance
// equals this path.
func distanceToImagePlane(surfaces []types.Surface) float64 {
	b := len(surfaces) - 1
	for b > 0 && surfaces[b].Radius() == 0 && surfaces[b].Thickness == 0 {
		b--
	}
	d := 0.0
	for j := b; j < len(surfaces)-1; j++ {
		d += surfaces[j].Thickness
	}
	return d
}

// planeResidual returns the image-plane position residual, or 0 when the solve
// does not hold the plane.
func planeResidual(r []float64, hold bool) float64 {
	if !hold || len(r) == 0 {
		return 0
	}
	return r[len(r)-1]
}

// maxAbs is the largest absolute value of r.
func maxAbs(r []float64) float64 {
	m := 0.0
	for _, v := range r {
		if a := math.Abs(v); a > m {
			m = a
		}
	}
	return m
}
