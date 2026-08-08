package coating

import (
	"math"
	"math/cmplx"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

type Catalog struct {
	ByName map[string]*types.CoatingEntry
}

func NewCatalog() *Catalog {
	return &Catalog{
		ByName: make(map[string]*types.CoatingEntry),
	}
}

func (c *Catalog) Add(entry types.CoatingEntry) {
	c.ByName[entry.Name] = &entry
}

func (c *Catalog) Lookup(name string) (*types.CoatingEntry, bool) {
	entry, ok := c.ByName[name]
	return entry, ok
}

func (c *Catalog) ResolveLayers(entry *types.CoatingEntry, gc *glass.Catalog, wavelength float64) {
	for i := range entry.Layers {
		layer := &entry.Layers[i]
		n, err := gc.RefractiveIndex(types.ParseMaterial(layer.Material), wavelength)
		if err != nil {
			n = 1.5
		}
		layer.N = n
	}
}

type TMMResult struct {
	Rs, Ts float64
	Rp, Tp float64
}

type tmmPolarization int

const (
	polS tmmPolarization = iota
	polP
)

func ComputeTMM(n0, ns float64, layers []types.CoatingLayer, lambda, thetaRad float64) TMMResult {
	rs, ts := computePol(n0, ns, layers, lambda, thetaRad, polS)
	rp, tp := computePol(n0, ns, layers, lambda, thetaRad, polP)
	return TMMResult{Rs: rs, Ts: ts, Rp: rp, Tp: tp}
}

func computePol(n0, ns float64, layers []types.CoatingLayer, lambda, theta0 float64, pol tmmPolarization) (R, T float64) {
	sinTheta0 := math.Sin(theta0)

	M := [2][2]complex128{
		{1 + 0i, 0 + 0i},
		{0 + 0i, 1 + 0i},
	}

	thetaPrev := theta0
	nPrev := n0

	for _, lay := range layers {
		nLayer := lay.N
		if nLayer == 0 {
			nLayer = 1.5
		}

		sinThetaPrev := math.Sin(thetaPrev)
		sinThetaLayer := nPrev * sinThetaPrev / nLayer
		if math.Abs(sinThetaLayer) > 1.0 {
			return 1, 0
		}
		thetaLayer := math.Asin(sinThetaLayer)
		cosThetaLayer := math.Cos(thetaLayer)

		delta := 2 * math.Pi * nLayer * cosThetaLayer * lay.Thickness * 1e-6 / lambda
		cosd := math.Cos(delta)
		sind := math.Sin(delta)

		var eta float64
		switch pol {
		case polS:
			eta = nLayer * cosThetaLayer
		case polP:
			if cosThetaLayer == 0 {
				cosThetaLayer = 1e-12
			}
			eta = nLayer / cosThetaLayer
		}

		m11 := complex(cosd, 0)
		m22 := complex(cosd, 0)
		m12 := complex(0, 1) * complex(sind/eta, 0)
		m21 := complex(0, 1) * complex(eta*sind, 0)

		Mi := [2][2]complex128{{m11, m12}, {m21, m22}}
		M = matMul2x2(M, Mi)

		nPrev = nLayer
		thetaPrev = thetaLayer
	}

	sinThetas := n0 * sinTheta0 / ns
	if math.Abs(sinThetas) > 1.0 {
		return 1, 0
	}
	thetas := math.Asin(sinThetas)
	cosTheta0 := math.Cos(theta0)
	cosThetas := math.Cos(thetas)

	var eta0, etas float64
	switch pol {
	case polS:
		eta0 = n0 * cosTheta0
		etas = ns * cosThetas
	case polP:
		if cosTheta0 == 0 {
			cosTheta0 = 1e-12
		}
		if cosThetas == 0 {
			cosThetas = 1e-12
		}
		eta0 = n0 / cosTheta0
		etas = ns / cosThetas
	}

	B := M[0][0] + M[0][1]*complex(etas, 0)
	C := M[1][0] + M[1][1]*complex(etas, 0)
	Y := C / B

	r := (complex(eta0, 0) - Y) / (complex(eta0, 0) + Y)
	t := 2 * complex(eta0, 0) / (complex(eta0, 0)*B + C)

	R = cmplx.Abs(r) * cmplx.Abs(r)
	T = (etas / eta0) * cmplx.Abs(t) * cmplx.Abs(t)
	return
}

func matMul2x2(a, b [2][2]complex128) [2][2]complex128 {
	return [2][2]complex128{
		{
			a[0][0]*b[0][0] + a[0][1]*b[1][0],
			a[0][0]*b[0][1] + a[0][1]*b[1][1],
		},
		{
			a[1][0]*b[0][0] + a[1][1]*b[1][0],
			a[1][0]*b[0][1] + a[1][1]*b[1][1],
		},
	}
}
