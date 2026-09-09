# Contrast Optimization (Geometric MTF, Wavefront Shift, Wavefront Pair)

This document describes the implementation of contrast optimization in RayWeaver. Three computational approaches are implemented:

| Phase | Method | Merit kinds | Status |
|---|---|---|---|
| 1 | **Direct Complex Sum** | `geometric_mtf_sag`, `geometric_mtf_tan` | Implemented |
| 2a | **Wavefront Shift** | `wavefront_shift_sag`, `wavefront_shift_tan` | Implemented |
| 2b | **Wavefront Pair Phase** | `wavefront_pair_phase` | Implemented |

---

## 1. Background

Geometric MTF computes the modulation transfer function from ray image-plane density without diffraction. It is suitable for early-stage optimization far from the diffraction limit because:

- **Speed**: No FFT, no PSF binning. Evaluates MTF at specified frequencies directly from ray coordinates.
- **Direction sensitivity**: Sagittal and Tangential MTF emerge naturally from directional projections.
- **Compatibility**: Reuses existing DLS pupil-grid trace (`IPoint{X, Y, OPL, PupilX, PupilY, Area, Intensity}`).

---

## 2. Phase 1 — Direct Complex Sum

### 2.1 Mathematical Definition

For a given field, wavelength, and image plane, let the pupil-grid rays produce image-plane coordinates `r_i = (x_i, y_i)` with weights `w_i = Area_i × Intensity_i`.

The geometric MTF in direction `u = (u_x, u_y)` at spatial frequency `ν` [lp/mm] is:

```
MTF_u(ν) = | Σ w_i exp(-j 2π ν (u_x x'_i + u_y y'_i)) | / Σ w_i
```

where `x'_i = x_i - x̄`, `y'_i = y_i - ȳ` are coordinates relative to the flux-weighted centroid.

### 2.2 Merit Operand

| Kind | Description | Parameters |
|---|---|---|
| `geometric_mtf_sag` | Sagittal MTF at specified frequency | `frequency` (lp/mm), `target` (0..1) |
| `geometric_mtf_tan` | Tangential MTF at specified frequency | `frequency` (lp/mm), `target` (0..1) |

The **hinge residual** is used (only penalizes under-performance):

```
residual = max(0, target - MTF_u(ν))
```

### 2.3 YAML Configuration

```yaml
merit:
  type: default
  terms:
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
```

---

## 3. Phase 2a — Wavefront Shift

### 3.1 Principle

Wavefront shift maximizes MTF at a specific frequency by minimizing the **wavefront difference between pupil-shifted ray pairs**. For spatial frequency `ν`, the corresponding pupil shift is:

```
Δu = ν · λ · 2    (normalized pupil coordinates)
```

The merit function is the weighted sum of squared wavefront differences:

```
MF = Σ w_i (OPL_i - OPL_j)²
```

where `j` is the nearest grid point to the shifted position `(PupilX_i - Δu, PupilY_i)` (sagittal) or `(PupilX_i, PupilY_i - Δu)` (tangential).

This is equivalent to minimizing the phase variance of the pupil autocorrelation at shift `Δu`, which maximizes the MTF at frequency `ν`.

### 3.2 Merit Operand

| Kind | Description | Parameters |
|---|---|---|
| `wavefront_shift_sag` | Sagittal wavefront shift merit | `frequency` (lp/mm), `wavelength` |
| `wavefront_shift_tan` | Tangential wavefront shift merit | `frequency` (lp/mm), `wavelength` |

No `target` — the optimizer minimizes the sum of squared wavefront differences directly. Lower merit = higher MTF.

### 3.3 YAML Configuration

```yaml
merit:
  type: default
  terms:
    - kind: wavefront_shift_sag
      field: 0
      wavelength: 0.00058756
      frequency: 50
      weight: 100
    - kind: wavefront_shift_tan
      field: 0
      wavelength: 0.00058756
      frequency: 50
      weight: 100
```

### 3.4 Computation Flow

```
DLS iteration
  └─ traceGridRays() → IPoint[] (with PupilX, PupilY, OPL, Area, Intensity)
       └─ ComputeWavefrontShift(points, frequency, wavelength, apertureRadius, direction)
            ├─ Δu = frequency × wavelength × 2  (normalized shift)
            ├─ Build spatial hash: discretized (PupilX, PupilY) → index
            ├─ For each valid point i:
            │     (sx, sy) = (PupilX_i - Δu, PupilY_i)  [sag] or (PupilX_i, PupilY_i - Δu) [tan]
            │     j = nearest grid point to (sx, sy)
            │     If j found and OK:
            │         δW = OPL_i - OPL_j
            │         merit += Area_i × Intensity_i × δW²
            └─ Return merit
```

### 3.5 Data Requirements

`IPoint` must include `PupilX/PupilY` (relative pupil coordinates). These are populated from `pupil.Sample` during grid tracing.

---

## 4. Phase 2b — Wavefront Pair Phase

### 4.1 Principle

This approach evaluates the wavefront structure at **9 fixed pupil reference points** and minimizes the weighted sum of squared phase differences between symmetric pairs. Unlike wavefront shift, the pairs are fixed (not frequency-dependent), providing a direction-inclusive wavefront quality metric.

### 4.2 Reference Points

9 points on the unit pupil:

| Index | Position | Role |
|---|---|---|
| 0 | (0, 0) | Center (reference) |
| 1 | (-r, 0) | Horizontal axis left |
| 2 | (r, 0) | Horizontal axis right |
| 3 | (0, -r) | Vertical axis bottom |
| 4 | (0, r) | Vertical axis top |
| 5 | (-r/√2, -r/√2) | 45° diagonal |
| 6 | (-r/√2, r/√2) | 135° diagonal |
| 7 | (r/√2, -r/√2) | 315° diagonal |
| 8 | (r/√2, r/√2) | 45° diagonal |

### 4.3 Pair Definitions

| Direction | Pairs | Weight |
|---|---|---|
| S (sagittal) | {1, 2} | 1.0 |
| T (tangential) | {3, 4} | 1.0 |
| D (diagonal) | {5, 8}, {6, 7} | 0.5 each |

### 4.4 Merit Function

```
MF = Σ_pairs w_pair × (Δφ)²
   = Σ_pairs w_pair × ((OPL_i - OPL_j) / λ)²
```

Lower merit = better wavefront quality. This naturally penalizes astigmatism (S/T imbalance) and coma (asymmetric wavefront).

### 4.5 Merit Operand

| Kind | Description | Parameters |
|---|---|---|
| `wavefront_pair_phase` | Wavefront-pair phase difference merit | `wavelength` |

No `target` or `frequency` — the merit is a fixed-structure wavefront metric.

### 4.6 YAML Configuration

```yaml
merit:
  type: default
  terms:
    - kind: wavefront_pair_phase
      field: 0
      wavelength: 0.00058756
      weight: 500
    - kind: wavefront_pair_phase
      field: 1
      wavelength: 0.00058756
      weight: 500
```

---

## 5. Comparison

| Feature | Phase 1 (gMTF) | Phase 2a (Wavefront Shift) | Phase 2b (Wavefront Pair) |
|---|---|---|---|
| **Input** | Image-plane (X, Y) | Pupil (PupilX, PupilY, OPL) | Pupil (PupilX, PupilY, OPL) |
| **Frequency** | Directly specified | Directly specified | Not specified (fixed pairs) |
| **Direction** | S/T independent | S/T independent | S/T/D simultaneous |
| **Residual** | Hinge: `max(0, target - MTF)` | Squared: `Σ(δW)²` | Squared: `Σ(Δφ)²` |
| **Target** | Required | Not used | Not used |
| **Pair finding** | N/A | Nearest-neighbor (spatial hash) | Fixed reference points |
| **Extra cost** | None | Pair search O(N) | 9-point lookup O(1) |
| **Use case** | Specific MTF target | Specific frequency optimization | Broad wavefront quality |

---

## 6. Implementation Locations

| File | Phase | Change |
|---|---|---|
| `internal/dls/stats.go` | 2a/2b | `PupilX, PupilY` on `IPoint` |
| `internal/dls/grid.go` | 2a/2b | Populate `PupilX/PupilY` from `Sample` |
| `internal/dls/mtf.go` | 1/2a/2b | `ComputeGeometricMTF`, `ComputeWavefrontShift`, `ComputeWavefrontPairPhase` |
| `internal/dls/mtf_test.go` | 1 | Unit tests for `ComputeGeometricMTF` |
| `internal/optimize/merit.go` | 1/2a/2b | Constants + evaluation cases |
| `internal/optimize/optimize.go` | 1/2a/2b | `meritTerm.frequency`, `isGridKind` |
| `internal/types/types.go` | 1 | `MeritTerm.Frequency` field |

---

## 7. References

- `gMTF.md` — Direct complex sum derivation, equivalence proof, YAML examples
- `DigitalContrastOptimization.md` — Wavefront shift theory, pupil shift derivation, IODC 2017 / SPIE 2017
- `瞳上 2 点位相差評価.md` — Wavefront pair phase approach, multi-direction wavefront control
