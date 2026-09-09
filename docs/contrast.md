# Contrast Optimization (Geometric MTF & DCO)

This document describes the implementation of contrast optimization in RayWeaver using geometric MTF as merit function operands. Two computational approaches are specified: **Phase 1 — Direct Complex Sum** (implemented first) and **Phase 2 — Digital Contrast Optimization (DCO)** (future extension).

---

## 1. Background

Geometric MTF (gMTF) computes the modulation transfer function from ray image-plane density without diffraction. It is suitable for early-stage optimization far from the diffraction limit because:

- **Speed**: No FFT, no PSF binning. Evaluates MTF at specified frequencies directly from ray coordinates via complex summation.
- **Direction sensitivity**: Sagittal and Tangential MTF emerge naturally from directional projections.
- **Compatibility**: Reuses existing DLS pupil-grid trace (`IPoint{X, Y, OPL, Area, Intensity}`).

Two algorithms are specified:

| Phase | Method | Key property |
|---|---|---|
| 1 | **Direct Complex Sum** | MTF = `|\Sum w_i exp(-j2πν u·r_i)| / \Sum w_i` |
| 2 | **DCO (Digital Contrast Optimization)** | Minimizes `Σ w_i (δW_i)²` for pupil-shifted ray pairs |

Both operate on the same pupil-grid data; DCO adds pupil-shift pairing.

---

## 2. Phase 1 — Direct Complex Sum

### 2.1 Mathematical Definition

For a given field, wavelength, and image plane, let the pupil-grid rays produce image-plane coordinates `r_i = (x_i, y_i)` with weights `w_i = Area_i × Intensity_i`.

The geometric MTF in direction `u = (u_x, u_y)` at spatial frequency `ν` [lp/mm] is:

```
MTF_u(ν) = | Σ w_i exp(-j 2π ν (u_x x'_i + u_y y'_i)) | / Σ w_i
```

where `x'_i = x_i - x̄`, `y'_i = y_i - ȳ` are coordinates relative to the flux-weighted centroid.

Standard directions:
- **Sagittal**: `u = (1, 0)` → MTF depends on `x` spread
- **Tangential**: `u = (0, 1)` → MTF depends on `y` spread
- **Arbitrary angle θ**: `u = (cos θ, sin θ)`

This is mathematically equivalent to PSF → LSF → FFT, just a different discretization of the same pupil integral.

### 2.2 Merit Operand

Two new merit kinds are introduced:

| Kind | Description | Parameters |
|---|---|---|
| `geometric_mtf_sag` | Sagittal MTF at specified frequency | `frequency` (lp/mm), `target` (0..1) |
| `geometric_mtf_tan` | Tangential MTF at specified frequency | `frequency` (lp/mm), `target` (0..1) |

The **hinge residual** is used (only penalizes under-performance):

```
residual = max(0, target - MTF_u(ν))
```

The DLS solver squares and weights this: `w * residual²`. This drives up MTF only where it falls below the target, avoiding over-optimizing already-satisfactory fields.

### 2.3 YAML Configuration

```yaml
optimization:
  configs:
    - id: config1
      merit:
        type: default
        terms:
          # On-axis: 50 lp/mm, target 0.4 for both S/T
          - kind: geometric_mtf_sag
            field: 0
            wavelength: 0.00058756
            frequency: 50
            target: 0.4
            weight: 1000
          - kind: geometric_mtf_tan
            field: 0
            wavelength: 0.00058756
            frequency: 50
            target: 0.4
            weight: 1000
          # Off-axis: slightly lower targets
          - kind: geometric_mtf_sag
            field: 1
            wavelength: 0.00058756
            frequency: 50
            target: 0.3
            weight: 800
          - kind: geometric_mtf_tan
            field: 1
            wavelength: 0.00058756
            frequency: 50
            target: 0.3
            weight: 800
```

Multi-wavelength: specify multiple terms with different `wavelength` values. Each is evaluated independently.

### 2.4 Computation Flow

```
DLS iteration
  └─ traceGridRays() → IPoint[] (X, Y, OPL, Area, Intensity, OK)
       └─ ComputeGeometricMTF(points, frequency)
            ├─ centroid = weighted_mean(X, Y) with weights = Area × Intensity
            ├─ for each valid point:
            │     w = Area × Intensity
            │     dx = X - centroid.X
            │     dy = Y - centroid.Y
            │     φ = 2π × frequency × (u_x·dx + u_y·dy)
            │     cs += w × cos(φ)
            │     sn += w × sin(φ)
            │     sumW += w
            └─ MTF = hypot(cs, sn) / sumW
```

### 2.5 Implementation Locations

| File | Change |
|---|---|
| `internal/dls/mtf.go` | **New** — `ComputeGeometricMTF(points []IPoint, freqLPmm float64) (sag, tan float64)` |
| `internal/dls/mtf_test.go` | **New** — Unit tests |
| `internal/optimize/merit.go` | Add constants + evaluation cases |
| `internal/optimize/optimize.go` | Add `frequency` to `meritTerm`; wire in `buildMeritTermFromTypes` |
| `internal/types/types.go` | Add `Frequency float64` to `MeritTerm` |

---

## 3. Phase 2 — Digital Contrast Optimization (DCO)

### 3.1 Principle

DCO maximizes MTF at a specific frequency by minimizing the **wavefront difference between pupil-shifted ray pairs**. For spatial frequency `ν`, the corresponding pupil shift is:

```
Δ = ν · λ · D
```

where `D` = entrance pupil diameter, `λ` = wavelength.

The DCO merit function is the weighted sum of squared wavefront differences:

```
MF = Σ w_i (W_i - W'_i)²
```

where `W_i` = OPL at pupil coordinate `p_i`, `W'_i` = OPL at `p_i - Δu/R` (normalized pupil shift), `w_i` = pupil quadrature weight.

This is equivalent to maximizing the modulus of the pupil autocorrelation at shift `Δ`, which equals the MTF at frequency `ν`.

### 3.2 Merit Operands

| Kind | Description | Parameters |
|---|---|---|
| `dco_sag` | DCO merit for sagittal frequency | `frequency` (lp/mm) |
| `dco_tan` | DCO merit for tangential frequency | `frequency` (lp/mm) |

No `target` — the optimizer minimizes the sum of squared wavefront differences directly. Lower merit = higher MTF.

### 3.3 YAML Configuration

```yaml
optimization:
  configs:
    - id: config1
      merit:
        type: default
        terms:
          - kind: dco_sag
            field: 0
            wavelength: 0.00058756
            frequency: 50
            weight: 100
          - kind: dco_tan
            field: 0
            wavelength: 0.00058756
            frequency: 50
            weight: 100
```

### 3.4 Computation Flow

```
DLS iteration
  └─ traceGridRays() → Sample[] (with PupilX, PupilY, OPL, Area, Intensity, OK)
       └─ ComputeDCO(samples, frequency, direction)
            ├─ Δ = frequency × wavelength × entrance_pupil_diameter
            ├─ For each valid sample i:
            │     px' = PupilX_i - Δ/R  (sagittal) or PupilX_i
            │     py' = PupilY_i          or PupilY_i - Δ/R
            │     Find j where PupilX_j ≈ px' and PupilY_j ≈ py'
            │     If j found and OK:
            │         δW = OPL_i - OPL_j
            │         sum += Area_i × Intensity_i × δW²
            └─ Return sum
```

**Pair finding**: Since the pupil grid is deterministic, shifted coordinates map to nearby grid points. A KD-tree or simple spatial hash (grid cell → index) provides O(1) lookup.

### 3.5 Required Data Extension

`IPoint` needs pupil coordinates for pair lookup. Add:

```go
type IPoint struct {
    X, Y       float64
    OPL        float64
    OK         bool
    Area       float64
    Intensity  float64
    // Phase 2 addition:
    PupilX     float64 // relative pupil coords (unit aperture × radius)
    PupilY     float64
}
```

### 3.6 Implementation Locations

| File | Change |
|---|---|
| `internal/dls/mtf.go` | Add `IPoint.PupilX/PupilY`; add `ComputeDCO()` |
| `internal/dls/grid.go` | Populate `PupilX/PupilY` from `Sample` |
| `internal/optimize/merit.go` | Add DCO constants + evaluation cases |
| `internal/optimize/optimize.go` | Wire DCO kinds |

---

## 4. Common Design Elements

### 4.1 Frequency Specification

Both phases use `frequency` in **lp/mm** (line pairs per millimeter). The Nyquist frequency of the pupil sampling is `1 / (2 × Δx)` where `Δx` is the pupil sampling interval. Users should choose frequencies well below Nyquist for reliable geometric MTF.

### 4.2 Weighting

Ray weights `w_i = Area_i × Intensity_i` combine:
- **Area**: Pupil cell area (from polar/hex grid, proportional to `r/R`)
- **Intensity**: Mean transmitted intensity `(I_s + I_p)/2` including Fresnel/TMM losses

This matches the existing `spot_rms_weighted` behavior.

### 4.3 Image Plane Mode

Phase 1 uses the DLS grid's current image plane (set by `chief`/`trace` or `optimize` variables). For best-focus evaluation, use `wavefront --best-focus` before optimization, or add a future `image_plane` option (`fixed`, `best_rms`, `best_mtf`, `common_focus`).

### 4.4 Multi-Wavelength

Each wavelength term is independent. The optimizer evaluates all specified wavelengths and combines their residuals with `wavWeight`.

---

## 5. Testing Strategy

### 5.1 Phase 1 Unit Tests (`internal/dls/mtf_test.go`)

- Known spot patterns: Gaussian, uniform disc, tilted line
- Verify MTF = 1 at ν=0 for any pattern
- Verify MTF decreases with frequency for broadened spots
- Verify S/T difference for astigmatic spot (elongated in Y → lower tan MTF)
- Weighted vs unweighted consistency

### 5.2 Integration Tests

- US2645157 triplet: add `geometric_mtf_sag/tan` terms, verify merit decreases and MTF improves
- Multi-config: verify per-config frequency/target settings
- Multi-wavelength: verify independent evaluation

### 5.3 Phase 2 Tests

- DCO vs direct sum consistency: at same frequency, DCO merit should correlate with `1 - MTF²`
- Pair-finding accuracy with various grid densities

---

## 6. Future Extensions

| Feature | Description |
|---|---|
| `geometric_mtf_arb` | Arbitrary angle MTF with `angle_deg` parameter |
| `image_plane` option | `best_rms`, `best_mtf`, `common_focus` per term |
| Smoothing kernel | Gaussian `exp(-2π²σ²ν²)` factor for numerical stability |
| Polychromatic MTF | Complex OTF averaging across wavelengths (per `gMTF.md`) |
| Diffraction MTF | Upgrade path: use `OPL` from `IPoint` for complex pupil function |

---

## 7. References

- `gMTF.md` — Full derivation, equivalence proof, YAML examples
- `DigitalContrastOptimization.md` — DCO theory, pupil shift derivation, IODC 2017 / SPIE 2017 references
- `internal/dls/stats.go` — `IPoint` definition, `Centroid`, `ComputeSpotRMS`
- `internal/optimize/optimize.go` — `meritTerm`, `evaluateKindTerm`, `ComputeResiduals`