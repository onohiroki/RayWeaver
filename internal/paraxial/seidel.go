package paraxial

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

type SeidelCoefficients struct {
	Spherical   float64 `yaml:"spherical"`
	Coma        float64 `yaml:"coma"`
	Astigmatism float64 `yaml:"astigmatism"`
	Petzval     float64 `yaml:"petzval"`
}

func traceChiefForward(surfaces []types.Surface, nIndex []float64, fieldSlope float64) []rayState {
	vertices := make([]rayState, 0, len(surfaces))
	z := 0.0
	y, u := 0.0, fieldSlope
	for i := 0; i < len(surfaces); i++ {
		nBefore := 1.0
		if i > 0 {
			nBefore = nIndex[i-1]
		}
		nAfter := nIndex[i]

		R := surfaces[i].ParaxialRadius
		if R != 0 {
			phi := (nAfter - nBefore) / R
			u = (nBefore*u - y*phi) / nAfter
		}
		// Store state at this surface (height at surface, angle after refraction)
		vertices = append(vertices, rayState{Z: z, Y: y, U: u})
		y = y + surfaces[i].Thickness*u
		z += surfaces[i].Thickness
	}
	return vertices
}

func ComputeSeidel(surfaces []types.Surface, fieldAngleDeg float64, wavelength float64, gc *glass.Catalog) SeidelCoefficients {
	nIndex := resolveIndices(surfaces, wavelength, gc)

	fieldSlope := math.Tan(fieldAngleDeg * math.Pi / 180.0)

	margVerts, _ := traceForward(surfaces, nIndex, 1.0, 0.0)
	chiefVerts := traceChiefForward(surfaces, nIndex, fieldSlope)

	n := len(surfaces)
	if len(margVerts) < n {
		n = len(margVerts)
	}
	if len(chiefVerts) < n {
		n = len(chiefVerts)
	}

	var S1, S2, S3, S4 float64

	for i := 0; i < n; i++ {
		y := margVerts[i].Y
		yp := chiefVerts[i].Y

		nBefore := 1.0
		if i > 0 {
			nBefore = nIndex[i-1]
		}
		nAfter := nIndex[i]
		if nAfter == 0 {
			nAfter = 1.0
		}

		uBefore := 0.0
		upBefore := fieldSlope
		if i > 0 {
			uBefore = margVerts[i-1].U
			upBefore = chiefVerts[i-1].U
		}

		uAfter := margVerts[i].U

		c := surfaces[i].Curvature

		A := nBefore * (y*c + uBefore)
		Ap := nBefore * (yp*c + upBefore)

		rNBefore := 1.0 / nBefore
		rNAfter := 1.0 / nAfter
		deltaUN := uAfter*rNAfter - uBefore*rNBefore

		S1 += A * A * y * deltaUN
		S2 += A * Ap * y * deltaUN
		S3 += Ap * Ap * y * deltaUN

		H := nBefore * (y*upBefore - yp*uBefore)
		S4 += H * H * c * (rNAfter - rNBefore)
	}

	// Standard ZEMAX sign convention: positive coefficients
	if S1 < 0 {
		S1 = -S1
	}
	if S2 < 0 {
		S2 = -S2
	}
	if S3 < 0 {
		S3 = -S3
	}
	if S4 < 0 {
		S4 = -S4
	}

	return SeidelCoefficients{
		Spherical:   S1,
		Coma:        S2,
		Astigmatism: S3,
		Petzval:     S4,
	}
}
