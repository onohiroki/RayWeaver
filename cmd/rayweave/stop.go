package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/clispec"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/stop"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// runStop materializes chief.pupil_model as a real plane stop.
//
// The virtual pupil is an entrance-pupil specification (a plane in object space
// and a diameter), not a stop plane: the surfaces in front of a physical stop
// image it, so a stop placed at the pupil plane with the pupil diameter would
// change the delivered beam. The stop package solves for the plane whose
// paraxial image is the requested pupil and scales the clear diameter by the
// front-group magnification; this command applies that edit, removes the
// virtual model it replaces, and reports the entrance pupil each config
// actually delivers.
func runStop(data []byte) {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	opts := clispec.Bind(clispec.Stop, fs)
	if err := opts.Parse(os.Args[2:]); err != nil {
		errExitf("Error: %v", err)
	}

	output := parseYAML[types.Output](data)
	input := &output.Input
	if input.Chief == nil {
		errExitf("Error: 'chief' section is required")
	}
	// Principle 3: echo the flag-won settings into the output document.
	opts.WriteBack(input)

	pm := input.Chief.PupilModel
	if pm == nil || pm.Mode != "virtual_entrance_pupil" {
		errExitf("Error: chief.pupil_model.mode must be virtual_entrance_pupil (the document has no virtual entrance pupil to materialize)")
	}
	if pm.Diameter <= 0 {
		errExitf("Error: chief.pupil_model.diameter must be positive (got %g mm)", pm.Diameter)
	}
	if input.Chief.StopSurface > 0 {
		errExitf("Error: chief.stop_surface is already %d; the document already has a physical stop", input.Chief.StopSurface)
	}
	if len(input.Configs) == 0 {
		errExitf("Error: no configs to process (define configs[].surfaces)")
	}

	ref := 0
	if s := opts.String("config"); s != "" {
		idx, msg := resolveConfig(input.Configs, s)
		if idx < 0 {
			errExitf("Error: %s", msg)
		}
		ref = idx
	}

	anchor := "rear"
	if input.StopInsert != nil && strings.TrimSpace(input.StopInsert.Anchor) != "" {
		anchor = input.StopInsert.Anchor
	}
	anchor = strings.ToLower(strings.TrimSpace(anchor))
	id := 0
	if input.StopInsert != nil {
		id = input.StopInsert.ID
	}

	wavelength := types.DefaultWavelength
	if input.Chief.ReferenceWavelength > 0 {
		wavelength = input.Chief.ReferenceWavelength
	}

	gc, _ := openGlass(input, opts.String("glass-dir"))

	ids := make([]string, len(input.Configs))
	surfaces := make([][]types.Surface, len(input.Configs))
	for i := range input.Configs {
		ids[i] = input.Configs[i].ID
		surfaces[i] = input.Configs[i].Surfaces
	}

	newSurfaces, report, err := stop.Materialize(stop.Request{
		ConfigIDs:  ids,
		Surfaces:   surfaces,
		Reference:  ref,
		EPZ:        pm.AxialPosition,
		EPD:        pm.Diameter,
		Anchor:     stop.Anchor(anchor),
		ID:         id,
		Wavelength: wavelength,
		Glass:      gc,
	})
	if err != nil {
		errExitf("Error: %v", err)
	}

	res := &types.StopInsertResult{
		PupilModel:      *pm,
		Anchor:          string(report.Anchor),
		ReferenceConfig: report.ReferenceConfig,
		FrameShiftMM:    report.FrameShiftMM,
		DryRun:          opts.Bool("dry-run"),
		Warnings:        append([]string{}, report.Warnings...),
	}
	for _, cr := range report.Configs {
		res.Configs = append(res.Configs, types.StopInsertConfigResult{
			ConfigID:              cr.ConfigID,
			Reference:             cr.Reference,
			StopSurface:           cr.StopSurface,
			StopZ:                 cr.StopZ,
			StopDiameter:          cr.StopDiameter,
			Magnification:         cr.Magnification,
			EntrancePupilZ:        cr.EntrancePupilZ,
			EntrancePupilDiameter: cr.EntrancePupilDiameter,
			EPZErrorMM:            cr.EPZErrorMM,
			EPDErrorMM:            cr.EPDErrorMM,
			PreviousSurface:       cr.PreviousSurface,
			NextSurface:           cr.NextSurface,
			GapFrontMM:            cr.GapFrontMM,
			GapRearMM:             cr.GapRearMM,
		})
	}

	if !opts.Bool("dry-run") {
		for i := range input.Configs {
			input.Configs[i].Surfaces = newSurfaces[i]
		}
		if report.FrameShiftMM != 0 {
			shiftStopFrame(input, report.FrameShiftMM)
		}
		input.Chief.StopSurface = report.StopID
		input.Chief.PupilModel = nil
		if input.PupilModel != nil {
			// The legacy top-level pupil_model is not a beam definition (only
			// chief.pupil_model is); drop it so the converted document has no
			// stale virtual pupil.
			input.PupilModel = nil
			res.Warnings = append(res.Warnings, "removed the legacy top-level pupil_model section (chief.pupil_model was the beam definition)")
		}
		if removed := dropPupilModelVariables(input.Optimization); len(removed) > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"removed %d optimisation variable(s) targeting the virtual pupil: %s",
				len(removed), strings.Join(removed, ", ")))
		}
		if warns := spliceStopIntoPaths(input, report.StopID, input.Configs[ref].Surfaces); len(warns) > 0 {
			res.Warnings = append(res.Warnings, warns...)
		}
		// The converted system has a new surface and different ray paths:
		// stored rays/results describe the pre-conversion geometry, so they are
		// stripped like `clean` does.
		removeComputedResults(&output)
	}

	if !opts.Bool("no-verify") {
		before := append([]types.Surface{}, surfaces[ref]...)
		after := append([]types.Surface{}, newSurfaces[ref]...)
		verify, warn := verifyStopPupil(input, ref, before, after, report.StopID, wavelength, gc, pm)
		if warn != "" {
			res.Warnings = append(res.Warnings, warn)
		}
		res.Verification = verify
	}

	if !opts.Bool("dry-run") {
		work := append([]types.Surface{}, input.Configs[ref].Surfaces...)
		surface.Precompute(work)
		for _, s := range work {
			if s.ID == report.StopID {
				output.Stop = &types.StopInfo{SurfaceID: s.ID, PhysicalZ: s.PhysicalZ, Diameter: s.Diameter}
				break
			}
		}
	}

	output.StopInsertResult = res
	withOutputMetadata(&output.Input, "stop", subcmdArgs())
	writeYAML(&output)

	fmt.Fprintf(os.Stderr, "=== Stop materialized ===\n")
	fmt.Fprintf(os.Stderr, "  virtual pupil: Z=%.4f mm, diameter=%.4f mm\n", pm.AxialPosition, pm.Diameter)
	if report.FrameShiftMM != 0 {
		fmt.Fprintf(os.Stderr, "  frame shift:  +%.4f mm (the stop is the new first surface)\n", report.FrameShiftMM)
	}
	for _, cr := range report.Configs {
		fmt.Fprintf(os.Stderr, "  config %s: surface %d at Z=%.4f mm, diameter %.4f mm, magnification %.5f -> EP Z=%.4f mm / EPD %.4f mm\n",
			cr.ConfigID, cr.StopSurface, cr.StopZ, cr.StopDiameter, cr.Magnification, cr.EntrancePupilZ, cr.EntrancePupilDiameter)
	}
	if v := res.Verification; v != nil && len(v.Fields) > 0 {
		fmt.Fprintf(os.Stderr, "  verification: max spot-RMS delta %.4f mm (%.2f%%)\n", v.MaxRMSDeltaMM, 100*v.MaxRMSDeltaRel)
	}
	for _, w := range res.Warnings {
		errOut("Warning: %s", w)
	}
}

// spliceStopIntoPaths adds the new stop to every explicitly declared forward
// ray path, so a document that spells out its surfaces keeps visiting the
// aperture. Non-monotonic (ghost) paths are left alone and reported: where the
// stop belongs in a path that deliberately revisits surfaces is ambiguous.
func spliceStopIntoPaths(input *types.Input, stopID int, surfaces []types.Surface) []string {
	var warnings []string
	spliced := 0
	splice := func(label string, path []int) []int {
		if len(path) == 0 {
			return path
		}
		out, changed, ok := splicePath(path, surfaces, stopID)
		if !ok {
			warnings = append(warnings, fmt.Sprintf(
				"%s path is not a forward traversal; the inserted stop %d is not in it", label, stopID))
			return path
		}
		if changed {
			spliced++
		}
		return out
	}
	if input.Chief != nil {
		for i := range input.Chief.Fields {
			input.Chief.Fields[i].Path = splice(fmt.Sprintf("chief field %d", i), input.Chief.Fields[i].Path)
		}
	}
	if input.Rays != nil {
		for i := range input.Rays.Rays {
			input.Rays.Rays[i].Path = splice(fmt.Sprintf("ray %q", input.Rays.Rays[i].ID), input.Rays.Rays[i].Path)
		}
	}
	for ci := range input.Configs {
		if rays := input.Configs[ci].Rays; rays != nil {
			for i := range rays.Rays {
				rays.Rays[i].Path = splice(fmt.Sprintf("config %s ray %q", input.Configs[ci].ID, rays.Rays[i].ID), rays.Rays[i].Path)
			}
		}
	}
	if input.TraceSingle != nil {
		input.TraceSingle.Path = splice("trace_single", input.TraceSingle.Path)
	}
	if spliced > 0 {
		warnings = append(warnings, fmt.Sprintf("added the inserted stop %d to %d explicit ray path(s)", stopID, spliced))
	}
	return warnings
}

// splicePath inserts stopID into a forward path (surface positions strictly
// increasing). spliced reports whether the path changed; ok is false for a
// non-monotonic path, whose traversal order the stop cannot be inferred for.
func splicePath(path []int, surfaces []types.Surface, stopID int) (out []int, spliced bool, ok bool) {
	stopIdx := -1
	for i := range surfaces {
		if surfaces[i].ID == stopID {
			stopIdx = i
			break
		}
	}
	if stopIdx < 0 {
		return path, false, false
	}
	for _, id := range path {
		if id == stopID {
			return path, false, true
		}
	}
	ranks := make([]int, len(path))
	for i, id := range path {
		ranks[i] = -1
		if id == 0 {
			continue
		}
		for si := range surfaces {
			if surfaces[si].ID == id {
				ranks[i] = si
				break
			}
		}
	}
	for i := 1; i < len(ranks); i++ {
		if ranks[i] <= ranks[i-1] {
			return path, false, false
		}
	}
	for i := 1; i < len(ranks); i++ {
		if ranks[i] > stopIdx {
			out = append(out, path[:i]...)
			out = append(out, stopID)
			out = append(out, path[i:]...)
			return out, true, true
		}
	}
	return path, false, true
}

// shiftStopFrame translates every absolute-Z input by dz, so a document whose
// stop became the new first surface keeps the same object distances and ray
// origins. Surface thicknesses are relative and need no change.
func shiftStopFrame(input *types.Input, dz float64) {
	if dz == 0 {
		return
	}
	if input.Chief != nil {
		for i := range input.Chief.Fields {
			shiftObjectZ(&input.Chief.Fields[i].ObjectZ, input.Chief.Fields[i].Height, dz)
		}
		if input.Chief.PassThrough != nil {
			input.Chief.PassThrough.Coordinate.Z += dz
		}
	}
	for ci := range input.Configs {
		for fi := range input.Configs[ci].Fields {
			f := &input.Configs[ci].Fields[fi]
			shiftObjectZ(&f.ObjectZ, f.Height, dz)
		}
		if rays := input.Configs[ci].Rays; rays != nil {
			for ri := range rays.Rays {
				rays.Rays[ri].Initial.Origin.Z += dz
			}
		}
	}
	if input.Rays != nil {
		for ri := range input.Rays.Rays {
			input.Rays.Rays[ri].Initial.Origin.Z += dz
		}
	}
	if ts := input.TraceSingle; ts != nil {
		if len(ts.Origin) >= 3 {
			ts.Origin[2] += dz
		}
		if len(ts.Aim) >= 3 {
			ts.Aim[2] += dz
		}
	}
	if input.Cam != nil && input.Cam.ImagePlaneZ != 0 {
		input.Cam.ImagePlaneZ += dz
	}
}

// shiftObjectZ shifts a field's object plane. A finite-conjugate field with no
// explicit object_z uses the chief pass's implicit -1000 mm plane; materialize
// it so the shifted document keeps the same object distance.
func shiftObjectZ(z *float64, height, dz float64) {
	if math.Abs(height) > 1e-12 && *z == 0 {
		*z = -1000 + dz
		return
	}
	if *z != 0 {
		*z += dz
	}
}

// dropPupilModelVariables removes optimisation variables that target the
// virtual entrance pupil (the model is gone after materialization). Links
// sourced from a removed variable are removed too, so no dangling reference
// survives into a later optimize/escape run.
func dropPupilModelVariables(opt *types.OptimizationConfig) []string {
	if opt == nil {
		return nil
	}
	var removed []string
	kept := opt.Variables[:0]
	for _, v := range opt.Variables {
		if v.Target.Type == "pupil_model" {
			removed = append(removed, v.Name)
			continue
		}
		kept = append(kept, v)
	}
	opt.Variables = kept

	keptLocal := opt.LocalVariables[:0]
	for _, v := range opt.LocalVariables {
		if v.Target.Type == "pupil_model" {
			removed = append(removed, v.Name)
			continue
		}
		keptLocal = append(keptLocal, v)
	}
	opt.LocalVariables = keptLocal

	gone := make(map[string]bool, len(removed))
	for _, name := range removed {
		gone[name] = true
	}
	for {
		changed := false
		keptLinks := opt.VariableLinks[:0]
		for _, lk := range opt.VariableLinks {
			if lk.Target.Type == "pupil_model" || gone[lk.Source] {
				removed = append(removed, lk.Name)
				gone[lk.Name] = true
				changed = true
				continue
			}
			keptLinks = append(keptLinks, lk)
		}
		opt.VariableLinks = keptLinks
		if !changed {
			break
		}
	}
	return removed
}

// verifyStopPupil compares the virtual-pupil system with the materialized one:
// the per-field chief-ray spot RMS before and after. A conversion that preserves
// the entrance pupil leaves the rays unchanged, so the RMS delta is a direct
// check of the stop solve.
func verifyStopPupil(input *types.Input, ref int, before, after []types.Surface, stopID int, wavelength float64, gc *glass.Catalog, pm *types.PupilModelConfig) (*types.StopInsertVerification, string) {
	fields := fieldDefsFromItems(input.Configs[ref].Fields)
	if len(fields) == 0 {
		fields = chiefFieldDefs(*input)
	}
	if len(fields) == 0 {
		return nil, "verification skipped: the reference config declares no fields"
	}
	if input.Chief == nil || input.Chief.ReferenceSurface <= 0 {
		return nil, "verification skipped: chief.reference_surface is not set"
	}
	refSurface := input.Chief.ReferenceSurface
	numRays := input.Chief.NumRays
	if numRays <= 0 {
		numRays = 256
	}
	grid := input.Chief.GridType
	if grid == "" {
		grid = types.GridPolar
	}
	pol := polarization(*input)
	definition := chiefRayDefinition(*input)

	b := append([]types.Surface{}, before...)
	a := append([]types.Surface{}, after...)
	surface.Precompute(b)
	surface.Precompute(a)

	beforeRes := chief.DetermineChiefRaysGridMode(
		types.System{Surfaces: b}, fields, refSurface, numRays, gc, pol, wavelength,
		false, grid, input.Chief.PassThrough, nil, nil, pm, 0, 0, definition)
	afterRes := chief.DetermineChiefRaysGridMode(
		types.System{Surfaces: a, StopSurface: stopID}, fields, refSurface, numRays, gc, pol, wavelength,
		false, grid, input.Chief.PassThrough, nil, nil, nil, 0, 0, definition)
	if err := chief.CheckFieldResolution("stop verify", beforeRes); err != nil {
		return nil, "verification skipped: " + err.Error()
	}
	if err := chief.CheckFieldResolution("stop verify", afterRes); err != nil {
		return nil, "verification skipped: " + err.Error()
	}

	v := &types.StopInsertVerification{}
	for i := range beforeRes {
		if beforeRes[i].SpotStats == nil || afterRes[i].SpotStats == nil {
			continue
		}
		bv := beforeRes[i].SpotStats.RMS_R
		av := afterRes[i].SpotStats.RMS_R
		if math.IsNaN(bv) || math.IsNaN(av) {
			continue
		}
		v.Fields = append(v.Fields, types.StopInsertFieldCheck{Index: i, BeforeMM: bv, AfterMM: av})
		d := math.Abs(av - bv)
		if d > v.MaxRMSDeltaMM {
			v.MaxRMSDeltaMM = d
		}
		if rel := d / math.Max(bv, 1e-12); rel > v.MaxRMSDeltaRel {
			v.MaxRMSDeltaRel = rel
		}
	}
	if len(v.Fields) == 0 {
		return nil, "verification skipped: neither pass produced spot statistics"
	}
	if v.MaxRMSDeltaRel > 0.05 && v.MaxRMSDeltaMM > 0.005 {
		v.Warning = fmt.Sprintf(
			"the materialized system's chief spot RMS differs by up to %.4f mm (%.1f%%); check the stop conversion",
			v.MaxRMSDeltaMM, 100*v.MaxRMSDeltaRel)
	}
	return v, ""
}
