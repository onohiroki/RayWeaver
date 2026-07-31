package chief

import (
	"math"
	"runtime"
	"sync"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/types"
)

type Result struct {
	FieldAngle     float64
	FieldHeight    float64
	FieldDir       types.Vec3
	ChiefRay       types.Ray
	ImageHeight    types.Vec3
	EntrancePupil  types.Pupil
	GridPoints     []types.GridPoint
	SpotStats      *types.SpotStats
	RayFan         *types.RayFan
	PerSurfaceMaxY []float64
	Wavelengths    []types.WavelengthStats
}

func DetermineChiefRays(
	system types.System,
	fields []types.FieldDef,
	refSurfaceID int,
	numRays int,
	gc *glass.Catalog,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
) []Result {
	return determineChiefRays(system, fields, refSurfaceID, numRays, gc, pol, wavelength, dumpMap, types.GridPolar, nil, nil, nil)
}

func DetermineChiefRaysGrid(
	system types.System,
	fields []types.FieldDef,
	refSurfaceID int,
	numRays int,
	gc *glass.Catalog,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	passThrough *types.PassThroughTarget,
	fanCfg *types.RayFanConfig,
	wavelengths []float64,
) []Result {
	if gridType == "" {
		gridType = types.GridPolar
	}
	return determineChiefRays(system, fields, refSurfaceID, numRays, gc, pol, wavelength, dumpMap, gridType, passThrough, fanCfg, wavelengths)
}

func determineChiefRays(
	system types.System,
	fields []types.FieldDef,
	refSurfaceID int,
	numRays int,
	gc *glass.Catalog,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	passThrough *types.PassThroughTarget,
	fanCfg *types.RayFanConfig,
	wavelengths []float64,
) []Result {
	engine := ray.NewEngine(gc, nil)

	apertureRadius := FindMinApertureRadius(system.Surfaces)
	if apertureRadius <= 0 {
		return nil
	}

	var results []Result

	for _, fd := range fields {
		dx, dy := 0.0, 1.0
		if len(fd.Direction) >= 2 {
			norm := math.Hypot(fd.Direction[0], fd.Direction[1])
			if norm > 0 {
				dx = fd.Direction[0] / norm
				dy = fd.Direction[1] / norm
			}
		}

		var result Result
		switch {
		case math.Abs(fd.ImageHeight) > 1e-12:
			angle := searchAngleForImageHeight(system, engine, fd.ImageHeight,
				dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, gridType, passThrough)
			thetaRad := angle * math.Pi / 180.0
			if passThrough != nil && passThrough.Surface > 0 {
				result = computeChiefRayAngleGridWithPassThrough(system, engine, thetaRad, dx, dy,
					refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType, passThrough)
			} else {
				result = computeChiefRayAngleGrid(system, engine, thetaRad, dx, dy,
					refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType)
			}
			result.FieldAngle = angle

		case math.Abs(fd.Height) > 1e-12:
			objectZ := fd.ObjectZ
			if objectZ == 0 {
				objectZ = -1000.0
			}
			if passThrough != nil && passThrough.Surface > 0 {
				result = computeChiefRayHeightGridWithPassThrough(system, engine, fd.Height, dx, dy,
					objectZ, refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType, passThrough)
			} else {
				result = computeChiefRayHeightGrid(system, engine, fd.Height, dx, dy,
					objectZ, refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType)
			}
			result.FieldHeight = fd.Height

		default:
			thetaRad := fd.Angle * math.Pi / 180.0
			if passThrough != nil && passThrough.Surface > 0 {
				result = computeChiefRayAngleGridWithPassThrough(system, engine, thetaRad, dx, dy,
					refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType, passThrough)
			} else {
				result = computeChiefRayAngleGrid(system, engine, thetaRad, dx, dy,
					refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType)
			}
			result.FieldAngle = fd.Angle
		}
		result.FieldDir = types.Vec3{X: dx, Y: dy}

		// Multi-wavelength spot stats
		if len(wavelengths) > 0 {
			result.Wavelengths = computeWavelengthStats(
				engine, system, refSurfaceID, pol,
				result.GridPoints, result.ImageHeight, wavelengths,
			)
		}

		if fanCfg != nil && len(fanCfg.Angles) > 0 {
			result.RayFan = computeRayFan(
				engine, system,
				result.ChiefRay.Initial.Origin,
				result.ChiefRay.Initial.Direction,
				refSurfaceID, pol, wavelength,
				apertureRadius, fanCfg.Angles, fanCfg.NumRays,
			)
		}

		results = append(results, result)
	}

	if len(results) >= 2 {
		inferStopPosition(results)
	}

	return results
}

// --- angle-based (infinite conjugate) ---

func computeChiefRayAngle(
	system types.System,
	engine *ray.Engine,
	thetaRad, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
) Result {
	return computeChiefRayAngleGrid(system, engine, thetaRad, dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, types.GridPolar)
}

func computeChiefRayAngleGrid(
	system types.System,
	engine *ray.Engine,
	thetaRad, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
) Result {
	zStart := -100.0

	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)
	rayDir := types.Vec3{
		X: sinT * dx,
		Y: sinT * dy,
		Z: cosT,
	}.Normalize()

	stopZ := computeStopZ(system.Surfaces)
	tanT := sinT / cosT
	pupilCenterX := -(stopZ - zStart) * tanT * dx
	pupilCenterY := -(stopZ - zStart) * tanT * dy

	cx, cy, grid := tracePupilGrid(system, engine, numRays, apertureRadius,
		pupilCenterX, pupilCenterY, zStart, rayDir, types.Vec3{},
		refSurfaceID, pol, wavelength, dumpMap, gridType)

	originY := searchOriginForTarget(rayDir.Y, rayDir, zStart, refSurfaceID, cy,
		buildPath(system.Surfaces), wavelength, pol, engine, system.Surfaces, false,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y })
	originX := 0.0
	if math.Abs(rayDir.X) > 1e-12 {
		originX = searchOriginForTarget(rayDir.X, rayDir, zStart, refSurfaceID, cx,
			buildPath(system.Surfaces), wavelength, pol, engine, system.Surfaces, true,
			func(sr types.SurfaceResult) float64 { return sr.Position.X })
	}

	origin := types.Vec3{X: originX, Y: originY, Z: zStart}
	return buildResult(engine, system, origin, rayDir, refSurfaceID, pol, wavelength, cx, cy, apertureRadius, grid, dumpMap)
}

// --- pass-through constrained chief ray (angle-based) ---

func computeChiefRayAngleGridWithPassThrough(
	system types.System,
	engine *ray.Engine,
	thetaRad, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	pt *types.PassThroughTarget,
) Result {
	zStart := -100.0

	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)
	rayDir := types.Vec3{
		X: sinT * dx,
		Y: sinT * dy,
		Z: cosT,
	}.Normalize()

	path := buildPath(system.Surfaces)
	ptCoord := pt.Coordinate

	originY := searchOriginForTarget(rayDir.Y, rayDir, zStart, pt.Surface, ptCoord.Y,
		path, wavelength, pol, engine, system.Surfaces, false,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y })

	originX := 0.0
	if math.Abs(rayDir.X) > 1e-12 || math.Abs(ptCoord.X) > 1e-12 {
		originX = searchOriginForTarget(rayDir.X, rayDir, zStart, pt.Surface, ptCoord.X,
			path, wavelength, pol, engine, system.Surfaces, true,
			func(sr types.SurfaceResult) float64 { return sr.Position.X })
	}

	origin := types.Vec3{X: originX, Y: originY, Z: zStart}

	stopZ := computeStopZ(system.Surfaces)
	t := (stopZ - origin.Z) / rayDir.Z
	pupilCenterX := origin.X + t*rayDir.X
	pupilCenterY := origin.Y + t*rayDir.Y

	cx, cy, grid := tracePupilGrid(system, engine, numRays, apertureRadius,
		pupilCenterX, pupilCenterY, zStart, rayDir, types.Vec3{},
		refSurfaceID, pol, wavelength, dumpMap, gridType)

	return buildResult(engine, system, origin, rayDir, refSurfaceID,
		pol, wavelength, cx, cy, apertureRadius, grid, dumpMap)
}

// --- pass-through constrained chief ray (height-based) ---

func computeChiefRayHeightGridWithPassThrough(
	system types.System,
	engine *ray.Engine,
	height, dx, dy, objectZ float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	pt *types.PassThroughTarget,
) Result {
	objectPoint := types.Vec3{X: height * dx, Y: height * dy, Z: objectZ}
	path := buildPath(system.Surfaces)
	zStart := objectZ + (0.0-objectZ)*0.5
	ptCoord := pt.Coordinate

	targetZ := computeTargetZ(system.Surfaces, pt.Surface)
	baseDir := types.Vec3{
		X: ptCoord.X - objectPoint.X,
		Y: ptCoord.Y - objectPoint.Y,
		Z: targetZ - objectPoint.Z,
	}.Normalize()

	refinedDir := searchDirectionForTarget(objectPoint, pt.Surface, ptCoord.Y, baseDir,
		path, wavelength, pol, engine, system.Surfaces,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y })

	if math.Abs(ptCoord.X) > 1e-12 {
		refinedDir = searchDirectionForTarget(objectPoint, pt.Surface, ptCoord.X, refinedDir,
			path, wavelength, pol, engine, system.Surfaces,
			func(sr types.SurfaceResult) float64 { return sr.Position.X })
	}

	cx, cy, grid := tracePupilGrid(system, engine, numRays, apertureRadius,
		0, 0, zStart, refinedDir, objectPoint,
		refSurfaceID, pol, wavelength, dumpMap, gridType)

	return buildResult(engine, system, objectPoint, refinedDir, refSurfaceID,
		pol, wavelength, cx, cy, apertureRadius, grid, dumpMap)
}

// --- height-based (finite conjugate) ---

func computeChiefRayHeight(
	system types.System,
	engine *ray.Engine,
	height, dx, dy, objectZ float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
) Result {
	return computeChiefRayHeightGrid(system, engine, height, dx, dy, objectZ, refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, types.GridPolar)
}

func computeChiefRayHeightGrid(
	system types.System,
	engine *ray.Engine,
	height, dx, dy, objectZ float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
) Result {
	objectPoint := types.Vec3{X: height * dx, Y: height * dy, Z: objectZ}
	path := buildPath(system.Surfaces)

	// Estimate pupil plane between object and first surface.
	zStart := objectZ + (0.0-objectZ)*0.5

	// Initial direction estimate: from object toward the optical axis at the pupil plane.
	baseDir := types.Vec3{
		X: -objectPoint.X,
		Y: -objectPoint.Y,
		Z: zStart - objectPoint.Z,
	}.Normalize()

	// Sample grid and get centroid.
	cx, cy, grid := tracePupilGrid(system, engine, numRays, apertureRadius,
		0, 0, zStart, baseDir, objectPoint,
		refSurfaceID, pol, wavelength, dumpMap, gridType)

	// Refine the direction so the chief ray passes through the centroid at reference surface.
	refinedDir := searchDirectionForTarget(objectPoint, refSurfaceID, cy, baseDir,
		path, wavelength, pol, engine, system.Surfaces,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y })

	return buildResult(engine, system, objectPoint, refinedDir, refSurfaceID,
		pol, wavelength, cx, cy, apertureRadius, grid, dumpMap)
}

// --- binary search helpers ---

// searchOriginForTarget finds the initial origin component (X or Y) on the
// zStart plane such that the ray passes through targetVal at the target
// surface after refraction.
func searchOriginForTarget(
	dirComp float64,
	rayDir types.Vec3,
	zStart float64, targetID int, targetVal float64, path []int,
	wavelength float64, pol types.JonesVector,
	engine *ray.Engine, surfaces []types.Surface,
	isX bool, getPos func(types.SurfaceResult) float64,
) float64 {
	if math.Abs(dirComp) < 1e-12 {
		return 0
	}

	stopZ := computeStopZ(surfaces)
	tanComp := dirComp / math.Sqrt(1-dirComp*dirComp)
	geoEst := -(stopZ - zStart) * tanComp

	makeRay := func(originComp float64) types.Ray {
		orig := types.Vec3{X: 0, Y: 0, Z: zStart}
		if isX {
			orig.X = originComp
		} else {
			orig.Y = originComp
		}
		return types.Ray{
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: orig, Direction: rayDir},
			Path:       path,
			Jones:      pol,
		}
	}

	trace := func(originComp float64) (float64, bool, bool) {
		r := makeRay(originComp)
		res := engine.TraceRay(r, surfaces)
		for _, sr := range res.Surfaces {
			if sr.SurfaceID == targetID {
				return getPos(sr), true, false
			}
		}
		if res.Error != "" {
			return math.Inf(1), true, true
		}
		return 0, false, false
	}

	vEst, okEst, clipEst := trace(geoEst)
	if okEst && !clipEst && math.Abs(vEst-targetVal) < 1e-12 {
		return geoEst
	}

	// Find a safe anchor: any origin value where trace succeeds.
	// Try geoEst first; if that clips, walk outward in both directions.
	anchor := geoEst
	var vAnc float64
	found := false

	if okEst && !clipEst {
		vAnc = vEst
		found = true
	} else {
		step := math.Max(math.Abs(geoEst)*0.05, 0.5)
		for s := step; s < 200; s += step {
			if v, ok, clipped := trace(geoEst + s); ok && !clipped {
				anchor = geoEst + s
				vAnc = v
				found = true
				break
			}
			if v, ok, clipped := trace(geoEst - s); ok && !clipped {
				anchor = geoEst - s
				vAnc = v
				found = true
				break
			}
		}
	}

	if !found {
		return geoEst
	}

	if math.Abs(vAnc-targetVal) < 1e-12 {
		return anchor
	}

	sign := 1.0
	if targetVal < vAnc {
		sign = -1.0
	}

	maxV := math.Max(math.Abs(anchor)*2, 20.0)
	if maxV < 5 {
		maxV = 5
	}
	step := maxV / 200
	if step < 0.01 {
		step = 0.01
	}

	v := anchor
	currentVal := vAnc

	for iter := 0; iter < 400; iter++ {
		prevV := v
		prevVal := currentVal

		v += sign * step
		if math.Abs(v) > maxV {
			break
		}

		val, ok, clipped := trace(v)
		if !ok {
			break
		}
		currentVal = val

		if clipped || (currentVal-targetVal)*(prevVal-targetVal) <= 0 {
			return binarySearchOrigin(math.Min(prevV, v), math.Max(prevV, v),
				targetVal, prevVal, currentVal, trace)
		}
	}

	return geoEst
}

func binarySearchOrigin(lo, hi, targetVal, valLo, valHi float64, trace func(float64) (float64, bool, bool)) float64 {
	for iter := 0; iter < 60; iter++ {
		mid := (lo + hi) / 2
		midVal, ok, _ := trace(mid)
		if !ok {
			return mid
		}
		if math.IsInf(midVal, 1) {
			if math.IsInf(valLo, 1) {
				lo = mid
			} else {
				hi = mid
			}
			continue
		}
		if math.Abs(midVal-targetVal) < 1e-12 {
			return mid
		}
		if (midVal-targetVal)*(valLo-targetVal) >= 0 {
			lo = mid
			valLo = midVal
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// searchDirectionForTarget varies the tangent component of the ray direction
// so that the ray passes through targetVal at the target surface.
func searchDirectionForTarget(
	objectPoint types.Vec3, targetID int, targetVal float64,
	baseDir types.Vec3, path []int,
	wavelength float64, pol types.JonesVector,
	engine *ray.Engine, surfaces []types.Surface,
	getPos func(types.SurfaceResult) float64,
) types.Vec3 {
	tanComp := baseDir.Y / baseDir.Z
	useX := math.Abs(baseDir.X) > math.Abs(baseDir.Y)
	if useX {
		tanComp = baseDir.X / baseDir.Z
	} else if math.Abs(baseDir.Y) < 1e-12 {
		return baseDir
	}

	trace := func(tan float64) (float64, bool, bool) {
		dz := 1.0 / math.Sqrt(1+tan*tan)
		dx, dy := 0.0, 0.0
		if useX {
			dx = tan * dz
		} else {
			dy = tan * dz
		}
		dir := types.Vec3{X: dx, Y: dy, Z: dz}.Normalize()
		r := types.Ray{
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: objectPoint, Direction: dir},
			Path:       path,
			Jones:      pol,
		}
		res := engine.TraceRay(r, surfaces)
		for _, sr := range res.Surfaces {
			if sr.SurfaceID == targetID {
				return getPos(sr), true, false
			}
		}
		if res.Error != "" {
			return math.Inf(1), true, true
		}
		return 0, false, false
	}

	v0, ok0, clip0 := trace(tanComp)
	if ok0 && !clip0 && math.Abs(v0-targetVal) < 1e-12 {
		return baseDir
	}

	// Anchor: trace with tan=0 (direction parallel to axis).
	anchor := 0.0
	vAnc, okAnc, clipAnc := trace(anchor)
	if !okAnc || clipAnc {
		return baseDir
	}
	if math.Abs(vAnc-targetVal) < 1e-12 {
		dz := 1.0
		dir := types.Vec3{X: 0, Y: 0, Z: dz}.Normalize()
		return dir
	}

	sign := 1.0
	if targetVal < vAnc {
		sign = -1.0
	}

	maxV := math.Max(math.Abs(tanComp)*2, 0.5)
	if maxV < 0.5 {
		maxV = 0.5
	}
	step := maxV / 200
	if step < 0.0001 {
		step = 0.0001
	}

	v := anchor
	currentVal := vAnc

	for iter := 0; iter < 400; iter++ {
		prevV := v
		prevVal := currentVal

		v += sign * step
		if math.Abs(v) > maxV {
			break
		}

		val, ok, clipped := trace(v)
		if !ok {
			break
		}
		currentVal = val

		if clipped || (currentVal-targetVal)*(prevVal-targetVal) <= 0 {
			lo, hi := math.Min(prevV, v), math.Max(prevV, v)
			bestTan := binarySearchOrigin(lo, hi, targetVal, prevVal, currentVal, trace)

			dz := 1.0 / math.Sqrt(1+bestTan*bestTan)
			dxBest, dyBest := 0.0, 0.0
			if useX {
				dxBest = bestTan * dz
			} else {
				dyBest = bestTan * dz
			}
			return types.Vec3{X: dxBest, Y: dyBest, Z: dz}.Normalize()
		}
	}

	return baseDir
}

// --- shared helpers ---

// generateGridPoints returns a list of pupil (px, py) sample coordinates
// for the given grid type and aperture radius.
func GenerateGridPoints(numRays int, apertureRadius float64, gridType types.GridType) []struct{ X, Y float64 } {
	var pts []struct{ X, Y float64 }

	switch gridType {
	case types.GridSquare:
		n := int(math.Sqrt(float64(numRays)))
		if n < 2 {
			n = 2
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				x := (float64(i)+0.5)/float64(n)*2 - 1
				y := (float64(j)+0.5)/float64(n)*2 - 1
				if x*x+y*y <= 1 {
					pts = append(pts, struct{ X, Y float64 }{
						X: x * apertureRadius,
						Y: y * apertureRadius,
					})
				}
			}
		}

	case types.GridHex:
		n := int(math.Sqrt(float64(numRays))) + 1
		if n < 2 {
			n = 2
		}
		dy := apertureRadius * 2 / float64(n)
		dx := dy * math.Sqrt(3) / 2
		for i := 0; i < n; i++ {
			y := -apertureRadius + (float64(i)+0.5)*dy
			xOff := 0.0
			if i%2 == 1 {
				xOff = dx / 2
			}
			nx := int(float64(n) * apertureRadius / (dx * float64(n) / 2))
			if nx < 1 {
				nx = 1
			}
			for j := 0; j < nx; j++ {
				x := -apertureRadius + (float64(j)+0.5)*dx + xOff
				if x*x+y*y <= apertureRadius*apertureRadius {
					pts = append(pts, struct{ X, Y float64 }{
						X: x,
						Y: y,
					})
				}
			}
		}

	default: // GridPolar
		n := int(math.Sqrt(float64(numRays)))
		if n < 2 {
			n = 2
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				r := (float64(i) + 0.5) / float64(n) * apertureRadius
				theta := 2 * math.Pi * (float64(j) + 0.5) / float64(n)
				pts = append(pts, struct{ X, Y float64 }{
					X: r * math.Cos(theta),
					Y: r * math.Sin(theta),
				})
			}
		}
	}

	return pts
}

// tracePupilGrid distributes numRays samples in the given grid pattern
// centred on (pupilCenterX, pupilCenterY) at plane zStart.
func tracePupilGrid(
	system types.System,
	engine *ray.Engine,
	numRays int,
	apertureRadius float64,
	pupilCenterX, pupilCenterY, zStart float64,
	rayDir, rayOrigin types.Vec3,
	refSurfaceID int,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
) (cx, cy float64, grid []types.GridPoint) {
	var totalWeight float64
	var weightedX, weightedY float64

	path := buildPath(system.Surfaces)
	isHeightBased := rayOrigin.Z != 0 || rayOrigin.X != 0 || rayOrigin.Y != 0

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())

	samples := GenerateGridPoints(numRays, apertureRadius, gridType)
	for i := range samples {
		px := pupilCenterX + samples[i].X
		py := pupilCenterY + samples[i].Y

		wg.Add(1)
		sem <- struct{}{}
		go func(px, py float64) {
			defer wg.Done()
			defer func() { <-sem }()

			var rDir types.Vec3
			var rOrg types.Vec3
			if isHeightBased {
				rOrg = rayOrigin
				rDir = types.Vec3{
					X: px - rayOrigin.X,
					Y: py - rayOrigin.Y,
					Z: zStart - rayOrigin.Z,
				}.Normalize()
			} else {
				rOrg = types.Vec3{X: px, Y: py, Z: zStart}
				rDir = rayDir
			}

			ray := types.Ray{
				Wavelength: wavelength,
				Initial:    types.RayState{Origin: rOrg, Direction: rDir},
				Path:       path,
				Jones:      pol,
			}

			traceResult := engine.TraceRay(ray, system.Surfaces)
			if traceResult.Error != "" {
				mu.Lock()
				grid = append(grid, types.GridPoint{
					PupilX: px, PupilY: py,
					ImageX: nil, ImageY: nil, Intensity: 0,
					ErrorCode: traceResult.ErrorCode,
					Origin:    rOrg, Direction: rDir,
				})
				mu.Unlock()
				return
			}

			for _, sr := range traceResult.Surfaces {
				if sr.SurfaceID == refSurfaceID {
					weight := (sr.IntensityS + sr.IntensityP) / 2.0

					mu.Lock()
					totalWeight += weight
					weightedX += sr.Position.X * weight
					weightedY += sr.Position.Y * weight
					ix, iy := sr.Position.X, sr.Position.Y
					grid = append(grid, types.GridPoint{
						PupilX: px, PupilY: py,
						ImageX: &ix, ImageY: &iy, Intensity: weight,
						OPL:    traceResult.OPLTotal,
						Origin: rOrg, Direction: rDir,
					})
					mu.Unlock()
				}
			}
		}(px, py)
	}

	wg.Wait()
	close(sem)

	if totalWeight > 0 {
		cx = weightedX / totalWeight
		cy = weightedY / totalWeight
	}
	return
}

func computeSpotStats(grid []types.GridPoint, cx, cy float64) *types.SpotStats {
	s := &types.SpotStats{
		Centroid: types.Vec3{X: cx, Y: cy},
		MinX:     1e18,
		MinY:     1e18,
		MaxX:     -1e18,
		MaxY:     -1e18,
	}
	var sumSqX, sumSqY float64

	for _, gp := range grid {
		s.TotalRays++
		if gp.ImageX == nil || gp.ImageY == nil {
			s.MissedRays++
			continue
		}
		s.TracedRays++
		dx := *gp.ImageX - cx
		dy := *gp.ImageY - cy
		sumSqX += dx * dx
		sumSqY += dy * dy

		if *gp.ImageX < s.MinX {
			s.MinX = *gp.ImageX
		}
		if *gp.ImageX > s.MaxX {
			s.MaxX = *gp.ImageX
		}
		if *gp.ImageY < s.MinY {
			s.MinY = *gp.ImageY
		}
		if *gp.ImageY > s.MaxY {
			s.MaxY = *gp.ImageY
		}
	}

	if s.TracedRays > 0 {
		n := float64(s.TracedRays)
		s.RMS_X = math.Sqrt(sumSqX / n)
		s.RMS_Y = math.Sqrt(sumSqY / n)
		s.RMS_R = math.Sqrt((sumSqX + sumSqY) / n)
	}
	return s
}

func computeWavelengthStats(
	engine *ray.Engine,
	system types.System,
	refSurfaceID int,
	pol types.JonesVector,
	grid []types.GridPoint,
	chiefImgHeight types.Vec3,
	wavelengths []float64,
) []types.WavelengthStats {
	path := buildPath(system.Surfaces)
	type wlResult struct {
		index int
		stats types.SpotStats
	}
	ch := make(chan wlResult, len(wavelengths))
	var wg sync.WaitGroup

	for i, wl := range wavelengths {
		wg.Add(1)
		go func(i int, wl float64) {
			defer wg.Done()
			var cx, cy float64
			var sumW, wcx, wcy float64
			var traced []types.GridPoint

			for _, gp := range grid {
				r := types.Ray{
					Wavelength: wl,
					Initial:    types.RayState{Origin: gp.Origin, Direction: gp.Direction},
					Path:       path,
					Jones:      pol,
				}
				tr := engine.TraceRay(r, system.Surfaces)
				if tr.Error != "" {
					traced = append(traced, types.GridPoint{
						PupilX: gp.PupilX, PupilY: gp.PupilY,
						ImageX: nil, ImageY: nil, Intensity: 0,
					})
					continue
				}
				for _, sr := range tr.Surfaces {
					if sr.SurfaceID == refSurfaceID {
						weight := (sr.IntensityS + sr.IntensityP) / 2.0
						sumW += weight
						wcx += sr.Position.X * weight
						wcy += sr.Position.Y * weight
						ix, iy := sr.Position.X, sr.Position.Y
						traced = append(traced, types.GridPoint{
							PupilX: gp.PupilX, PupilY: gp.PupilY,
							ImageX: &ix, ImageY: &iy, Intensity: weight,
						})
						break
					}
				}
			}
			cx, cy = chiefImgHeight.X, chiefImgHeight.Y
			if sumW > 0 {
				cx = wcx / sumW
				cy = wcy / sumW
			}
			ss := computeSpotStats(traced, cx, cy)
			ch <- wlResult{i, *ss}
		}(i, wl)
	}
	wg.Wait()
	close(ch)

	stats := make([]types.WavelengthStats, len(wavelengths))
	for r := range ch {
		stats[r.index] = types.WavelengthStats{Value: wavelengths[r.index], SpotStats: r.stats}
	}
	return stats
}

func buildResult(
	engine *ray.Engine, system types.System,
	origin, rayDir types.Vec3,
	refSurfaceID int,
	pol types.JonesVector,
	wavelength float64,
	cx, cy, apertureRadius float64,
	grid []types.GridPoint,
	dumpMap bool,
) Result {
	path := buildPath(system.Surfaces)
	chiefRay := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: origin, Direction: rayDir},
		Path:       path,
		Jones:      pol,
	}

	// Trace chief ray for actual image height
	if tr := engine.TraceRay(chiefRay, system.Surfaces); tr.Error == "" {
		for _, sr := range tr.Surfaces {
			if sr.SurfaceID == refSurfaceID {
				cx = sr.Position.X
				cy = sr.Position.Y
			}
		}
	}

	res := Result{
		ImageHeight:   types.Vec3{X: cx, Y: cy},
		EntrancePupil: types.Pupil{Radius: apertureRadius},
		ChiefRay:      chiefRay,
		GridPoints:    grid,
	}
	if len(grid) > 0 {
		res.SpotStats = computeSpotStats(grid, cx, cy)
	}
	return res
}

func computeRayFan(
	engine *ray.Engine, system types.System,
	origin, rayDir types.Vec3,
	refSurfaceID int,
	pol types.JonesVector,
	wavelength float64,
	apertureRadius float64,
	angles []float64,
	numFan int,
) *types.RayFan {
	if numFan <= 0 {
		numFan = 256
	}

	path := buildPath(system.Surfaces)
	zStart := origin.Z

	// Fan rays sample the entrance pupil: scan positions are offset from the
	// pupil center, which is the point at zStart where a ray parallel to the
	// chief ray crosses the stop. Without this offset, off-axis fields would
	// start fan rays near the axis and miss the lens aperture entirely.
	rayDirN := rayDir.Normalize()
	stopZ := computeStopZ(system.Surfaces)
	pupilCenterX := -(stopZ - zStart) * rayDirN.X / rayDirN.Z
	pupilCenterY := -(stopZ - zStart) * rayDirN.Y / rayDirN.Z

	// Chief ray image point
	chiefRay := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: origin, Direction: rayDir},
		Path:       path,
		Jones:      pol,
	}
	var chiefX, chiefY float64
	if tr := engine.TraceRay(chiefRay, system.Surfaces); tr.Error == "" {
		for _, sr := range tr.Surfaces {
			if sr.SurfaceID == refSurfaceID {
				chiefX = sr.Position.X
				chiefY = sr.Position.Y
			}
		}
	}

	traceOne := func(px, py float64) (types.FanPoint, bool) {
		rDir := rayDir
		rOrg := types.Vec3{X: pupilCenterX + px, Y: pupilCenterY + py, Z: zStart}
		r := types.Ray{
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: rOrg, Direction: rDir},
			Path:       path,
			Jones:      pol,
		}
		tr := engine.TraceRay(r, system.Surfaces)
		if tr.Error != "" {
			return types.FanPoint{}, false
		}
		fp := types.FanPoint{
			PupilX: px,
			PupilY: py,
		}
		for _, sr := range tr.Surfaces {
			if sr.SurfaceID == refSurfaceID {
				fp.EX = sr.Position.X - chiefX
				fp.EY = sr.Position.Y - chiefY
			}
		}
		fp.Path = tr.Surfaces
		return fp, true
	}

	fan := &types.RayFan{}

	for _, angleDeg := range angles {
		rad := angleDeg * math.Pi / 180.0
		cosA := math.Cos(rad)
		sinA := math.Sin(rad)

		points := make([]types.FanPoint, 0, numFan)
		for k := 0; k < numFan; k++ {
			t := -apertureRadius + (float64(k)+0.5)*2.0*apertureRadius/float64(numFan)
			px := t * cosA
			py := t * sinA
			fp, ok := traceOne(px, py)
			if !ok {
				continue
			}
			points = append(points, fp)
		}

		switch {
		case math.Abs(angleDeg-90) < 1e-9:
			fan.Meridional = points
		case math.Abs(angleDeg) < 1e-9:
			fan.Sagittal = points
		default:
			fan.Rotated = append(fan.Rotated, types.RotatedFan{
				AngleDeg: angleDeg,
				Points:   points,
			})
		}
	}

	if len(fan.Meridional) == 0 && len(fan.Sagittal) == 0 && len(fan.Rotated) == 0 {
		return nil
	}
	return fan
}

func computeStopZ(surfaces []types.Surface) float64 {
	stopID := findStopID(surfaces)
	if stopID == 0 {
		return 0
	}
	z := 0.0
	for _, s := range surfaces {
		if s.ID == stopID {
			return z
		}
		z += s.Thickness
	}
	return 0
}

func findStopID(surfaces []types.Surface) int {
	stopID := 0
	minD := math.MaxFloat64
	for _, s := range surfaces {
		if !s.AutoAperture && s.Diameter > 0 && s.Diameter < minD {
			minD = s.Diameter
			stopID = s.ID
		}
	}
	if stopID != 0 {
		return stopID
	}
	minD = math.MaxFloat64
	for _, s := range surfaces {
		if s.Diameter > 0 && s.Diameter < minD {
			minD = s.Diameter
			stopID = s.ID
		}
	}
	return stopID
}

func computeTargetZ(surfaces []types.Surface, targetID int) float64 {
	z := 0.0
	for _, s := range surfaces {
		if s.ID == targetID {
			return z
		}
		z += s.Thickness
	}
	return z
}

func FindMinApertureRadius(surfaces []types.Surface) float64 {
	minR := math.MaxFloat64
	for _, s := range surfaces {
		if s.Diameter > 0 && s.Diameter/2 < minR {
			minR = s.Diameter / 2
		}
	}
	if minR == math.MaxFloat64 {
		return 0
	}
	return minR
}

func buildPath(surfaces []types.Surface) []int {
	path := []int{0}
	for _, s := range surfaces {
		if s.ID > 0 {
			path = append(path, s.ID)
		}
	}
	return path
}

// imageHeightForAngle returns the projected centroid (dx*cx + dy*cy) at the
// reference surface for a given field angle (degrees).  Uses a full grid trace
// with the given numRays.
func imageHeightForAngle(
	system types.System,
	engine *ray.Engine,
	angleDeg, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	gridType types.GridType,
) (float64, bool) {
	thetaRad := angleDeg * math.Pi / 180.0
	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)
	zStart := -100.0
	rayDir := types.Vec3{X: sinT * dx, Y: sinT * dy, Z: cosT}.Normalize()

	stopZ := computeStopZ(system.Surfaces)
	tanT := sinT / cosT
	pupilCenterX := -(stopZ - zStart) * tanT * dx
	pupilCenterY := -(stopZ - zStart) * tanT * dy

	cx, cy, grid := tracePupilGrid(system, engine, numRays, apertureRadius,
		pupilCenterX, pupilCenterY, zStart, rayDir, types.Vec3{},
		refSurfaceID, pol, wavelength, false, gridType)

	height := cx*dx + cy*dy
	for _, gp := range grid {
		if gp.ImageX != nil {
			return height, true
		}
	}
	return 0, false
}

// searchAngleForImageHeight finds the field angle (degrees) whose projected
// centroid (dx*cx + dy*cy) at the reference surface equals targetY.
func searchAngleForImageHeight(
	system types.System,
	engine *ray.Engine,
	targetY, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	gridType types.GridType,
	passThrough *types.PassThroughTarget,
) float64 {
	heightFn := func(angleDeg float64) (float64, bool) {
		if passThrough != nil && passThrough.Surface > 0 {
			y, ok := imageHeightForAnglePT(system, engine, angleDeg, dx, dy, refSurfaceID, pol, wavelength, passThrough)
			return y, ok
		}
		return imageHeightForAngle(system, engine, angleDeg, dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, gridType)
	}

	// Bracket search: start with 0–15° and expand as needed.
	loDeg, hiDeg := 0.0, 15.0
	yLo, okLo := heightFn(loDeg)
	yHi, okHi := heightFn(hiDeg)

	for iter := 0; iter < 25; iter++ {
		if okLo && okHi && yLo <= targetY && targetY <= yHi {
			break
		}
		if hiDeg > 70 {
			hiDeg = 70
			yHi, okHi = heightFn(hiDeg)
		}
		if !okLo || yLo > targetY {
			loDeg -= 3.0
			if loDeg < -80 {
				return 0
			}
			yLo, okLo = heightFn(loDeg)
		} else if !okHi {
			hiDeg = (loDeg + hiDeg) / 2
			if math.Abs(hiDeg-loDeg) < 1e-12 {
				return 0
			}
			yHi, okHi = heightFn(hiDeg)
		} else if yHi < targetY {
			hiDeg += 3.0
			if hiDeg > 80 {
				return 0
			}
			yHi, okHi = heightFn(hiDeg)
		} else {
			break
		}
	}

	if !okLo || !okHi || yLo > targetY || yHi < targetY {
		return 0
	}

	for iter := 0; iter < 40; iter++ {
		midDeg := (loDeg + hiDeg) / 2
		yMid, ok := heightFn(midDeg)
		if !ok {
			if math.Abs(midDeg-loDeg) > math.Abs(hiDeg-midDeg) {
				loDeg = midDeg
			} else {
				hiDeg = midDeg
			}
			continue
		}
		if math.Abs(yMid-targetY) < 1e-12 {
			return midDeg
		}
		if yMid < targetY {
			loDeg = midDeg
			yLo = yMid
		} else {
			hiDeg = midDeg
			yHi = yMid
		}
		if math.Abs(hiDeg-loDeg) < 1e-12 {
			break
		}
	}
	return (loDeg + hiDeg) / 2
}

// imageHeightForAnglePT returns the Y position at the reference surface for a
// ray at the given field angle, using pass_through to ensure the ray passes
// through the stop center.  This is more accurate than the paraxial pupil
// approximation used by imageHeightForAngle when the entrance pupil differs
// from the physical stop.
func imageHeightForAnglePT(
	system types.System,
	engine *ray.Engine,
	angleDeg, dx, dy float64,
	refSurfaceID int,
	pol types.JonesVector,
	wavelength float64,
	pt *types.PassThroughTarget,
) (float64, bool) {
	thetaRad := angleDeg * math.Pi / 180.0
	zStart := -100.0
	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)
	rayDir := types.Vec3{X: sinT * dx, Y: sinT * dy, Z: cosT}.Normalize()

	path := buildPath(system.Surfaces)

	originY := searchOriginForTarget(rayDir.Y, rayDir, zStart, pt.Surface, pt.Coordinate.Y,
		path, wavelength, pol, engine, system.Surfaces, false,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y })

	originX := 0.0
	if math.Abs(rayDir.X) > 1e-12 || math.Abs(pt.Coordinate.X) > 1e-12 {
		originX = searchOriginForTarget(rayDir.X, rayDir, zStart, pt.Surface, pt.Coordinate.X,
			path, wavelength, pol, engine, system.Surfaces, true,
			func(sr types.SurfaceResult) float64 { return sr.Position.X })
	}

	origin := types.Vec3{X: originX, Y: originY, Z: zStart}

	ray := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: origin, Direction: rayDir},
		Path:       path,
		Jones:      pol,
	}

	traceResult := engine.TraceRay(ray, system.Surfaces)
	if traceResult.Error != "" {
		return 0, false
	}

	for _, sr := range traceResult.Surfaces {
		if sr.SurfaceID == refSurfaceID {
			return sr.Position.Y, true
		}
	}
	return 0, false
}

func inferStopPosition(results []Result) {
	if len(results) < 2 {
		return
	}

	r0 := results[0].ChiefRay
	r1 := results[1].ChiefRay

	d0 := r0.Initial.Direction
	d1 := r1.Initial.Direction
	p0 := r0.Initial.Origin
	p1 := r1.Initial.Origin

	cross := d0.Cross(d1)
	if cross.Length() < 1e-12 {
		return
	}

	diff := p1.Subtract(p0)
	t := diff.Cross(d1).Dot(cross) / cross.Dot(cross)
	intersection := p0.Add(d0.Scale(t))

	for i := range results {
		results[i].EntrancePupil.Center = intersection
	}
}
