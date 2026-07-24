package optimize

import (
	"fmt"
	"math"
	"os"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

const (
	defaultMaxIter = 100
	defaultMu      = 1.0
	defaultTol     = 1e-6
	defaultEpsilon = 1e-6
	defaultNumRays = 64
)

type Variable struct {
	Name      string
	SurfaceID int
	GlassName string
	Param     string
	Min       float64
	Max       float64
}

type Logger interface {
	LogIter(iter int, merit, improvement, stepNorm float64, variables []float64)
	LogFinal(iter int, status string, merit float64, stepNorm float64, variables []float64)
}

type MeritTerm struct {
	FieldAngle  float64
	FieldDir    []float64
	FieldWeight float64
	Wavelength  float64
	WavWeight   float64
	Weight      float64
}

type Config struct {
	Surfaces      []types.Surface
	Variables     []Variable
	MeritTerms    []MeritTerm
	GlassCatalog  *glass.Catalog
	CoatingCatalog interface{}
	MaxIter       int
	Mu            float64
	Tol           float64
	Epsilon       float64
	NumRays       int
	Logger        Logger
}

type Result struct {
	BeforeMerit float64         `yaml:"before_merit"`
	AfterMerit  float64         `yaml:"after_merit"`
	Iterations  int             `yaml:"iterations"`
	Status      string          `yaml:"status"`
	Variables   []VariableState `yaml:"variables"`
}

type VariableState struct {
	Name      string  `yaml:"name,omitempty"`
	SurfaceID int     `yaml:"surface_id,omitempty"`
	GlassName string  `yaml:"glass_name,omitempty"`
	Param     string  `yaml:"param"`
	Before    float64 `yaml:"before"`
	After     float64 `yaml:"after"`
}

type imagePoint struct {
	X, Y float64
	OK   bool
}

type pupilPoint struct {
	X, Y float64
}

type Optimizer struct {
	surfaces        []types.Surface
	variables       []Variable
	meritTerms      []MeritTerm
	gc              *glass.Catalog
	glassOverrides  map[string]*types.Glass
	tempGC          *glass.Catalog
	apertureExtents map[int]float64
	maxIter         int
	mu              float64
	tol             float64
	epsilon         float64
	numRays         int
	logger          Logger
}

func NewOptimizer(cfg Config) *Optimizer {
	maxIter := cfg.MaxIter
	if maxIter <= 0 {
		maxIter = defaultMaxIter
	}
	mu := cfg.Mu
	if mu <= 0 {
		mu = defaultMu
	}
	tol := cfg.Tol
	if tol <= 0 {
		tol = defaultTol
	}
	epsilon := cfg.Epsilon
	if epsilon <= 0 {
		epsilon = defaultEpsilon
	}
	numRays := cfg.NumRays
	if numRays <= 0 {
		numRays = defaultNumRays
	}

	glassOverrides := make(map[string]*types.Glass)
	if cfg.GlassCatalog != nil {
		for i := range cfg.Variables {
			v := &cfg.Variables[i]
			if v.Param == "nd" || v.Param == "vd" {
				key := resolveGlassKeyFromSurface(cfg.Surfaces, cfg.GlassCatalog, *v)
				v.GlassName = key
				if key != "" {
					if g, ok := cfg.GlassCatalog.Lookup(key); ok {
						cp := *g
						cp.Label = key
						if cp.Type == types.GlassTypeCatalog {
							cp.Type = types.GlassTypeModel
							cp.DispersionFormula = ""
						}
						glassOverrides[key] = &cp
					}
				}
			}
		}
	}

	return &Optimizer{
		surfaces:       cfg.Surfaces,
		variables:      cfg.Variables,
		meritTerms:     cfg.MeritTerms,
		gc:             cfg.GlassCatalog,
		glassOverrides: glassOverrides,
		maxIter:        maxIter,
		mu:             mu,
		tol:            tol,
		epsilon:        epsilon,
		numRays:        numRays,
		logger:         cfg.Logger,
	}
}

func resolveGlassKeyFromSurface(surfaces []types.Surface, gc *glass.Catalog, v Variable) string {
	for _, s := range surfaces {
		if s.ID == v.SurfaceID {
			if s.Material == "" || s.Material == "AIR" {
				return ""
			}
			return s.Material
		}
	}
	return ""
}

func (o *Optimizer) Optimize() Result {
	x := o.getInitialState()
	merit := o.evaluateMerit(x)
	beforeMerit := merit

	mu := o.mu
	nVars := len(x)

	bestX := make([]float64, nVars)
	copy(bestX, x)
	bestMerit := merit

	var lastDelta []float64

	for iter := 0; iter < o.maxIter; iter++ {
		J, r := o.computeJacobianAndResiduals(x)

		g := make([]float64, nVars)
		for j := 0; j < nVars; j++ {
			sum := 0.0
			for i := 0; i < len(r); i++ {
				sum += J[i][j] * r[i]
			}
			g[j] = sum
		}

		H := make([][]float64, nVars)
		for j := 0; j < nVars; j++ {
			H[j] = make([]float64, nVars)
			for k := 0; k < nVars; k++ {
				sum := 0.0
				for i := 0; i < len(r); i++ {
					sum += J[i][j] * J[i][k]
				}
				H[j][k] = sum
			}
		}
		for j := 0; j < nVars; j++ {
			H[j][j] += mu
		}

		negG := make([]float64, nVars)
		for j := 0; j < nVars; j++ {
			negG[j] = -g[j]
		}

		delta := solveLinearSystem(H, negG)
		if delta == nil {
			mu *= 2.0
			continue
		}

		xNew := make([]float64, nVars)
		for j := 0; j < nVars; j++ {
			xNew[j] = x[j] + delta[j]
		}
		projectOntoBox(xNew, o.variables)

		meritNew := o.evaluateMerit(xNew)
		actualReduction := merit - meritNew

		predictedReduction := 0.0
		for i := 0; i < len(r); i++ {
			sum := 0.0
			for j := 0; j < nVars; j++ {
				sum += J[i][j] * delta[j]
			}
			predictedReduction += r[i] * sum
		}
		halfDeltaHDelta := 0.0
		for j := 0; j < nVars; j++ {
			sum := 0.0
			for k := 0; k < nVars; k++ {
				sum += H[j][k] * delta[k]
			}
			halfDeltaHDelta += delta[j] * sum
		}
		predictedReduction -= 0.5 * halfDeltaHDelta

		rho := 1.0
		if predictedReduction > 1e-20 {
			rho = actualReduction / predictedReduction
		} else if predictedReduction < -1e-20 {
			rho = -1.0
		}

		if rho > 0.25 {
			mu *= math.Max(1.0/3.0, 1.0-(2.0*rho-1.0)*(2.0*rho-1.0)*(2.0*rho-1.0))
		} else {
			mu *= 2.0
		}

		if actualReduction > 0 {
			copy(x, xNew)
			merit = meritNew
			if merit < bestMerit {
				bestMerit = merit
				copy(bestX, x)
			}
		}

		o.applyApertureExtents()

		norm := 0.0
		for _, d := range delta {
			norm += d * d
		}
		stepNorm := math.Sqrt(norm)
		lastDelta = delta

		improvement := merit - meritNew
		if o.logger != nil {
			currVars := make([]float64, len(o.variables))
			for i, v := range o.variables {
				currVars[i] = currentVariableValue(o, v)
			}
			o.logger.LogIter(iter+1, merit, improvement, stepNorm, currVars)
		}

		if math.Sqrt(norm) < o.tol {
			if o.logger != nil {
				finalVars := o.buildVariableStates(bestX)
				vars := make([]float64, len(finalVars))
				for i, s := range finalVars {
					vars[i] = s.After
				}
				o.logger.LogFinal(iter+1, "converged", bestMerit, stepNorm, vars)
			}
			return Result{
				BeforeMerit: beforeMerit,
				AfterMerit:  bestMerit,
				Iterations:  iter + 1,
				Status:      "converged",
				Variables:   o.buildVariableStates(bestX),
			}
		}
	}

	_ = lastDelta

	if o.logger != nil {
		finalVars := o.buildVariableStates(bestX)
		vars := make([]float64, len(finalVars))
		for i, s := range finalVars {
			vars[i] = s.After
		}
		finalStepNorm := 0.0
		if lastDelta != nil {
			for _, d := range lastDelta {
				finalStepNorm += d * d
			}
			finalStepNorm = math.Sqrt(finalStepNorm)
		}
		o.logger.LogFinal(o.maxIter, "max_iterations", bestMerit, finalStepNorm, vars)
	}

	return Result{
		BeforeMerit: beforeMerit,
		AfterMerit:  bestMerit,
		Iterations:  o.maxIter,
		Status:      "max_iterations",
		Variables:   o.buildVariableStates(bestX),
	}
}

func (o *Optimizer) evaluateMerit(x []float64) float64 {
	surfaces := o.applyVariables(x)
	onAxisFloor := make(map[int]float64)
	offAxisMax := make(map[int]float64)
	merit := 0.0

	for _, term := range o.meritTerms {
		points, extents := o.traceFieldGrid(surfaces, term.FieldAngle, term.FieldDir, term.Wavelength)
		if term.FieldAngle == 0 {
			for id, e := range extents {
				if e > onAxisFloor[id] {
					onAxisFloor[id] = e
				}
			}
		} else {
			for id, e := range extents {
				if e > offAxisMax[id] {
					offAxisMax[id] = e
				}
			}
		}
		rms := computeSpotRMS(points)
		merit += term.Weight * term.FieldWeight * term.WavWeight * rms * rms
	}

	o.apertureExtents = make(map[int]float64, len(offAxisMax))
	for id, e := range offAxisMax {
		floor := onAxisFloor[id]
		if e >= floor {
			o.apertureExtents[id] = e
		} else {
			o.apertureExtents[id] = floor
		}
	}
	for id, e := range onAxisFloor {
		if _, ok := o.apertureExtents[id]; !ok {
			o.apertureExtents[id] = e
		}
	}
	return merit
}

func (o *Optimizer) applyVariables(x []float64) []types.Surface {
	result := make([]types.Surface, len(o.surfaces))
	copy(result, o.surfaces)

	needTempGC := false
	for i, v := range o.variables {
		switch v.Param {
		case "curvature", "thickness":
			idx := surfaceIndex(result, v.SurfaceID)
			if idx < 0 {
				continue
			}
			switch v.Param {
			case "curvature":
				result[idx].Curvature = x[i]
			case "thickness":
				result[idx].Thickness = x[i]
			}
		case "nd", "vd":
			g, ok := o.glassOverrides[v.GlassName]
			if !ok {
				continue
			}
			switch v.Param {
			case "nd":
				g.ND = x[i]
			case "vd":
				g.VD = x[i]
			}
			needTempGC = true
		}
	}

	if needTempGC {
		o.tempGC = glass.NewCatalog()
		for _, g := range o.gc.ByName {
			cp := *g
			o.tempGC.Add(cp)
		}
		for _, ov := range o.glassOverrides {
			cp := *ov
			o.tempGC.Add(cp)
		}
	} else {
		o.tempGC = nil
	}

	surface.Precompute(result)
	return result
}

func (o *Optimizer) traceFieldGrid(surfaces []types.Surface, fieldAngle float64, fieldDir []float64, wavelength float64) ([]imagePoint, map[int]float64) {
	gc := o.gc
	if o.tempGC != nil {
		gc = o.tempGC
	}
	engine := ray.NewEngine(gc, nil)
	p := buildPath(surfaces)

	thetaRad := fieldAngle * math.Pi / 180.0
	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)

	dx, dy := 0.0, 1.0
	if len(fieldDir) >= 2 {
		norm := math.Hypot(fieldDir[0], fieldDir[1])
		if norm > 0 {
			dx = fieldDir[0] / norm
			dy = fieldDir[1] / norm
		}
	}

	rayDir := types.Vec3{X: sinT * dx, Y: sinT * dy, Z: cosT}.Normalize()

	apertureRadius := findFixedApertureRadius(surfaces)
	if apertureRadius <= 0 {
		apertureRadius = findMinApertureRadius(surfaces)
	}
	if apertureRadius <= 0 {
		return nil, nil
	}

	zStart := -100.0
	grid := generatePupilGrid(o.numRays, apertureRadius)

	stopZ := computeStopZ(surfaces)
	tanComponent := math.Sqrt(rayDir.X*rayDir.X + rayDir.Y*rayDir.Y)
	if rayDir.Z > 1e-12 && tanComponent > 1e-12 {
		tanComponent /= rayDir.Z
		pupilOffsetX := -(stopZ - zStart) * tanComponent * dx
		pupilOffsetY := -(stopZ - zStart) * tanComponent * dy
		for i := range grid {
			grid[i].X += pupilOffsetX
			grid[i].Y += pupilOffsetY
		}
	}

	perSurfMax := make(map[int]float64)
	var points []imagePoint
	for _, pt := range grid {
		origin := types.Vec3{X: pt.X, Y: pt.Y, Z: zStart}
		r := types.Ray{
			Wavelength:        wavelength,
			Initial:           types.RayState{Origin: origin, Direction: rayDir},
			Path:              p,
			Jones:             types.NewCircularJones(true),
			SkipGlassPathCheck: fieldAngle == 0,
		}

		result := engine.TraceRay(r, surfaces)
		if result.Error != "" {
			points = append(points, imagePoint{OK: false})
			continue
		}

		for _, sr := range result.Surfaces {
			ax := math.Abs(sr.Position.X)
			ay := math.Abs(sr.Position.Y)
			e := ax
			if ay > e {
				e = ay
			}
			if e > perSurfMax[sr.SurfaceID] {
				perSurfMax[sr.SurfaceID] = e
			}
		}

		if len(result.Surfaces) > 0 {
			last := result.Surfaces[len(result.Surfaces)-1]
			points = append(points, imagePoint{X: last.Position.X, Y: last.Position.Y, OK: true})
		} else {
			points = append(points, imagePoint{OK: false})
		}
	}

	return points, perSurfMax
}

func computeSpotRMS(points []imagePoint) float64 {
	var sumX, sumY float64
	var count int

	for _, p := range points {
		if !p.OK {
			continue
		}
		sumX += p.X
		sumY += p.Y
		count++
	}

	if count == 0 {
		return math.Inf(1)
	}

	cx := sumX / float64(count)
	cy := sumY / float64(count)

	var sumSq float64
	for _, p := range points {
		if !p.OK {
			continue
		}
		dx := p.X - cx
		dy := p.Y - cy
		sumSq += dx*dx + dy*dy
	}

	return math.Sqrt(sumSq / float64(count))
}

func sanitizeFloat64(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func (o *Optimizer) computeJacobianAndResiduals(x []float64) ([][]float64, []float64) {
	nTerms := len(o.meritTerms)
	nVars := len(x)

	for j := 0; j < nVars; j++ {
		x[j] = sanitizeFloat64(x[j])
	}

	r0 := o.computeResiduals(x)

	J := make([][]float64, nTerms)
	for i := 0; i < nTerms; i++ {
		J[i] = make([]float64, nVars)
	}

	for j := 0; j < nVars; j++ {
		xPert := make([]float64, nVars)
		copy(xPert, x)
		xPert[j] += o.epsilon

		rPert := o.computeResiduals(xPert)

		for i := 0; i < nTerms; i++ {
			diff := rPert[i] - r0[i]
			J[i][j] = sanitizeFloat64(diff / o.epsilon)
		}
	}

	return J, r0
}

func (o *Optimizer) computeResiduals(x []float64) []float64 {
	surfaces := o.applyVariables(x)
	r := make([]float64, len(o.meritTerms))

	for i, term := range o.meritTerms {
		points, _ := o.traceFieldGrid(surfaces, term.FieldAngle, term.FieldDir, term.Wavelength)
		rms := computeSpotRMS(points)
		r[i] = math.Sqrt(term.Weight*term.FieldWeight*term.WavWeight) * rms
	}

	return r
}

func (o *Optimizer) getInitialState() []float64 {
	x := make([]float64, len(o.variables))
	for i, v := range o.variables {
		switch v.Param {
		case "curvature", "thickness":
			idx := surfaceIndex(o.surfaces, v.SurfaceID)
			if idx < 0 {
				x[i] = (v.Min + v.Max) / 2
				continue
			}
			switch v.Param {
			case "curvature":
				x[i] = o.surfaces[idx].Curvature
			case "thickness":
				x[i] = o.surfaces[idx].Thickness
			}
		case "nd", "vd":
			if o.gc == nil || v.GlassName == "" {
				x[i] = (v.Min + v.Max) / 2
				continue
			}
			if g, ok := o.gc.Lookup(v.GlassName); ok {
				switch v.Param {
				case "nd":
					x[i] = g.ND
				case "vd":
					x[i] = g.VD
				}
			} else {
				x[i] = (v.Min + v.Max) / 2
			}
		default:
			x[i] = (v.Min + v.Max) / 2
		}
	}
	return x
}

func (o *Optimizer) buildVariableStates(x []float64) []VariableState {
	states := make([]VariableState, len(o.variables))
	for i, v := range o.variables {
		st := VariableState{
			Name:      v.Name,
			SurfaceID: v.SurfaceID,
			GlassName: v.GlassName,
			Param:     v.Param,
			After:     x[i],
		}

		switch v.Param {
		case "curvature", "thickness":
			if idx := surfaceIndex(o.surfaces, v.SurfaceID); idx >= 0 {
				switch v.Param {
				case "curvature":
					st.Before = o.surfaces[idx].Curvature
				case "thickness":
					st.Before = o.surfaces[idx].Thickness
				}
			}
		case "nd", "vd":
			if o.gc != nil && v.GlassName != "" {
				if g, ok := o.gc.Lookup(v.GlassName); ok {
					switch v.Param {
					case "nd":
						st.Before = g.ND
					case "vd":
						st.Before = g.VD
					}
				}
			}
		}

		states[i] = st
	}
	return states
}

func solveLinearSystem(H [][]float64, g []float64) []float64 {
	n := len(g)
	aug := make([][]float64, n)
	for i := 0; i < n; i++ {
		aug[i] = make([]float64, n+1)
		for j := 0; j < n; j++ {
			aug[i][j] = sanitizeFloat64(H[i][j])
		}
		aug[i][n] = sanitizeFloat64(g[i])
	}

	for col := 0; col < n; col++ {
		maxVal := math.Abs(aug[col][col])
		maxRow := col
		for row := col + 1; row < n; row++ {
			if math.Abs(aug[row][col]) > maxVal {
				maxVal = math.Abs(aug[row][col])
				maxRow = row
			}
		}
		aug[col], aug[maxRow] = aug[maxRow], aug[col]

		if math.Abs(aug[col][col]) < 1e-15 {
			return nil
		}

		for row := col + 1; row < n; row++ {
			factor := aug[row][col] / aug[col][col]
			for j := col; j <= n; j++ {
				aug[row][j] -= factor * aug[col][j]
			}
		}
	}

	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		x[i] = aug[i][n]
		for j := i + 1; j < n; j++ {
			x[i] -= aug[i][j] * x[j]
		}
		x[i] /= aug[i][i]
	}
	return x
}

func projectOntoBox(x []float64, variables []Variable) {
	for i, v := range variables {
		x[i] = sanitizeFloat64(x[i])
		if x[i] < v.Min {
			x[i] = v.Min
		} else if x[i] > v.Max {
			x[i] = v.Max
		}
	}
}

func buildPath(surfaces []types.Surface) []int {
	path := []int{0}
	for _, s := range surfaces {
		if s.ID > 0 {
			path = append(path, s.ID)
		}
	}
	return path
}

func findFixedApertureRadius(surfaces []types.Surface) float64 {
	minR := math.MaxFloat64
	for _, s := range surfaces {
		if !s.AutoAperture && s.Diameter > 0 && s.Diameter/2 < minR {
			minR = s.Diameter / 2
		}
	}
	if minR == math.MaxFloat64 {
		return 0
	}
	return minR
}

func findMinApertureRadius(surfaces []types.Surface) float64 {
	minR := math.MaxFloat64
	for _, s := range surfaces {
		if s.Diameter > 0 && s.Diameter/2 < minR {
			minR = s.Diameter / 2
		}
	}
	if minR == math.MaxFloat64 {
		return 0
	}
	return minR
}

func surfaceIndex(surfaces []types.Surface, id int) int {
	for i, s := range surfaces {
		if s.ID == id {
			return i
		}
	}
	return -1
}

func generatePupilGrid(numRays int, apertureRadius float64) []pupilPoint {
	var pts []pupilPoint
	n := int(math.Sqrt(float64(numRays)))
	if n < 2 {
		n = 2
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			r := (float64(i) + 0.5) / float64(n) * apertureRadius
			theta := 2 * math.Pi * (float64(j) + 0.5) / float64(n)
			pts = append(pts, pupilPoint{
				X: r * math.Cos(theta),
				Y: r * math.Sin(theta),
			})
		}
	}
	return pts
}

func computeStopZ(surfaces []types.Surface) float64 {
	stopID := 0
	minD := math.MaxFloat64
	for _, s := range surfaces {
		if s.Diameter > 0 && s.Diameter < minD {
			minD = s.Diameter
			stopID = s.ID
		}
	}
	if stopID == 0 {
		return 0
	}
	z := 0.0
	for _, s := range surfaces {
		if s.ID == stopID {
			return z
		}
		z += s.Thickness
	}
	return 0
}

func (o *Optimizer) applyApertureExtents() {
	for id, extent := range o.apertureExtents {
		for i := range o.surfaces {
			if o.surfaces[i].ID == id && o.surfaces[i].AutoAperture {
				o.surfaces[i].Diameter = 2 * extent
			}
		}
	}
}

func currentVariableValue(o *Optimizer, v Variable) float64 {
	switch v.Param {
	case "curvature":
		if idx := surfaceIndex(o.surfaces, v.SurfaceID); idx >= 0 {
			return o.surfaces[idx].Curvature
		}
	case "thickness":
		if idx := surfaceIndex(o.surfaces, v.SurfaceID); idx >= 0 {
			return o.surfaces[idx].Thickness
		}
	case "nd":
		if g, ok := o.glassOverrides[v.GlassName]; ok {
			return g.ND
		}
	case "vd":
		if g, ok := o.glassOverrides[v.GlassName]; ok {
			return g.VD
		}
	}
	return (v.Min + v.Max) / 2
}

func Debugf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
}
