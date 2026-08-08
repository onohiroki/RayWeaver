package optimize

import (
	"github.com/hiroki/rayweaver/internal/glass"
)

type glassAccum struct {
	nd, vd       float64
	hasND, hasVD bool
	origLabel    string
}

// MaterializeGlassEntries accumulates nd/vd variables grouped by glass key,
// and invokes rewrite for each key pair so the caller can rewrite matching
// surface materials to the optimised inline model glass. Keys without an nd/vd
// pair are left untouched (their surface keeps its original catalogue key).
func MaterializeGlassEntries(variables []Variable, x []float64, gc *glass.Catalog, glassKey func(Variable) (string, bool), rewrite func(origKey string, nd, vd float64)) {
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

	for origKey, acc := range glassMap {
		if !acc.hasND || !acc.hasVD {
			continue
		}
		rewrite(origKey, acc.nd, acc.vd)
	}
}
