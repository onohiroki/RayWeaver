package main

import (
	"fmt"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// computeParaxial runs the paraxial trace for the selected config and returns
// the result. The logic mirrors runParaxial but returns structured data instead
// of writing pipeline YAML.
func computeParaxial(data []byte, input types.Input, gc *glass.Catalog, configFlag string) *types.ParaxialResult {
	surfaces := configSurfaces(input.Configs, &configFlag)
	surface.Precompute(surfaces)

	selectedSys := input.System
	selectedSys.Surfaces = surfaces
	if input.Chief != nil {
		selectedSys.StopSurface = input.Chief.StopSurface
	}

	wavelength := types.DefaultWavelength
	if input.Chief != nil && input.Chief.ReferenceWavelength > 0 {
		wavelength = input.Chief.ReferenceWavelength
	}
	if input.Chief == nil {
		input.Chief = &types.ChiefInput{}
	}
	input.Chief.ReferenceWavelength = wavelength
	objectHeight := 0.0
	if input.Paraxial != nil {
		objectHeight = input.Paraxial.ObjectHeight
	}

	var chiefRays []types.ChiefRayResult
	var temp struct {
		ChiefRays []types.ChiefRayResult `yaml:"chief_rays"`
	}
	if err := yaml.Unmarshal(data, &temp); err == nil {
		chiefRays = temp.ChiefRays
	}

	if selectedSys.StopSurface <= 0 && len(chiefRays) == 0 {
		pupil, err := dynamicPupilForInput(input, &configFlag, surfaces, gc)
		if err != nil {
			// The pupil is derived from the first field only, so an unreachable
			// image_height is a warning here rather than a command failure.
			errOut("Warning: %v", err)
		}
		if pupil != nil {
			chiefRays = append(chiefRays, types.ChiefRayResult{EntrancePupil: pupil})
		}
	}

	result := paraxial.Compute(selectedSys, wavelength, gc, objectHeight, chiefRays)
	return &result
}

// listParaxial renders the paraxial first-order properties as a key-value
// table. With showRoles, an element-roles table is appended.
func listParaxial(data []byte, input types.Input, gc *glass.Catalog, format string, configFlag string, showRoles bool) {
	result := computeParaxial(data, input, gc, configFlag)

	var props []paraxialProp
	add := func(name string, val float64) {
		if val != 0 {
			props = append(props, paraxialProp{Name: name, Value: formatTableFloat(val)})
		}
	}
	add("Focal Length (EFL) [mm]", result.FocalLength)
	add("BFL [mm]", result.SecondPrincipalFocus)
	add("F/# (inf conj)", result.InfConjImageSpaceFNumber)
	add("NA (inf conj)", result.InfConjImageSpaceNA)
	add("F/# (working)", result.ImageSpaceFNumber)
	add("NA (working)", result.ImageSpaceNA)
	add("EPD [mm]", result.EntrancePupilDiameter)
	add("EP Location [mm]", result.EntrancePupilLocation)
	add("Exit Pupil Dia [mm]", result.ExitPupilDiameter)
	add("Exit Pupil Loc [mm]", result.ExitPupilLocation)
	add("Half Angle of View [deg]", result.HalfAngleOfView)
	add("Total Track [mm]", result.TotalTrack)
	add("Magnification", result.Magnification)

	switch format {
	case "yaml", "json":
		type kvOutput struct {
			Properties []paraxialProp      `json:"properties" yaml:"properties"`
			Roles      []types.ElementRole `json:"element_roles,omitempty" yaml:"element_roles,omitempty"`
		}
		out := kvOutput{Properties: props}
		if showRoles {
			out.Roles = result.ElementRoles
		}
		emitListOutput(format, out)
	case "csv":
		fmt.Println("Paraxial Properties:")
		fmt.Println("Property,Value")
		for _, p := range props {
			fmt.Printf("%s,%s\n", p.Name, p.Value)
		}
		if showRoles && len(result.ElementRoles) > 0 {
			fmt.Println()
			fmt.Println("Element Roles:")
			fmt.Println("Surfaces,Phi,W,Role,nd Target,vd Target,nd Actual,vd Actual")
			for _, r := range result.ElementRoles {
				sids := fmt.Sprintf("%v", r.SurfaceIDs)
				fmt.Printf("%s,%s,%s,%s,%s,%s,%s,%s\n",
					sids,
					formatTableFloat(r.Phi),
					formatTableFloat(r.W),
					r.Role,
					formatTableFloat(r.NDTarget),
					formatTableFloat(r.VTarget),
					formatTableFloat(r.NDActual),
					formatTableFloat(r.VActual))
			}
		}
	default: // "table"
		fmt.Println("Paraxial:")
		if len(props) == 0 {
			fmt.Println("(no paraxial data)")
			return
		}
		cols := []tableColumn{
			{header: "Property"},
			{header: "Value", right: true},
		}
		for _, p := range props {
			cols[0].cells = append(cols[0].cells, p.Name)
			cols[1].cells = append(cols[1].cells, p.Value)
		}
		fmt.Print(renderTable(cols))

		if showRoles && len(result.ElementRoles) > 0 {
			fmt.Println()
			fmt.Println("Element Roles:")
			roleCols := []tableColumn{
				{header: "Surfaces"},
				{header: "Phi", right: true},
				{header: "W", right: true},
				{header: "Role"},
				{header: "nd Target", right: true},
				{header: "vd Target", right: true},
				{header: "nd Actual", right: true},
				{header: "vd Actual", right: true},
			}
			for _, r := range result.ElementRoles {
				roleCols[0].cells = append(roleCols[0].cells, fmt.Sprintf("%v", r.SurfaceIDs))
				roleCols[1].cells = append(roleCols[1].cells, formatTableFloat(r.Phi))
				roleCols[2].cells = append(roleCols[2].cells, formatTableFloat(r.W))
				roleCols[3].cells = append(roleCols[3].cells, r.Role)
				roleCols[4].cells = append(roleCols[4].cells, formatTableFloat(r.NDTarget))
				roleCols[5].cells = append(roleCols[5].cells, formatTableFloat(r.VTarget))
				roleCols[6].cells = append(roleCols[6].cells, formatTableFloat(r.NDActual))
				roleCols[7].cells = append(roleCols[7].cells, formatTableFloat(r.VActual))
			}
			fmt.Print(renderTable(roleCols))
		}
	}
}
