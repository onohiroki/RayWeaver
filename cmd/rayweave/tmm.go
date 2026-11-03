package main

import (
	"github.com/hiroki/rayweaver/internal/coating"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

func runTMM(data []byte) {
	input := parseYAML[types.TMMInput](data)

	// Resolve layer refractive indices: try glass_catalog, then direct n:
	if input.GlassCatalog != nil {
		gc := glass.NewCatalog()
		for _, g := range input.GlassCatalog.Entries {
			gc.Add(g)
		}
		coating.ResolveLayers(input.Layers, gc, input.Lambda)
	}

	thetaRad := raymath.DegToRad(input.ThetaDeg)
	result := coating.ComputeTMM(input.NAir, input.NSub, input.Layers, input.Lambda, thetaRad)

	output := types.TMMOutput{
		Input: input,
		Rs:    result.Rs,
		Ts:    result.Ts,
		Rp:    result.Rp,
		Tp:    result.Tp,
	}

	writeYAML(&output)
}
