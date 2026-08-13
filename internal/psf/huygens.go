package psf

import (
	"math"
	"runtime"
	"sort"
	"sync"

	"github.com/hiroki/rayweaver/internal/types"
)

// ImageGridSpec describes the flat image-plane sampling. The grid is square
// (NX = NY) and centred on the field's chief-ray image point.
type ImageGridSpec struct {
	NX, NY int
	X0, Y0 float64 // corner (lower-left) in global coordinates
	DX, DY float64 // pixel size (mm)
}

// FieldGrid is the computed complex electric field on the flat image plane,
// stored row-major (index = y*NX + x).
type FieldGrid struct {
	Spec     ImageGridSpec
	Ex, Ey   []complex128
	Ez       []complex128
	Intensity []float64
}

// NewFieldGrid allocates a FieldGrid with the given spec.
func NewFieldGrid(spec ImageGridSpec) *FieldGrid {
	n := spec.NX * spec.NY
	return &FieldGrid{
		Spec:      spec,
		Ex:        make([]complex128, n),
		Ey:        make([]complex128, n),
		Ez:        make([]complex128, n),
		Intensity: make([]float64, n),
	}
}

// ComputeField evaluates the direct vector Huygens integral of the wavefront
// samples onto the flat image plane:
//
//	E(P) = (1/λ) · Σ_j E_j · exp(ik(OPL_j + n·R_j)) · K_j · ΔA_j / R_j
//
// with R_j = |P - q_j| and the obliquity factor K_j = (1 + s_j·R̂_j)/2.
// When ideal is true the OPL is replaced by a perfect converging sphere to the
// grid centre (n·|q_j - centre|), giving the diffraction-limited reference
// used for the Strehl ratio.
//
// The image-plane rows are evaluated in parallel (runtime.NumCPU() workers,
// or opts.Workers when > 0); each row writes to a disjoint slice of the
// output grid, so no locking is needed.
func ComputeField(samples []WavefrontSample, center types.Vec3, imagePlaneZ float64,
	nImage, wavelength float64, spec ImageGridSpec, ideal bool) *FieldGrid {
	return computeField(samples, center, imagePlaneZ, nImage, wavelength, spec, ideal, 0)
}

// computeField is ComputeField with an explicit worker count (0 = NumCPU).
func computeField(samples []WavefrontSample, center types.Vec3, imagePlaneZ float64,
	nImage, wavelength float64, spec ImageGridSpec, ideal bool, workers int) *FieldGrid {
	grid := NewFieldGrid(spec)
	k := 2 * math.Pi / wavelength

	// Precompute the ideal OPL reference distance for every sample once,
	// instead of recomputing it for each image-plane pixel.
	idealOPL := make([]float64, len(samples))
	if ideal {
		for i, s := range samples {
			idealOPL[i] = -nImage * s.Position.Subtract(center).Length()
		}
	}

	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers < 1 {
		workers = 1
	}
	if workers > spec.NY {
		workers = spec.NY
	}

	var wg sync.WaitGroup
	rowsPerWorker := (spec.NY + workers - 1) / workers
	for w := 0; w < workers; w++ {
		j0 := w * rowsPerWorker
		j1 := j0 + rowsPerWorker
		if j1 > spec.NY {
			j1 = spec.NY
		}
		if j0 >= j1 {
			continue
		}
		wg.Add(1)
		go func(j0, j1 int) {
			defer wg.Done()
			for j := j0; j < j1; j++ {
				for i := 0; i < spec.NX; i++ {
					p := types.Vec3{
						X: spec.X0 + (float64(i)+0.5)*spec.DX,
						Y: spec.Y0 + (float64(j)+0.5)*spec.DY,
						Z: imagePlaneZ,
					}
					idx := j*spec.NX + i
					var ex, ey, ez complex128
					for si, s := range samples {
						Rvec := p.Subtract(s.Position)
						R := Rvec.Length()
						if R < 1e-9 {
							continue
						}
						Rhat := Rvec.Scale(1 / R)
						obliquity := 0.5 * (1 + s.Direction.Dot(Rhat))
						if obliquity < 0 {
							obliquity = 0
						}

						opl := s.OPL
						if ideal {
							opl = idealOPL[si]
						}
						phase := k * (opl + nImage*R)
						w := obliquity * s.Area / R
						cf := complex((w*math.Cos(phase))/wavelength, (w*math.Sin(phase))/wavelength)
						ex += cf * s.Field.X
						ey += cf * s.Field.Y
						ez += cf * s.Field.Z
					}
					grid.Ex[idx] = ex
					grid.Ey[idx] = ey
					grid.Ez[idx] = ez
					grid.Intensity[idx] = absSq(ex) + absSq(ey) + absSq(ez)
				}
			}
		}(j0, j1)
	}
	wg.Wait()
	return grid
}

func absSq(c complex128) float64 {
	return real(c)*real(c) + imag(c)*imag(c)
}

// fieldPair is one sample set's actual + ideal (diffraction-limited reference)
// grids. The pair shares the per-pixel geometry (R, obliquity, area weight)
// across the two variants, so evaluating them in one pass is cheaper than two
// separate ComputeField calls.
type fieldPair struct {
	samples []WavefrontSample
	center  types.Vec3
	actual  *FieldGrid
	ideal   *FieldGrid
}

// computePairs evaluates several field pairs (actual + ideal) through a single
// shared row-parallel worker pool. Geometry is computed once per (pixel,
// sample) and shared between the actual and ideal phases of each pair. All
// pairs are processed by the same pool, so the CPU is fully utilised across
// them without oversubscription. workers 0 = runtime.NumCPU().
func computePairs(pairs []fieldPair, imagePlaneZ, nImage, wavelength float64,
	spec ImageGridSpec, workers int) {
	if len(pairs) == 0 || spec.NX == 0 || spec.NY == 0 {
		return
	}
	k := 2 * math.Pi / wavelength

	// Precompute each pair's ideal OPL reference once.
	idealOPL := make([][]float64, len(pairs))
	for pi := range pairs {
		idealOPL[pi] = make([]float64, len(pairs[pi].samples))
		for si, s := range pairs[pi].samples {
			idealOPL[pi][si] = -nImage * s.Position.Subtract(pairs[pi].center).Length()
		}
	}

	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers < 1 {
		workers = 1
	}
	if workers > spec.NY {
		workers = spec.NY
	}

	var wg sync.WaitGroup
	rowsPerWorker := (spec.NY + workers - 1) / workers
	for w := 0; w < workers; w++ {
		j0 := w * rowsPerWorker
		j1 := j0 + rowsPerWorker
		if j1 > spec.NY {
			j1 = spec.NY
		}
		if j0 >= j1 {
			continue
		}
		wg.Add(1)
		go func(j0, j1 int) {
			defer wg.Done()
			for j := j0; j < j1; j++ {
				for i := 0; i < spec.NX; i++ {
					p := types.Vec3{
						X: spec.X0 + (float64(i)+0.5)*spec.DX,
						Y: spec.Y0 + (float64(j)+0.5)*spec.DY,
						Z: imagePlaneZ,
					}
					idx := j*spec.NX + i
					for pi := range pairs {
						pr := &pairs[pi]
						var ax, ay, az, ix, iy, iz complex128
						for si, s := range pr.samples {
							Rvec := p.Subtract(s.Position)
							R := Rvec.Length()
							if R < 1e-9 {
								continue
							}
							Rhat := Rvec.Scale(1 / R)
							obliquity := 0.5 * (1 + s.Direction.Dot(Rhat))
							if obliquity < 0 {
								obliquity = 0
							}
							w := obliquity * s.Area / R

							// actual phase
							phaseA := k * (s.OPL + nImage*R)
							cfA := complex((w*math.Cos(phaseA))/wavelength, (w*math.Sin(phaseA))/wavelength)
							ax += cfA * s.Field.X
							ay += cfA * s.Field.Y
							az += cfA * s.Field.Z

							// ideal phase (shared geometry)
							phaseI := k * (idealOPL[pi][si] + nImage*R)
							cfI := complex((w*math.Cos(phaseI))/wavelength, (w*math.Sin(phaseI))/wavelength)
							ix += cfI * s.Field.X
							iy += cfI * s.Field.Y
							iz += cfI * s.Field.Z
						}
						pr.actual.Ex[idx] = ax
						pr.actual.Ey[idx] = ay
						pr.actual.Ez[idx] = az
						pr.actual.Intensity[idx] = absSq(ax) + absSq(ay) + absSq(az)
						pr.ideal.Ex[idx] = ix
						pr.ideal.Ey[idx] = iy
						pr.ideal.Ez[idx] = iz
						pr.ideal.Intensity[idx] = absSq(ix) + absSq(iy) + absSq(iz)
					}
				}
			}
		}(j0, j1)
	}
	wg.Wait()
}

// Normalize scales the intensity grid so its sum over the sampled window
// equals 1 (Σ I·Δx·Δy = 1).
func (g *FieldGrid) Normalize() {
	sum := 0.0
	for _, v := range g.Intensity {
		sum += v
	}
	if sum <= 0 || math.IsNaN(sum) {
		return
	}
	for i := range g.Intensity {
		g.Intensity[i] /= sum
	}
}

// Peak returns the peak intensity and its (x, y) coordinates in global units.
func (g *FieldGrid) Peak() (value, x, y float64) {
	best := -1.0
	bi, bj := 0, 0
	for j := 0; j < g.Spec.NY; j++ {
		for i := 0; i < g.Spec.NX; i++ {
			v := g.Intensity[j*g.Spec.NX+i]
			if v > best {
				best = v
				bi, bj = i, j
			}
		}
	}
	x = g.Spec.X0 + (float64(bi)+0.5)*g.Spec.DX
	y = g.Spec.Y0 + (float64(bj)+0.5)*g.Spec.DY
	return best, x, y
}

// Centroid returns the intensity-weighted centroid of the grid.
func (g *FieldGrid) Centroid() (x, y float64) {
	var sw, wx, wy float64
	for j := 0; j < g.Spec.NY; j++ {
		for i := 0; i < g.Spec.NX; i++ {
			w := g.Intensity[j*g.Spec.NX+i]
			if w <= 0 {
				continue
			}
			sw += w
			wx += w * (g.Spec.X0 + (float64(i)+0.5)*g.Spec.DX)
			wy += w * (g.Spec.Y0 + (float64(j)+0.5)*g.Spec.DY)
		}
	}
	if sw > 0 {
		return wx / sw, wy / sw
	}
	return 0, 0
}

// FWHM returns the full width at half maximum through the peak along the x
// and y axes (sub-pixel via linear interpolation).
func (g *FieldGrid) FWHM() (fx, fy float64) {
	peak, _, _ := g.Peak()
	half := peak * 0.5
	if peak <= 0 {
		return 0, 0
	}
	fx = fwhmAxis(g, half, true)
	fy = fwhmAxis(g, half, false)
	return fx, fy
}

func fwhmAxis(g *FieldGrid, half float64, horizontal bool) float64 {
	// Locate the peak pixel.
	pi, pj := 0, 0
	best := -1.0
	for j := 0; j < g.Spec.NY; j++ {
		for i := 0; i < g.Spec.NX; i++ {
			if v := g.Intensity[j*g.Spec.NX+i]; v > best {
				best = v
				pi, pj = i, j
			}
		}
	}
	d := g.Spec.DX
	if !horizontal {
		d = g.Spec.DY
	}
	width := 0.0
	for _, sign := range []int{1, -1} {
		var prev, prevCoord float64
		if horizontal {
			prev = g.Intensity[pj*g.Spec.NX+pi]
			prevCoord = g.Spec.X0 + (float64(pi)+0.5)*g.Spec.DX
		} else {
			prev = g.Intensity[pj*g.Spec.NX+pi]
			prevCoord = g.Spec.Y0 + (float64(pj)+0.5)*g.Spec.DY
		}
		step := 0.0
		for n := 1; ; n++ {
			i, j := pi+sign*n, pj
			if !horizontal {
				i, j = pi, pj+sign*n
			}
			if i < 0 || i >= g.Spec.NX || j < 0 || j >= g.Spec.NY {
				break
			}
			v := g.Intensity[j*g.Spec.NX+i]
			if v >= half {
				prev = v
				continue
			}
			// Crossed the half-maximum between prev and v. Linear interp.
			t := (half - v) / (prev - v)
			if horizontal {
				step = math.Abs(prevCoord - (g.Spec.X0 + (float64(pi)+0.5)*g.Spec.DX + float64(sign)*float64(n)*d))
				step = float64(n)-t
			} else {
				step = float64(n) - t
			}
			step *= d
			break
		}
		width += step
	}
	return width
}

// EncircledEnergy returns the fraction of total intensity within radius r of
// the given centre (the grid centroid by convention).
func (g *FieldGrid) EncircledEnergy(cx, cy float64, radius float64) float64 {
	total := 0.0
	inside := 0.0
	for j := 0; j < g.Spec.NY; j++ {
		for i := 0; i < g.Spec.NX; i++ {
			v := g.Intensity[j*g.Spec.NX+i]
			total += v
			x := g.Spec.X0 + (float64(i)+0.5)*g.Spec.DX
			y := g.Spec.Y0 + (float64(j)+0.5)*g.Spec.DY
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= radius*radius {
				inside += v
			}
		}
	}
	if total <= 0 {
		return 0
	}
	return inside / total
}

// pixelRadius is a sorted pixel distance²/weight pair for energy sweeps. The
// distance is stored squared so the build loop and the sort key avoid a sqrt
// per pixel; the caller sqrt's only the returned radius.
type pixelRadius struct {
	r2, w float64
}

// RadiusForEnergy returns the smallest radius (from the centre) enclosing the
// given energy fraction, sweeping out to the grid edge.
func (g *FieldGrid) RadiusForEnergy(cx, cy, fraction float64) float64 {
	total := 0.0
	for _, v := range g.Intensity {
		total += v
	}
	if total <= 0 {
		return 0
	}
	pts := make([]pixelRadius, 0, g.Spec.NX*g.Spec.NY)
	for j := 0; j < g.Spec.NY; j++ {
		for i := 0; i < g.Spec.NX; i++ {
			w := g.Intensity[j*g.Spec.NX+i]
			if w <= 0 {
				continue
			}
			x := g.Spec.X0 + (float64(i)+0.5)*g.Spec.DX
			y := g.Spec.Y0 + (float64(j)+0.5)*g.Spec.DY
			dx, dy := x-cx, y-cy
			pts = append(pts, pixelRadius{r2: dx*dx + dy*dy, w: w})
		}
	}
	sortPixels(pts)
	acc := 0.0
	for _, p := range pts {
		acc += p.w
		if acc/total >= fraction {
			return math.Sqrt(p.r2)
		}
	}
	return 0
}

func sortPixels(pts []pixelRadius) {
	sort.Slice(pts, func(i, j int) bool { return pts[i].r2 < pts[j].r2 })
}
