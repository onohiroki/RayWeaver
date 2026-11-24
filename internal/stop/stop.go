// Package stop materializes the virtual entrance pupil as a real plane stop.
//
// The virtual pupil model (chief.pupil_model, mode virtual_entrance_pupil) is an
// *entrance-pupil* specification: a plane at axial_position (measured from the
// first surface vertex, along the unfolded axis) and a diameter. A physical
// stop is the entrance pupil's object, not the entrance pupil itself: the
// surfaces in front of the stop image it into object space, so a stop placed at
// the pupil plane with the pupil diameter changes the delivered beam.
//
// Materialize therefore solves for a plane inside an air gap whose paraxial
// image is the requested entrance pupil (position and diameter), and inserts a
// zero-power plane there. The resulting system's first-order pupil is the
// virtual model's pupil, so the beam, the f-number and the per-field vignetting
// definitions are unchanged.
package stop

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// Anchor selects how the stop is placed in a multi-config document.
type Anchor string

const (
	// Rear keeps the distance to the surface behind the stop: the stop moves
	// with the rear group.
	Rear Anchor = "rear"
	// Front keeps the distance to the surface in front of the stop: the stop
	// moves with the front group.
	Front Anchor = "front"
)

const (
	// solveSamples is the number of positions sampled across an air gap before
	// the root is bracketed.
	solveSamples = 97
	// solveTolMM is the stop-plane tolerance of the entrance-pupil solve.
	solveTolMM = 1e-7
	// rootTolMM is the largest entrance-pupil residual a bisected root may keep.
	rootTolMM = 1e-4
	// edgeTolMM keeps the inserted plane strictly inside its air gap.
	edgeTolMM = 1e-6
	// probeDiameterMM is the paraxial probe aperture. The entrance-pupil
	// location is independent of it and the radius scales linearly, so the
	// front-group magnification is the probe's entrance-pupil radius divided by
	// its stop radius (1 mm).
	probeDiameterMM = 2.0
	// hugeEPMM rejects divergent entrance-pupil samples (a gap that images the
	// stop plane to infinity); a bisection across such a pole is not a root.
	hugeEPMM = 1e9
)

// Request is one stop-materialization run.
type Request struct {
	// ConfigIDs and Surfaces are the document's configs in order. Surfaces are
	// not mutated; the materialized lists are returned.
	ConfigIDs []string
	Surfaces  [][]types.Surface
	// Reference is the config whose stop position is solved; the other configs
	// follow it with the same anchor rule.
	Reference int
	// EPZ and EPD are the virtual entrance pupil's position (mm, unfolded frame)
	// and diameter (mm).
	EPZ float64
	EPD float64
	// Anchor is Rear (default) or Front.
	Anchor Anchor
	// ID is the surface ID to insert; 0 selects one above the largest ID.
	ID int
	// Wavelength is the reference wavelength for the paraxial solve.
	Wavelength float64
	// Glass is the glass catalogue the paraxial trace resolves materials from.
	Glass *glass.Catalog
}

// ConfigReport is one config's materialization result.
type ConfigReport struct {
	ConfigID              string
	Reference             bool
	StopSurface           int
	StopZ                 float64
	StopDiameter          float64
	Magnification         float64
	EntrancePupilZ        float64
	EntrancePupilDiameter float64
	EPZErrorMM            float64
	EPDErrorMM            float64
	PreviousSurface       int
	NextSurface           int
	GapFrontMM            float64
	GapRearMM             float64
}

// Report is the materialization report.
type Report struct {
	Anchor          Anchor
	Reference       int
	ReferenceConfig string
	StopID          int
	FrameShiftMM    float64
	Configs         []ConfigReport
	Warnings        []string
}

// Materialize inserts the plane stop into every config's surface list. The
// returned lists are new slices; the request's surfaces are not modified.
func Materialize(req Request) ([][]types.Surface, Report, error) {
	if req.EPD <= 0 {
		return nil, Report{}, fmt.Errorf("virtual entrance-pupil diameter must be positive (got %g mm)", req.EPD)
	}
	if len(req.Surfaces) == 0 {
		return nil, Report{}, fmt.Errorf("no configs to process")
	}
	if req.Reference < 0 || req.Reference >= len(req.Surfaces) {
		return nil, Report{}, fmt.Errorf("reference config %d is out of range", req.Reference)
	}
	for ci := range req.Surfaces {
		if len(req.Surfaces[ci]) == 0 {
			return nil, Report{}, fmt.Errorf("config %s has no surfaces", req.configLabel(ci))
		}
	}
	anchor := req.Anchor
	if anchor == "" {
		anchor = Rear
	}
	if anchor != Rear && anchor != Front {
		return nil, Report{}, fmt.Errorf("unknown anchor %q (want %s | %s)", anchor, Rear, Front)
	}

	id := req.ID
	if id <= 0 {
		id = nextSurfaceID(req.Surfaces)
	}
	if err := checkIDUnused(req.Surfaces, id); err != nil {
		return nil, Report{}, err
	}

	rep := Report{
		Anchor:          anchor,
		Reference:       req.Reference,
		ReferenceConfig: req.configLabel(req.Reference),
		StopID:          id,
	}

	// The stop falls at or in front of the first surface: the target plane has
	// no optical power in front of it, so the stop is the plane itself and the
	// diameter is the entrance-pupil diameter. The document frame is shifted so
	// the stop becomes the first surface.
	if req.EPZ <= 0 {
		if anchor == Front {
			return nil, Report{}, fmt.Errorf(
				"anchor %q needs a surface in front of the stop, but the virtual entrance pupil at Z=%g mm is at or before the first surface, so the stop would be the first surface; use --anchor %s",
				Front, req.EPZ, Rear)
		}
		distance := -req.EPZ
		rep.FrameShiftMM = distance
		out := make([][]types.Surface, len(req.Surfaces))
		for ci := range req.Surfaces {
			out[ci] = insertFront(req.Surfaces[ci], id, distance, req.EPD)
		}
		for ci := range out {
			cr, err := measure(out[ci], req.configLabel(ci), ci == req.Reference, id, 0, req.EPD, req.Wavelength, req.Glass)
			if err != nil {
				return nil, Report{}, err
			}
			cr.PreviousSurface = 0
			cr.NextSurface = req.Surfaces[ci][0].ID
			cr.GapFrontMM = 0
			cr.GapRearMM = distance
			cr.StopZ = 0
			cr.StopDiameter = req.EPD
			cr.Magnification = 1
			rep.Configs = append(rep.Configs, cr)
		}
		return out, rep, nil
	}

	refSurfaces := clone(req.Surfaces[req.Reference])
	surface.Precompute(refSurfaces)
	zs := unfoldedZs(refSurfaces)
	bend := firstBendIndex(refSurfaces)
	gaps := usableGaps(refSurfaces, zs, bend)
	if len(gaps) == 0 {
		return nil, Report{}, fmt.Errorf("no air gap before the first fold to host the stop")
	}

	tempID := nextSurfaceID(req.Surfaces)
	if tempID == id {
		tempID = id + 1
	}
	bestGap, bestZ, found := bestRoot(refSurfaces, zs, gaps, req.EPZ, req.Wavelength, req.Glass, tempID)
	if !found {
		return nil, Report{}, fmt.Errorf(
			"no stop plane produces an entrance pupil at Z=%g mm (diameter %g mm); %s",
			req.EPZ, req.EPD, gapDiagnostics(refSurfaces, zs, gaps, req.Wavelength, req.Glass, tempID))
	}

	dFront := bestZ - zs[bestGap]
	dRear := zs[bestGap+1] - bestZ
	prevID := refSurfaces[bestGap].ID
	nextID := refSurfaces[bestGap+1].ID

	out := make([][]types.Surface, len(req.Surfaces))
	for ci := range req.Surfaces {
		s := clone(req.Surfaces[ci])
		surface.Precompute(s)
		cz := unfoldedZs(s)
		pi := indexOfID(s, prevID)
		ni := indexOfID(s, nextID)
		if pi < 0 || ni < 0 || ni != pi+1 {
			return nil, Report{}, fmt.Errorf(
				"config %s does not contain the air gap between surfaces %d and %d that hosts the stop",
				req.configLabel(ci), prevID, nextID)
		}
		if !s[pi].Material.IsAir() {
			return nil, Report{}, fmt.Errorf(
				"config %s: the space after surface %d is not air, so it cannot host the stop",
				req.configLabel(ci), prevID)
		}
		z := bestZ
		if ci != req.Reference {
			switch anchor {
			case Front:
				z = cz[pi] + dFront
			default:
				z = cz[ni] - dRear
			}
		}
		if z <= cz[pi]+edgeTolMM || z >= cz[ni]-edgeTolMM {
			return nil, Report{}, fmt.Errorf(
				"config %s: the %s-anchored stop plane Z=%.6f mm falls outside the air gap between surfaces %d and %d (%.6f .. %.6f mm)",
				req.configLabel(ci), anchor, z, prevID, nextID, cz[pi], cz[ni])
		}
		_, m, ok := magnificationAt(s, cz, pi, z, req.Wavelength, req.Glass, tempID)
		if !ok || m <= 0 {
			return nil, Report{}, fmt.Errorf(
				"config %s: cannot resolve the front-group magnification at the stop plane Z=%.6f mm",
				req.configLabel(ci), z)
		}
		diameter := req.EPD / m
		out[ci] = insertAt(s, cz, pi, z, id, diameter)
		cr, err := measure(out[ci], req.configLabel(ci), ci == req.Reference, id, req.EPZ, req.EPD, req.Wavelength, req.Glass)
		if err != nil {
			return nil, Report{}, err
		}
		cr.StopZ = z
		cr.StopDiameter = diameter
		cr.Magnification = m
		cr.PreviousSurface = prevID
		cr.NextSurface = nextID
		cr.GapFrontMM = z - cz[pi]
		cr.GapRearMM = cz[ni] - z
		if cbend := firstBendIndex(s); cbend >= 0 && cr.EntrancePupilZ > cz[cbend]+1e-6 {
			return nil, Report{}, fmt.Errorf(
				"config %s: the stop's entrance pupil (Z=%.6f mm) lies beyond the first fold (Z=%.6f mm), where the unfolded Z is not a global coordinate; stop materialization is not supported there",
				req.configLabel(ci), cr.EntrancePupilZ, cz[cbend])
		}
		if !cr.Reference && math.Abs(cr.EPZErrorMM) > 0.01 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf(
				"config %s: the stop's entrance pupil is at Z=%.4f mm, %.4f mm from the virtual model's %.4f mm (one physical stop cannot hold every zoom position's pupil position)",
				req.configLabel(ci), cr.EntrancePupilZ, cr.EPZErrorMM, req.EPZ))
		}
		rep.Configs = append(rep.Configs, cr)
	}
	return out, rep, nil
}

// nextSurfaceID returns one above the largest ID in any config.
func nextSurfaceID(all [][]types.Surface) int {
	maxID := 0
	for _, surfaces := range all {
		for _, s := range surfaces {
			if s.ID > maxID {
				maxID = s.ID
			}
		}
	}
	return maxID + 1
}

// checkIDUnused rejects an explicit ID already used by any config. A shared
// stop surface must be free in every config, since the same ID addresses it in
// each position.
func checkIDUnused(all [][]types.Surface, id int) error {
	if id <= 0 {
		return fmt.Errorf("stop surface ID must be positive (got %d)", id)
	}
	for ci, surfaces := range all {
		for _, s := range surfaces {
			if s.ID == id {
				return fmt.Errorf("surface ID %d is already used in config %d; pick another with --id", id, ci)
			}
		}
	}
	return nil
}

func (req Request) configLabel(ci int) string {
	if ci >= 0 && ci < len(req.ConfigIDs) && req.ConfigIDs[ci] != "" {
		return req.ConfigIDs[ci]
	}
	return fmt.Sprintf("config %d", ci)
}

// measure computes the inserted stop's paraxial entrance pupil.
func measure(surfaces []types.Surface, configID string, reference bool, id int, targetEPZ, targetEPD, wavelength float64, gc *glass.Catalog) (ConfigReport, error) {
	work := clone(surfaces)
	surface.Precompute(work)
	loc, radius, ok := paraxial.EntrancePupil(work, id, wavelength, gc)
	if !ok || math.IsNaN(loc) || math.IsInf(loc, 0) {
		return ConfigReport{}, fmt.Errorf(
			"config %s: the inserted stop has no paraxial entrance pupil (it has no clear aperture, or the back-traced ray leaves parallel)",
			configID)
	}
	epd := 2 * radius
	return ConfigReport{
		ConfigID:              configID,
		Reference:             reference,
		StopSurface:           id,
		EntrancePupilZ:        loc,
		EntrancePupilDiameter: epd,
		EPZErrorMM:            loc - targetEPZ,
		EPDErrorMM:            epd - targetEPD,
	}, nil
}

// unfoldedZs returns the unfolded vertex Z of every surface (the path length
// from the first surface). Folds are not followed: callers restrict themselves
// to the prefix before the first bend.
func unfoldedZs(surfaces []types.Surface) []float64 {
	z := make([]float64, len(surfaces))
	cur := 0.0
	for i := range surfaces {
		z[i] = cur
		cur += surfaces[i].Thickness
	}
	return z
}

// firstBendIndex returns the index of the first surface whose decenter steps
// bend the beam frame, or -1. The unfolded Z stops being a global coordinate
// from that surface on.
func firstBendIndex(surfaces []types.Surface) int {
	for i := range surfaces {
		if surfaces[i].Bends() {
			return i
		}
	}
	return -1
}

// usableGaps returns the indices i of the air gaps (between surfaces i and i+1)
// that a stop may be inserted into: air on the object side, a positive
// thickness, and before the first fold.
func usableGaps(surfaces []types.Surface, zs []float64, bend int) []int {
	limit := len(surfaces) - 1
	if bend >= 0 && bend < limit {
		limit = bend
	}
	var gaps []int
	for i := 0; i < limit; i++ {
		if !surfaces[i].Material.IsAir() {
			continue
		}
		if zs[i+1]-zs[i] <= 2*edgeTolMM {
			continue
		}
		gaps = append(gaps, i)
	}
	return gaps
}

// bestRoot solves every usable gap for the requested entrance-pupil position
// and returns the solution closest to it, preferring the gap that contains it.
func bestRoot(surfaces []types.Surface, zs []float64, gaps []int, target, wavelength float64, gc *glass.Catalog, tempID int) (gap int, z float64, ok bool) {
	bestScore := math.Inf(1)
	for _, gi := range gaps {
		zr, found := solveGap(surfaces, zs, gi, target, wavelength, gc, tempID)
		if !found {
			continue
		}
		score := math.Abs(zr - target)
		// A stop plane inside the gap holding the requested pupil plane is the
		// natural answer; other gaps only win when no such root exists.
		if zs[gi] < target && target < zs[gi+1] {
			score -= 1e6
		}
		if score < bestScore {
			bestScore, gap, z, ok = score, gi, zr, true
		}
	}
	return gap, z, ok
}

// solveGap bisects the entrance-pupil position across one air gap.
func solveGap(surfaces []types.Surface, zs []float64, gap int, target, wavelength float64, gc *glass.Catalog, tempID int) (float64, bool) {
	lo := zs[gap] + edgeTolMM
	hi := zs[gap+1] - edgeTolMM
	if hi <= lo {
		return 0, false
	}
	f := func(z float64) (float64, bool) {
		loc, _, ok := epAt(surfaces, zs, gap, z, wavelength, gc, tempID)
		if !ok || math.IsNaN(loc) || math.IsInf(loc, 0) || math.Abs(loc) > hugeEPMM {
			return 0, false
		}
		return loc - target, true
	}

	prevZ, prevF, prevOK := lo, 0.0, false
	prevF, prevOK = f(lo)
	bestZ, bestFound := 0.0, false
	for k := 1; k <= solveSamples; k++ {
		z := lo + (hi-lo)*float64(k)/float64(solveSamples)
		fz, ok := f(z)
		if prevOK && ok && (prevF == 0 || fz == 0 || (prevF < 0) != (fz < 0)) {
			if rz, rok := bisect(f, prevZ, z); rok {
				if fv, fok := f(rz); fok && math.Abs(fv) <= rootTolMM {
					if !bestFound || math.Abs(rz-target) < math.Abs(bestZ-target) {
						bestZ, bestFound = rz, true
					}
				}
			}
		}
		prevZ, prevF, prevOK = z, fz, ok
	}
	return bestZ, bestFound
}

// bisect finds a zero of f between lo and hi, whose values bracket it.
func bisect(f func(float64) (float64, bool), lo, hi float64) (float64, bool) {
	flo, ok := f(lo)
	if !ok {
		return 0, false
	}
	if flo == 0 {
		return lo, true
	}
	for i := 0; i < 200; i++ {
		if hi-lo <= solveTolMM {
			return (lo + hi) / 2, true
		}
		mid := (lo + hi) / 2
		fm, ok := f(mid)
		if !ok {
			return 0, false
		}
		if fm == 0 {
			return mid, true
		}
		if (flo < 0) != (fm < 0) {
			hi = mid
		} else {
			lo, flo = mid, fm
		}
	}
	return (lo + hi) / 2, true
}

// epAt inserts a probe plane at stop Z in the gap and returns the paraxial
// entrance pupil of the resulting system.
func epAt(surfaces []types.Surface, zs []float64, gap int, z, wavelength float64, gc *glass.Catalog, tempID int) (loc, radius float64, ok bool) {
	cand := insertAt(surfaces, zs, gap, z, tempID, probeDiameterMM)
	surface.Precompute(cand)
	return paraxial.EntrancePupil(cand, tempID, wavelength, gc)
}

// magnificationAt returns the front-group magnification at the stop plane: the
// probe entrance-pupil radius divided by the probe stop radius.
func magnificationAt(surfaces []types.Surface, zs []float64, gap int, z, wavelength float64, gc *glass.Catalog, tempID int) (loc, m float64, ok bool) {
	loc, radius, ok := epAt(surfaces, zs, gap, z, wavelength, gc, tempID)
	if !ok {
		return 0, 0, false
	}
	return loc, radius / (probeDiameterMM / 2), true
}

// insertAt splits the gap after surfaces[gap] and inserts the stop plane.
func insertAt(surfaces []types.Surface, zs []float64, gap int, z float64, id int, diameter float64) []types.Surface {
	out := make([]types.Surface, 0, len(surfaces)+1)
	out = append(out, surfaces[:gap+1]...)
	out[gap].Thickness = z - zs[gap]
	out = append(out, newStop(id, zs[gap+1]-z, diameter))
	out = append(out, surfaces[gap+1:]...)
	return out
}

// insertFront inserts the stop as the new first surface, distance in front of
// the old first surface.
func insertFront(surfaces []types.Surface, id int, distance, diameter float64) []types.Surface {
	out := make([]types.Surface, 0, len(surfaces)+1)
	out = append(out, newStop(id, distance, diameter))
	out = append(out, surfaces...)
	return out
}

// newStop builds the zero-power air plane that carries the aperture.
func newStop(id int, thickness, diameter float64) types.Surface {
	return types.Surface{
		ID:           id,
		Type:         types.Sphere,
		Thickness:    thickness,
		Material:     types.Material{},
		Diameter:     diameter,
		AutoAperture: false,
		Role:         "stop",
	}
}

func clone(surfaces []types.Surface) []types.Surface {
	out := make([]types.Surface, len(surfaces))
	copy(out, surfaces)
	return out
}

func indexOfID(surfaces []types.Surface, id int) int {
	for i := range surfaces {
		if surfaces[i].ID == id {
			return i
		}
	}
	return -1
}

// gapDiagnostics lists each gap's finite entrance-pupil range, so a failed
// solve says where the stop could have gone.
func gapDiagnostics(surfaces []types.Surface, zs []float64, gaps []int, wavelength float64, gc *glass.Catalog, tempID int) string {
	if len(gaps) == 0 {
		return "no air gap is available"
	}
	var msg string
	for _, gi := range gaps {
		lo, hi := math.Inf(1), math.Inf(-1)
		loZ := zs[gi] + edgeTolMM
		hiZ := zs[gi+1] - edgeTolMM
		for k := 0; k <= 8; k++ {
			z := loZ + (hiZ-loZ)*float64(k)/8
			loc, _, ok := epAt(surfaces, zs, gi, z, wavelength, gc, tempID)
			if !ok || math.IsNaN(loc) || math.IsInf(loc, 0) || math.Abs(loc) > hugeEPMM {
				continue
			}
			lo = math.Min(lo, loc)
			hi = math.Max(hi, loc)
		}
		if math.IsInf(lo, 1) {
			msg += fmt.Sprintf(" [%.3f..%.3f] mm: entrance pupil diverges;", zs[gi], zs[gi+1])
			continue
		}
		msg += fmt.Sprintf(" [%.3f..%.3f] mm: entrance pupil %.3f..%.3f mm;", zs[gi], zs[gi+1], lo, hi)
	}
	return "air gaps (stop plane -> achievable entrance pupil):" + msg
}
