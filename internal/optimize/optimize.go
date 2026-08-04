package optimize

import (
	"fmt"
	"math"
	"os"
	"runtime"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/constraint"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// Config is the single-configuration optimisation input. It is an adapter
// over the unified Optimizer: the configuration becomes config "config1" and
// the variables become local variables of that config.
type Config struct {
	Surfaces       []types.Surface
	Variables      []Variable
	MeritTerms     []MeritTerm
	Fields         []types.FieldItem
	Constraints    []types.ConstraintOperand
	GlassCatalog   *glass.Catalog
	CoatingCatalog interface{}
	StopSurface    int
	RefSurface     int
	PupilZ         float64
	MaxIter        int
	Mu             float64
	Tol            float64
	Epsilon        float64
	NumRays        int
	ApertureMargin float64
	MuConMax       float64
	Workers        int
	Logger         dls.Logger
	Hull           *glass.ConvexHull
	HullMargin     float64
	HullWeight     float64
}

// ConfigInput describes one configuration (zoom position) of a
// multi-configuration optimisation.
type ConfigInput struct {
	ID          string
	Weight      float64
	StopSurface int
	RefSurface  int
	PupilZ      float64
	Surfaces    []types.Surface
	Fields      []types.FieldItem
	Wavelengths []types.WavelengthItem
	MeritTerms  []types.MeritTerm
	Constraints []types.ConstraintOperand
}

// Variable is one optimisation variable. It binds one component of the
// variable vector to either:
//   - a single surface parameter of one config (Config, SurfaceID, Param), or
//   - many surface parameters across configs via scale/offset bindings
//     (IsShared + Bindings).
type Variable struct {
	Name      string
	SurfaceID int
	GlassName string
	Param     string
	Min       float64
	Max       float64
	IsShared  bool
	Bindings  []types.SharedVariableBinding
	Config    string
}

// MeritTerm is a pre-resolved single-config merit term (weights and field
// angle already matched against the fields/wavelengths).
type MeritTerm struct {
	Kind        string
	FieldAngle  float64
	FieldDir    []float64
	FieldWeight float64
	Wavelength  float64
	Wavelength2 float64
	WavWeight   float64
	Weight      float64
	Target      float64
}

type Result struct {
	BeforeMerit float64
	AfterMerit  float64
	Iterations  int
	Status      string
	Variables   []VariableState
}

type VariableState struct {
	Name      string
	Config    string
	SurfaceID int
	GlassName string
	Param     string
	Before    float64
	After     float64
}

type glassPair struct {
	ndIndex int
	vdIndex int
	name    string
}

// config is the internal per-configuration state of the unified Optimizer.
type config struct {
	id          string
	weight      float64
	stopSurface int
	refSurface  int
	pupilZ      float64
	fieldDefs   []types.FieldDef
	surfaces    []types.Surface
	fields      []types.FieldItem
	wavelengths []types.WavelengthItem
	meritTerms  []meritTerm
	constraints []types.ConstraintOperand
}

// meritTerm is the unified merit term: weights and (for angle fields) the
// field angle are resolved at construction; image-height fields are converted
// to an angle per evaluation because they depend on the current surfaces.
type meritTerm struct {
	kind           string
	fieldAngle     float64
	useImageHeight bool
	imageHeight    float64
	fieldWeight    float64
	wavelength     float64
	wavelength2    float64
	wavWeight      float64
	weight         float64
	target         float64
}

// resolvePupilZ returns the pupil Z used to centre grid traces for a config:
// the explicit stop surface Z when one is set, otherwise the caller-provided
// dynamic pupil Z. The stop surface is never inferred.
func resolvePupilZ(surfaces []types.Surface, stopSurface int, pupilZ float64) float64 {
	if stopSurface > 0 {
		sc := append([]types.Surface{}, surfaces...)
		surface.Precompute(sc)
		for _, s := range sc {
			if s.ID == stopSurface {
				return s.PhysicalZ
			}
		}
	}
	return pupilZ
}

// fieldDefsFromItems converts the per-config field items into chief field
// definitions for the dynamic-pupil pass.
func fieldDefsFromItems(items []types.FieldItem) []types.FieldDef {
	if len(items) == 0 {
		return nil
	}
	out := make([]types.FieldDef, len(items))
	for i, f := range items {
		out[i] = types.FieldDef{
			Angle:       f.AngleDeg,
			ImageHeight: f.ImageHeight,
			Direction:   []float64{0, 1},
		}
	}
	return out
}

// UpdatePupils re-derives each config's dynamic entrance pupil at the current
// variable state x and stores it as the grid centring for the rest of the DLS
// iteration (the solver calls it once per iteration). The pupil therefore
// follows the lens during optimisation — the aperture position moves — while
// staying frozen within one iteration so the base-point and Jacobian residual
// evaluations share the same grid centring.
func (o *Optimizer) UpdatePupils(x []float64) {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)
	pol := types.NewCircularJones(true)

	for ci := range o.configs {
		cfg := &o.configs[ci]
		if cfg.refSurface <= 0 || len(cfg.fieldDefs) == 0 {
			continue
		}
		surfaces := configSurfaces[cfg.id]
		results := chief.DetermineChiefRaysGrid(
			types.System{Surfaces: surfaces, StopSurface: cfg.stopSurface},
			cfg.fieldDefs, cfg.refSurface, o.numRays, gc, pol,
			types.DefaultWavelength, false, types.GridPolar, nil, nil, nil,
		)
		for _, r := range results {
			if r.EntrancePupil != nil {
				cfg.pupilZ = r.EntrancePupil.Center.Z
				break
			}
		}
	}
}

type Optimizer struct {
	configs          []config
	variables        []Variable
	gc               *glass.Catalog
	glassOverrides   map[string]*types.Glass
	initialDiameters map[string]float64
	maxIter          int
	mu               float64
	tol              float64
	epsilon          float64
	numRays          int
	apertureMargin   float64
	muConMax         float64
	workers          int
	gridRotation     float64
	logger           dls.Logger
	hull             *glass.ConvexHull
	hullMargin       float64
	hullWeight       float64
	hullPairs        []glassPair
}

// NewOptimizer builds a single-configuration Optimizer (backward-compatible
// entry point).
func NewOptimizer(cfg Config) *Optimizer {
	c := config{
		id:          "config1",
		weight:      1.0,
		stopSurface: cfg.StopSurface,
		refSurface:  cfg.RefSurface,
		pupilZ:      resolvePupilZ(cfg.Surfaces, cfg.StopSurface, cfg.PupilZ),
		fieldDefs:   fieldDefsFromItems(cfg.Fields),
		surfaces:    cfg.Surfaces,
		fields:      cfg.Fields,
		constraints: cfg.Constraints,
	}
	for _, t := range cfg.MeritTerms {
		c.meritTerms = append(c.meritTerms, meritTerm{
			kind:        t.Kind,
			fieldAngle:  t.FieldAngle,
			fieldWeight: t.FieldWeight,
			wavelength:  t.Wavelength,
			wavelength2: t.Wavelength2,
			wavWeight:   t.WavWeight,
			weight:      t.Weight,
			target:      t.Target,
		})
	}
	variables := make([]Variable, len(cfg.Variables))
	for i, v := range cfg.Variables {
		variables[i] = Variable{
			Name:      v.Name,
			SurfaceID: v.SurfaceID,
			GlassName: v.GlassName,
			Param:     v.Param,
			Min:       v.Min,
			Max:       v.Max,
			Config:    "config1",
		}
	}
	return newOptimizer(
		[]config{c}, variables, cfg.GlassCatalog,
		cfg.MaxIter, cfg.Mu, cfg.Tol, cfg.Epsilon, cfg.ApertureMargin, cfg.NumRays,
		cfg.MuConMax, cfg.Workers, cfg.Logger, cfg.Hull, cfg.HullMargin, cfg.HullWeight,
	)
}

// NewMultiOptimizer builds a unified Optimizer over one or more configs,
// with shared variables (one x driving many bindings) and local variables
// (one x driving a single surface of one config).
func NewMultiOptimizer(configs []ConfigInput, sharedVars []types.SharedVariable, localVars []types.LocalVariableDef, gc *glass.Catalog, maxIter int, mu, tol, epsilon, apertureMargin float64, numRays int, muConMax float64, workers int, logger dls.Logger, hull *glass.ConvexHull, hullMargin, hullWeight float64) *Optimizer {
	internal := make([]config, len(configs))
	for i, ci := range configs {
		c := config{
			id:          ci.ID,
			weight:      ci.Weight,
			stopSurface: ci.StopSurface,
			refSurface:  ci.RefSurface,
			pupilZ:      resolvePupilZ(ci.Surfaces, ci.StopSurface, ci.PupilZ),
			fieldDefs:   fieldDefsFromItems(ci.Fields),
			surfaces:    ci.Surfaces,
			fields:      ci.Fields,
			wavelengths: ci.Wavelengths,
			constraints: ci.Constraints,
		}
		for _, t := range ci.MeritTerms {
			c.meritTerms = append(c.meritTerms, buildMeritTermFromTypes(t, ci))
		}
		internal[i] = c
	}

	var variables []Variable
	for si, sv := range sharedVars {
		if !sv.Active {
			continue
		}
		name := sv.Name
		if name == "" {
			name = fmt.Sprintf("shared_%d", si)
		}
		variables = append(variables, Variable{
			Name:     name,
			IsShared: true,
			Bindings: sv.Bindings,
			Min:      sv.Min,
			Max:      sv.Max,
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
		variables = append(variables, Variable{
			Name:      name,
			SurfaceID: lv.Target.ID,
			Param:     lv.Target.Param,
			Min:       lv.Min,
			Max:       lv.Max,
			Config:    lv.Config,
		})
	}

	return newOptimizer(
		internal, variables, gc,
		maxIter, mu, tol, epsilon, apertureMargin, numRays, muConMax,
		workers, logger, hull, hullMargin, hullWeight,
	)
}

func buildMeritTermFromTypes(t types.MeritTerm, ci ConfigInput) meritTerm {
	mt := meritTerm{
		kind:        t.Kind,
		wavelength:  t.Wavelength,
		wavelength2: t.Wavelength2,
		weight:      t.Weight,
		target:      t.Target,
		fieldWeight: 1.0,
		wavWeight:   1.0,
	}
	for _, f := range ci.Fields {
		if f.ID == t.Field {
			if f.AngleDeg != 0 || f.ImageHeight == 0 {
				mt.fieldAngle = f.AngleDeg
			} else {
				mt.useImageHeight = true
				mt.imageHeight = f.ImageHeight
			}
			if f.Weight > 0 {
				mt.fieldWeight = f.Weight
			}
			break
		}
	}
	for _, w := range ci.Wavelengths {
		if math.Abs(w.Value-t.Wavelength) < 1e-12 {
			if w.Weight > 0 {
				mt.wavWeight = w.Weight
			}
			break
		}
	}
	return mt
}

func newOptimizer(configs []config, variables []Variable, gc *glass.Catalog, maxIter int, mu, tol, epsilon, apertureMargin float64, numRays int, muConMax float64, workers int, logger dls.Logger, hull *glass.ConvexHull, hullMargin, hullWeight float64) *Optimizer {
	if maxIter <= 0 {
		maxIter = 100
	}
	if mu <= 0 {
		mu = 1.0
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
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}

	// Config weights default to 1.0 so a single-config YAML that omits
	// configs[].weight still contributes its full merit.
	for i := range configs {
		if configs[i].weight <= 0 {
			configs[i].weight = 1.0
		}
	}

	variablesCopy := make([]Variable, len(variables))
	copy(variablesCopy, variables)

	glassOverrides := make(map[string]*types.Glass)
	for i := range variablesCopy {
		v := &variablesCopy[i]
		if v.IsShared {
			continue
		}
		if v.Param == "nd" || v.Param == "vd" {
			cfg := findConfigByID(configs, v.Config)
			if cfg == nil {
				continue
			}
			key := resolveGlassKeyFromSurface(cfg.surfaces, gc, v.SurfaceID)
			v.GlassName = key
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

	initialDiameters := make(map[string]float64)
	for ci := range configs {
		cfg := &configs[ci]
		for _, s := range cfg.surfaces {
			if s.AutoAperture {
				initialDiameters[cfgSurfKey(cfg.id, s.ID)] = s.Diameter
			}
		}
	}

	hullPairs := buildHullPairs(variablesCopy)

	return &Optimizer{
		configs:          configs,
		variables:        variablesCopy,
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
		workers:          workers,
		logger:           logger,
		hull:             hull,
		hullMargin:       hullMargin,
		hullWeight:       hullWeight,
		hullPairs:        hullPairs,
	}
}

func findConfigByID(configs []config, id string) *config {
	for i := range configs {
		if configs[i].id == id {
			return &configs[i]
		}
	}
	return nil
}

// primaryConfig returns the first config, used by the public single-term
// evaluator which has no config context.
func (o *Optimizer) primaryConfig() *config {
	if len(o.configs) == 0 {
		return &config{}
	}
	return &o.configs[0]
}

func (o *Optimizer) Variables() []dls.VariableInfo {
	result := make([]dls.VariableInfo, len(o.variables))
	for i, v := range o.variables {
		var param string
		if v.IsShared {
			if len(v.Bindings) > 0 {
				param = v.Bindings[0].Param
			}
		} else {
			param = v.Param
		}
		result[i] = dls.VariableInfo{
			Name:      v.Name,
			SurfaceID: v.SurfaceID,
			GlassName: v.GlassName,
			Param:     param,
			Min:       v.Min,
			Max:       v.Max,
		}
	}
	return result
}

func (o *Optimizer) Options() dls.Options {
	return dls.Options{
		MaxIter:        o.maxIter,
		Mu:             o.mu,
		Tol:            o.tol,
		Epsilon:        o.epsilon,
		NumRays:        o.numRays,
		ApertureMargin: o.apertureMargin,
		MuConMax:       o.muConMax,
		Workers:        o.workers,
		Logger:         o.logger,
	}
}

func (o *Optimizer) InitialState() []float64 {
	return o.getInitialState()
}

// effectiveGC returns the glass catalog reflecting any in-flight nd/vd
// overrides (tempGC), falling back to the base catalog.
func effectiveGC(base, temp *glass.Catalog) *glass.Catalog {
	if temp != nil {
		return temp
	}
	return base
}

// applyVariables maps the variable vector x onto the surfaces of every config
// and returns the updated surfaces plus the glass catalog reflecting any
// in-flight nd/vd overrides (nil when no override is active). It is pure: no
// Optimizer state is mutated, so the DLS Jacobian loop can be parallelised.
func (o *Optimizer) applyVariables(x []float64) (map[string][]types.Surface, *glass.Catalog) {
	configSurfaces := make(map[string][]types.Surface, len(o.configs))
	for ci := range o.configs {
		cfg := &o.configs[ci]
		s := make([]types.Surface, len(cfg.surfaces))
		copy(s, cfg.surfaces)
		configSurfaces[cfg.id] = s
	}

	needTempGC := false
	localOverrides := make(map[string]*types.Glass, len(o.glassOverrides))
	for k, g := range o.glassOverrides {
		cp := *g
		localOverrides[k] = &cp
	}
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
				SetSurfaceParam(&surfaces[idx], b.Param, scale*val+b.Offset)
			}
			continue
		}

		surfaces, ok := configSurfaces[v.Config]
		if !ok {
			continue
		}
		idx := surfaceIndex(surfaces, v.SurfaceID)
		if idx < 0 {
			continue
		}

		if ai, ok := AsphereCoefIndex(v.Param); ok {
			for len(surfaces[idx].Coefficients) <= ai {
				surfaces[idx].Coefficients = append(surfaces[idx].Coefficients, 0)
			}
			surfaces[idx].Coefficients[ai] = val
			continue
		}
		switch v.Param {
		case "curvature":
			surfaces[idx].Curvature = val
		case "conic":
			surfaces[idx].Conic = val
		case "thickness":
			surfaces[idx].Thickness = val
		case "diameter":
			surfaces[idx].Diameter = val
		case "radius":
			if val == 0 {
				surfaces[idx].Curvature = 0
			} else {
				surfaces[idx].Curvature = 1.0 / val
			}
		case "nd", "vd":
			key := resolveGlassKeyFromSurface(surfaces, o.gc, v.SurfaceID)
			if g, ok := localOverrides[key]; ok {
				switch v.Param {
				case "nd":
					g.ND = val
				case "vd":
					g.VD = val
				}
				needTempGC = true
			}
		}
	}

	var tempGC *glass.Catalog
	if needTempGC {
		tempGC = glass.NewCatalog()
		if o.gc != nil {
			for _, g := range o.gc.ByName {
				cp := *g
				tempGC.Add(cp)
			}
		}
		for _, ov := range localOverrides {
			cp := *ov
			tempGC.Add(cp)
		}
	}

	for ci := range o.configs {
		cfg := &o.configs[ci]
		surface.Precompute(configSurfaces[cfg.id])
	}

	return configSurfaces, tempGC
}

// restoreDiameters resets auto_aperture surfaces to their initial diameters
// so sizeAutoApertures can measure the true geometric extent.
func (o *Optimizer) restoreDiameters(cfg *config, surfaces []types.Surface) {
	for i := range surfaces {
		key := cfgSurfKey(cfg.id, surfaces[i].ID)
		if d, ok := o.initialDiameters[key]; ok {
			surfaces[i].Diameter = d
		}
	}
}

// termFieldAngle returns the field angle for a merit term, converting
// image-height fields via ray tracing (surfaces-dependent).
func (o *Optimizer) termFieldAngle(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog) float64 {
	if !term.useImageHeight {
		return term.fieldAngle
	}
	return o.imageHeightToFieldAngle(cfg, surfaces, term.imageHeight, term.wavelength, gc)
}

// constraintFieldAngle resolves the field angle of a constraint operand.
func (o *Optimizer) constraintFieldAngle(cfg *config, c types.ConstraintOperand, surfaces []types.Surface, gc *glass.Catalog) float64 {
	for _, f := range cfg.fields {
		if f.ID == c.Field {
			if f.AngleDeg != 0 || f.ImageHeight == 0 {
				return f.AngleDeg
			}
			return o.imageHeightToFieldAngle(cfg, surfaces, f.ImageHeight, 0, gc)
		}
	}
	return 0
}

// traceFieldGrid traces the pupil grid for a merit term and returns the spot
// points.
func (o *Optimizer) traceFieldGrid(gc *glass.Catalog, surfaces []types.Surface, cfg *config, term *meritTerm) []dls.IPoint {
	angle := o.termFieldAngle(cfg, term, surfaces, gc)
	points, _ := dls.TraceFieldGrid(gc, surfaces, cfg.pupilZ, angle, []float64{0, 1}, term.wavelength, o.apertureMargin, o.numRays, o.gridRotation, o.gridWorkers())
	return points
}

// gridWorkers returns the number of goroutines for grid-ray parallelism. The
// Jacobian column loop already runs o.workers goroutines per residual call, so
// the grid workers are capped so the two levels do not oversubscribe the CPU.
func (o *Optimizer) gridWorkers() int {
	w := runtime.GOMAXPROCS(0) / o.workers
	if w < 1 {
		w = 1
	}
	return w
}

// imageHeightToFieldAngle finds the field angle that lands the chief ray at
// the target image height, via bisection on the image-plane intersection.
func (o *Optimizer) imageHeightToFieldAngle(cfg *config, surfaces []types.Surface, targetHeight, wavelength float64, gc *glass.Catalog) float64 {
	path := dls.BuildPath(surfaces)
	engine := ray.NewEngine(gc, nil)

	apertureRadius := dls.ApertureRadiusForGrid(surfaces, wavelength, gc, o.apertureMargin)
	if apertureRadius <= 0 {
		return 0
	}

	pol := types.NewCircularJones(true)

	lo, hi := 0.0, 45.0
	for iter := 0; iter < 30; iter++ {
		mid := (lo + hi) / 2
		dir := raymath.DirectionFromAngle(mid)

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

// sizeAutoApertures measures the true geometric beam extent at the extreme
// field angle (ignoring aperture clipping) and sizes every AutoAperture
// surface so its diameter covers the full bundle. Callers must restore the
// initial diameters first.
func (o *Optimizer) sizeAutoApertures(cfg *config, surfaces []types.Surface, gc *glass.Catalog) {
	extremeAngle := 0.0
	for ti := range cfg.meritTerms {
		term := &cfg.meritTerms[ti]
		a := math.Abs(o.termFieldAngle(cfg, term, surfaces, gc))
		if a > extremeAngle {
			extremeAngle = a
		}
	}
	if extremeAngle <= 0 {
		return
	}

	extents := make(map[int]float64)
	for ti := range cfg.meritTerms {
		term := &cfg.meritTerms[ti]
		if term.kind != "" && term.kind != dls.MeritSpotRMS {
			continue
		}
		angle := o.termFieldAngle(cfg, term, surfaces, gc)
		if math.Abs(angle) != extremeAngle {
			continue
		}
		perSurf := dls.TraceFieldGridExtents(gc, surfaces, cfg.pupilZ, angle, []float64{0, 1}, term.wavelength, o.apertureMargin, o.numRays, o.gridRotation, o.gridWorkers())
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

func (o *Optimizer) EvaluateMerit(x []float64) float64 {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	merit := 0.0
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc)

		cfgMerit := 0.0
		for ti := range cfg.meritTerms {
			term := &cfg.meritTerms[ti]
			if term.kind == "" || term.kind == dls.MeritSpotRMS {
				points := o.traceFieldGrid(gc, surfaces, cfg, term)
				rms := dls.ComputeSpotRMS(points)
				cfgMerit += term.weight * term.fieldWeight * term.wavWeight * rms * rms
			} else {
				val := o.evaluateKindTerm(cfg, term, surfaces, gc)
				diff := val - term.target
				cfgMerit += term.weight * term.fieldWeight * term.wavWeight * diff * diff
			}
		}
		merit += cfg.weight * cfgMerit
	}

	if o.hull != nil {
		for _, pair := range o.hullPairs {
			merit += o.hull.Penalty(x[pair.ndIndex], x[pair.vdIndex], o.hullMargin, o.hullWeight)
		}
	}
	return merit
}

// MeritBreakdown evaluates the merit at x and returns the contribution of
// each merit term (and the objective total), so the value reported by DLS can
// be reconciled against an external evaluation (e.g. `chief` spot RMS).
func (o *Optimizer) MeritBreakdown(x []float64) map[string]float64 {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	out := make(map[string]float64)
	objTotal := 0.0
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc)

		for ti := range cfg.meritTerms {
			term := &cfg.meritTerms[ti]
			var contrib float64
			if term.kind == "" || term.kind == dls.MeritSpotRMS {
				points := o.traceFieldGrid(gc, surfaces, cfg, term)
				rms := dls.ComputeSpotRMS(points)
				contrib = term.weight * term.fieldWeight * term.wavWeight * rms * rms
			} else {
				val := o.evaluateKindTerm(cfg, term, surfaces, gc)
				diff := val - term.target
				contrib = term.weight * term.fieldWeight * term.wavWeight * diff * diff
			}
			kind := term.kind
			if kind == "" {
				kind = dls.MeritSpotRMS
			}
			key := fmt.Sprintf("config:%s %s(f%.1f,%.6f)", cfg.id, kind, o.termFieldAngle(cfg, term, surfaces, gc), term.wavelength)
			out[key] = contrib
			objTotal += cfg.weight * contrib
		}
	}
	out["objective_total"] = objTotal
	return out
}

func (o *Optimizer) ComputeResiduals(x []float64) []float64 {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var allR []float64
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc)

		for ti := range cfg.meritTerms {
			term := &cfg.meritTerms[ti]
			w := math.Sqrt(cfg.weight * term.weight * term.fieldWeight * term.wavWeight)
			if term.kind == "" || term.kind == dls.MeritSpotRMS {
				points := o.traceFieldGrid(gc, surfaces, cfg, term)
				allR = append(allR, w*dls.ComputeSpotRMS(points))
			} else {
				val := o.evaluateKindTerm(cfg, term, surfaces, gc)
				allR = append(allR, w*(val-term.target))
			}
		}
	}

	if o.hull != nil {
		for _, pair := range o.hullPairs {
			allR = append(allR, o.hull.Residual(x[pair.ndIndex], x[pair.vdIndex], o.hullMargin, o.hullWeight))
		}
	}
	return allR
}

func (o *Optimizer) ComputeConstraints(x []float64) []float64 {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var allC []float64
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc)

		for _, c := range cfg.constraints {
			if !c.Active {
				allC = append(allC, 0)
				continue
			}
			angle := o.constraintFieldAngle(cfg, c, surfaces, gc)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.stopSurface, cfg.pupilZ)
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
func (o *Optimizer) FinalConstraintViolations(x []float64, tol float64) []ConstraintViolation {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var out []ConstraintViolation
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc)

		for _, c := range cfg.constraints {
			if !c.Active {
				continue
			}
			angle := o.constraintFieldAngle(cfg, c, surfaces, gc)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.stopSurface, cfg.pupilZ)
			err := constraint.ComputeError(c.Kind, value, c)
			w := c.Weight
			if w <= 0 {
				w = 1.0
			}
			residual := math.Sqrt(w) * err
			if math.Abs(residual) > tol {
				out = append(out, ConstraintViolation{
					ID:       c.ID,
					Config:   cfg.id,
					Kind:     string(c.Kind),
					Measure:  string(c.Measure),
					Residual: residual,
				})
			}
		}
	}
	return out
}

// FinalConstraintMeasurements evaluates every active constraint at x and
// returns its measured value and weighted residual. Unlike
// FinalConstraintViolations it reports all constraints, not just the violated
// ones, so callers (e.g. the optimize command) can record the actual value the
// constraint reached (for example the final vignetting factor).
func (o *Optimizer) FinalConstraintMeasurements(x []float64) []types.ConstraintMeasurement {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var out []types.ConstraintMeasurement
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc)

		for _, c := range cfg.constraints {
			if !c.Active {
				continue
			}
			angle := o.constraintFieldAngle(cfg, c, surfaces, gc)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.stopSurface, cfg.pupilZ)
			err := constraint.ComputeError(c.Kind, value, c)
			w := c.Weight
			if w <= 0 {
				w = 1.0
			}
			out = append(out, types.ConstraintMeasurement{
				ID:       c.ID,
				Config:   cfg.id,
				Kind:     string(c.Kind),
				Measure:  string(c.Measure),
				Field:    c.Field,
				Value:    value,
				Residual: math.Sqrt(w) * err,
			})
		}
	}
	return out
}

func (o *Optimizer) Optimize() Result {
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

// FinalApertures returns the sized auto_aperture diameters of every config
// after applying x.
func (o *Optimizer) FinalApertures(x []float64) map[string]map[int]float64 {
	configSurfaces, tempGC := o.applyVariables(x)
	result := make(map[string]map[int]float64)

	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, effectiveGC(o.gc, tempGC))

		cfgResult := make(map[int]float64)
		for i := range surfaces {
			if surfaces[i].AutoAperture {
				cfgResult[surfaces[i].ID] = surfaces[i].Diameter
			}
		}
		result[cfg.id] = cfgResult
	}
	return result
}

// FinalConfigs returns the surfaces of every config after applying x, with
// nd/vd-optimised model glasses materialised (surface materials rewritten to
// the new glass keys) and the new glass entries to append to the catalog.
func (o *Optimizer) FinalConfigs(x []float64) (map[string][]types.Surface, []types.Glass) {
	configSurfaces, _ := o.applyVariables(x)
	newGlasses := o.materializeGlasses(configSurfaces, x)
	return configSurfaces, newGlasses
}

// materializeGlasses collects the optimised nd/vd pairs into model glass
// entries and rewrites the affected surface materials to the new glass keys.
func (o *Optimizer) materializeGlasses(configSurfaces map[string][]types.Surface, x []float64) []types.Glass {
	return MaterializeGlassEntries(o.variables, x, o.gc,
		func(v Variable) (string, bool) {
			if v.IsShared {
				return "", false
			}
			return v.GlassName, v.GlassName != ""
		},
		func(origKey, newKey string) {
			for ci := range o.configs {
				cfgID := o.configs[ci].id
				for i := range configSurfaces[cfgID] {
					if configSurfaces[cfgID][i].Material == origKey {
						configSurfaces[cfgID][i].Material = newKey
					}
				}
			}
		})
}

// AsphereCoefIndex maps an asphere coefficient parameter name (a4/a6/a8/a10/
// a12, or the array aliases coefficient_0..coefficient_4) to the index in
// types.Surface.Coefficients, which holds the h^(2i+4) coefficients.
func AsphereCoefIndex(param string) (int, bool) {
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

// SetSurfaceParam sets a single surface parameter by name.
func SetSurfaceParam(s *types.Surface, param string, val float64) {
	if ai, ok := AsphereCoefIndex(param); ok {
		for len(s.Coefficients) <= ai {
			s.Coefficients = append(s.Coefficients, 0)
		}
		s.Coefficients[ai] = val
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

func getSurfaceParam(s types.Surface, param string) float64 {
	if ai, ok := AsphereCoefIndex(param); ok {
		if ai < len(s.Coefficients) {
			return s.Coefficients[ai]
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

func (o *Optimizer) getInitialState() []float64 {
	x := make([]float64, len(o.variables))
	for i, v := range o.variables {
		if v.IsShared {
			if v.Min == 0 && v.Max == 0 {
				v.Min, v.Max = -1, 1
			}
			x[i] = (v.Min + v.Max) / 2
			if len(v.Bindings) > 0 {
				b := v.Bindings[0]
				if cfg := findConfigByID(o.configs, b.Config); cfg != nil {
					if idx := surfaceIndex(cfg.surfaces, b.ID); idx >= 0 {
						if v0 := getSurfaceParam(cfg.surfaces[idx], b.Param); v0 != 0 {
							x[i] = v0
						}
					}
				}
			}
			if x[i] < v.Min {
				x[i] = v.Min
			} else if x[i] > v.Max {
				x[i] = v.Max
			}
			continue
		}

		cfg := findConfigByID(o.configs, v.Config)
		switch v.Param {
		case "curvature", "conic", "thickness", "diameter", "radius",
			"a4", "a6", "a8", "a10", "a12",
			"coefficient_0", "coefficient_1", "coefficient_2", "coefficient_3", "coefficient_4":
			if cfg != nil {
				if idx := surfaceIndex(cfg.surfaces, v.SurfaceID); idx >= 0 {
					x[i] = getSurfaceParam(cfg.surfaces[idx], v.Param)
				} else {
					x[i] = (v.Min + v.Max) / 2
				}
			} else {
				x[i] = (v.Min + v.Max) / 2
			}
		case "nd", "vd":
			key := v.GlassName
			if key == "" && cfg != nil {
				key = resolveGlassKeyFromSurface(cfg.surfaces, o.gc, v.SurfaceID)
			}
			if o.gc != nil && key != "" {
				if g, ok := o.gc.Lookup(key); ok {
					switch v.Param {
					case "nd":
						x[i] = g.ND
					case "vd":
						x[i] = g.VD
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
	return x
}

func (o *Optimizer) buildVariableStates(x []float64) []VariableState {
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
					if b.Config == cfg.id {
						if idx := surfaceIndex(cfg.surfaces, b.ID); idx >= 0 {
							st.Before = getSurfaceParam(cfg.surfaces[idx], b.Param)
						}
						break
					}
				}
				if st.Before != 0 {
					break
				}
			}
		} else {
			st.SurfaceID = v.SurfaceID
			st.Param = v.Param
			st.GlassName = v.GlassName
			if cfg := findConfigByID(o.configs, v.Config); cfg != nil {
				if idx := surfaceIndex(cfg.surfaces, v.SurfaceID); idx >= 0 {
					if v.Param == "nd" || v.Param == "vd" {
						key := v.GlassName
						if key == "" {
							key = resolveGlassKeyFromSurface(cfg.surfaces, o.gc, v.SurfaceID)
						}
						if o.gc != nil && key != "" {
							if g, ok := o.gc.Lookup(key); ok {
								switch v.Param {
								case "nd":
									st.Before = g.ND
								case "vd":
									st.Before = g.VD
								}
							}
						}
					} else {
						st.Before = getSurfaceParam(cfg.surfaces[idx], v.Param)
					}
				}
			}
		}

		states[i] = st
	}
	return states
}

func buildHullPairs(variables []Variable) []glassPair {
	ndMap := make(map[string]int)
	vdMap := make(map[string]int)
	for i, v := range variables {
		if v.IsShared {
			continue
		}
		switch v.Param {
		case "nd":
			ndMap[v.GlassName] = i
		case "vd":
			vdMap[v.GlassName] = i
		}
	}
	var pairs []glassPair
	for name, ndIdx := range ndMap {
		if vdIdx, ok := vdMap[name]; ok {
			pairs = append(pairs, glassPair{ndIndex: ndIdx, vdIndex: vdIdx, name: name})
		}
	}
	return pairs
}

func resolveGlassKeyFromSurface(surfaces []types.Surface, gc *glass.Catalog, id int) string {
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

func surfaceIndex(surfaces []types.Surface, id int) int {
	for i, s := range surfaces {
		if s.ID == id {
			return i
		}
	}
	return -1
}

func cfgSurfKey(configID string, surfID int) string {
	return configID + ":" + fmt.Sprint(surfID)
}

func Debugf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
}
