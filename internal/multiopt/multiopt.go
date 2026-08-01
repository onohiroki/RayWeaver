package multiopt

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/constraint"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

type ConfigInput struct {
	ID          string
	Weight      float64
	StopSurface int
	Surfaces    []types.Surface
	Fields      []types.FieldItem
	Wavelengths []types.WavelengthItem
	MeritTerms  []types.MeritTerm
	Constraints []types.ConstraintOperand
}

type VariableInfo struct {
	Name      string
	IsShared  bool
	SharedIdx int
	Bindings  []types.SharedVariableBinding
	Config    string
	Target    types.VariableTarget
	Min       float64
	Max       float64
}

type hullPair struct {
	ndIndex int
	vdIndex int
}

type MultiOptimizer struct {
	configs          []ConfigInput
	variables        []VariableInfo
	sharedVars       []types.SharedVariable
	localVars        []types.LocalVariableDef
	gc               *glass.Catalog
	glassOverrides   map[string]*types.Glass
	tempGC           *glass.Catalog
	apertureExtents  map[string]float64
	initialDiameters map[string]float64
	maxIter          int
	mu               float64
	tol              float64
	epsilon          float64
	numRays          int
	apertureMargin   float64
	muConMax         float64
	gridRotation     float64
	logger           dls.Logger
	hull             *glass.ConvexHull
	hullMargin       float64
	hullWeight       float64
	hullPairs        []hullPair
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
	Config    string  `yaml:"config,omitempty"`
	SurfaceID int     `yaml:"surface_id,omitempty"`
	Param     string  `yaml:"param"`
	Before    float64 `yaml:"before"`
	After     float64 `yaml:"after"`
}

type imagePoint struct {
	X, Y float64
	OPL  float64
	OK   bool
}

type pupilPoint struct {
	X, Y float64
}

func New(configs []ConfigInput, sharedVars []types.SharedVariable, localVars []types.LocalVariableDef, gc *glass.Catalog, maxIter int, mu, tol, epsilon, apertureMargin float64, numRays int, muConMax float64, logger dls.Logger, hull *glass.ConvexHull, hullMargin, hullWeight float64) *MultiOptimizer {
	if maxIter <= 0 {
		maxIter = 100
	}
	if tol <= 0 {
		tol = 1e-6
	}
	if epsilon <= 0 {
		epsilon = 1e-6
	}
	if numRays <= 0 {
		numRays = 64
	}
	// aperture_margin < 1.0 makes the pupil grid smaller than the aperture,
	// which clips rays at surface edges and stalls DLS convergence.
	if apertureMargin < 1.0 {
		apertureMargin = 1.0
	}

	var variables []VariableInfo
	glassOverrides := make(map[string]*types.Glass)

	for si, sv := range sharedVars {
		if !sv.Active {
			continue
		}
		name := sv.Name
		if name == "" {
			name = fmt.Sprintf("shared_%d", si)
		}
		variables = append(variables, VariableInfo{
			Name:      name,
			IsShared:  true,
			SharedIdx: si,
			Bindings:  sv.Bindings,
			Min:       sv.Min,
			Max:       sv.Max,
		})
	}

	for li, lv := range localVars {
		if !lv.Active {
			continue
		}
		name := lv.Name
		if name == "" {
			name = fmt.Sprintf("local_%d", li)
		}
		variables = append(variables, VariableInfo{
			Name:     name,
			IsShared: false,
			Config:   lv.Config,
			Target:   lv.Target,
			Min:      lv.Min,
			Max:      lv.Max,
		})

		if lv.Target.Param == "nd" || lv.Target.Param == "vd" {
			for _, cfg := range configs {
				if cfg.ID == lv.Config {
					key := resolveGlassKeyFromSurfaces(cfg.Surfaces, gc, lv.Target.ID)
					if key != "" {
						if g, ok := gc.Lookup(key); ok {
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
	}

	initialDiameters := make(map[string]float64)
	for _, cfg := range configs {
		for _, s := range cfg.Surfaces {
			if s.AutoAperture {
				initialDiameters[cfgSurfKey(cfg.ID, s.ID)] = s.Diameter
			}
		}
	}

	// Build hull pairs from local nd/vd variables
	var hullPairs []hullPair
	if hull != nil {
		ndMap := make(map[string]int)
		vdMap := make(map[string]int)
		for i, v := range variables {
			if v.IsShared {
				continue
			}
			key := v.Config + ":" + v.Target.Type + ":" + fmt.Sprintf("%d", v.Target.ID)
			switch v.Target.Param {
			case "nd":
				ndMap[key] = i
			case "vd":
				vdMap[key] = i
			}
		}
		for key, ndIdx := range ndMap {
			if vdIdx, ok := vdMap[key]; ok {
				hullPairs = append(hullPairs, hullPair{ndIndex: ndIdx, vdIndex: vdIdx})
			}
		}
	}

	return &MultiOptimizer{
		configs:          configs,
		variables:        variables,
		sharedVars:       sharedVars,
		localVars:        localVars,
		gc:               gc,
		glassOverrides:   glassOverrides,
		initialDiameters: initialDiameters,
		maxIter:          maxIter,
		mu:               mu,
		tol:              tol,
		epsilon:          epsilon,
		numRays:          numRays,
		apertureMargin:   apertureMargin,
		muConMax:         muConMax,
		logger:           logger,
		hull:             hull,
		hullMargin:       hullMargin,
		hullWeight:       hullWeight,
		hullPairs:        hullPairs,
	}
}

func (o *MultiOptimizer) Variables() []dls.VariableInfo {
	result := make([]dls.VariableInfo, len(o.variables))
	for i, v := range o.variables {
		var param string
		if v.IsShared {
			if len(v.Bindings) > 0 {
				param = v.Bindings[0].Param
			}
		} else {
			param = v.Target.Param
		}
		result[i] = dls.VariableInfo{
			Name:  v.Name,
			Param: param,
			Min:   v.Min,
			Max:   v.Max,
		}
	}
	return result
}

func (o *MultiOptimizer) Options() dls.Options {
	return dls.Options{
		MaxIter:        o.maxIter,
		Mu:             o.mu,
		Tol:            o.tol,
		Epsilon:        o.epsilon,
		NumRays:        o.numRays,
		ApertureMargin: o.apertureMargin,
		MuConMax:       o.muConMax,
		Logger:         o.logger,
	}
}

func (o *MultiOptimizer) InitialState() []float64 {
	return o.getInitialState()
}

func (o *MultiOptimizer) EvaluateMerit(x []float64) float64 {
	return o.evaluateMerit(x)
}

// MeritBreakdown evaluates the merit at x and returns the contribution of each
// merit term (and the objective total), so the value reported by DLS can be
// reconciled against an external evaluation (e.g. `chief` spot RMS).
func (o *MultiOptimizer) MeritBreakdown(x []float64) map[string]float64 {
	configSurfaces := o.applyVariables(x)
	out := make(map[string]float64)
	objTotal := 0.0
	for _, cfg := range o.configs {
		surfaces := configSurfaces[cfg.ID]
		for i := range surfaces {
			key := cfgSurfKey(cfg.ID, surfaces[i].ID)
			if d, ok := o.initialDiameters[key]; ok {
				surfaces[i].Diameter = d
			}
		}
		surface.Precompute(surfaces)
		o.sizeAutoApertures(cfg, surfaces)

		for _, term := range cfg.MeritTerms {
			fw := fieldWeightForTerm(cfg, term)
			ww := wavWeightForTerm(cfg, term)
			var contrib float64
			if term.Kind == "" || term.Kind == dls.MeritSpotRMS {
				points, _ := o.traceFieldGrid(surfaces, cfg, term)
				rms := computeSpotRMS(points)
				contrib = term.Weight * fw * ww * rms * rms
			} else {
				val := o.evaluateKindTerm(cfg, term, surfaces)
				diff := val - term.Target
				contrib = term.Weight * fw * ww * diff * diff
			}
			kind := term.Kind
			if kind == "" {
				kind = dls.MeritSpotRMS
			}
			key := fmt.Sprintf("config:%s %s(f%d,%.6f)", cfg.ID, kind, term.Field, term.Wavelength)
			out[key] = contrib
			objTotal += cfg.Weight * contrib
		}
	}
	out["objective_total"] = objTotal
	return out
}

func (o *MultiOptimizer) ComputeResiduals(x []float64) []float64 {
	return o.computeResiduals(x)
}

func (o *MultiOptimizer) Optimize() Result {
	dlsResult := dls.Solve(o)
	x := make([]float64, len(dlsResult.Variables))
	for i, vs := range dlsResult.Variables {
		x[i] = vs.After
	}
	return Result{
		BeforeMerit: dlsResult.BeforeMerit,
		AfterMerit:  dlsResult.AfterMerit,
		Iterations:  dlsResult.Iterations,
		Status:      dlsResult.Status,
		Variables:   o.buildVariableStates(x),
	}
}

func (o *MultiOptimizer) FinalApertures(x []float64) map[string]map[int]float64 {
	configSurfaces := o.applyVariables(x)
	result := make(map[string]map[int]float64)

	for _, cfg := range o.configs {
		surfaces := configSurfaces[cfg.ID]

		for i := range surfaces {
			key := cfgSurfKey(cfg.ID, surfaces[i].ID)
			if d, ok := o.initialDiameters[key]; ok {
				surfaces[i].Diameter = d
			}
		}
		surface.Precompute(surfaces)
		o.sizeAutoApertures(cfg, surfaces)

		cfgResult := make(map[int]float64)
		for i := range surfaces {
			if surfaces[i].AutoAperture {
				cfgResult[surfaces[i].ID] = surfaces[i].Diameter
			}
		}
		result[cfg.ID] = cfgResult
	}
	return result
}

func (o *MultiOptimizer) evaluateMerit(x []float64) float64 {
	configSurfaces := o.applyVariables(x)

	merit := 0.0
	for _, cfg := range o.configs {
		surfaces := configSurfaces[cfg.ID]

		for i := range surfaces {
			key := cfgSurfKey(cfg.ID, surfaces[i].ID)
			if d, ok := o.initialDiameters[key]; ok {
				surfaces[i].Diameter = d
			}
		}

		surface.Precompute(surfaces)
		o.sizeAutoApertures(cfg, surfaces)

		cfgMerit := 0.0

		for _, term := range cfg.MeritTerms {
			fw := fieldWeightForTerm(cfg, term)
			ww := wavWeightForTerm(cfg, term)
			if term.Kind == "" || term.Kind == dls.MeritSpotRMS {
				points, _ := o.traceFieldGrid(surfaces, cfg, term)
				rms := computeSpotRMS(points)
				cfgMerit += term.Weight * fw * ww * rms * rms
			} else {
				val := o.evaluateKindTerm(cfg, term, surfaces)
				diff := val - term.Target
				cfgMerit += term.Weight * fw * ww * diff * diff
			}
		}

		merit += cfg.Weight * cfgMerit
	}

	if o.hull != nil {
		for _, pair := range o.hullPairs {
			nd := x[pair.ndIndex]
			vd := x[pair.vdIndex]
			merit += o.hull.Penalty(nd, vd, o.hullMargin, o.hullWeight)
		}
	}

	return merit
}

func (o *MultiOptimizer) applyVariables(x []float64) map[string][]types.Surface {
	configSurfaces := make(map[string][]types.Surface)
	for _, cfg := range o.configs {
		s := make([]types.Surface, len(cfg.Surfaces))
		copy(s, cfg.Surfaces)
		configSurfaces[cfg.ID] = s
	}

	needTempGC := false

	for vi, v := range o.variables {
		val := x[vi]

		if v.IsShared {
			for _, b := range v.Bindings {
				surfaces, ok := configSurfaces[b.Config]
				if !ok {
					continue
				}
				idx := surfaceIndex(surfaces, b.ID)
				if idx < 0 {
					continue
				}
				scale := b.Scale
				if scale == 0 {
					scale = 1.0
				}
				applied := scale*val + b.Offset
				setParam(&surfaces[idx], b.Param, applied)
			}
		} else {
			if v.Target.Type != "surface" {
				continue
			}
			surfaces, ok := configSurfaces[v.Config]
			if !ok {
				continue
			}
			idx := surfaceIndex(surfaces, v.Target.ID)
			if idx < 0 {
				continue
			}
			setParam(&surfaces[idx], v.Target.Param, val)

			if v.Target.Param == "nd" || v.Target.Param == "vd" {
				key := resolveGlassKeyFromSurfaces(surfaces, o.gc, v.Target.ID)
				if key != "" {
					if g, ok := o.glassOverrides[key]; ok {
						switch v.Target.Param {
						case "nd":
							g.ND = val
						case "vd":
							g.VD = val
						}
						needTempGC = true
					}
				}
			}
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

	for _, cfg := range o.configs {
		surface.Precompute(configSurfaces[cfg.ID])
	}

	return configSurfaces
}

func (o *MultiOptimizer) traceFieldGrid(surfaces []types.Surface, cfg ConfigInput, term types.MeritTerm) ([]imagePoint, map[int]float64) {
	gc := o.gc
	if o.tempGC != nil {
		gc = o.tempGC
	}
	engine := ray.NewEngine(gc, nil)

	fieldAngle := o.fieldAngleForTerm(cfg, term, surfaces)
	wavelength := term.Wavelength

	path := buildPath(surfaces)

	thetaRad := fieldAngle * math.Pi / 180.0
	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)

	dx, dy := 0.0, 1.0

	rayDir := types.Vec3{X: sinT * dx, Y: sinT * dy, Z: cosT}.Normalize()

	apertureRadius := apertureRadiusForGrid(surfaces, wavelength, gc, o.apertureMargin)
	if apertureRadius <= 0 {
		return nil, nil
	}

	zStart := -100.0
	grid := generatePupilGrid(o.numRays, apertureRadius, o.gridRotation)

	stopZ := computeStopZ(surfaces, cfg.StopSurface)
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
			Wavelength:         wavelength,
			Initial:            types.RayState{Origin: origin, Direction: rayDir},
			Path:               path,
			Jones:              types.NewCircularJones(true),
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
			points = append(points, imagePoint{X: last.Position.X, Y: last.Position.Y, OPL: result.OPLTotal, OK: true})
		} else {
			points = append(points, imagePoint{OK: false})
		}
	}

	return points, perSurfMax
}

func (o *MultiOptimizer) computeResiduals(x []float64) []float64 {
	configSurfaces := o.applyVariables(x)

	var allR []float64
	for _, cfg := range o.configs {
		surfaces := configSurfaces[cfg.ID]

		for i := range surfaces {
			key := cfgSurfKey(cfg.ID, surfaces[i].ID)
			if d, ok := o.initialDiameters[key]; ok {
				surfaces[i].Diameter = d
			}
		}

		surface.Precompute(surfaces)
		o.sizeAutoApertures(cfg, surfaces)

		termR := make([]float64, len(cfg.MeritTerms))
		for ti, term := range cfg.MeritTerms {
			fw := fieldWeightForTerm(cfg, term)
			ww := wavWeightForTerm(cfg, term)
			if term.Kind == "" || term.Kind == dls.MeritSpotRMS {
				points, _ := o.traceFieldGrid(surfaces, cfg, term)
				rms := computeSpotRMS(points)
				termR[ti] = math.Sqrt(cfg.Weight*term.Weight*fw*ww) * rms
			} else {
				val := o.evaluateKindTerm(cfg, term, surfaces)
				diff := val - term.Target
				termR[ti] = math.Sqrt(cfg.Weight*term.Weight*fw*ww) * diff
			}
		}

		allR = append(allR, termR...)
	}

	if o.hull != nil {
		for _, pair := range o.hullPairs {
			nd := x[pair.ndIndex]
			vd := x[pair.vdIndex]
			allR = append(allR, o.hull.Residual(nd, vd, o.hullMargin, o.hullWeight))
		}
	}

	return allR
}

func (o *MultiOptimizer) traceFieldGridExtents(surfaces []types.Surface, cfg ConfigInput, term types.MeritTerm) map[int]float64 {
	gc := o.gc
	if o.tempGC != nil {
		gc = o.tempGC
	}
	fieldAngle := o.fieldAngleForTerm(cfg, term, surfaces)
	return dls.TraceFieldGridExtents(gc, surfaces, cfg.StopSurface, fieldAngle, []float64{0, 1}, term.Wavelength, o.apertureMargin, o.numRays, o.gridRotation)
}

// sizeAutoApertures measures the true geometric beam extent at the extreme
// field angle (ignoring aperture clipping) and sizes every AutoAperture
// surface so its diameter covers the full bundle. Callers must restore the
// initial diameters and precompute the surfaces first.
func (o *MultiOptimizer) sizeAutoApertures(cfg ConfigInput, surfaces []types.Surface) {
	extremeAngle := 0.0
	for _, term := range cfg.MeritTerms {
		angle := o.fieldAngleForTerm(cfg, term, surfaces)
		a := math.Abs(angle)
		if a > extremeAngle {
			extremeAngle = a
		}
	}
	if extremeAngle <= 0 {
		return
	}

	extents := make(map[int]float64)
	for _, term := range cfg.MeritTerms {
		if term.Kind != "" && term.Kind != dls.MeritSpotRMS {
			continue
		}
		angle := o.fieldAngleForTerm(cfg, term, surfaces)
		if math.Abs(angle) != extremeAngle {
			continue
		}
		perSurf := o.traceFieldGridExtents(surfaces, cfg, term)
		for id, e := range perSurf {
			if e > extents[id] {
				extents[id] = e
			}
		}
	}

	for id, e := range extents {
		for i := range surfaces {
			if surfaces[i].ID == id && surfaces[i].AutoAperture {
				surfaces[i].Diameter = 2 * e
			}
		}
	}
}

// ConstraintViolation reports an active constraint whose weighted residual
// magnitude exceeds a tolerance at the final state.
type ConstraintViolation struct {
	ID       string
	Config   string
	Kind     string
	Measure  string
	Residual float64
}

// FinalConstraintViolations evaluates the active constraints at x and returns
// those whose weighted residual magnitude exceeds tol.
func (o *MultiOptimizer) FinalConstraintViolations(x []float64, tol float64) []ConstraintViolation {
	configSurfaces := o.applyVariables(x)

	gc := o.gc
	if o.tempGC != nil {
		gc = o.tempGC
	}

	var out []ConstraintViolation
	for _, cfg := range o.configs {
		surfaces := configSurfaces[cfg.ID]

		for i := range surfaces {
			key := cfgSurfKey(cfg.ID, surfaces[i].ID)
			if d, ok := o.initialDiameters[key]; ok {
				surfaces[i].Diameter = d
			}
		}

		surface.Precompute(surfaces)
		o.sizeAutoApertures(cfg, surfaces)

		for _, c := range cfg.Constraints {
			if !c.Active {
				continue
			}
			angle := o.fieldAngleForTerm(cfg, types.MeritTerm{Field: c.Field}, surfaces)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.StopSurface)
			err := constraint.ComputeError(c.Kind, value, c)
			w := c.Weight
			if w <= 0 {
				w = 1.0
			}
			residual := math.Sqrt(w) * err
			if math.Abs(residual) > tol {
				out = append(out, ConstraintViolation{
					ID:       c.ID,
					Config:   cfg.ID,
					Kind:     string(c.Kind),
					Measure:  string(c.Measure),
					Residual: residual,
				})
			}
		}
	}
	return out
}

func (o *MultiOptimizer) ComputeConstraints(x []float64) []float64 {
	configSurfaces := o.applyVariables(x)

	gc := o.gc
	if o.tempGC != nil {
		gc = o.tempGC
	}

	var allC []float64
	for _, cfg := range o.configs {
		surfaces := configSurfaces[cfg.ID]

		for i := range surfaces {
			key := cfgSurfKey(cfg.ID, surfaces[i].ID)
			if d, ok := o.initialDiameters[key]; ok {
				surfaces[i].Diameter = d
			}
		}

		surface.Precompute(surfaces)
		o.sizeAutoApertures(cfg, surfaces)

		for _, c := range cfg.Constraints {
			if !c.Active {
				allC = append(allC, 0)
				continue
			}
			angle := o.fieldAngleForTerm(cfg, types.MeritTerm{Field: c.Field}, surfaces)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.StopSurface)
			err := constraint.ComputeError(c.Kind, value, c)
			w := c.Weight
			if w <= 0 {
				w = 1.0
			}
			allC = append(allC, math.Sqrt(w)*err)
		}
	}

	return allC
}

func (o *MultiOptimizer) getInitialState() []float64 {
	x := make([]float64, len(o.variables))
	for i, v := range o.variables {
		if v.IsShared {
			if v.Min == 0 && v.Max == 0 {
				v.Min, v.Max = -1, 1
			}
			// Initialize from first binding's surface value, fall back to midpoint
			x[i] = (v.Min + v.Max) / 2
			if len(v.Bindings) > 0 {
				b := v.Bindings[0]
				for _, cfg := range o.configs {
					if cfg.ID == b.Config {
						idx := surfaceIndex(cfg.Surfaces, b.ID)
						if idx >= 0 {
							v0 := getParam(cfg.Surfaces[idx], b.Param)
							if v0 != 0 {
								x[i] = v0
							}
						}
						break
					}
				}
			}
			// Clamp to bounds — the first binding's surface value may be
			// outside the declared [min, max] range for this shared variable.
			if x[i] < v.Min {
				x[i] = v.Min
			} else if x[i] > v.Max {
				x[i] = v.Max
			}
		} else {
			switch v.Target.Param {
			case "curvature", "conic", "thickness", "diameter", "a4", "a6", "a8", "a10", "a12", "coefficient_0", "coefficient_1", "coefficient_2", "coefficient_3", "coefficient_4":
				for _, cfg := range o.configs {
					if cfg.ID == v.Config {
						idx := surfaceIndex(cfg.Surfaces, v.Target.ID)
						if idx >= 0 {
							x[i] = getParam(cfg.Surfaces[idx], v.Target.Param)
						} else {
							x[i] = (v.Min + v.Max) / 2
						}
						break
					}
				}
			case "nd", "vd":
				if o.gc != nil {
					for _, cfg := range o.configs {
						if cfg.ID == v.Config {
							key := resolveGlassKeyFromSurfaces(cfg.Surfaces, o.gc, v.Target.ID)
							if key != "" {
								if g, ok := o.gc.Lookup(key); ok {
									switch v.Target.Param {
									case "nd":
										x[i] = g.ND
									case "vd":
										x[i] = g.VD
									}
								}
							}
							break
						}
					}
				}
				if x[i] == 0 {
					x[i] = (v.Min + v.Max) / 2
				}
			default:
				x[i] = (v.Min + v.Max) / 2
			}
		}
	}
	return x
}

func (o *MultiOptimizer) buildVariableStates(x []float64) []VariableState {
	states := make([]VariableState, len(o.variables))
	for i, v := range o.variables {
		st := VariableState{
			Name:   v.Name,
			Config: v.Config,
			After:  x[i],
		}

		if v.IsShared {
			if len(v.Bindings) > 0 {
				st.SurfaceID = v.Bindings[0].ID
				st.Param = v.Bindings[0].Param
			}
			for _, cfg := range o.configs {
				for _, b := range v.Bindings {
					if b.Config == cfg.ID {
						idx := surfaceIndex(cfg.Surfaces, b.ID)
						if idx >= 0 {
							st.Before = getParam(cfg.Surfaces[idx], b.Param)
						}
						break
					}
				}
				if st.Before != 0 {
					break
				}
			}
		} else {
			st.SurfaceID = v.Target.ID
			st.Param = v.Target.Param
			for _, cfg := range o.configs {
				if cfg.ID == v.Config {
					idx := surfaceIndex(cfg.Surfaces, v.Target.ID)
					if idx >= 0 {
						st.Before = getParam(cfg.Surfaces[idx], v.Target.Param)
					}
					break
				}
			}
		}

		states[i] = st
	}
	return states
}

// evaluateKindTerm evaluates a non-spot merit term (e.g. seidel_distortion,
// lateral_color, opd_rms) for the given config. Returns 0 for unknown kinds.
func (o *MultiOptimizer) evaluateKindTerm(cfg ConfigInput, term types.MeritTerm, surfaces []types.Surface) float64 {
	gc := o.gc
	if o.tempGC != nil {
		gc = o.tempGC
	}

	switch term.Kind {
	case optimize.MeritOPDRMS:
		points, _ := o.traceFieldGrid(surfaces, cfg, term)
		pts := make([]dls.IPoint, len(points))
		for i, p := range points {
			pts[i] = dls.IPoint{X: p.X, Y: p.Y, OPL: p.OPL, OK: p.OK}
		}
		return optimize.ComputeOPDRMS(pts)
	default:
		ot := optimize.MeritTerm{
			Kind:        term.Kind,
			FieldAngle:  o.fieldAngleForTerm(cfg, term, surfaces),
			FieldDir:    []float64{0, 1},
			Wavelength:  term.Wavelength,
			Wavelength2: term.Wavelength2,
			Target:      term.Target,
		}
		return optimize.EvaluateMeritKind(term.Kind, ot, surfaces, gc, nil)
	}
}

func (o *MultiOptimizer) fieldAngleForTerm(cfg ConfigInput, term types.MeritTerm, surfaces []types.Surface) float64 {
	for _, f := range cfg.Fields {
		if f.ID == term.Field {
			if f.AngleDeg != 0 || f.ImageHeight == 0 {
				return f.AngleDeg
			}
			return o.imageHeightToFieldAngle(cfg, surfaces, f.ImageHeight, term.Wavelength)
		}
	}
	return 0
}

func (o *MultiOptimizer) imageHeightToFieldAngle(cfg ConfigInput, surfaces []types.Surface, targetHeight, wavelength float64) float64 {
	gc := o.gc
	if o.tempGC != nil {
		gc = o.tempGC
	}
	path := buildPath(surfaces)
	engine := ray.NewEngine(gc, nil)

	apertureRadius := apertureRadiusForGrid(surfaces, wavelength, gc, o.apertureMargin)
	if apertureRadius <= 0 {
		return 0
	}

	pol := types.NewCircularJones(true)

	lo, hi := 0.0, 45.0
	for iter := 0; iter < 30; iter++ {
		mid := (lo + hi) / 2
		thetaRad := mid * math.Pi / 180.0
		dir := types.Vec3{X: 0, Y: math.Sin(thetaRad), Z: math.Cos(thetaRad)}.Normalize()

		origin := types.Vec3{X: 0, Y: 0, Z: -100.0}
		r := types.Ray{
			Wavelength:         wavelength,
			Initial:            types.RayState{Origin: origin, Direction: dir},
			Path:               path,
			Jones:              pol,
			SkipGlassPathCheck: mid == 0,
		}
		result := engine.TraceRay(r, surfaces)
		if result.Error != "" {
			hi = mid
			continue
		}
		if len(result.Surfaces) == 0 {
			return 0
		}
		last := result.Surfaces[len(result.Surfaces)-1]
		y := last.Position.Y

		if math.Abs(y-targetHeight) < 1e-6 {
			return mid
		}
		if y < targetHeight {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

func fieldWeightForTerm(cfg ConfigInput, term types.MeritTerm) float64 {
	for _, f := range cfg.Fields {
		if f.ID == term.Field {
			if f.Weight > 0 {
				return f.Weight
			}
			return 1.0
		}
	}
	return 1.0
}

func wavWeightForTerm(cfg ConfigInput, term types.MeritTerm) float64 {
	for _, w := range cfg.Wavelengths {
		if math.Abs(w.Value-term.Wavelength) < 1e-12 {
			if w.Weight > 0 {
				return w.Weight
			}
			return 1.0
		}
	}
	return 1.0
}

func cfgSurfKey(configID string, surfID int) string {
	return configID + ":" + fmt.Sprint(surfID)
}

// asphereCoefIndex maps an asphere coefficient parameter name (a4/a6/a8/a10/
// a12, or the array aliases coefficient_0..coefficient_4) to the index in
// types.Surface.Coefficients, which holds the h^(2i+4) coefficients.
func asphereCoefIndex(param string) (int, bool) {
	switch param {
	case "a4", "coefficient_0":
		return 0, true
	case "a6", "coefficient_1":
		return 1, true
	case "a8", "coefficient_2":
		return 2, true
	case "a10", "coefficient_3":
		return 3, true
	case "a12", "coefficient_4":
		return 4, true
	}
	return 0, false
}

func setParam(s *types.Surface, param string, val float64) {
	if idx, ok := asphereCoefIndex(param); ok {
		for len(s.Coefficients) <= idx {
			s.Coefficients = append(s.Coefficients, 0)
		}
		s.Coefficients[idx] = val
		return
	}
	switch param {
	case "curvature":
		s.Curvature = val
	case "conic":
		s.Conic = val
	case "thickness":
		s.Thickness = val
	case "diameter":
		s.Diameter = val
	case "radius":
		if val == 0 {
			s.Curvature = 0
		} else {
			s.Curvature = 1.0 / val
		}
	}
}

func getParam(s types.Surface, param string) float64 {
	if idx, ok := asphereCoefIndex(param); ok {
		if idx < len(s.Coefficients) {
			return s.Coefficients[idx]
		}
		return 0
	}
	switch param {
	case "curvature":
		return s.Curvature
	case "conic":
		return s.Conic
	case "thickness":
		return s.Thickness
	case "diameter":
		return s.Diameter
	case "radius":
		return s.Radius()
	}
	return 0
}

func resolveGlassKeyFromSurfaces(surfaces []types.Surface, gc *glass.Catalog, id int) string {
	for _, s := range surfaces {
		if s.ID == id {
			if s.Material == "" || s.Material == "AIR" {
				return ""
			}
			return s.Material
		}
	}
	return ""
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

func apertureRadiusForGrid(surfaces []types.Surface, wavelength float64, gc *glass.Catalog, margin float64) float64 {
	if r := paraxialEntranceRadius(surfaces, wavelength, gc, margin); r > 0 {
		return r
	}
	r := findFixedApertureRadius(surfaces)
	if r <= 0 {
		r = findMinApertureRadius(surfaces)
	}
	return r
}

func paraxialEntranceRadius(surfaces []types.Surface, wavelength float64, gc *glass.Catalog, margin float64) float64 {
	sys := types.System{Surfaces: surfaces}
	res := paraxial.Compute(sys, wavelength, gc, 0, nil)
	if res.EntrancePupilDiameter > 0 {
		return (res.EntrancePupilDiameter / 2) * margin
	}
	return 0
}

func generatePupilGrid(numRays int, apertureRadius float64, rotationOffset float64) []pupilPoint {
	var pts []pupilPoint
	n := int(math.Sqrt(float64(numRays)))
	if n < 2 {
		n = 2
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			r := (float64(i) + 0.5) / float64(n) * apertureRadius
			theta := 2*math.Pi*(float64(j)+0.5)/float64(n) + rotationOffset
			pts = append(pts, pupilPoint{
				X: r * math.Cos(theta),
				Y: r * math.Sin(theta),
			})
		}
	}
	return pts
}

func computeStopZ(surfaces []types.Surface, stopID int) float64 {
	if stopID <= 0 {
		stopID = findStopID(surfaces)
	}
	if stopID == 0 {
		return 0
	}
	for _, s := range surfaces {
		if s.ID == stopID {
			return s.PhysicalZ
		}
	}
	return 0
}

func findStopID(surfaces []types.Surface) int {
	stopID := 0
	minD := math.MaxFloat64
	for _, s := range surfaces {
		if !s.AutoAperture && s.Diameter > 0 && s.Diameter < minD {
			minD = s.Diameter
			stopID = s.ID
		}
	}
	if stopID != 0 {
		return stopID
	}
	minD = math.MaxFloat64
	for _, s := range surfaces {
		if s.Diameter > 0 && s.Diameter < minD {
			minD = s.Diameter
			stopID = s.ID
		}
	}
	return stopID
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
		return 1e6
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
