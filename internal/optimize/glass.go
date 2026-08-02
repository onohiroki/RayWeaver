package optimize

import (
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

type glassAccum struct {
	nd, vd       float64
	hasND, hasVD bool
	origLabel    string
}

// MaterializeGlassEntries accumulates nd/vd variables grouped by glass key,
// builds model-glass entries, and invokes rewrite for each key pair so the
// caller can rewrite matching surface materials.
func MaterializeGlassEntries(variables []Variable, x []float64, gc *glass.Catalog, glassKey func(Variable) (string, bool), rewrite func(origKey, newKey string)) []types.Glass {
	glassMap := map[string]*glassAccum{}
	for i, v := range variables {
		if v.Param != "nd" && v.Param != "vd" {
			continue
		}
		key, ok := glassKey(v)
		if !ok {
			continue
		}
		acc, ok := glassMap[key]
		if !ok {
			acc = &glassAccum{}
			if gc != nil {
				if g, ok2 := gc.Lookup(key); ok2 {
					acc.origLabel = g.Label
					acc.nd = g.ND
					acc.vd = g.VD
					acc.hasND = true
					acc.hasVD = true
				}
			}
			glassMap[key] = acc
		}
		switch v.Param {
		case "nd":
			acc.nd = x[i]
			acc.hasND = true
		case "vd":
			acc.vd = x[i]
			acc.hasVD = true
		}
	}

	var newGlasses []types.Glass
	for origKey, acc := range glassMap {
		if !acc.hasND || !acc.hasVD {
			continue
		}
		g := types.Glass{
			Type: types.GlassTypeModel,
			ND:   acc.nd,
			VD:   acc.vd,
		}
		if acc.origLabel != "" {
			g.Label = acc.origLabel
		}
		newKey := types.ResolveGlassKey(g)
		newGlasses = append(newGlasses, g)
		rewrite(origKey, newKey)
	}
	return newGlasses
}
