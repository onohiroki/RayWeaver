package optimize

import (
	"fmt"
	"math"
	"os"

	"github.com/hiroki/rayweaver/internal/constraint"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

type Config struct {
	Surfaces       []types.Surface
	Variables      []Variable
	MeritTerms     []MeritTerm
	Fields         []types.FieldItem
	Constraints    []types.ConstraintOperand
	GlassCatalog   *glass.Catalog
	CoatingCatalog interface{}
	MaxIter        int
	Mu             float64
	Tol            float64
	Epsilon        float64
	NumRays        int
	ApertureMargin float64
	MuConMax       float64
	Logger         dls.Logger
	Hull           *glass.ConvexHull
	HullMargin     float64
	HullWeight     float64
}

type Variable struct {
	Name      string
	SurfaceID int
	GlassName string
	Param     string
	Min       float64
	Max       float64
}

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

type Optimizer struct {
	surfaces         []types.Surface
	variables        []Variable
	meritTerms       []MeritTerm
	fields           []types.FieldItem
	constraints      []types.ConstraintOperand
	gc               *glass.Catalog
	glassOverrides   map[string]*types.Glass
	tempGC           *glass.Catalog
	initialDiameters map[int]float64
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
	hullPairs        []glassPair
}

func NewOptimizer(cfg Config) *Optimizer {
	maxIter := cfg.MaxIter
	if maxIter <= 0 {
		maxIter = 100
	}
	mu := cfg.Mu
	if mu <= 0 {
		mu = 1.0
	}
	tol := cfg.Tol
	if tol <= 0 {
		tol = 1e-6
	}
	epsilon := cfg.Epsilon
	if epsilon <= 0 {
		epsilon = 1e-6
	}
	numRays := cfg.NumRays
	if numRays <= 0 {
		numRays = 64
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

	initialDiameters := make(map[int]float64)
	for _, s := range cfg.Surfaces {
		if s.AutoAperture {
			initialDiameters[s.ID] = s.Diameter
		}
	}

	apertureMargin := cfg.ApertureMargin
	if apertureMargin <= 0 {
		apertureMargin = 1.0
	}
	// aperture_margin < 1.0 makes the pupil grid smaller than the aperture,
	// which clips rays at surface edges and stalls DLS convergence.
	if apertureMargin < 1.0 {
		apertureMargin = 1.0
	}

	hullPairs := buildHullPairs(cfg.Variables)

	return &Optimizer{
		surfaces:         cfg.Surfaces,
		variables:        cfg.Variables,
		meritTerms:       cfg.MeritTerms,
		fields:           cfg.Fields,
		constraints:      cfg.Constraints,
		gc:               cfg.GlassCatalog,
		glassOverrides:   glassOverrides,
		initialDiameters: initialDiameters,
		maxIter:          maxIter,
		mu:               mu,
		tol:              tol,
		epsilon:          epsilon,
		numRays:          numRays,
		apertureMargin:   apertureMargin,
		muConMax:         cfg.MuConMax,
		logger:           cfg.Logger,
		hull:             cfg.Hull,
		hullMargin:       cfg.HullMargin,
		hullWeight:       cfg.HullWeight,
		hullPairs:        hullPairs,
	}
}

func (o *Optimizer) Variables() []dls.VariableInfo {
	result := make([]dls.VariableInfo, len(o.variables))
	for i, v := range o.variables {
		result[i] = dls.VariableInfo{
			Name:      v.Name,
			SurfaceID: v.SurfaceID,
			GlassName: v.GlassName,
			Param:     v.Param,
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
		Logger:         o.logger,
	}
}

func (o *Optimizer) InitialState() []float64 {
	return o.getInitialState()
}

func (o *Optimizer) addHullPenalty(merit float64, x []float64) float64 {
	if o.hull == nil || len(o.hullPairs) == 0 {
		return merit
	}
	for _, pair := range o.hullPairs {
		nd := x[pair.ndIndex]
		vd := x[pair.vdIndex]
		pen := o.hull.Penalty(nd, vd, o.hullMargin, o.hullWeight)
		merit += pen
	}
	return merit
}

func (o *Optimizer) makeHullResiduals(x []float64) []float64 {
	if o.hull == nil || len(o.hullPairs) == 0 {
		return nil
	}
	res := make([]float64, len(o.hullPairs))
	for i, pair := range o.hullPairs {
		nd := x[pair.ndIndex]
		vd := x[pair.vdIndex]
		res[i] = o.hull.Residual(nd, vd, o.hullMargin, o.hullWeight)
	}
	return res
}

func (o *Optimizer) EvaluateMerit(x []float64) float64 {
	surfaces := o.applyVariables(x)

	for i := range surfaces {
		if d, ok := o.initialDiameters[surfaces[i].ID]; ok {
			surfaces[i].Diameter = d
		}
	}

	o.sizeAutoApertures(surfaces)

	merit := 0.0

	for _, term := range o.meritTerms {
		if term.Kind == "" || term.Kind == dls.MeritSpotRMS {
			points, _ := o.traceFieldGrid(surfaces, term.FieldAngle, term.FieldDir, term.Wavelength)

			rms := dls.ComputeSpotRMS(points)
			merit += term.Weight * term.FieldWeight * term.WavWeight * rms * rms
		} else {
			val := EvaluateMeritKind(term.Kind, term, surfaces, o.currentGC(), o)
			diff := val - term.Target
			merit += term.Weight * term.FieldWeight * term.WavWeight * diff * diff
		}
	}

	merit = o.addHullPenalty(merit, x)
	return merit
}

// MeritBreakdown evaluates the merit at x and returns the contribution of each
// merit term (and the objective total), so the value reported by DLS can be
// reconciled against an external evaluation (e.g. `chief` spot RMS).
func (o *Optimizer) MeritBreakdown(x []float64) map[string]float64 {
	surfaces := o.applyVariables(x)
	for i := range surfaces {
		if d, ok := o.initialDiameters[surfaces[i].ID]; ok {
			surfaces[i].Diameter = d
		}
	}
	o.sizeAutoApertures(surfaces)

	out := make(map[string]float64)
	total := 0.0
	for _, term := range o.meritTerms {
		var contrib float64
		if term.Kind == "" || term.Kind == dls.MeritSpotRMS {
			points, _ := o.traceFieldGrid(surfaces, term.FieldAngle, term.FieldDir, term.Wavelength)
			rms := dls.ComputeSpotRMS(points)
			contrib = term.Weight * term.FieldWeight * term.WavWeight * rms * rms
		} else {
			val := EvaluateMeritKind(term.Kind, term, surfaces, o.currentGC(), o)
			diff := val - term.Target
			contrib = term.Weight * term.FieldWeight * term.WavWeight * diff * diff
		}
		kind := term.Kind
		if kind == "" {
			kind = dls.MeritSpotRMS
		}
		out[fmt.Sprintf("%s(f%.1f,%.6f)", kind, term.FieldAngle, term.Wavelength)] = contrib
		total += contrib
	}
	out["objective_total"] = total
	return out
}

func (o *Optimizer) ComputeResiduals(x []float64) []float64 {
	surfaces := o.applyVariables(x)
	nTerms := len(o.meritTerms)
	nHull := len(o.hullPairs)
	r := make([]float64, nTerms+nHull)

	for i := range surfaces {
		if d, ok := o.initialDiameters[surfaces[i].ID]; ok {
			surfaces[i].Diameter = d
		}
	}

	o.sizeAutoApertures(surfaces)

	for i, term := range o.meritTerms {
		if term.Kind == "" || term.Kind == dls.MeritSpotRMS {
			points, _ := o.traceFieldGrid(surfaces, term.FieldAngle, term.FieldDir, term.Wavelength)

			rms := dls.ComputeSpotRMS(points)
			r[i] = math.Sqrt(term.Weight*term.FieldWeight*term.WavWeight) * rms
		} else {
			val := EvaluateMeritKind(term.Kind, term, surfaces, o.currentGC(), o)
			diff := val - term.Target
			r[i] = math.Sqrt(term.Weight*term.FieldWeight*term.WavWeight) * diff
		}
	}

	// Append hull residuals at the end
	if o.hull != nil {
		for i, pair := range o.hullPairs {
			nd := x[pair.ndIndex]
			vd := x[pair.vdIndex]
			r[nTerms+i] = o.hull.Residual(nd, vd, o.hullMargin, o.hullWeight)
		}
	}

	return r
}

func (o *Optimizer) ComputeConstraints(x []float64) []float64 {
	surfaces := o.applyVariables(x)

	for i := range surfaces {
		if d, ok := o.initialDiameters[surfaces[i].ID]; ok {
			surfaces[i].Diameter = d
		}
	}

	o.sizeAutoApertures(surfaces)

	gcConstraint := o.gc
	if o.tempGC != nil {
		gcConstraint = o.tempGC
	}

	c := make([]float64, len(o.constraints))
	for j, op := range o.constraints {
		if !op.Active {
			c[j] = 0
			continue
		}
		value := constraint.Evaluate(op, surfaces, o.resolveFieldAngle(op.Field), gcConstraint, o.numRays, o.apertureMargin)
		err := constraint.ComputeError(op.Kind, value, op)
		w := op.Weight
		if w <= 0 {
			w = 1.0
		}
		c[j] = math.Sqrt(w) * err
	}

	return c
}

// ConstraintViolation reports an active constraint whose weighted residual
// magnitude exceeds tol at the final state.
type ConstraintViolation struct {
	ID       string
	Kind     string
	Measure  string
	Residual float64
}

// FinalConstraintViolations evaluates the active constraints at x and returns
// those whose weighted residual magnitude exceeds tol.
func (o *Optimizer) FinalConstraintViolations(x []float64, tol float64) []ConstraintViolation {
	surfaces := o.applyVariables(x)

	for i := range surfaces {
		if d, ok := o.initialDiameters[surfaces[i].ID]; ok {
			surfaces[i].Diameter = d
		}
	}
	o.sizeAutoApertures(surfaces)

	gcConstraint := o.gc
	if o.tempGC != nil {
		gcConstraint = o.tempGC
	}

	var out []ConstraintViolation
	for _, op := range o.constraints {
		if !op.Active {
			continue
		}
		value := constraint.Evaluate(op, surfaces, o.resolveFieldAngle(op.Field), gcConstraint, o.numRays, o.apertureMargin)
		err := constraint.ComputeError(op.Kind, value, op)
		w := op.Weight
		if w <= 0 {
			w = 1.0
		}
		residual := math.Sqrt(w) * err
		if math.Abs(residual) > tol {
			out = append(out, ConstraintViolation{
				ID:       op.ID,
				Kind:     string(op.Kind),
				Measure:  string(op.Measure),
				Residual: residual,
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

func (o *Optimizer) FinalApertures(x []float64) map[int]float64 {
	return o.finalAperturesImpl(x)
}

func (o *Optimizer) finalAperturesImpl(x []float64) map[int]float64 {
	surfaces := o.applyVariables(x)

	for i := range surfaces {
		if d, ok := o.initialDiameters[surfaces[i].ID]; ok {
			surfaces[i].Diameter = d
		}
	}

	o.sizeAutoApertures(surfaces)

	result := make(map[int]float64)
	for i := range surfaces {
		if surfaces[i].AutoAperture {
			result[surfaces[i].ID] = surfaces[i].Diameter
		}
	}
	return result
}

// asphereCoefIndex maps an asphere coefficient parameter name (a4/a6/a8/a10/
// a12, or the array aliases coefficient_0..coefficient_4) to the index in
// types.Surface.Coefficients.
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

func (o *Optimizer) applyVariables(x []float64) []types.Surface {
	result := make([]types.Surface, len(o.surfaces))
	copy(result, o.surfaces)

	needTempGC := false
	for i, v := range o.variables {
		if idx, ok := asphereCoefIndex(v.Param); ok {
			si := dls.SurfaceIndex(result, v.SurfaceID)
			if si < 0 {
				continue
			}
			for len(result[si].Coefficients) <= idx {
				result[si].Coefficients = append(result[si].Coefficients, 0)
			}
			result[si].Coefficients[idx] = x[i]
			continue
		}
		switch v.Param {
		case "curvature", "conic", "thickness":
			idx := dls.SurfaceIndex(result, v.SurfaceID)
			if idx < 0 {
				continue
			}
			switch v.Param {
			case "curvature":
				result[idx].Curvature = x[i]
			case "conic":
				result[idx].Conic = x[i]
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
		if o.gc != nil {
			for _, g := range o.gc.ByName {
				cp := *g
				o.tempGC.Add(cp)
			}
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

func (o *Optimizer) traceFieldGrid(surfaces []types.Surface, fieldAngle float64, fieldDir []float64, wavelength float64) ([]dls.IPoint, map[int]float64) {
	gc := o.currentGC()
	return dls.TraceFieldGrid(gc, surfaces, fieldAngle, fieldDir, wavelength, o.apertureMargin, o.numRays, o.gridRotation)
}

// currentGC returns the glass catalog reflecting any in-flight nd/vd variable
// overrides (o.tempGC), falling back to the base catalog.
func (o *Optimizer) currentGC() *glass.Catalog {
	if o.tempGC != nil {
		return o.tempGC
	}
	return o.gc
}

func (o *Optimizer) traceFieldGridExtents(surfaces []types.Surface, fieldAngle float64, fieldDir []float64, wavelength float64) map[int]float64 {
	gc := o.currentGC()
	return dls.TraceFieldGridExtents(gc, surfaces, fieldAngle, fieldDir, wavelength, o.apertureMargin, o.numRays, o.gridRotation)
}

// sizeAutoApertures measures the true geometric beam extent at the extreme
// field angle (ignoring aperture clipping) and sizes every AutoAperture
// surface so its diameter covers the full bundle. Callers must restore the
// initial diameters first.
func (o *Optimizer) sizeAutoApertures(surfaces []types.Surface) {
	extremeAngle := 0.0
	for _, term := range o.meritTerms {
		a := math.Abs(term.FieldAngle)
		if a > extremeAngle {
			extremeAngle = a
		}
	}
	if extremeAngle <= 0 {
		return
	}

	extents := make(map[int]float64)
	for _, term := range o.meritTerms {
		if term.Kind != "" && term.Kind != dls.MeritSpotRMS {
			continue
		}
		if math.Abs(term.FieldAngle) != extremeAngle {
			continue
		}
		perSurf := o.traceFieldGridExtents(surfaces, term.FieldAngle, term.FieldDir, term.Wavelength)
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

func (o *Optimizer) getInitialState() []float64 {
	x := make([]float64, len(o.variables))
	for i, v := range o.variables {
		if idx, ok := asphereCoefIndex(v.Param); ok {
			si := dls.SurfaceIndex(o.surfaces, v.SurfaceID)
			if si >= 0 && idx < len(o.surfaces[si].Coefficients) {
				x[i] = o.surfaces[si].Coefficients[idx]
			} else {
				x[i] = (v.Min + v.Max) / 2
			}
			continue
		}
		switch v.Param {
		case "curvature", "conic", "thickness":
			idx := dls.SurfaceIndex(o.surfaces, v.SurfaceID)
			if idx < 0 {
				x[i] = (v.Min + v.Max) / 2
				continue
			}
			switch v.Param {
			case "curvature":
				x[i] = o.surfaces[idx].Curvature
			case "conic":
				x[i] = o.surfaces[idx].Conic
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

		if idx, ok := asphereCoefIndex(v.Param); ok {
			if si := dls.SurfaceIndex(o.surfaces, v.SurfaceID); si >= 0 && idx < len(o.surfaces[si].Coefficients) {
				st.Before = o.surfaces[si].Coefficients[idx]
			}
			states[i] = st
			continue
		}
		switch v.Param {
		case "curvature", "conic", "thickness":
			if idx := dls.SurfaceIndex(o.surfaces, v.SurfaceID); idx >= 0 {
				switch v.Param {
				case "curvature":
					st.Before = o.surfaces[idx].Curvature
				case "conic":
					st.Before = o.surfaces[idx].Conic
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

func (o *Optimizer) resolveFieldAngle(fieldID int) float64 {
	if fieldID == 0 {
		return 0
	}
	for _, f := range o.fields {
		if f.ID == fieldID {
			return f.AngleDeg
		}
	}
	return 0
}

func buildHullPairs(variables []Variable) []glassPair {
	ndMap := make(map[string]int)
	vdMap := make(map[string]int)
	for i, v := range variables {
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

func Debugf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
}
