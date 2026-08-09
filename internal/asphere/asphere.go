// Package asphere implements the asphere candidate surface selection and
// initial sag estimation analysis: it traces a pupil grid per field, builds
// polar footprint cells on each candidate surface, scores the surfaces for
// how well a rotationally-symmetric asphere could correct the residual OPD,
// and estimates initial asphere coefficients via a fast OPD-to-sag
// approximation.
package asphere

import (
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
	PupilSamplesRadial      int
	RemovePiston            bool
	RemoveTilt              bool
	RemoveDefocus           bool
	TopK                    int
	MinRaysPerCell          int
	ScoreWeights            types.AsphereScoreWeights
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
		PupilSamplesRadial:      21,
		RemovePiston:            true,
		RemoveTilt:              true,
		RemoveDefocus:           false,
		TopK:                    3,
		MinRaysPerCell:          3,
		ScoreWeights: types.AsphereScoreWeights{
			Common:        0.35,
			Unique:        0.15,
			Fit:           0.20,
			Sensitivity:   0.15,
			Conflict:      0.10,
			Manufacturing: 0.05,
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
	if c.PupilSamplesRadial > 0 {
		cfg.PupilSamplesRadial = c.PupilSamplesRadial
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
	}
	return cfg
}

// Result is the complete asphere candidate analysis output.
type Result struct {
	Rankings []types.AsphereSurfaceScore
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

	cellsBySurf := make(map[int][]types.AsphereCellStat, len(candidates))
	index := make(map[int][2]float64, len(candidates))
	for _, s := range candidates {
		cells := BuildCellGrid(footprints, s.ID, cfg.CellRings, cfg.CellAngles)
		totalWeight := totalWeightOnSurface(footprints, s.ID)
		cellsBySurf[s.ID] = ComputeCellStats(cells, cfg.MinRaysPerCell, totalWeight)
		index[s.ID] = mediaIndices(surfaces, s.ID, primaryWL, gc)
	}

	stopZ, hasStop := stopSurfaceZ(surfaces, stopSurface)
	rankings := RankSurfaces(candidates, cellsBySurf, index, cfg.ScoreWeights, cfg.MaxEvenOrder, stopZ, hasStop)

	for i := range rankings {
		if i >= cfg.TopK {
			break
		}
		rs := &rankings[i]
		s := findSurface(surfaces, rs.SurfaceID)
		if s == nil {
			continue
		}
		pair := index[rs.SurfaceID]
		coeffs, warns := FitAsphereCoeffs(cellsBySurf[rs.SurfaceID], *s, pair[0], pair[1], cfg)
		rs.Coefficients = coeffs
		rs.ScaledCoefficients = ScaleCoefficients(coeffs, cfg.SagScale)
		rs.Warnings = append(rs.Warnings, warns...)
		result.Warnings = append(result.Warnings, warns...)
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
