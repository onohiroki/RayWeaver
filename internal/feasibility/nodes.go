// Package feasibility implements the total, differentiable merit machinery
// described in docs/methods/feasibility-continuation.md: signed-distance
// residuals, the paraxial initializer and the nested node solve.
package feasibility

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// NodePath is one ray's path solved as a system: the hit point on every surface
// in the path, the final least-squares residual norm and whether the solve
// converged.
//
// Points and Surfaces are in path order (surface 0, the object plane, is
// skipped). A physical ray satisfies the surface equations and Snell's law at
// every surface, so Residual is zero for a valid path; a non-zero residual is
// the amount by which the best least-squares path violates the geometry. That
// residual is the natural penalty for the outer optimizer when no physical path
// exists (the "missed surface" event of a sequential trace).
type NodePath struct {
	Points    []types.Vec3
	Surfaces  []int
	Residual  float64
	Converged bool
}

// SolveOptions configures SolveNodes. Zero values select the defaults.
type SolveOptions struct {
	// MaxIter is the Levenberg-Marquardt iteration cap (default 50).
	MaxIter int
	// Tolerance is the residual norm below which the path is converged
	// (default 1e-8).
	Tolerance float64
	// Lambda is the initial LM damping (default 1e-3).
	Lambda float64
	// Initial optionally seeds every node with a global hit point, in path
	// order (surface 0 skipped). Nil starts from the launch line's intersections
	// with each surface's vertex plane.
	Initial []types.Vec3
}

// SolveNodes solves the path of one ray through surfaces as a system of
// equations, instead of marching surface to surface. The unknowns are the local
// (x, y) coordinates of the hit point on every path surface (the local z is the
// surface sag), so each node has two degrees of freedom. The residuals are the
// launch-direction condition and Snell's law (or the reflection law on a
// mirror) at every surface except the last. The system is square: 2n unknowns,
// 2n residuals.
//
// surfaces must already be precomputed (surface.Precompute). path lists the
// surface IDs in trace order; 0 (the object plane) is skipped. launch is the
// physical ray state at the start of the path. gc supplies the refractive
// indices; a nil catalog is treated as air.
//
// The solve is deterministic: fixed iteration order, fixed finite-difference
// step and a plain Cholesky solve, so the same input produces bit-identical
// output.
func SolveNodes(surfaces []types.Surface, path []int, launch types.RayState, wavelength float64, gc *glass.Catalog, opt SolveOptions) NodePath {
	sys, ok := buildSystem(surfaces, path, launch, wavelength, gc)
	if !ok {
		return NodePath{}
	}

	u := make([]float64, 2*sys.n)
	sys.initial(u, opt.Initial)

	maxIter := opt.MaxIter
	if maxIter <= 0 {
		maxIter = 50
	}
	tol := opt.Tolerance
	if tol <= 0 {
		tol = 1e-8
	}
	lambda := opt.Lambda
	if lambda <= 0 {
		lambda = 1e-3
	}

	r := sys.residual(u)
	cost := sumSq(r)
	bestU := append([]float64(nil), u...)
	bestCost := cost

	for iter := 0; iter < maxIter; iter++ {
		if math.Sqrt(cost) <= tol {
			break
		}
		jac := sys.jacobian(u, r)
		delta, ok := solveLM(jac, r, lambda)
		if !ok {
			lambda = math.Min(lambda*10, 1e12)
			continue
		}
		next := make([]float64, len(u))
		for i := range u {
			next[i] = u[i] + delta[i]
		}
		rNext := sys.residual(next)
		costNext := sumSq(rNext)
		if costNext < cost {
			u, r, cost = next, rNext, costNext
			if costNext < bestCost {
				copy(bestU, next)
				bestCost = costNext
			}
			lambda = math.Max(lambda*0.3, 1e-12)
		} else {
			lambda = math.Min(lambda*10, 1e12)
		}
	}

	valid := true
	for i := 0; i < sys.n; i++ {
		if !sys.valid(bestU, i) {
			valid = false
			break
		}
	}
	residual := math.Sqrt(bestCost)
	return NodePath{
		Points:    sys.points(bestU),
		Surfaces:  sys.ids(),
		Residual:  residual,
		Converged: valid && residual <= tol,
	}
}

type system struct {
	surfaces []types.Surface // path order, object plane skipped
	physIdx  []int           // index of each path surface in the physical list
	indices  []float64       // refractive index after each physical surface
	launch   types.RayState
	n        int
}

func buildSystem(surfaces []types.Surface, path []int, launch types.RayState, wavelength float64, gc *glass.Catalog) (*system, bool) {
	if gc == nil {
		gc = glass.NewCatalog()
	}
	byID := make(map[int]int, len(surfaces))
	for i, s := range surfaces {
		byID[s.ID] = i
	}
	sys := &system{
		indices: physicalIndices(surfaces, wavelength, gc),
		launch: types.RayState{
			Origin:    launch.Origin,
			Direction: launch.Direction.Normalize(),
		},
	}
	for _, id := range path {
		if id == 0 {
			continue
		}
		k, ok := byID[id]
		if !ok {
			return nil, false
		}
		sys.surfaces = append(sys.surfaces, surfaces[k])
		sys.physIdx = append(sys.physIdx, k)
	}
	sys.n = len(sys.surfaces)
	return sys, sys.n > 0
}

func physicalIndices(surfaces []types.Surface, wavelength float64, gc *glass.Catalog) []float64 {
	n := make([]float64, len(surfaces))
	for i, s := range surfaces {
		if s.Material.IsAir() {
			n[i] = 1
			continue
		}
		v, err := gc.RefractiveIndex(s.Material, wavelength)
		if err != nil || v == 0 {
			v = 1
		}
		n[i] = v
	}
	for i := range surfaces {
		if !surfaces[i].Reflects() {
			continue
		}
		j := i - 1
		for j >= 0 && surfaces[j].Reflects() {
			j--
		}
		if j < 0 {
			n[i] = 1
		} else {
			n[i] = n[j]
		}
	}
	return n
}

func (s *system) ids() []int {
	ids := make([]int, s.n)
	for i := range s.surfaces {
		ids[i] = s.surfaces[i].ID
	}
	return ids
}

func (s *system) points(u []float64) []types.Vec3 {
	pts := make([]types.Vec3, s.n)
	for i := range pts {
		pts[i] = s.point(u, i)
	}
	return pts
}

// point returns the global hit point of node i from its local (x, y). The
// local z is the surface sag, so the point is on the surface by construction.
func (s *system) point(u []float64, i int) types.Vec3 {
	x, y := u[2*i], u[2*i+1]
	z := surface.SagFunc(s.surfaces[i])(math.Hypot(x, y))
	if math.IsNaN(z) || math.IsInf(z, 0) {
		z = 0
	}
	return s.surfaces[i].LocalToGlobal.MultiplyPoint(types.Vec3{X: x, Y: y, Z: z})
}

func (s *system) valid(u []float64, i int) bool {
	z := surface.SagFunc(s.surfaces[i])(math.Hypot(u[2*i], u[2*i+1]))
	return !math.IsNaN(z) && !math.IsInf(z, 0)
}

func (s *system) initial(u []float64, initial []types.Vec3) {
	if len(initial) == s.n {
		for i, p := range initial {
			local := s.surfaces[i].GlobalToLocal.MultiplyPoint(p)
			u[2*i], u[2*i+1] = local.X, local.Y
		}
		return
	}
	// Start on the launch line's intersection with each surface's vertex
	// tangent plane. This is the virtual extension of the sequential trace:
	// always defined (unless the ray is parallel to the plane) and close to the
	// physical path for a mild system.
	for i := range s.surfaces {
		lo := s.surfaces[i].GlobalToLocal.MultiplyPoint(s.launch.Origin)
		ld := s.surfaces[i].GlobalToLocal.MultiplyVector(s.launch.Direction).Normalize()
		if math.Abs(ld.Z) > 1e-12 {
			t := -lo.Z / ld.Z
			if t < 0 {
				t = 0
			}
			u[2*i] = lo.X + ld.X*t
			u[2*i+1] = lo.Y + ld.Y*t
		} else {
			u[2*i] = lo.X
			u[2*i+1] = lo.Y
		}
	}
}

func (s *system) residual(u []float64) []float64 {
	r := make([]float64, 0, 2*s.n)

	// Launch condition: the first segment must follow the launch direction.
	p0 := s.launch.Origin
	d := s.point(u, 0).Subtract(p0)
	if l := d.Length(); l > 1e-12 {
		d = d.Scale(1 / l)
	} else {
		d = s.launch.Direction
	}
	t1, t2 := tangentBasis(s.launch.Direction)
	dr := d.Subtract(s.launch.Direction)
	r = append(r, dr.Dot(t1), dr.Dot(t2))

	// Snell (or reflection) at every surface except the last, using the
	// outgoing segment towards the next node as the refracted direction.
	for i := 0; i+1 < s.n; i++ {
		pi := s.point(u, i)
		pPrev := p0
		if i > 0 {
			pPrev = s.point(u, i-1)
		}
		dIn := pi.Subtract(pPrev).Normalize()
		dOut := s.point(u, i+1).Subtract(pi).Normalize()

		local := s.surfaces[i].GlobalToLocal.MultiplyPoint(pi)
		nLocal := surface.Normal(s.surfaces[i], local)
		n := s.surfaces[i].LocalToGlobal.MultiplyVector(nLocal).Normalize()

		var res types.Vec3
		if s.surfaces[i].Reflects() {
			refl := dIn.Subtract(n.Scale(2 * dIn.Dot(n)))
			res = dOut.Subtract(refl)
		} else {
			n1 := 1.0
			if i > 0 {
				n1 = s.indices[s.physIdx[i-1]]
			}
			n2 := s.indices[s.physIdx[i]]
			// n1 (dIn x n) = n2 (dOut x n): the tangential components match.
			res = dIn.Cross(n).Scale(n1).Subtract(dOut.Cross(n).Scale(n2))
		}
		b1, b2 := tangentBasis(n)
		r = append(r, res.Dot(b1), res.Dot(b2))
	}
	return r
}

func tangentBasis(n types.Vec3) (types.Vec3, types.Vec3) {
	ref := types.Vec3{X: 1}
	if math.Abs(n.X) > 0.9 {
		ref = types.Vec3{Y: 1}
	}
	t1 := n.Cross(ref).Normalize()
	t2 := n.Cross(t1).Normalize()
	return t1, t2
}

func (s *system) jacobian(u, r []float64) [][]float64 {
	m, n := len(r), len(u)
	jac := make([][]float64, m)
	for i := range jac {
		jac[i] = make([]float64, n)
	}
	perturbed := make([]float64, n)
	for j := 0; j < n; j++ {
		copy(perturbed, u)
		step := 1e-6 * math.Max(1, math.Abs(u[j]))
		perturbed[j] += step
		rp := s.residual(perturbed)
		for i := 0; i < m && i < len(rp); i++ {
			jac[i][j] = (rp[i] - r[i]) / step
		}
	}
	return jac
}

func sumSq(a []float64) float64 {
	sum := 0.0
	for _, v := range a {
		sum += v * v
	}
	return sum
}

// solveLM solves (J^T J + lambda I) delta = -J^T r by Cholesky factorisation.
func solveLM(jac [][]float64, r []float64, lambda float64) ([]float64, bool) {
	if len(jac) == 0 || len(jac[0]) == 0 {
		return nil, false
	}
	n := len(jac[0])
	a := make([][]float64, n)
	for i := range a {
		a[i] = make([]float64, n)
	}
	b := make([]float64, n)
	for k := range jac {
		if len(jac[k]) != n {
			continue
		}
		bk := r[k]
		for i := 0; i < n; i++ {
			b[i] -= jac[k][i] * bk
			for j := 0; j < n; j++ {
				a[i][j] += jac[k][i] * jac[k][j]
			}
		}
	}
	for i := 0; i < n; i++ {
		a[i][i] += lambda
	}
	return choleskySolve(a, b)
}

func choleskySolve(a [][]float64, b []float64) ([]float64, bool) {
	n := len(b)
	l := make([][]float64, n)
	for i := range l {
		l[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for j := 0; j <= i; j++ {
			sum := a[i][j]
			for k := 0; k < j; k++ {
				sum -= l[i][k] * l[j][k]
			}
			if i == j {
				if sum <= 0 {
					return nil, false
				}
				l[i][j] = math.Sqrt(sum)
			} else {
				if l[j][j] == 0 {
					return nil, false
				}
				l[i][j] = sum / l[j][j]
			}
		}
	}
	// Forward substitution: L y = b.
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		sum := b[i]
		for k := 0; k < i; k++ {
			sum -= l[i][k] * y[k]
		}
		y[i] = sum / l[i][i]
	}
	// Back substitution: L^T x = y.
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		sum := y[i]
		for k := i + 1; k < n; k++ {
			sum -= l[k][i] * x[k]
		}
		x[i] = sum / l[i][i]
	}
	return x, true
}
