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
		apertureMargin = 2.0
	}

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

func (o *Optimizer) EvaluateMerit(x []float64) float64 {
	surfaces := o.applyVariables(x)

	for i := range surfaces {
		if d, ok := o.initialDiameters[surfaces[i].ID]; ok {
			surfaces[i].Diameter = d
		}
	}

	extremeAngle := 0.0
	for _, term := range o.meritTerms {
		a := math.Abs(term.FieldAngle)
		if a > extremeAngle {
			extremeAngle = a
		}
	}

	extents := make(map[int]float64)
	merit := 0.0

	for pass := 0; pass < 2; pass++ {
		for _, term := range o.meritTerms {
			if term.Kind == "" || term.Kind == dls.MeritSpotRMS {
				isExtreme := math.Abs(term.FieldAngle) == extremeAngle && extremeAngle > 0
				if (pass == 0) != isExtreme {
					continue
				}

				points, perSurf := o.traceFieldGrid(surfaces, term.FieldAngle, term.FieldDir, term.Wavelength)

				if isExtreme {
					for id, e := range perSurf {
						if e > extents[id] {
							extents[id] = e
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

				rms := dls.ComputeSpotRMS(points)
				merit += term.Weight * term.FieldWeight * term.WavWeight * rms * rms
			} else {
				if pass == 1 {
					continue
				}
				val := EvaluateMeritKind(term.Kind, term, surfaces, o.gc, o)
				diff := val - term.Target
				merit += term.Weight * term.FieldWeight * term.WavWeight * diff * diff
			}
		}
	}

	return merit
}

func (o *Optimizer) ComputeResiduals(x []float64) []float64 {
	surfaces := o.applyVariables(x)
	nTerms := len(o.meritTerms)
	r := make([]float64, nTerms)

	for i := range surfaces {
		if d, ok := o.initialDiameters[surfaces[i].ID]; ok {
			surfaces[i].Diameter = d
		}
	}

	extremeAngle := 0.0
	for _, term := range o.meritTerms {
		a := math.Abs(term.FieldAngle)
		if a > extremeAngle {
			extremeAngle = a
		}
	}

	extents := make(map[int]float64)

	for pass := 0; pass < 2; pass++ {
		for i, term := range o.meritTerms {
			if term.Kind == "" || term.Kind == dls.MeritSpotRMS {
				isExtreme := math.Abs(term.FieldAngle) == extremeAngle && extremeAngle > 0
				if (pass == 0) != isExtreme {
					continue
				}

				points, perSurf := o.traceFieldGrid(surfaces, term.FieldAngle, term.FieldDir, term.Wavelength)

				if isExtreme {
					for id, e := range perSurf {
						if e > extents[id] {
							extents[id] = e
						}
					}
					for id, e := range extents {
						for j := range surfaces {
							if surfaces[j].ID == id && surfaces[j].AutoAperture {
								surfaces[j].Diameter = 2 * e
							}
						}
					}
				}

				rms := dls.ComputeSpotRMS(points)
				r[i] = math.Sqrt(term.Weight*term.FieldWeight*term.WavWeight) * rms
			} else {
				if pass == 1 {
					continue
				}
				val := EvaluateMeritKind(term.Kind, term, surfaces, o.gc, o)
				diff := val - term.Target
				r[i] = math.Sqrt(term.Weight*term.FieldWeight*term.WavWeight) * diff
			}
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
		value := constraint.Evaluate(op, surfaces, o.resolveFieldAngle(op.Field), gcConstraint)
		err := constraint.ComputeError(op.Kind, value, op)
		w := op.Weight
		if w <= 0 {
			w = 1.0
		}
		c[j] = math.Sqrt(w) * err
	}

	return c
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

func (o *Optimizer) applyVariables(x []float64) []types.Surface {
	result := make([]types.Surface, len(o.surfaces))
	copy(result, o.surfaces)

	needTempGC := false
	for i, v := range o.variables {
		switch v.Param {
		case "curvature", "thickness":
			idx := dls.SurfaceIndex(result, v.SurfaceID)
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
	gc := o.gc
	if o.tempGC != nil {
		gc = o.tempGC
	}
	return dls.TraceFieldGrid(gc, surfaces, fieldAngle, fieldDir, wavelength, o.apertureMargin, o.numRays, o.gridRotation)
}

func (o *Optimizer) getInitialState() []float64 {
	x := make([]float64, len(o.variables))
	for i, v := range o.variables {
		switch v.Param {
		case "curvature", "thickness":
			idx := dls.SurfaceIndex(o.surfaces, v.SurfaceID)
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
			if idx := dls.SurfaceIndex(o.surfaces, v.SurfaceID); idx >= 0 {
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
