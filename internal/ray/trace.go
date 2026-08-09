package ray

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/coating"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

type RayError string

const (
	ErrSurfaceNotFound RayError = "surface_not_found"
	ErrMissedSurface   RayError = "missed_surface"
	ErrApertureStop    RayError = "aperture_stop"
	ErrTIR             RayError = "total_internal_reflection"
	ErrGlassPathShort  RayError = "glass_path_too_short"
	ErrGlassPathLong   RayError = "glass_path_too_long"
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

	var glassEntryPos types.Vec3
	var glassEntrySurfaceID int

	// In Lenient mode, aperture and glass-path checks still run but each failure
	// is recorded per-surface (rather than aborting the trace). The explicit skip
	// flags (SkipApertureCheck etc.) still bypass the checks entirely.
	skipAperture := ray.SkipApertureCheck
	skipGlassPath := ray.SkipGlassPathCheck
	skipAutoAperture := ray.SkipAutoApertureCheck

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
			result.ErrorCode = string(ErrSurfaceNotFound)
			return result
		}

		localOrigin := currentSurf.GlobalToLocal.MultiplyPoint(state.Origin)
		localDir := currentSurf.GlobalToLocal.MultiplyVector(state.Direction).Normalize()

		t, ok := intersect(currentSurf, localOrigin, localDir)
		if !ok {
			if ray.Lenient {
				sr := types.SurfaceResult{
					SurfaceID:   currentID,
					Position:    state.Origin,
					Direction:   state.Direction,
					Interaction: types.Missed,
					Thickness:   0,
					OPL:         0,
					Jones:       jones,
					ErrorCode:   string(ErrMissedSurface),
				}
				if len(result.Surfaces) > 0 {
					prev := &result.Surfaces[len(result.Surfaces)-1]
					sr.OPL = prev.OPL
				}
				result.Surfaces = append(result.Surfaces, sr)
				continue
			}
			result.Error = "ray missed surface"
			result.ErrorCode = string(ErrMissedSurface)
			return result
		}
		if i == 0 && t < 1e-12 {
			if ray.Lenient {
				sr := types.SurfaceResult{
					SurfaceID:   currentID,
					Position:    state.Origin,
					Direction:   state.Direction,
					Interaction: types.Missed,
					Thickness:   t,
					OPL:         0,
					Jones:       jones,
					ErrorCode:   string(ErrMissedSurface),
				}
				if len(result.Surfaces) > 0 {
					prev := &result.Surfaces[len(result.Surfaces)-1]
					sr.OPL = prev.OPL
				}
				result.Surfaces = append(result.Surfaces, sr)
				continue
			}
			result.Error = fmt.Sprintf("ray missed surface (t=%.6e < 0 on first lens surface)", t)
			result.ErrorCode = string(ErrMissedSurface)
			return result
		}

		hitPoint := types.Vec3{
			X: localOrigin.X + localDir.X*t,
			Y: localOrigin.Y + localDir.Y*t,
			Z: localOrigin.Z + localDir.Z*t,
		}

		if currentSurf.Diameter > 0 && !skipAperture && !(skipAutoAperture && currentSurf.AutoAperture) && !(skipGlassPath && currentSurf.AutoAperture) {
			h := math.Sqrt(hitPoint.X*hitPoint.X + hitPoint.Y*hitPoint.Y)
			if h > currentSurf.Diameter/2 {
				if ray.Lenient {
					sr := types.SurfaceResult{
						SurfaceID:   currentID,
						Position:    currentSurf.LocalToGlobal.MultiplyPoint(hitPoint),
						Direction:   state.Direction,
						Interaction: types.Missed,
						Thickness:   t,
						OPL:         0,
						Jones:       jones,
						ErrorCode:   string(ErrApertureStop),
					}
					if len(result.Surfaces) > 0 {
						prev := &result.Surfaces[len(result.Surfaces)-1]
						sr.OPL = prev.OPL
					}
					result.Surfaces = append(result.Surfaces, sr)
					continue
				}
				result.Error = "ray missed surface (aperture stop)"
				result.ErrorCode = string(ErrApertureStop)
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
		if currentSurf.Reflects() {
			interaction = types.Reflect
		} else if i+1 < len(ray.Path) {
			interaction = raymath.DetermineInteraction(prevID, currentID, ray.Path[i+1])
		}

		// Incident and emergent media depend on the physical travel direction.
		// A forward ray approaches surface i from the object side (medium
		// M[i-1]) and leaves into M[i]; after a path-encoded reflection it
		// travels backward, approaching from the image side (medium M[i]) and
		// leaving into M[i-1]. Fold-mirror surfaces rotate the beam frame so
		// the ray keeps travelling +Z locally; localDir.Z < 0 therefore
		// identifies ghost backward travel.
		n1mat := materialBefore(surfaces, currentID)
		n2mat := currentSurf.Material
		if localDir.Z < 0 {
			n1mat, n2mat = n2mat, n1mat
		}
		n1, _ := e.Glass.RefractiveIndex(n1mat, ray.Wavelength)
		n2, _ := e.Glass.RefractiveIndex(n2mat, ray.Wavelength)

		tir := false
		if interaction == types.Reflect {
			state.Direction = raymath.Reflect(localDir, normal)
		} else {
			newDir, ok := raymath.Refract(localDir, normal, n1, n2)
			if !ok {
				if ray.Lenient {
					tir = true
					interaction = types.Reflect
					state.Direction = raymath.Reflect(localDir, normal)
				} else {
					result.Error = "total internal reflection"
					result.ErrorCode = string(ErrTIR)
					return result
				}
			} else {
				state.Direction = newDir
			}
		}

		globalDir := currentSurf.LocalToGlobal.MultiplyVector(state.Direction).Normalize()
		state.Direction = globalDir

		var intensityS, intensityP float64
		var cosTheta2 float64

		if tir {
			// TIR in Lenient mode: 100 % reflection.
			intensityS = 1.0
			intensityP = 1.0
			cosTheta2 = cosTheta1
		} else if interaction == types.Reflect {
			cosTheta2 = math.Sqrt(math.Max(0, 1-(n1/n2)*(n1/n2)*(1-cosTheta1*cosTheta1)))
			rs, rp, _, _ := raymath.FresnelAmplitude(n1, n2, cosTheta1, cosTheta2)
			if currentSurf.Reflects() {
				// Fold mirror: ideal reflection.
				intensityS = 1.0
				intensityP = 1.0
			} else {
				// Path-encoded ghost reflection at a lens surface: Fresnel.
				intensityS = rs * rs
				intensityP = rp * rp
			}
		} else {
			cosTheta2 = math.Sqrt(math.Max(0, 1-(n1/n2)*(n1/n2)*(1-cosTheta1*cosTheta1)))
			_, _, ts, tp := raymath.FresnelAmplitude(n1, n2, cosTheta1, cosTheta2)
			intensityS = ts * ts * (n2 * cosTheta2) / (n1 * cosTheta1)
			intensityP = tp * tp * (n2 * cosTheta2) / (n1 * cosTheta1)
		}

		if currentSurf.Coating != "" && e.Coating != nil {
			if entry, ok := e.Coating.Lookup(currentSurf.Coating); ok {
				tmmResult := coating.ComputeTMM(n1, n2, entry.Layers, ray.Wavelength, math.Acos(cosTheta1))
				if interaction == types.Reflect {
					intensityS = tmmResult.Rs
					intensityP = tmmResult.Rp
				} else {
					intensityS *= tmmResult.Ts
					intensityP *= tmmResult.Tp
				}
			}
		}

		globalPos := currentSurf.LocalToGlobal.MultiplyPoint(hitPoint)
		globalDir = state.Direction

		if !skipGlassPath && glassEntrySurfaceID > 0 {
			path := globalPos.Subtract(glassEntryPos).Length()
			entrySurf := findSurface(surfaces, glassEntrySurfaceID)
			if entrySurf != nil {
				if entrySurf.MinGlassPath > 0 && path < entrySurf.MinGlassPath {
					if ray.Lenient {
						sr := types.SurfaceResult{
							SurfaceID:   currentID,
							Position:    globalPos,
							Direction:   globalDir,
							Interaction: interaction,
							Thickness:   t,
							OPL:         0,
							Jones:       jones,
							ErrorCode:   string(ErrGlassPathShort),
						}
						if len(result.Surfaces) > 0 {
							prev := &result.Surfaces[len(result.Surfaces)-1]
							sr.OPL = prev.OPL
						}
						result.Surfaces = append(result.Surfaces, sr)
						state.Origin = globalPos
						glassEntrySurfaceID = 0
						continue
					}
					result.Error = "ray missed surface (glass path too short)"
					result.ErrorCode = string(ErrGlassPathShort)
					return result
				}
				if entrySurf.MaxGlassPath > 0 && path > entrySurf.MaxGlassPath {
					if ray.Lenient {
						sr := types.SurfaceResult{
							SurfaceID:   currentID,
							Position:    globalPos,
							Direction:   globalDir,
							Interaction: interaction,
							Thickness:   t,
							OPL:         0,
							Jones:       jones,
							ErrorCode:   string(ErrGlassPathLong),
						}
						if len(result.Surfaces) > 0 {
							prev := &result.Surfaces[len(result.Surfaces)-1]
							sr.OPL = prev.OPL
						}
						result.Surfaces = append(result.Surfaces, sr)
						state.Origin = globalPos
						glassEntrySurfaceID = 0
						continue
					}
					result.Error = "ray missed surface (glass path too long)"
					result.ErrorCode = string(ErrGlassPathLong)
					return result
				}
			}
		}
		if interaction == types.Reflect {
			glassEntrySurfaceID = 0
		} else if !currentSurf.Material.IsAir() {
			glassEntryPos = globalPos
			glassEntrySurfaceID = currentID
		} else {
			glassEntrySurfaceID = 0
		}

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
		if tir {
			sr.ErrorCode = string(ErrTIR)
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

// materialBefore returns the medium on the object side of the surface: the
// material of the surface that precedes `id` in the sequence, skipping any
// intervening fold mirrors (which do not separate media). It is the region a
// forward-travelling ray is in just before hitting the surface, and the region
// a backward-travelling (ghost) ray leaves when crossing the surface.
func materialBefore(surfaces []types.Surface, id int) types.Material {
	idx := indexOfSurface(surfaces, id)
	if idx <= 0 {
		return types.Material{}
	}
	for idx > 0 {
		s := &surfaces[idx-1]
		if !s.Reflects() {
			return s.Material
		}
		idx--
	}
	return types.Material{}
}

func indexOfSurface(surfaces []types.Surface, id int) int {
	for i := range surfaces {
		if surfaces[i].ID == id {
			return i
		}
	}
	return -1
}

func intersect(surf *types.Surface, origin, dir types.Vec3) (float64, bool) {
	return surface.Intersect(*surf, origin, dir)
}

func computeNormal(surf *types.Surface, p types.Vec3) types.Vec3 {
	return surface.Normal(*surf, p)
}
