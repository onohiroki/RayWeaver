package ray

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/coating"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

type Engine struct {
	Glass   *glass.Catalog
	Coating *coating.Catalog
}

func NewEngine(gc *glass.Catalog, cc *coating.Catalog) *Engine {
	return &Engine{
		Glass:   gc,
		Coating: cc,
	}
}

func (e *Engine) TraceRay(ray types.Ray, surfaces []types.Surface) types.RayResult {
	result := types.RayResult{
		ID:         ray.ID,
		Wavelength: ray.Wavelength,
	}

	state := ray.Initial
	jones := ray.Jones

	for i := 0; i < len(ray.Path); i++ {
		currentID := ray.Path[i]

		if currentID == 0 {
			sr := types.SurfaceResult{
				SurfaceID:   0,
				Position:    state.Origin,
				Direction:   state.Direction.Normalize(),
				Interaction: types.Transmit,
				Thickness:   0,
				OPL:         0,
				Jones:       jones,
				IntensityS:  1.0,
				IntensityP:  1.0,
			}
			if len(result.Surfaces) > 0 {
				prev := &result.Surfaces[len(result.Surfaces)-1]
				sr.OPL = prev.OPL
			}
			result.Surfaces = append(result.Surfaces, sr)
			continue
		}

		prevID := 0
		if i > 0 {
			prevID = ray.Path[i-1]
		}

		currentSurf := findSurface(surfaces, currentID)
		if currentSurf == nil {
			result.Error = fmt.Sprintf("surface %d not found", currentID)
			return result
		}

		localOrigin := currentSurf.GlobalToLocal.MultiplyPoint(state.Origin)
		localDir := currentSurf.GlobalToLocal.MultiplyVector(state.Direction).Normalize()

		t, ok := intersect(currentSurf, localOrigin, localDir)
		if !ok {
			result.Error = "ray missed surface"
			return result
		}
		if i == 0 && t < 1e-12 {
			result.Error = fmt.Sprintf("ray missed surface (t=%.6e < 0 on first lens surface)", t)
			return result
		}

		hitPoint := types.Vec3{
			X: localOrigin.X + localDir.X*t,
			Y: localOrigin.Y + localDir.Y*t,
			Z: localOrigin.Z + localDir.Z*t,
		}

		if currentSurf.Diameter > 0 {
			h := math.Sqrt(hitPoint.X*hitPoint.X + hitPoint.Y*hitPoint.Y)
			if h > currentSurf.Diameter/2 {
				result.Error = "ray missed surface (aperture stop)"
				return result
			}
		}

		normal := computeNormal(currentSurf, hitPoint)

		cosTheta1 := -localDir.Dot(normal)
		if cosTheta1 < 0 {
			cosTheta1 = -cosTheta1
			normal = normal.Negate()
		}

		interaction := types.Transmit
		if i+1 < len(ray.Path) {
			interaction = raymath.DetermineInteraction(prevID, currentID, ray.Path[i+1])
		}

		n1, _ := e.Glass.RefractiveIndex(getPrevMaterial(surfaces, prevID), ray.Wavelength)
		n2, _ := e.Glass.RefractiveIndex(currentSurf.Material, ray.Wavelength)

		if interaction == types.Reflect {
			state.Direction = raymath.Reflect(localDir, normal)
		} else {
			newDir, ok := raymath.Refract(localDir, normal, n1, n2)
			if !ok {
				result.Error = "total internal reflection"
				return result
			}
			state.Direction = newDir
		}

		cosTheta2 := math.Sqrt(1 - (n1/n2)*(n1/n2)*(1-cosTheta1*cosTheta1))

		rs, rp, ts, tp := raymath.FresnelAmplitude(n1, n2, cosTheta1, cosTheta2)

		var intensityS, intensityP float64
		if interaction == types.Reflect {
			intensityS = rs * rs
			intensityP = rp * rp
		} else {
			intensityS = ts * ts * (n2 * cosTheta2) / (n1 * cosTheta1)
			intensityP = tp * tp * (n2 * cosTheta2) / (n1 * cosTheta1)
		}

		if currentSurf.Coating != "" && e.Coating != nil {
			if entry, ok := e.Coating.Lookup(currentSurf.Coating); ok {
				nSub := n2
				if interaction == types.Reflect {
					nSub = n1
				}
				tmmResult := coating.ComputeTMM(n1, nSub, entry.Layers, ray.Wavelength, math.Acos(cosTheta1))
				intensityS *= tmmResult.Ts
				intensityP *= tmmResult.Tp
			}
		}

		globalPos := currentSurf.LocalToGlobal.MultiplyPoint(hitPoint)
		globalDir := currentSurf.LocalToGlobal.MultiplyVector(state.Direction).Normalize()

		segmentOPL := math.Abs(t) * n1

		sr := types.SurfaceResult{
			SurfaceID:   currentID,
			Position:    globalPos,
			Direction:   globalDir,
			Interaction: interaction,
			Thickness:   t,
			OPL:         segmentOPL,
			Jones:       jones,
			IntensityS:  intensityS,
			IntensityP:  intensityP,
		}

		if len(result.Surfaces) > 0 {
			prev := &result.Surfaces[len(result.Surfaces)-1]
			sr.OPL = prev.OPL + segmentOPL
		}

		result.Surfaces = append(result.Surfaces, sr)
		state.Origin = globalPos
	}

	if len(result.Surfaces) > 0 {
		last := result.Surfaces[len(result.Surfaces)-1]
		result.OPLTotal = last.OPL
		result.IntensityS = last.IntensityS
		result.IntensityP = last.IntensityP
	}

	return result
}

func findSurface(surfaces []types.Surface, id int) *types.Surface {
	for i := range surfaces {
		if surfaces[i].ID == id {
			return &surfaces[i]
		}
	}
	return nil
}

func getPrevMaterial(surfaces []types.Surface, prevID int) string {
	if prevID == 0 {
		return "AIR"
	}
	s := findSurface(surfaces, prevID)
	if s == nil {
		return "AIR"
	}
	return s.Material
}

func intersect(surf *types.Surface, origin, dir types.Vec3) (float64, bool) {
	switch surf.Type {
	case types.Sphere:
		return raymath.IntersectSphere(origin, dir, surf.Radius())
	case types.AspherePolynomial:
		sagFunc := func(h float64) float64 {
			return raymath.PolynomialAsphereSag(h, surf.Radius(), surf.Conic, surf.Coefficients)
		}
		return raymath.IntersectAsphere(origin, dir, sagFunc, 50, 1e-12)
	case types.AsphereZernike:
		sagFunc := func(h float64) float64 {
			return raymath.ZernikeAsphereSag(h, surf.Radius(), surf.Conic, surf.Coefficients, surf.NormRadius)
		}
		return raymath.IntersectAsphere(origin, dir, sagFunc, 50, 1e-12)
	default:
		return raymath.IntersectSphere(origin, dir, surf.Radius())
	}
}

func computeNormal(surf *types.Surface, p types.Vec3) types.Vec3 {
	switch surf.Type {
	case types.Sphere:
		if surf.Radius() == 0 {
			return types.Vec3{0, 0, 1}
		}
		return raymath.SphereNormal(p, surf.Radius())
	case types.AspherePolynomial:
		sagFunc := func(h float64) float64 {
			return raymath.PolynomialAsphereSag(h, surf.Radius(), surf.Conic, surf.Coefficients)
		}
		return raymath.AsphereNormal(p, sagFunc)
	case types.AsphereZernike:
		sagFunc := func(h float64) float64 {
			return raymath.ZernikeAsphereSag(h, surf.Radius(), surf.Conic, surf.Coefficients, surf.NormRadius)
		}
		return raymath.AsphereNormal(p, sagFunc)
	default:
		if surf.Radius() == 0 {
			return types.Vec3{0, 0, 1}
		}
		return raymath.SphereNormal(p, surf.Radius())
	}
}
