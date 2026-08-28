package ray

import (
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

func ResolveRay(ray *types.Ray, surfaces []types.Surface, engine *Engine) {
	if ray.Aim != nil {
		dir := ray.Aim.Subtract(ray.Initial.Origin)
		ray.Initial.Direction = dir.Normalize()
		ray.Aim = nil
	}
	if ray.PassThrough != nil {
		resolvePassThrough(ray, surfaces, engine)
		ray.PassThrough = nil
	}
}

func resolvePassThrough(ray *types.Ray, surfaces []types.Surface, engine *Engine) {
	targetID := ray.PassThrough.Surface
	targetY := ray.PassThrough.Coordinate.Y
	targetX := ray.PassThrough.Coordinate.X

	if ray.PassThrough.Variable == "origin" {
		resolvePassThroughOrigin(ray, surfaces, engine, targetID, targetY, targetX)
	} else {
		resolvePassThroughDirection(ray, surfaces, engine, targetID, targetY, targetX)
	}
}

func resolvePassThroughDirection(ray *types.Ray, surfaces []types.Surface, engine *Engine, targetID int, targetY, targetX float64) {
	if targetY != 0 {
		bestDy := findBracket(
			ray, surfaces, engine, targetID,
			targetY, 0.6,
			func(r *types.Ray, dy float64) {
				dz := math.Sqrt(1 - dy*dy)
				r.Initial.Direction = types.Vec3{X: r.Initial.Direction.X, Y: dy, Z: dz}
			},
			func(sr types.SurfaceResult) float64 { return sr.Position.Y },
		)
		dz := math.Sqrt(1 - bestDy*bestDy)
		ray.Initial.Direction = types.Vec3{X: ray.Initial.Direction.X, Y: bestDy, Z: dz}
	}

	if targetX != 0 {
		bestDx := findBracket(
			ray, surfaces, engine, targetID,
			targetX, 0.6,
			func(r *types.Ray, dx float64) {
				dy := r.Initial.Direction.Y
				dz := math.Sqrt(1 - dx*dx - dy*dy)
				r.Initial.Direction = types.Vec3{X: dx, Y: dy, Z: dz}
			},
			func(sr types.SurfaceResult) float64 { return sr.Position.X },
		)
		dz2 := 1 - bestDx*bestDx - ray.Initial.Direction.Y*ray.Initial.Direction.Y
		if dz2 > 0 {
			ray.Initial.Direction.X = bestDx
			ray.Initial.Direction.Z = math.Sqrt(dz2)
		}
	}
}

func resolvePassThroughOrigin(ray *types.Ray, surfaces []types.Surface, engine *Engine, targetID int, targetY, targetX float64) {
	if targetY != 0 {
		maxV := math.Max(math.Abs(targetY)*2, 20)
		bestY := findBracket(
			ray, surfaces, engine, targetID,
			targetY, maxV,
			func(r *types.Ray, y0 float64) {
				r.Initial.Origin.Y = y0
			},
			func(sr types.SurfaceResult) float64 { return sr.Position.Y },
		)
		ray.Initial.Origin.Y = bestY
	}

	if targetX != 0 {
		maxV := math.Max(math.Abs(targetX)*2, 20)
		bestX := findBracket(
			ray, surfaces, engine, targetID,
			targetX, maxV,
			func(r *types.Ray, x0 float64) {
				r.Initial.Origin.X = x0
			},
			func(sr types.SurfaceResult) float64 { return sr.Position.X },
		)
		ray.Initial.Origin.X = bestX
	}
}

func findBracket(
	ray *types.Ray, surfaces []types.Surface, engine *Engine,
	targetID int, targetValue, maxV float64,
	apply func(*types.Ray, float64),
	getValue func(types.SurfaceResult) float64,
) float64 {
	trace := func(v float64) (val float64, ok bool, clipped bool) {
		t := *ray
		t.PassThrough = nil
		t.Lenient = false
		apply(&t, v)
		r := engine.TraceRay(t, surfaces, false)
		if r.Error != "" {
			if r.Error == "ray missed surface (aperture stop)" {
				return targetValue * 2, true, true
			}
			return 0, false, false
		}
		for _, sr := range r.Surfaces {
			if sr.SurfaceID == targetID {
				return getValue(sr), true, false
			}
		}
		return 0, false, false
	}

	v0, ok, _ := trace(0)
	if !ok {
		return 0
	}

	if math.Abs(v0-targetValue) < 1e-9 {
		return 0
	}

	sign := 1.0
	if targetValue-v0 < 0 {
		sign = -1.0
	}

	step := maxV / 200
	if step < 0.001 {
		step = 0.001
	}

	v := 0.0
	currentVal := v0
	var prevV, prevVal float64

	for iter := 0; iter < 400; iter++ {
		prevV = v
		prevVal = currentVal
		v += sign * step
		if math.Abs(v) > maxV {
			return 0
		}
		val, ok, clipped := trace(v)
		if !ok {
			return 0
		}
		currentVal = val

		if clipped || (currentVal-targetValue)*(prevVal-targetValue) <= 0 {
			if clipped {
				currentVal = targetValue * 2
			}
			return binarySearch(math.Min(prevV, v), math.Max(prevV, v), targetValue, prevVal, currentVal, trace)
		}
	}

	return 0
}

func binarySearch(lo, hi, targetValue, valLo, valHi float64, trace func(float64) (float64, bool, bool)) float64 {
	for iter := 0; iter < 60; iter++ {
		mid := (lo + hi) / 2
		midVal, ok, clipped := trace(mid)
		if !ok {
			return mid
		}
		if clipped {
			hi = mid
			continue
		}
		if math.Abs(midVal-targetValue) < 1e-9 {
			return mid
		}
		if (midVal-targetValue)*(valLo-targetValue) >= 0 {
			lo = mid
			valLo = midVal
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}
