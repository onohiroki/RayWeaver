package optimize

import (
	"math"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/constraint"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// SetLightApertureSizing enables or disables the light aperture sizing mode.
// When enabled, the hex-grid ray count for auto-aperture beam-extent
// measurement is reduced from max(numRays, 256) to numRays, trading
// aperture precision for speed during PSO exploration.
func (o *Optimizer) SetLightApertureSizing(on bool) {
	o.lightApertureSizing = on
}

// SetApertureMarginMM sets the physical clearance (mm) added to each
// auto_aperture final diameter (matching chief --clear-aperture-margin-mm).
// Values <= 0 keep the default 0.2 mm.
func (o *Optimizer) SetApertureMarginMM(mm float64) {
	if mm > 0 {
		o.apertureMarginMM = mm
	}
}

// autoApertureDiameter returns the diameter an auto_aperture surface is sized
// to: twice the measured beam extent plus the physical clearance, capped by the
// surface's max_diameter when one is set. A bound surface is the deliberate
// vignetting degree of freedom (a mechanical envelope); the run reports the cap
// when it binds (WarnBeamClipping).
func autoApertureDiameter(s types.Surface, extent, marginMM float64) float64 {
	d := 2 * (extent + marginMM)
	if s.MaxDiameter > 0 && d > s.MaxDiameter {
		return s.MaxDiameter
	}
	return d
}

// sizeAutoApertures measures the true geometric beam extent of every field
// (ignoring aperture clipping) and sizes every AutoAperture surface so its
// diameter covers the union envelope of all fields' marginal rays. Using all
// fields (rather than only the extreme field) keeps the lens large enough for
// the widest bundle. Callers must restore the initial diameters first. The
// per-(field, wavelength) extent grids are cached in cache when non-nil.
//
// Every config field is measured, not only the fields that happen to carry a
// grid merit term: a merit that drives off-axis fields purely through wavefront
// terms (e.g. wavefront_astigmatism / wavefront_sphere_rms on the corner) would
// otherwise undersize the apertures to the on-axis beam and clip the off-axis
// wavefront grid, collapsing the corner fit.
func (o *Optimizer) sizeAutoApertures(cfg *config, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache, p appliedPupil) {
	if len(o.initialDiameters) == 0 {
		return
	}
	// During a Jacobian sweep, reuse the diameters cached by BeginJacobian
	// instead of re-tracing beam extents.
	if o.jacobianActive {
		if m := o.reuseDiameters[cfg.id]; m != nil {
			for i := range surfaces {
				if surfaces[i].AutoAperture {
					if d, ok := m[surfaces[i].ID]; ok {
						surfaces[i].Diameter = d
					}
				}
			}
		}
		return
	}
	extents := make(map[int]float64)

	// The extents are geometric (aperture-clipping skipped), so one trace per
	// (field, wavelength) is enough - but every config wavelength has to be
	// covered, not just one representative. Transverse colour moves the
	// footprint between the config's wavelengths (measured 0.12 mm at 23 deg
	// on a 50 mm f/4 six-element), so sizing from a single wavelength can
	// return a diameter smaller than the same surface's footprint at another
	// and clip the field the merit evaluates.
	wls := apertureSizingWavelengths(cfg)

	// Measure every field's beam. Fall back to the grid merit term angles when
	// the config has no explicit field list.
	angles := make(map[float64]bool)
	for fi := range cfg.fields {
		angles[o.fieldSizingAngle(cfg, fi, surfaces, gc, wls[0])] = true
	}
	if len(angles) == 0 {
		for ti := range cfg.meritTerms {
			term := &cfg.meritTerms[ti]
			if !isTraceKind(term.kind) {
				continue
			}
			angles[o.termFieldAngle(cfg, term, surfaces, gc)] = true
		}
	}

	for angle := range angles {
		for _, wl := range wls {
			key := gridKey{configID: cfg.id, fieldAngle: angle, wavelength: wl}
			var perSurf map[int]float64
			if cache != nil {
				if m, ok := cache.extents[key]; ok {
					perSurf = m
				} else {
					perSurf = o.fieldExtents(cfg, surfaces, gc, &meritTerm{wavelength: wl}, angle, p)
					cache.extents[key] = perSurf
				}
			} else {
				perSurf = o.fieldExtents(cfg, surfaces, gc, &meritTerm{wavelength: wl}, angle, p)
			}
			for id, e := range perSurf {
				if e > extents[id] {
					extents[id] = e
				}
			}
		}
	}

	for id, e := range extents {
		for i := range surfaces {
			if surfaces[i].ID == id && surfaces[i].AutoAperture {
				surfaces[i].Diameter = autoApertureDiameter(surfaces[i], e, o.apertureMarginMM)
			}
		}
	}
}

// WarnThinEdges reports every glass element whose sized clear aperture leaves
// less edge thickness than its min_glass_path.
//
// The sizing grows an auto_aperture diameter to the beam (max_diameter only
// caps it), so a strongly curved element can end up with a zero or negative
// edge and the runaway is silent unless the document declares an edge_thickness
// constraint. The measurement is the constraint's own (center thickness + back
// sagitta − front sagitta at the rim), so the warning and a declared constraint
// agree. Warning only; the diameters are untouched.
func (o *Optimizer) WarnThinEdges(x []float64, apertures map[string]map[int]float64) {
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, nil, pupils[cfg.id])
		if apMap := apertures[cfg.id]; apMap != nil {
			for i := range surfaces {
				if d, ok := apMap[surfaces[i].ID]; ok {
					surfaces[i].Diameter = d
				}
			}
		}
		for i := range surfaces {
			s := &surfaces[i]
			if s.MinGlassPath <= 0 {
				continue
			}
			op := types.ConstraintOperand{
				Kind:    types.ConstraintInequalityLower,
				Measure: types.MeasureEdgeThickness,
				Surface: s.ID,
				Active:  true,
			}
			edge := constraint.Evaluate(op, surfaces, 0, gc, 0, 1.0, cfg.stopSurface, 0, 0)
			if edge < s.MinGlassPath {
				Warnf("warning: config %q surface %d: the sized clear aperture %.4f mm leaves %.4f mm edge thickness, below min_glass_path %.4f mm — reduce max_diameter, raise the element thickness, or add an edge_thickness constraint",
					cfg.id, s.ID, s.Diameter, edge, s.MinGlassPath)
			}
		}
	}
}

// extentRays returns the ray count for a beam-extent measurement. The extent
// grid must resolve the beam edge well enough that the sized auto_aperture
// diameters cover the true bundle (a coarse grid under-measures off-axis
// extents and the resulting lens vignettes the beam). floor raises a low
// numRays grid to the requested minimum density. When lightApertureSizing is
// set, the floor is skipped (PSO trades aperture precision for speed).
func (o *Optimizer) extentRays(floor int) int {
	if o.lightApertureSizing {
		return o.numRays
	}
	if o.numRays >= floor {
		return o.numRays
	}
	return floor
}

// FinalApertures returns the sized auto_aperture diameters of every config
// after applying x.
func (o *Optimizer) FinalApertures(x []float64) map[string]map[int]float64 {
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	result := make(map[string]map[int]float64)

	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.finalAutoApertures(cfg, surfaces, effectiveGC(o.gc, tempGC), pupils[cfg.id])

		cfgResult := make(map[int]float64)
		for i := range surfaces {
			if surfaces[i].AutoAperture {
				cfgResult[surfaces[i].ID] = surfaces[i].Diameter
			}
		}
		result[cfg.id] = cfgResult
	}

	// One physical element has one clear aperture: a zoom's positions share the
	// glass, so each auto_aperture surface gets the largest diameter any
	// position needs. Sizing the positions independently wrote a document in
	// which the same element had three different clear apertures and only the
	// position it was sized for was fully delivered.
	physical := make(map[int]float64)
	for _, m := range result {
		for id, d := range m {
			if d > physical[id] {
				physical[id] = d
			}
		}
	}
	for _, m := range result {
		for id := range m {
			if d := physical[id]; d > 0 {
				m[id] = d
			}
		}
	}
	return result
}

// finalAutoApertures sizes the auto_aperture surfaces of cfg from the same
// dynamic-pupil hex grid as chief --clear-aperture, so the output lens covers
// the true beam envelope (fixed apertures included) plus the configured
// clearance. It runs once per config; the cheaper sizeAutoApertures is used
// during merit evaluation where exact extents are not required.
func (o *Optimizer) finalAutoApertures(cfg *config, surfaces []types.Surface, gc *glass.Catalog, p appliedPupil) {
	if cfg.refSurface <= 0 || len(cfg.fieldDefs) == 0 {
		o.sizeAutoApertures(cfg, surfaces, gc, nil, p)
		return
	}
	// With a virtual entrance pupil the envelope grid must trace the same
	// bundle the merit (sizeAutoApertures -> fieldExtents -> gridCentring) and
	// the escape feasibility validation trace: passing nil would envelope the
	// dynamic-pupil bundle instead, so the sized diameters would clip the real
	// one and reject minima as insufficient_field_throughput. The x-applied
	// model mirrors FinalPupilModels.
	var pm *types.PupilModelConfig
	if cfg.pupilModel != nil && p.active {
		m := *cfg.pupilModel
		m.AxialPosition = p.z
		if p.dia > 0 {
			m.Diameter = p.dia
		}
		pm = &m
	}
	pol := types.NewCircularJones(true)
	engine := ray.NewEngine(gc, nil)
	surface.Precompute(surfaces)
	path := dls.BuildPath(surfaces)
	// Envelope every config wavelength and keep the widest: sizing from the
	// reference wavelength alone can return a diameter below the same
	// surface's footprint at another wavelength, which clips the image plane
	// (the surface with the largest field-dependent extent) and eats into the
	// prescribed per-field vignetting.
	env := make(map[int]float64)
	for _, wl := range apertureSizingWavelengths(cfg) {
		results := chief.DetermineChiefRaysGrid(
			types.System{Surfaces: surfaces, StopSurface: cfg.stopSurface},
			cfg.fieldDefs, cfg.refSurface, o.extentRays(512), gc, pol,
			wl, false, types.GridHex, nil, nil, nil, pm, 0, 0,
		)
		for id, e := range chief.BeamEnvelope(results, engine, surfaces, path, wl, pol) {
			if e > env[id] {
				env[id] = e
			}
		}
	}
	for i := range surfaces {
		if !surfaces[i].AutoAperture {
			continue
		}
		if e := env[surfaces[i].ID]; e > 0 {
			surfaces[i].Diameter = autoApertureDiameter(surfaces[i], e, o.apertureMarginMM)
		}
	}
}

// apertureSizingWavelengths returns the wavelengths the auto_aperture sizing
// must envelope: every wavelength the config declares, plus the reference
// wavelength when it is not among them. A single representative wavelength is
// not enough - transverse colour shifts the footprint between wavelengths, and
// an aperture sized from the narrow one clips the widest.
func apertureSizingWavelengths(cfg *config) []float64 {
	ref := effectiveReferenceWavelength(cfg.referenceWavelength)
	wls := make([]float64, 0, len(cfg.wavelengths)+1)
	// Fold near-duplicates: documents often declare the reference wavelength
	// with a slightly different rounding (0.0005876 vs 0.00058756), and
	// re-tracing it would only cost time. 1e-4 relative is 0.06 nm at the
	// d-line - far below anything that moves a footprint.
	covered := func(v float64) bool {
		for _, w := range wls {
			if math.Abs(w-v) <= 1e-4*math.Max(math.Abs(w), math.Abs(v)) {
				return true
			}
		}
		return false
	}
	for _, w := range cfg.wavelengths {
		v := effectiveReferenceWavelength(w.Value)
		if covered(v) {
			continue
		}
		wls = append(wls, v)
	}
	// Terms can evaluate wavelengths outside the config table (custom sets,
	// spectral expansion or aggregate polychromatic kinds); size for all of
	// them, not only the declared design lines.
	scan := func(terms []meritTerm) {
		for _, t := range terms {
			if t.wavelength > 0 && !covered(t.wavelength) {
				wls = append(wls, t.wavelength)
			}
			for _, s := range t.wavelengths {
				if s.Value > 0 && !covered(s.Value) {
					wls = append(wls, s.Value)
				}
			}
		}
	}
	scan(cfg.meritTerms)
	for _, terms := range cfg.meritModes {
		scan(terms)
	}
	if !covered(ref) {
		wls = append(wls, ref)
	}
	return wls
}
