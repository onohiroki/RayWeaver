// Package asphere implements the asphere candidate surface selection and
// initial sag estimation analysis: it traces a pupil grid per field, builds
// polar footprint cells on each candidate surface, scores the surfaces for
// how well a rotationally-symmetric asphere could correct the residual OPD,
// and estimates initial asphere coefficients via a fast OPD-to-sag
// approximation.
package asphere

import (
	"math"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// Config is the resolved asphere analysis configuration (defaults applied).
type Config struct {
	CandidateSurfaces       []int
	MaxEvenOrder            int
	IncludeConic            bool
	PreserveVertexCurvature bool
	SagScale                float64
	MaxSag                  float64
	MaxSlopeDeg             float64
	MaxCurvatureVariation   float64
	CellRings               int
	CellAngles              int
	TBins                   int
	PupilSamplesRadial      int
	SensitivitySamples      int
	RemovePiston            bool
	RemoveTilt              bool
	RemoveDefocus           bool
	TopK                    int
	MinRaysPerCell          int
	ScoreWeights            types.AsphereScoreWeights
	// CalibrateScale replaces the fixed SagScale per surface with a scale
	// derived from the measured ray-trace response (base/asphere merit and
	// d_merit_d_coef) when the sensitivity pass runs. ScaleProbes, when
	// non-empty, overrides the quadratic estimate with an explicit list of
	// scales to trace and verify.
	CalibrateScale bool
	ScaleProbes    []float64
}

// DefaultConfig returns the default analysis configuration.
func DefaultConfig() Config {
	return Config{
		MaxEvenOrder:            10,
		IncludeConic:            true,
		PreserveVertexCurvature: true,
		SagScale:                0.2,
		MaxSag:                  0.05,
		MaxSlopeDeg:             25.0,
		MaxCurvatureVariation:   2.0,
		CellRings:               8,
		CellAngles:              16,
		TBins:                   8,
		PupilSamplesRadial:      21,
		SensitivitySamples:      9,
		RemovePiston:            true,
		RemoveTilt:              true,
		RemoveDefocus:           false,
		TopK:                    3,
		MinRaysPerCell:          3,
		CalibrateScale:          true,
		ScoreWeights: types.AsphereScoreWeights{
			Common:        0.35,
			Unique:        0.15,
			Fit:           0.20,
			Sensitivity:   0.15,
			Conflict:      0.10,
			Manufacturing: 0.05,
			Asym:          0.10,
		},
	}
}

// ConfigFromYAML resolves a configuration from the YAML section, filling
// defaults for unset values. Unset booleans (nil pointers) keep the defaults.
func ConfigFromYAML(c *types.AsphereCandidateConfig) Config {
	cfg := DefaultConfig()
	if c == nil {
		return cfg
	}
	if len(c.CandidateSurfaces) > 0 {
		cfg.CandidateSurfaces = c.CandidateSurfaces
	}
	if c.MaxEvenOrder > 0 {
		cfg.MaxEvenOrder = c.MaxEvenOrder
	}
	if c.IncludeConic != nil {
		cfg.IncludeConic = *c.IncludeConic
	}
	if c.PreserveVertexCurvature != nil {
		cfg.PreserveVertexCurvature = *c.PreserveVertexCurvature
	}
	if c.SagScale != 0 {
		cfg.SagScale = c.SagScale
	}
	if c.MaxSag != 0 {
		cfg.MaxSag = c.MaxSag
	}
	if c.MaxSlopeDeg != 0 {
		cfg.MaxSlopeDeg = c.MaxSlopeDeg
	}
	if c.MaxCurvatureVariation != 0 {
		cfg.MaxCurvatureVariation = c.MaxCurvatureVariation
	}
	if c.CellRings > 0 {
		cfg.CellRings = c.CellRings
	}
	if c.CellAngles > 0 {
		cfg.CellAngles = c.CellAngles
	}
	if c.TBins > 0 {
		cfg.TBins = c.TBins
	}
	if c.PupilSamplesRadial > 0 {
		cfg.PupilSamplesRadial = c.PupilSamplesRadial
	}
	if c.SensitivitySamples != nil {
		cfg.SensitivitySamples = *c.SensitivitySamples
	}
	if c.RemovePiston != nil {
		cfg.RemovePiston = *c.RemovePiston
	}
	if c.RemoveTilt != nil {
		cfg.RemoveTilt = *c.RemoveTilt
	}
	if c.RemoveDefocus != nil {
		cfg.RemoveDefocus = *c.RemoveDefocus
	}
	if c.TopK > 0 {
		cfg.TopK = c.TopK
	}
	if c.MinRaysPerCell > 0 {
		cfg.MinRaysPerCell = c.MinRaysPerCell
	}
	if c.CalibrateScale != nil {
		cfg.CalibrateScale = *c.CalibrateScale
	}
	if len(c.ScaleProbes) > 0 {
		cfg.ScaleProbes = c.ScaleProbes
	}
	if w := c.ScoreWeights; w != (types.AsphereScoreWeights{}) {
		if w.Common != 0 {
			cfg.ScoreWeights.Common = w.Common
		}
		if w.Unique != 0 {
			cfg.ScoreWeights.Unique = w.Unique
		}
		if w.Fit != 0 {
			cfg.ScoreWeights.Fit = w.Fit
		}
		if w.Sensitivity != 0 {
			cfg.ScoreWeights.Sensitivity = w.Sensitivity
		}
		if w.Conflict != 0 {
			cfg.ScoreWeights.Conflict = w.Conflict
		}
		if w.Manufacturing != 0 {
			cfg.ScoreWeights.Manufacturing = w.Manufacturing
		}
		if w.Unstable != 0 {
			cfg.ScoreWeights.Unstable = w.Unstable
		}
		if w.Asym != 0 {
			cfg.ScoreWeights.Asym = w.Asym
		}
	}
	return cfg
}

// Result is the complete asphere candidate analysis output.
type Result struct {
	Rankings []types.AsphereSurfaceScore
	Profiles []types.AsphereOPDProfile
	Warnings []string
}

// Run performs the full asphere candidate analysis over the given surfaces
// (one config) and returns the ranked surfaces with estimated coefficients.
// fields must carry the field weights; wavelengths lists the design
// wavelengths (mm). stopSurface selects the grid centring (0 = dynamic pupil);
// refSurface is the chief reference surface used to seed the dynamic pupil
// (0 when unused). The surfaces are precomputed in place (surface.Precompute).
func Run(surfaces []types.Surface, fields []Field, wavelengths []float64, cfg Config, gc *glass.Catalog, stopSurface, refSurface int) Result {
	var result Result

	surface.Precompute(surfaces)
	candidates := resolveCandidates(surfaces, cfg.CandidateSurfaces)
	if len(candidates) == 0 {
		result.Warnings = append(result.Warnings, "no candidate surfaces")
		return result
	}

	pupilZs := computePupilZs(surfaces, fields, gc, stopSurface, refSurface)
	footprints := GenerateFootprints(surfaces, fields, wavelengths, cfg.PupilSamplesRadial, gc, pupilZs)
	PreprocessOPD(footprints, cfg.RemoveTilt, cfg.RemoveDefocus)

	primaryWL := types.DefaultWavelength
	if len(wavelengths) > 0 {
		primaryWL = wavelengths[0]
	}

	metricsBySurf := make(map[int]SurfaceMetrics, len(candidates))
	index := make(map[int][2]float64, len(candidates))
	for _, s := range candidates {
		jf := JointRadialFit(footprints, s.ID, maxOrder(cfg.MaxEvenOrder))
		asym := BeamFrameAsym(footprints, s.ID, jf.Coef, jf.RMax, cfg.TBins, cfg.MinRaysPerCell, jf.Total)
		conf, uniq := SharedConflictUnique(footprints, s.ID, jf.Coef, jf.RMax, 2.5, jf.Total)
		portrait := FieldLowOrderPortrait(footprints, s.ID, jf.Coef, jf.RMax, cfg.MinRaysPerCell)
		cons := 0.0
		if !math.IsNaN(portrait.AstigR2) && !math.IsNaN(portrait.DefocusR2) {
			cons = clamp01((clamp01(portrait.AstigR2) + clamp01(portrait.DefocusR2)) / 2)
		}
		var meanR, meanW float64
		for _, fd := range footprints {
			for _, h := range fd.RayHits {
				if h.OK {
					if sh, ok := h.Hits[s.ID]; ok {
						w := effWeight(h)
						meanR += w * math.Hypot(sh.Position.X, sh.Position.Y)
						meanW += w
					}
				}
			}
		}
		if meanW > 0 {
			meanR /= meanW
		}
		metricsBySurf[s.ID] = SurfaceMetrics{Joint: jf, Asym: asym, Conflict: conf, Unique: uniq, MeanR: meanR, FieldConsistency: cons, AstigY0R2: portrait.AstigR2, DefocusY0R2: portrait.DefocusR2}
		index[s.ID] = mediaIndices(surfaces, s.ID, primaryWL, gc)
	}

	// Phase 3: measure the traced sensitivity of every candidate surface. Each
	// surface gets a provisional fit, then the merit (weighted RMS OPD) is
	// traced with the scaled asphere applied and compared against the shared
	// base system merit. The relative improvement (1 - asphere/base) is the
	// measured sensitivity term H used by the ranking — it directly measures
	// how much an asphere on that surface reduces the residual, unlike the
	// analytic index-contrast proxy.
	measuredH := make(map[int]float64, len(candidates))
	sensitivity := make(map[int]types.AsphereSensitivityMatrix, len(candidates))
	if cfg.SensitivitySamples > 0 {
		base := traceMerit(surfaces, fields, wavelengths, cfg.SensitivitySamples, gc, pupilZs, cfg)
		for _, s := range candidates {
			pair := index[s.ID]
			coeffs, _ := FitAsphereCoeffsJoint(metricsBySurf[s.ID].Joint, s, pair[0], pair[1], cfg)
			scaled := ScaleCoefficients(coeffs, cfg.SagScale)
			// Always measure, even for a zero (unfit) asphere: such a surface
			// gets improvement ≈ 0 and is correctly demoted instead of falling
			// back to the analytic proxy.
			sens := Sensitivity(surfaces, fields, wavelengths, cfg.SensitivitySamples, gc, pupilZs, s.ID, scaled, cfg, base)
			if cfg.CalibrateScale {
				sens = calibrateSensitivity(surfaces, fields, wavelengths, cfg, gc, pupilZs, s.ID, coeffs, base, sens)
			}
			if !math.IsNaN(sens.Improvement) {
				h := sens.Improvement
				if sens.CalibratedScale > 0 {
					h = sens.CalibratedImprovement
				}
				measuredH[s.ID] = h
			}
			if !math.IsNaN(sens.BaseMerit) && !math.IsNaN(sens.AsphereMerit) {
				sensitivity[s.ID] = sens
			}
		}
	}

	stopZ, hasStop := stopSurfaceZ(surfaces, stopSurface)
	rankings := RankSurfaceMetrics(candidates, metricsBySurf, index, cfg.ScoreWeights, cfg.MaxEvenOrder, stopZ, hasStop, measuredH)

	// Per-field OPD overlap profiles for every candidate surface (the graph
	// data behind the ranking: how each field's wavefront error varies across
	// the surface and how much the field profiles overlap).
	var candIDs []int
	for _, s := range candidates {
		candIDs = append(candIDs, s.ID)
	}
	result.Profiles = BuildOPDProfiles(footprints, candIDs, cfg.TBins)

	// Select the top-K surfaces that actually yield a valid asphere fit.
	// Surfaces whose fit fails (e.g. degenerate index difference, no
	// footprint) are skipped so the top-K are all genuinely aspherisable.
	fitted := 0
	for i := range rankings {
		if fitted >= cfg.TopK {
			break
		}
		rs := &rankings[i]
		s := findSurface(surfaces, rs.SurfaceID)
		if s == nil {
			continue
		}
		pair := index[rs.SurfaceID]
		coeffs, warns := FitAsphereCoeffsJoint(metricsBySurf[rs.SurfaceID].Joint, *s, pair[0], pair[1], cfg)
		rs.AsymResidual = metricsBySurf[rs.SurfaceID].Asym
		rs.FieldConsistency = metricsBySurf[rs.SurfaceID].FieldConsistency
		rs.AstigY0R2 = metricsBySurf[rs.SurfaceID].AstigY0R2
		rs.DefocusY0R2 = metricsBySurf[rs.SurfaceID].DefocusY0R2
		rs.Coefficients = coeffs
		rs.ScaledCoefficients = ScaleCoefficients(coeffs, cfg.SagScale)
		rs.Warnings = append(rs.Warnings, warns...)
		result.Warnings = append(result.Warnings, warns...)

		// Attach the measured sensitivity matrix (merits + per-coefficient
		// derivatives) computed in the Phase-3 pass, and the calibration's
		// embedded coefficients when it produced a scale (else the fixed
		// sag_scale-scaled set remains the embedded one).
		if sens, ok := sensitivity[rs.SurfaceID]; ok {
			rs.Sensitivity = &sens
			if cfg.CalibrateScale && sens.CalibratedScale > 0 {
				rs.CalibratedCoefficients = ScaleCoefficients(coeffs, sens.CalibratedScale)
			}
		}
		if coeffs != (types.AsphereCoeffs{}) {
			fitted++
		}
	}

	result.Rankings = rankings
	return result
}

// resolveCandidates resolves the candidate surface list, defaulting to every
// non-mirror surface with a positive ID when the list is empty.
func resolveCandidates(surfaces []types.Surface, ids []int) []types.Surface {
	if len(ids) == 0 {
		var out []types.Surface
		for _, s := range surfaces {
			if s.ID > 0 && !s.Reflects() {
				out = append(out, s)
			}
		}
		return out
	}
	want := make(map[int]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []types.Surface
	for _, s := range surfaces {
		if want[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

func findSurface(surfaces []types.Surface, id int) *types.Surface {
	for i := range surfaces {
		if surfaces[i].ID == id {
			return &surfaces[i]
		}
	}
	return nil
}

func indexOfSurface(surfaces []types.Surface, id int) int {
	for i := range surfaces {
		if surfaces[i].ID == id {
			return i
		}
	}
	return -1
}

// mediaIndices returns the refractive indices (n1, n2) on the object/image
// sides of a surface at the given wavelength. Mirrors get n2 = -n1.
func mediaIndices(surfaces []types.Surface, id int, wl float64, gc *glass.Catalog) [2]float64 {
	idx := indexOfSurface(surfaces, id)
	if idx < 0 {
		return [2]float64{1, 1}
	}
	n1 := 1.0
	for j := idx - 1; j >= 0; j-- {
		if !surfaces[j].Reflects() {
			n1 = refractiveIndex(gc, surfaces[j].Material, wl)
			break
		}
	}
	n2 := refractiveIndex(gc, surfaces[idx].Material, wl)
	if surfaces[idx].Reflects() {
		n2 = -n1
	}
	return [2]float64{n1, n2}
}

func refractiveIndex(gc *glass.Catalog, mat types.Material, wl float64) float64 {
	if gc == nil {
		return 1
	}
	n, err := gc.RefractiveIndex(mat, wl)
	if err != nil {
		return 1
	}
	return n
}

func totalWeightOnSurface(footprints []FieldFootprintData, surfaceID int) float64 {
	var w float64
	for _, fd := range footprints {
		for _, h := range fd.RayHits {
			if !h.OK {
				continue
			}
			if _, ok := h.Hits[surfaceID]; ok {
				w += h.Weight
			}
		}
	}
	return w
}

func stopSurfaceZ(surfaces []types.Surface, stopSurface int) (float64, bool) {
	if stopSurface <= 0 {
		return 0, false
	}
	for _, s := range surfaces {
		if s.ID == stopSurface {
			return s.PhysicalZ, true
		}
	}
	return 0, false
}

// computePupilZs returns the per-field entrance-pupil Z (aperture position)
// used to centre each field's grid. With an explicit stop it is the stop
// surface's physical Z for every field; otherwise it is the dynamic pupil
// (each field's chief-ray crossing with field 0's) from a cheap chief pass.
func computePupilZs(surfaces []types.Surface, fields []Field, gc *glass.Catalog, stopSurface, refSurface int) []float64 {
	zs := make([]float64, len(fields))
	if stopSurface > 0 {
		for _, s := range surfaces {
			if s.ID == stopSurface {
				stopZ := s.PhysicalZ
				for i := range zs {
					zs[i] = stopZ
				}
				return zs
			}
		}
		return zs
	}
	if refSurface <= 0 || len(fields) < 2 {
		return zs
	}
	fdefs := make([]types.FieldDef, len(fields))
	for i, f := range fields {
		fdefs[i] = types.FieldDef{Angle: f.Angle, Direction: f.Direction}
	}
	results := chief.DetermineChiefRaysGrid(
		types.System{Surfaces: surfaces},
		fdefs, refSurface, 200, gc,
		types.NewCircularJones(true), types.DefaultWavelength, false,
		types.GridPolar, nil, nil, nil,
	)
	anyPupil := false
	for i, r := range results {
		if i < len(zs) && r.EntrancePupil != nil && r.EntrancePupil.Center.Z != 0 {
			zs[i] = r.EntrancePupil.Center.Z
			anyPupil = true
		}
	}
	if !anyPupil {
		// The dynamic pupil is ill-conditioned (e.g. a heavily degraded system
		// whose chief rays do not cross in-lens). Fall back to the position of
		// the tightest fixed aperture, where the beam is physically limited.
		seed := surface.FixedMinApertureRadiusZ(surfaces)
		if seed != 0 {
			for i := range zs {
				zs[i] = seed
			}
		}
	}
	return zs
}
