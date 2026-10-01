# Contrast Optimization (Geometric MTF, Wavefront Shift, Wavefront Pair)

This document describes the implementation of contrast optimization in RayWeaver. Three computational approaches are implemented:

| Phase | Method | Merit kinds | Status |
|---|---|---|---|
| 1 | **Direct Complex Sum** | `geometric_mtf_sag`, `geometric_mtf_tan` | Implemented |
| 2a | **Wavefront Shift** | `wavefront_shift_sag`, `wavefront_shift_tan` | Implemented |
| 2b | **Wavefront Pair Phase** | `wavefront_pair_phase` | Implemented |
| 3 | **Diffraction MTF (Huygens)** | `diffraction_mtf_sag`, `diffraction_mtf_tan` | Implemented |

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

Wavefront shift raises the MTF at a specific frequency by driving down the **wavefront difference between pupil-shifted ray pairs**. The incoherent OTF is the autocorrelation of the pupil function, so the path difference between two pupil samples one *displacement* apart carries the whole frequency content. For spatial frequency `ν` that displacement is

```
s = ν · λ · z        (pupil plane, z = the pupil-to-image path scale)
```

which in entrance-pupil coordinates is `ν · λ · R_ent/NA`; for an infinite conjugate the working F-number collapses that to `ν · λ · EFL`. The optimizer supplies that length as the shear scale, so `frequency` is a genuine spatial frequency in lp/mm.

The merit is the weighted **variance** of the path differences over a reference-sphere-referenced wavefront `W`:

```
MF = Σ w_i (ΔW_i - <ΔW>)² / Σ w_i,   w_i = Area_i × Intensity_i
ΔW_i = W(PupilX_i, PupilY_i) - W(PupilX_i - s, PupilY_i)   [sag]
     = W(PupilX_i, PupilY_i) - W(PupilX_i, PupilY_i - s)   [tan]
```

`W` is the OPL with a constant, a tilt and a radial quadratic removed — the same three terms `wavefront_sphere_rms` removes. That subtraction is not cosmetic: the focusing of a converging bundle is itself a quadratic in the pupil, its sheared difference is therefore *linear*, and a statistic that did not remove it would measure where the focus is rather than how well the wavefront is formed (an ideal lens would score a large residual and the solver would walk the image plane). Removing the tilt is equally required — a displaced PSF has the same MTF magnitude, so the tilt must not be charged — and the piston cancels in the difference anyway.

For a small aberration `|OTF(ν)| ≈ 1 - ½·Var(2π·ΔW/λ)`, so minimising `MF` maximises the MTF at `ν`. Because the sheared difference of a smooth wavefront is proportional to `s` and the statistic is a variance, the reading is weighted as `s²` — it is a frequency-specific proxy, not another spot size.

What survives the reference sphere is the same aberration set `wavefront_sphere_rms` keeps: **astigmatism, spherical aberration and coma**. **Defocus is the exception** — it is one of the removed quadratics, and that is not a limitation of the estimator but of what an OTF at a best-focus plane can see: the delivered-plane gate is measured at the plane the back-focus solve picks, so the residual defocus there is small by construction, and the per-field defocus that field curvature is made of is carried by the `wavefront_defocus` terms (one per field, also cheap). Use those for curvature, not these.

### 3.2 Merit Operand

| Kind | Description | Parameters |
|---|---|---|
| `wavefront_shift_sag` | Sagittal wavefront shift merit | `frequency` (lp/mm), `wavelength` |
| `wavefront_shift_tan` | Tangential wavefront shift merit | `frequency` (lp/mm), `wavelength` |

No `target` — the optimizer minimizes the variance directly. Lower merit = higher MTF.

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
       └─ ComputeWavefrontShift(points, frequency, wavelength, shearScale, direction)
            ├─ s = frequency × wavelength × shearScale   (mm, the pupil displacement)
            ├─ Remove the reference sphere: weighted least-squares fit of
            │     c0 + c1·x + c2·y + c3·(x²+y²) over the pupil, subtracted in place
            ├─ mesh.Triangulate(PupilX, PupilY) → Delaunay + vertex→triangle index
            ├─ For each valid point i:
            │     (sx, sy) = (PupilX_i - s, PupilY_i)  [sag] or (PupilX_i, PupilY_i - s) [tan]
            │     Locate the triangle containing (sx, sy) by scanning the triangles
            │     incident to the nearest sample (for a Delaunay triangulation the
            │     nearest site is always a vertex of the containing triangle, so the
            │     search is complete). Outside the triangulation → no pupil overlap at
            │     this displacement → the pair is dropped.
            │     δW = W_i - W_interpolated(sx, sy)
            │     merit += Area_i × Intensity_i × δW²
            ├─ Return the weighted variance of δW (merit)
            └─ 0 when no pair survives (past the cutoff, s ≥ the pupil diameter)
```

The interpolation matters: the displaced point is a real displacement now, not a
sub-cell offset, so a nearest-neighbour key match finds nothing on a polar grid.
That is what made the kind inert until this revision — it paired samples by exact
0.001 mm key equality, which no traced grid satisfies, and reported 0 for every
state (measured 0.000000 against a live `spot_rms` of 0.001529 on the same
design). The old shear was the dimensionless constant 2 subtracted from the
millimetre-scale `PupilX`, which on a stop-free f/3.9 system under-shot the
requested displacement by ~24x, so `frequency: 10` acted on a 0.4 lp/mm shear.

### 3.5 Data Requirements

`IPoint` must include `PupilX/PupilY` (relative pupil coordinates, in mm — the
grid is scaled by the aperture radius) plus `OPL`, `Area` and `Intensity`. These
are populated from `pupil.Sample` during grid tracing.

The four-term reference-sphere fit is degenerate for a collinear or coincident
pupil sample set; the fit is then skipped rather than trusted.

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

## 4A. Phase 3 — Diffraction MTF (Huygens Integral)

### 4A.1 Principle

Phases 1–2b are geometric: they read the MTF off ray coordinates and never
integrate a wave. Phase 3 is the in-optimization counterpart of the gate
measurement (`focus mtf --frequencies F`): it traces the field's entrance
pupil, propagates the polarized wavefront to the reference surface, integrates
it on the delivered flat image plane with the direct vector Huygens integral
and reads the MTF at the term's frequency off that PSF — the same window, the
same image-grid auto-enlargement rule and the same FFT the `psf` command
reports. A term value and a reported value are therefore the same number for
the same state, which the geometric kinds cannot claim (they overstate the
diffraction MTF by ~2.8× near the hinge).

### 4A.2 Merit Operand

| Kind | Description | Parameters |
|---|---|---|
| `diffraction_mtf_sag` | Sagittal diffraction MTF at specified frequency | `frequency` (lp/mm), `target` (0..1) |
| `diffraction_mtf_tan` | Tangential diffraction MTF at specified frequency | `frequency` (lp/mm), `target` (0..1) |

The same hinge residual as Phase 1 applies — `max(0, target − MTF)` — so a
design already at the gate is never pushed to buy MTF it does not need. An
unevaluable term (no PSF can be formed) yields 0, i.e. the full target
deficit: the solver is pushed toward a state where a PSF exists at all
instead of being fed a fabricated number.

### 4A.3 Sampling

The gate runs at 1600 rays; the merit defaults to 400 **effective** samples
per field. The merit needs the same *kind* of pupil sampling, not the same
photon budget — but the floor is not low, because the MTF error from an
under-sampled pupil is large near the hinge. Measured on a diffraction-limited
triplet at 10 c/mm against the 1600-ray gate value (0.907 / 0.907):

| effective samples | sagittal | tangential |
|---|---|---|
| 64 | 0.693 | 0.685 |
| 128 | 0.703 | 0.703 |
| 256 | 0.838 | 0.840 |
| **400 (default)** | **0.902** | **0.904** |
| 1600 (gate) | 0.907 | 0.907 |

128 samples read ~0.20 *below* the gate, so a hinge at target 0.50 would have
been satisfied at a true MTF of ~0.30. 400 is within 0.005-0.02.

`num_rays` is the count of samples that **survive** the pupil, not the nominal
grid size. Both pupil-grid builders clip the full nominal grid against a
field's prescribed vignetting ellipse rather than re-laying the grid into it,
so a nominal count would silently under-sample a vignetted field in proportion
to its area (measured: 400 nominal gives 360 effective at 80% area). The
optimizer therefore launches `ceil(num_rays / area)` nominal rays, saturating
at four times the target so a near-dead ellipse cannot ask for a million rays.

Sampling is configured YAML-only under `optimization.diffraction_mtf` (zero
values select the built-in defaults):

```yaml
optimization:
  diffraction_mtf:
    num_rays: 400        # effective (surviving) samples per field (default 400)
    grid_size: 64        # image-grid pixels before auto-enlargement (default 64)
    max_grid: 128        # cap on the auto-enlarged grid (default 128)
    polarizations: [RCP+LCP]   # incoherent average (default [RCP+LCP])
```

`max_grid` is a pixel-count cap only: the window — and therefore the frequency
spacing `df = 1/(2*half)` — is untouched, so the 10 c/mm bin is preserved. In
the spot-driven regime `half = 3*spotRMS`, so the grid always spans `6*half/n`
cells per RMS radius whatever the spot size: the PSF stays amply resolved while
the Airy-core rule the cap replaces is irrelevant for a spot tens of Airy disks
wide. The cap binds only when the natural grid exceeds it (`half > 32*Airy`
≈ 0.022 mm at 128), where the Gaussian estimate `exp(-987*spotRMS^2)` of MTF(10)
is already at or below ~0.62, and the grid's Nyquist frequency stays far above
the frequency the gate reads. Measured cost on a 6-element at 10 c/mm (128
rays, RCP+LCP): 230 ms at 512, 60 ms at 256, 18 ms at 128 — each halving is a
~4x saving. In the well-corrected regime the window is diffraction-sized, the
grid stays at 64, and the term is identical to the standalone measurement.

`polarizations: [RCP]` is 1.7x cheaper than `[RCP+LCP]`. On an all-refractive
system the two circular states give identical intensity maps, so the values
agree at every ray count (verified on a triplet and a 6-element), but that is a
property of the system, not of the code — check it before relying on it.

### 4A.4 Computation Flow

```
DLS iteration
  └─ evaluateKindTerm(diffraction_mtf_sag/tan)
       └─ diffractionMTFValues  ── per-evaluation cache keyed (field, wavelength, frequency)
            └─ computeDiffractionMTF
                 ├─ frozen pupil Z (per-iteration, shared with the wavefront terms)
                 │    └─ fallback: dynamic pupil
                 ├─ psf.FrozenPupilGrid → psf.TraceWavefront (polarized, to the reference surface)
                 ├─ psf.DefaultImageGrid → capImageGrid(max_grid)
                 ├─ direct vector Huygens integral (per polarization state)
                 └─ psf.ComputeMTF → sagittal / tangential at term.frequency
```

The launch count is the vignetting-compensated one
(`vignettedNumRays(num_rays, field.vignetting)`), so a field with a prescribed
ellipse is sampled as densely as an unclipped one.

Both axes come from one evaluation, so a sag+tan term pair on the same field
shares one pupil trace and one Huygens integration.

---

## 5. Comparison

| Feature | Phase 1 (gMTF) | Phase 2a (Wavefront Shift) | Phase 2b (Wavefront Pair) | Phase 3 (Diffraction MTF) |
|---|---|---|---|---|
| **Input** | Image-plane (X, Y) | Pupil (PupilX, PupilY, OPL) | Pupil (PupilX, PupilY, OPL) | Wavefront → Huygens PSF |
| **Frequency** | Directly specified | Directly specified | Not specified (fixed pairs) | Directly specified |
| **Direction** | S/T independent | S/T independent | S/T/D simultaneous | S/T independent |
| **Residual** | Hinge: `max(0, target - MTF)` | Squared: `Σ(δW)²` | Squared: `Σ(Δφ)²` | Hinge: `max(0, target - MTF)` |
| **Target** | Required | Not used | Not used | Required |
| **Pair finding** | N/A | Nearest-neighbor (spatial hash) | Fixed reference points | N/A |
| **Extra cost** | None | Pair search O(N) | 9-point lookup O(1) | One Huygens integral per field |
| **Use case** | Specific MTF target | Specific frequency optimization | Broad wavefront quality | Gate-exact MTF target |

---

## 6. Implementation Locations

| File | Phase | Change |
|---|---|---|
| `internal/dls/stats.go` | 2a/2b | `PupilX, PupilY` on `IPoint` |
| `internal/dls/grid.go` | 2a/2b | Populate `PupilX/PupilY` from `Sample` |
| `internal/dls/mtf.go` | 1/2a/2b | `ComputeGeometricMTF`, `ComputeWavefrontShift`, `ComputeWavefrontPairPhase` |
| `internal/dls/mtf_test.go` | 1 | Unit tests for `ComputeGeometricMTF` |
| `internal/optimize/merit.go` | 1/2a/2b/3 | Constants + evaluation cases |
| `internal/optimize/optimize.go` | 1/2a/2b/3 | `meritTerm.frequency`, `isGridKind`, `isTraceKind` |
| `internal/types/types.go` | 1/3 | `MeritTerm.Frequency`, `DiffractionMTFConfig` |
| `internal/psf/diffraction.go` | 3 | `FrozenPupilGrid`, `ComputeDiffractionMTF`, `capImageGrid` |
| `internal/psf/diffraction_cap_test.go` | 3 | Unit tests for `capImageGrid` |
| `internal/optimize/diffraction_mtf_test.go` | 3 | Routing, hinge and cache-sharing tests |

---

## 7. References

- `gMTF.md` — Direct complex sum derivation, equivalence proof, YAML examples
- `DigitalContrastOptimization.md` — Wavefront shift theory, pupil shift derivation, IODC 2017 / SPIE 2017
- `瞳上 2 点位相差評価.md` — Wavefront pair phase approach, multi-direction wavefront control
