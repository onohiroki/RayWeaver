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
	Distortion  float64 `yaml:"distortion"`
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

	var S1, S2, S3, S4, S5 float64

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

		s1 := A * A * y * deltaUN
		s2 := A * Ap * y * deltaUN
		s3 := Ap * Ap * y * deltaUN

		H := nBefore * (y*upBefore - yp*uBefore)
		s4 := H * H * c * (rNAfter - rNBefore)

		// Distortion (S5): per-surface contribution (Ap/A)*(S3 + S4).
		// A == 0 (normal incidence of the marginal ray) is a degenerate case;
		// skip the surface to avoid a division by zero.
		var s5 float64
		if math.Abs(A) > 1e-12 {
			s5 = (Ap / A) * (s3 + s4)
		}

		S1 += s1
		S2 += s2
		S3 += s3
		S4 += s4
		S5 += s5
	}

	// Standard ZEMAX sign convention: positive coefficients.
	// S5 (distortion) keeps its sign since barrel/pincushion is meaningful.
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
		Distortion:  S5,
	}
}
