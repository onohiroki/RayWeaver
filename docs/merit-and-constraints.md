# Merit functions and constraints

This is the consolidated reference for everything the optimizer minimizes
(`optimize`, `escape`, `pso`): the **merit terms** and the **constraints** you
can configure, how to set each one, what it does, and how it is computed.

It is the "one place" view that ties together the command manual
([optimize.md](optimize.md)), the merit-term methods
([methods/merit-functions.md](methods/merit-functions.md)), the DLS solver and
augmented-Lagrangian constraint treatment
([methods/dls-optimization.md](methods/dls-optimization.md)), the Okudaira
Region Active Method ([methods/region-active.md](methods/region-active.md)), and
the contrast/MTF merit kinds ([contrast.md](contrast.md)). Those documents
remain the deep dives; this one is the full catalog with the setting syntax.

## Contents

1. [Overview](#1-overview)
2. [How to configure](#2-how-to-configure)
3. [Merit term catalog](#3-merit-term-catalog)
4. [Assembly, weighting and normalization](#4-assembly-weighting-and-normalization)
5. [Conditional merit schedule](#5-conditional-merit-schedule)
6. [Degenerate penalties](#6-degenerate-penalties)
7. [Constraint catalog](#7-constraint-catalog)
8. [Constraint enforcement](#8-constraint-enforcement)
9. [Quick reference](#9-quick-reference)
10. [References](#10-references)

---

## 1. Overview

The optimizer minimizes a scalar **merit function**

```
M(x) = Σ_configs  w_cfg · Σ_terms  w_term · ((value(x) − target) / scale)²
```

assembled from weighted merit **terms**, plus (when configured) glass-hull and
glass-attraction penalties. Each term contributes one least-squares residual

```
r = √(w_cfg · w_term · w_field · w_wavelength) · (value − target) / scale
```

so that `Σ r² == M` holds **exactly**. The DLS solver relies on this identity:
the constraint penalties are appended to the same residual vector, and a
degenerate term is a bounded penalty, never a fabricated `1e6` sentinel (see
[§6](#6-degenerate-penalties)).

**Constraints** are *not* merit terms. They are enforced by the solver through
an augmented-Lagrangian penalty (see [§8](#8-constraint-enforcement)) and are
used for hard physical requirements — a target EFL, a minimum edge thickness, a
maximum total track — that must hold rather than merely be preferred.

Merit terms live in `optimization.merit` (single-config) or `configs[].merit`
(multi-config); constraints live in `optimization.constraints` or
`configs[].constraints`. Both sections accept the same operand shape in either
place.

---

## 2. How to configure

### 2.1 Merit function placement

```yaml
# Single-config: the merit is the objective for the one system.
optimization:
  method: dls
  num_rays: 64
  merit:
    type: spot_rms          # label only; the terms do the work
    terms:
      - {kind: spot_rms, field: 0, wavelength: 0.0005876, weight: 1.0}

configs:
  - id: config1
    surfaces: [...]
    # Multi-config: each config carries its own merit (weight- and sum-combined)
    merit:
      type: spot_rms
      terms:
        - {kind: spot_rms, field: 0, wavelength: 0.0005876, weight: 1.0}
        - {kind: distortion_pct, field: 2, weight: 0.5}
```

- In multi-config mode the per-config merits are summed weighted by the config
  `weight`. Multi-config is auto-detected when `shared_variables`,
  `local_variables`, `variable_links`, or `configs[].merit` exist.
- A config may instead declare `merit_modes` (named term lists blended by a
  schedule — see [§5](#5-conditional-merit-schedule)); configs without
  `merit_modes` keep their fixed `merit` at full weight.
- A term without a `kind` is treated as `spot_rms`.

### 2.2 Merit term fields

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | Term kind (see [§3](#3-merit-term-catalog)); empty = `spot_rms` |
| `field` | int | The field's **ID** (matches `fields[].id`), not its list index; default 0 |
| `wavelength` | float | Wavelength in **mm** (e.g. `0.0005876` = 587.6 nm); 0 = reference |
| `comparison_wavelength` | float | Second wavelength (mm) for the colour kinds |
| `target` | float | The value the term drives toward; residual is `(value − target)²`. For `field_alive` it is the **aliveness threshold**, and for the MTF kinds the **target MTF** |
| `frequency` | float | Spatial frequency in **lp/mm** for the MTF / wavefront-shift kinds |
| `fraction` | float | Encircled-energy fraction for `spot_ee_radius` (default 0.8 = EE80) |
| `surface_set` | []int | Surface IDs the term operates on (`glass_role`, `pupil_position`, `vignetting`, `clear_aperture`, `edge_thickness`); the first entry is used |
| `weight` | float | Term weight; the term contributes `weight · (…/scale)²` |
| `scale` | float | Explicit normalization denominator (overrides `merit_normalization`); see [§4.3](#43-weight-normalization) |

### 2.3 Constraint operand fields

```yaml
optimization:
  constraints:
    - id: efl_lock
      kind: equality           # equality | inequality_upper | inequality_lower | band | fuzzy
      measure: efl
      target: 50.0
      weight: 1000.0
      active: true
    - id: min_edge
      kind: inequality_lower
      measure: edge_thickness
      surface: 3
      back_surface: 4          # optional; default = next surface in system order
      lower: 1.0
      weight: 100.0
      active: true
```

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Identifier reported in the output |
| `kind` | string | Constraint kind (see [§7.1](#71-constraint-kinds)) |
| `measure` | string | Quantity measured (see [§7.2](#72-constraint-measures)) |
| `field` | int | Field ID the measurement uses (for field-dependent measures) |
| `wavelength` | float | Wavelength (mm) for the measurement |
| `surface` | int | Target surface ID for surface-local measures |
| `back_surface` | int | Rear surface for `edge_thickness`; 0 = next surface in order |
| `target` | float | Target value (equality / fuzzy) |
| `lower` / `upper` | float | Bounds (inequality / band) |
| `band_width` | float | Inner dead-band half-width for `fuzzy` |
| `softness` | float | Linear scale beyond the band for `fuzzy` |
| `weight` | float | Constraint weight; the residual is `√weight · error` (≤0 → 1) |
| `active` | bool | Whether the constraint participates |

### 2.4 Global merit knobs

| Setting | Default | Effect |
|---|---|---|
| `optimization.num_rays` | 64 | Entrance-pupil grid rays per grid-based merit evaluation |
| `optimization.aperture_margin` | 2.0 | Grid-radius multiplier over the aperture (clamped ≥ 1) |
| `optimization.merit_normalization` | off | Per-kind residual scaling (see [§4.3](#43-weight-normalization)) |
| `optimization.degenerate` | see [§6](#6-degenerate-penalties) | Bounded penalties for unevaluable terms |
| `optimization.wavefront_reference` | `best_focus` | `best_focus` or `image_plane`: the OPD reference of the wavefront terms (see [§3.4](#34-wavefront-kinds)) |
| `optimization.diffraction_mtf` | see [§3.8](#38-contrast--mtf-kinds) | Sampling of the `diffraction_mtf_*` kinds |
| `optimization.merit_schedule` | – | Mode blending (see [§5](#5-conditional-merit-schedule)) |

The grid-based and wavefront terms are centred on the config's **per-iteration
frozen pupil** — settled once per DLS iteration and held for the base point and
every Jacobian perturbation — so the finite-difference derivative matches the
merit actually minimized.

---

## 3. Merit term catalog

Every entry gives the **effect**, the **key parameters**, the **residual** in
the formula `weight · (value − target)²` (unless noted), and the
**computation**.

### 3.1 Spot-size kinds

The spot family shares one pupil grid (`optimization.num_rays`,
`optimization.aperture_margin`). Plain `spot_rms` uses equal weights; the
off-axis kinds weight each grid ray by its **pupil-cell area × mean transmitted
intensity** (Fresnel/TMM losses), falling back to area, then to equal weight.

| Kind | Value |
|---|---|
| `spot_rms` | RMS spot radius about the centroid on the reference surface |
| `spot_rms_t` | Tangential RMS — deviation projected on the field's image-plane azimuth (`fields[].direction`, default +Y) |
| `spot_rms_s` | Sagittal RMS — the perpendicular component |
| `spot_rms_worst` | `max(RMS_T, RMS_S)` |
| `spot_rms_weighted` | Flux-weighted RMS about the flux-weighted centroid |
| `spot_ee_radius` | Radius about the flux-weighted centroid enclosing the flux `fraction` (default 0.8) |

```yaml
- {kind: spot_rms_weighted, field: 1, wavelength: 0.0005876, weight: 1.0}
- {kind: spot_ee_radius,     field: 2, wavelength: 0.0005876, fraction: 0.8, weight: 1.0}
```

**Why the off-axis kinds exist.** Plain `spot_rms` is rotationally symmetric
and uniformly weighted: it cannot separate coma (a tangential flare) from
astigmatism, is dominated by a sparse comatic tail, and ignores vignetting and
reflection losses. `spot_rms_t/_s` split the axes, `_worst` attacks the
dominant axis, `_weighted` respects the energy distribution, and `spot_ee_radius`
is insensitive to a sparse tail (so it correlates better with MTF off-axis).

**Computation.** A field/wavelength pupil grid is traced and the intensity- and
area-weighted spot statistics are computed about the flux-weighted centroid (the
same statistics `chief` and `vignette` report). The residual is
`(value − target)²`; a `target` of 0 (the default) minimizes the spot, a non-zero
target drives the spot to that size. Degenerate grid → bounded spot penalty.

### 3.2 Pupil-coverage kinds

Both reuse the same grid trace (no extra tracing), with `ratio` = the fraction
of grid rays that survived (OK):

| Kind | Residual |
|---|---|
| `field_alive` | `max(0, threshold − ratio)`, threshold from `target` (default 0.3); **bounded** by the threshold |
| `pupil_fill` | `(1 − ratio) / ratio`, capped at 999 for `ratio < 1e-3`; **unbounded** |

```yaml
- {kind: field_alive, field: 2, target: 0.3, weight: 5000}
- {kind: pupil_fill,  field: 2, weight: 50000}
```

`field_alive` is a guardrail; its `target` is the **threshold only** and the
residual target is always 0, so a high threshold penalises any fill below it and
never rewards a dead field. `pupil_fill` exists because the spot/OPD kinds are
computed on the **surviving** rays: clipping the pupil shrinks their residual,
so a design could otherwise trade a dead periphery for a smaller aberration.
`pupil_fill` grows without bound as the surviving fraction shrinks
(`0.5 → 1`, `0.1 → 9`, `0.03 → ~32`), so a suitable weight keeps partial
clipping dominant. Use both together (a bounded-plus-unbounded pair).

### 3.3 OPD kind

| Kind | Residual |
|---|---|
| `opd_rms` | `(OPD_RMS − target)²` |

```yaml
- {kind: opd_rms, field: 0, wavelength: 0.0005876, weight: 100}
```

**Computation.** The pupil grid is traced and each ray's optical path length
(OPL) recorded. The value is the RMS of `OPL − ⟨OPL⟩` over the accepted rays:

```
OPD_RMS = √( (1/N) Σᵢ (OPLᵢ − OPL̄)² )
```

A reference-sphere-free aberration measure. Degenerate grid → bounded OPD
penalty (`optimization.degenerate.opd_value`).

### 3.4 Wavefront kinds

The wavefront terms fit the field's OPD sampled on the reference surface
(default: the last optical surface; override with `chief.reference_surface`).
The OPD is referenced to the **per-field best-focus point** — the sphere center
from the coherent-PSF-peak maximization — so the coefficients match
`wavefront_result.fields[]`. Setting `optimization.wavefront_reference:
image_plane` instead references the delivered image plane, so the defocus term
carries the image-plane focus error (field curvature + system defocus) reported
in mm.

**Paraboloid kinds.** A least-squares quadratic is fitted:

```
P(x,y) = a·x² + b·y² + c·xy + d·x + e·y + f
```

| Kind | Value |
|---|---|
| `wavefront_defocus` | `(a+b)/2` (in mm when referencing the image plane) |
| `wavefront_astigmatism` | `√(((a−b)/2)² + (c/2)²)` |
| `wavefront_tilt` | `√(d² + e²)` |
| `wavefront_rms_residual` | area-weighted RMS of `OPD − P` (high-order residual) |
| `wavefront_x2` / `y2` / `xy` / `x` / `y` / `constant` | the raw coefficients `a…f` |

```yaml
- {kind: wavefront_astigmatism, field: 1, wavelength: 0.0005876, target: 0, weight: 14000}
- {kind: wavefront_rms_residual, field: 0, wavelength: 0.0005876, weight: 8000}
```

`target: 0` drives the corresponding low-order aberration to zero; a non-zero
target drives it to that value.

**Reference-sphere kinds.**

| Kind | Value |
|---|---|
| `wavefront_sphere_rms` | RMS of the residual after removing piston + tilt + defocus from the best-fit reference sphere — **astigmatism retained** |
| `wavefront_sphere_pv` | The same residual's peak-to-valley |

These fit `S(x,y) = a + b·x + c·y + d·(x²+y²)`. This is the standard
wavefront-error definition and the exact quantity `psf --best-focus` reports as
`rms_opd`/`pv_opd` (and `wavefront_result.fields[].statistics.rms`/`pv`), from
which the Strehl is computed. A `wavefront_sphere_rms` term with `target: 0`
therefore drives the reported Strehl directly and balances astigmatism against
high-order residual in a single term.

**Computation and failure handling.** The grid follows `optimization.num_rays`
and `optimization.aperture_margin` at the frozen pupil. A reference surface set
to the image plane is rejected and falls back to the last optical surface. On a
failed fit the term retries once with the dynamic pupil; if it still fails it
returns the bounded wavefront penalty (`optimization.degenerate.wavefront_value`).

**Weight design caution.** The contribution is `weight × value²`, so calibrate
the weight against the *measured* residual (use `optimize --verbose`'s
`breakdown` event), not an optimistic ideal value: a `wavefront_rms_residual`
weighted for an estimated `value` can dominate the merit ~10× once the solver
moves. Prefer spot terms to control a strongly-aberrated corner residual.

### 3.5 Colour and distortion kinds

| Kind | Value |
|---|---|
| `distortion_pct` | `100 · (y_chief − y_paraxial) / y_paraxial` |
| `lateral_color` | `y_chief(λ₂) − y_chief(λ₁)` (chief-ray image heights) |
| `longitudinal_color` | `EFL(λ₂) − EFL(λ₁)` |

```yaml
- {kind: distortion_pct, field: 2, wavelength: 0.0005876, weight: 0.5}
- {kind: lateral_color, field: 1, wavelength: 0.0004358,
   comparison_wavelength: 0.0006563, weight: 0.5}
- {kind: longitudinal_color, wavelength: 0.0004358,
   comparison_wavelength: 0.0006563, weight: 1.0}
```

`wavelength` is λ₁ and `comparison_wavelength` is λ₂. For distortion,
`y_paraxial = EFL · tan θ`. These are cheap chief-ray / paraxial evaluations
rather than grid traces.

### 3.6 Seidel kinds

| Kind | Value |
|---|---|
| `seidel_spherical` | Third-order spherical aberration coefficient |
| `seidel_coma` | Third-order coma |
| `seidel_astigmatism` | Third-order astigmatism |
| `seidel_distortion` | Third-order distortion |

```yaml
- {kind: seidel_astigmatism, field: 1, wavelength: 0.0005876, weight: 100}
```

Computed by the paraxial third-order (Seidel) machinery
(`internal/paraxial/seidel.go`) for a field and wavelength.

### 3.7 Glass-role kind

`glass_role` steers a lens element's glass toward the vd **and** nd its
chromatic role requires, judged not by the bare sign of its power but by its
marginal-ray-height-weighted power `w = φ·y²` against its opposite-sign
neighbours.

```yaml
- {kind: glass_role, field: 0, wavelength: 0.0005876, surface_set: [3], weight: 0.0005}
```

The element is identified by `surface_set[0]` (one of its bounding glass
surfaces).

**Classification** (per element, in system order):

```
w_e     = φ_e · y_e²                       (axial-colour weight)
W_opp   = Σ |w| of adjacent opposite-sign elements   (0 if none)
role    = "neutral"      if |w_e| < 0.05·(|w_e|+W_opp) or W_opp == 0   → vd* = 45
          "dominant"     if |w_e| > W_opp                              → vd* = 60
          "compensating" otherwise                                     → vd* = clamp(60·|w_e|/W_opp, 20, 60)
```

The dominant member is the couple's **crown**, the compensating member its
**flint** (scaled by the actual power balance). The rule is sign-free, so it can
also express a positive flint or a negative crown. `nd*` follows the normal
glass line plus a +0.04 boost for positive-power elements:

```
nd* = clamp(1.635 − 0.0025·(vd* − 50) + boost, 1.4, 2.0)   boost = 0.04 if w_e > 0
```

**Combined residual** (a single signed magnitude):

```
residual = sign(dv + dn) · √(dv² + dn²)
dv = vd_actual − vd*
dn = K·(nd_actual − nd*),   K = 60
```

so `residual² = (vd_actual − vd*)² + K²·(nd_actual − nd*)²` and the
least-squares identity holds. The sign keeps the finite-difference Jacobian
pointing consistently. The role classification depends on the current powers and
marginal heights, so it is recomputed and **frozen once per DLS iteration** (same
convention as the frozen pupil). The residual is in Abbe-number units
(`residual²` ~ hundreds for a swapped element), so a weight around
`1e-4…1e-3` balances it against the spot terms; combined with a colour-only
`merit_schedule` mode it can fix a swapped flint/crown. See
[methods/merit-functions.md](methods/merit-functions.md) §2 for the worked
Cooke-triplet example.

### 3.8 System and pupil-geometry kinds

| Kind | Value | Key params |
|---|---|---|
| `focal_length` | Effective focal length (mm, signed) | `target` = desired EFL |
| `pupil_position` | Entrance-pupil centre Z (mm) | `field`; `target` = desired Z |
| `pupil_diameter` | Entrance-pupil diameter (mm) | `field`; `target` = desired EPD |
| `vignetting` | Vignetted fraction of the pupil grid (0 = none) | `field`, `surface_set[0]` |
| `clear_aperture` | Beam-envelope diameter at the surface (mm) | `field`, `surface_set[0]` |
| `edge_thickness` | Glass-path span at the surface (mm) | `surface_set[0]`; nonzero only when `min_glass_path`/`max_glass_path` are set |

```yaml
- {kind: focal_length, weight: 5000, target: 50.0}
- {kind: pupil_diameter, field: 0, weight: 1000, target: 25.4}
- {kind: clear_aperture, field: 2, surface_set: [7], target: 40.0, weight: 10}
```

`focal_length` is the system-level EFL term (it replaces the older
constraint-based EFL preference); the sign is preserved, so a flipped
(negative-EFL) system receives a large penalty. `pupil_position`,
`pupil_diameter` and `clear_aperture` run a chief pass mirroring the document's
own stop and chief-ray definition, so they measure what `chief` reports.

### 3.9 Contrast / MTF kinds

Four families, all evaluated from pupil-grid data or a Huygens integral. Only
`geometric_mtf_*` and `diffraction_mtf_*` are MTF-valued and use a **hinge
residual** — they only penalise under-performance:

```
residual = max(0, target − MTF)          (implemented by clamping value at target)
```

with `target` being the minimum acceptable MTF (0..1) and `frequency` the
spatial frequency in lp/mm.

| Kind | Value | Params |
|---|---|---|
| `geometric_mtf_sag` | Sagittal MTF at `frequency` from image-plane ray density | `frequency`, `target` |
| `geometric_mtf_tan` | Tangential MTF at `frequency` | `frequency`, `target` |
| `wavefront_shift_sag` | Weighted variance of the reference-sphere-referenced OPL difference across the sagittal pupil shear | `frequency`; no target |
| `wavefront_shift_tan` | Same, tangentially sheared | `frequency`; no target |
| `wavefront_pair_phase` | Σ weighted squared phase difference over 9 fixed pupil reference points — **inert in the DLS path**, see below | `wavelength`; no target |
| `diffraction_mtf_sag` | Sagittal MTF from a full vector Huygens PSF (the gate measurement) | `frequency`, `target` |
| `diffraction_mtf_tan` | Tangential MTF from the Huygens PSF | `frequency`, `target` |

```yaml
- {kind: geometric_mtf_sag, field: 0, wavelength: 0.00058756, frequency: 50, target: 0.4, weight: 1000}
- {kind: diffraction_mtf_tan, field: 2, wavelength: 0.00058756, frequency: 50, target: 0.3, weight: 100}
```

**Geometric MTF** (Phase 1, direct complex sum):

```
MTF_u(ν) = | Σᵢ wᵢ exp(−j 2πν (u_x x′ᵢ + u_y y′ᵢ)) | / Σᵢ wᵢ
```

over the flux-weighted image-plane coordinates `(x′ᵢ, y′ᵢ)` relative to the
centroid, with `wᵢ = areaᵢ × intensityᵢ`.

**Wavefront shift** (Phase 2a): the incoherent OTF is the autocorrelation of the
pupil function, so the path difference between two pupil samples one *displacement*
apart carries the frequency content. The displacement a spatial frequency
corresponds to is `s = ν·λ·R_ent/NA`, which for an infinite conjugate collapses
to `ν·λ·EFL`; the optimizer supplies that length as the shear scale, so
`frequency` is a genuine spatial frequency in lp/mm. A constant, a tilt and a
radial quadratic — the reference sphere, the same three terms
`wavefront_sphere_rms` removes — are fitted over the pupil and subtracted before
the differences are formed, and the value is the weighted variance of what is
left:

```
value = Σᵢ wᵢ (ΔWᵢ − ⟨ΔW⟩)² / Σᵢ wᵢ,   wᵢ = areaᵢ·intensityᵢ
ΔWᵢ = W(PupilXᵢ, PupilYᵢ) − W(PupilXᵢ − s, PupilYᵢ)   [sag]
     = W(PupilXᵢ, PupilYᵢ) − W(PupilXᵢ, PupilYᵢ − s)   [tan]
```

The displaced point is interpolated on a Delaunay triangulation of the pupil
samples; one that has left the sampled pupil has no pupil overlap at that
displacement and its pair is dropped. For a small aberration
`|OTF(ν)| ≈ 1 − ½·Var(2π·ΔW/λ)`, so minimizing the value maximizes the MTF at
`ν`. The reading is weighted as `s²` — the sheared difference of a smooth
wavefront is proportional to `s` and the statistic is a variance of it — so it is
a frequency-specific proxy rather than another spot size.

Two properties decide what the kind can be used for.

The reference sphere is what makes it a *wavefront* measure. The focusing of a
converging bundle is itself a quadratic in the pupil, so its sheared difference
is *linear* in the pupil; without the subtraction an ideal lens scores a large
residual and the solver walks the image plane instead of fixing the wavefront.
Removing the tilt is equally required — a displaced PSF has the same MTF
magnitude, so the tilt must not be charged — and the piston cancels in the
difference anyway.

The same subtraction is why the kind does **not** see defocus, and therefore not
field curvature: a defocus term is one of the removed quadratics. That is not an
estimator limitation but what an OTF at a best-focus plane can show. The
delivered-plane gate is measured at the plane the back-focus solve picks, where
the residual defocus is small by construction, so the per-field defocus that
curvature is made of belongs in the `wavefront_defocus` terms (one per field,
also cheap). What survives the reference sphere is the set
`wavefront_sphere_rms` keeps: astigmatism, spherical aberration, coma.

Cost is one Delaunay plus one interpolation per field per merit evaluation
(~1 ms), which is why this kind, not the Huygens term, is the affordable
frequency-specific MTF term for an inner DLS loop: the `diffraction_mtf_*` terms
were measured at 92% of a merit evaluation on a 6-element (400 samples × a
128² image grid, five fields), i.e. 15 s per DLS iteration with `central_diff`
over 50 variables and 4–15 hours per clean phase.

**Wavefront pair phase** (Phase 2b): 9 fixed pupil reference points (centre,
axes, diagonals) are paired (sagittal {left,right}, tangential {bottom,top},
diagonals {…}), and

```
value = Σ_pairs w_pair · ((OPL_i − OPL_j) / λ)²
```

penalizing astigmatism (S/T imbalance) and coma (asymmetric wavefront).

**This kind is registered but measures nothing in the DLS path.** Its nine
reference points sit at exactly `±R` and `±R/√2` on the axes and diagonals,
while a traced polar grid puts its outer ring at `rings/(rings+1)·R` and its
spokes at `2πj/spokes`; the lookup is an exact coordinate match, so no reference
point lands on a sample and the value is 0 for every state. Verified on a traced
6-element: 0.000000 against a live `spot_rms` of 0.001529 on the same grid.
Making it usable needs its reference radii and directions reworked onto samples
that exist (interpolating onto them, as `wavefront_shift_*` now does), which is a
redesign rather than a fix; nothing in the repository uses it.

**Diffraction MTF** (Phase 3): the field's entrance pupil is traced to the
reference surface, propagated with the direct vector Huygens integral on the
delivered flat image plane, and the MTF is read off that PSF — the same window,
auto-enlargement and FFT the `psf` command reports, so a term value and a
reported value are the same number. Sampling is YAML-only:

```yaml
optimization:
  diffraction_mtf:
    num_rays: 400        # effective (surviving) samples per field (default 400)
    grid_size: 64        # image-grid pixels before auto-enlargement (default 64)
    max_grid: 128        # cap on auto-enlarged grid (default 128)
    polarizations: [RCP+LCP]   # incoherent average (default)
```

`num_rays` counts **surviving** samples (the launch count is
vignetting-compensated), so 400 is the floor that keeps the near-hinge MTF within
~0.005–0.02 of the 1600-ray gate. See [contrast.md](contrast.md) for the full
derivations and comparisons.

---

## 4. Assembly, weighting and normalization

### 4.1 The merit

```
M = Σ_configs  w_cfg · Σ_terms  w_term · w_field · w_wavelength · ((value − target)/scale)²
```

`w_field` is the field's declared `fields[].weight` and `w_wavelength` the
wavelength's declared weight (both default 1). Configs are combined by their
`weight`.

### 4.2 Residual scaling

The solver builds `r = √(w_cfg · w_term · w_field · w_wavelength) · (value −
target)/scale`, so `Σ r² == M`. The schedule's per-mode weight enters as an extra
`√w_k` factor (see [§5](#5-conditional-merit-schedule)). This exact identity is
why constraints can be appended to the same vector and why `optimize --verbose`
can report a `breakdown` that reconciles with the DLS merit.

### 4.3 Weight normalization

Enabled by `optimization.merit_normalization.enabled: true` (off by default),
each term's residual is divided by a characteristic `scale` so weights become
comparable across kinds with different units:

- **Spot kinds** (`spot_rms`, `_t`, `_s`, `_worst`, `_weighted`,
  `spot_ee_radius`) use the diffraction-limited Airy radius `0.61·λ/NA`, so a
  diffraction-limited spot normalizes to 1 (falls back to the wavelength when
  the NA is unavailable).
- **Other length-valued kinds** (`opd_rms`, `wavefront_rms_residual`,
  `wavefront_sphere_rms`, `wavefront_sphere_pv`, `longitudinal_color`,
  `lateral_color`) use the term's wavelength (mm), i.e. the residual is measured
  in waves.
- **Kinds with no natural length scale** (MTF, `focal_length`,
  `field_alive`, `pupil_fill`, `distortion_pct`, `glass_role`, …) keep scale 1.

Precedence: a per-term `scale:` overrides both the automatic scale and the
per-kind map and works even when normalization is disabled; then the
`merit_normalization.scales` per-kind map; then the automatic rule.

```yaml
optimization:
  merit_normalization:
    enabled: true
    scales:                 # optional per-kind overrides (the denominator)
      spot_rms: 0.003
      wavefront_rms_residual: 0.0005876
```

To recalibrate, measure the actual term magnitudes with `optimize --verbose`
(the `{"event":"breakdown"}` JSONL line) before changing weights.

> **Also available but not merit terms:** glass-hull and glass-attraction
> penalties are appended to the merit (see [optimize.md](optimize.md) and
> [methods/glass-dispersion.md](methods/glass-dispersion.md)). The hull keeps
> nd/vd physical; the attraction pulls them toward real catalog glasses.

---

## 5. Conditional merit schedule

A fixed weighted sum can be replaced by a blend of **named merit modes** whose
weights follow the evaluation state — e.g. run a colour-only merit while the
imaging merit is still unconverged, then ramp the imaging terms in.

```yaml
configs:
  - id: cfg1
    merit_modes:
      - name: color_first
        terms:
          - {kind: longitudinal_color, wavelength: 0.0004358,
             comparison_wavelength: 0.0006563, weight: 1.0}
          - {kind: glass_role, surface_set: [3, 5], weight: 0.01}
      - name: full
        terms:
          - {kind: spot_rms, field: 0, wavelength: 0.0005876, weight: 1.0}
          - {kind: wavefront_astigmatism, field: 1, wavelength: 0.0005876, weight: 14000}

optimization:
  merit_schedule:
    metric: merit_ratio        # merit_ratio | iteration | glass_role | spot_diffraction | phase
    curve: linear              # linear | sigmoid | step
    anchor_from: 1.0
    anchor_to: 0.05
    glass_surfaces: [3, 5]     # required when metric is glass_role
    metric_aggregation: mean   # mean (default) | max  (spot_diffraction only)
    modes:
      - {name: color_first, weight_from: 1.0, weight_to: 0.0}
      - {name: full,        weight_from: 0.0, weight_to: 1.0}
```

The blended merit is

```
M(x) = Σ_configs w_cfg · Σ_modes w_k(s(x)) · M_{cfg,k}(x)
```

- A config with `merit_modes` uses only those terms; `merit_modes` **without** a
  `merit_schedule` evaluates the config's fixed `merit` instead (mode terms
  ignored).
- `s(x)` is normalised to `t ∈ [0,1]` between the anchors, and each mode's weight
  interpolates `weight_from → weight_to` along the curve: `linear`,
  `sigmoid` (`σ(t) = 1/(1+exp(−10(t−0.5)))`), or `step` (hard switch at
  `t = 0.5`). The residual carries a `√w_k` scale so `Σ r² == M` still holds.

**Metrics:**

| `metric` | `s(x)` |
|---|---|
| `merit_ratio` (default) | current merit / initial merit |
| `iteration` | DLS iteration number |
| `glass_role` | glass-role residual magnitude summed over `glass_surfaces` (all configs) |
| `spot_diffraction` | weighted-average geometric spot RMS / Airy radius (`0.61·λ/NA`); ratio ≥ 1 means the spot exceeds the diffraction limit. Default anchors 3→1 switch geometric→wavefront |
| `phase` (escape only) | escape cycle phase: 0 = escape, 0.5 = glass, 1 = clean |

`spot_diffraction`'s aggregation is `mean` (default) or `max` (worst field). The
weights and metric are recomputed once per iteration and **frozen**, and are
reported as `opt_results.active_mode` / `mode_weights` / `mode_changes` and as
JSONL `{"event":"weights"}` records.

A mode may also carry `back_focus_type: paraxial|wavefront`: when that mode is
dominant (its weight reaches `back_focus_solve.schedule.dominant_threshold`,
default 0.5) the hard back-focus solve switches to that type (hard switch, frozen
per iteration). See
[optimize.md](optimize.md#back-focus-solve-optimizationback_focus_solve) and
[methods/merit-functions.md](methods/merit-functions.md) §5.

---

## 6. Degenerate penalties

A merit term that cannot be evaluated — a pupil grid with no valid rays, or a
wavefront fit that fails even with the dynamic pupil — returns a **bounded
penalty** instead of the legacy `1e6` sentinel (which fed `weight·1e12` into the
merit and stalled the line search):

```yaml
optimization:
  degenerate:
    spot_value: 0.1          # mm; spot_rms*, spot_ee_radius
    opd_value: 1.0e-2        # mm; opd_rms
    wavefront_value: 1.0e-2  # mm; wavefront_* kinds
```

All values default (0.1 / 0.01 / 0.01 mm); non-positive values keep the default.
The fallbacks are sized to exceed a realistic residual, so a failed evaluation is
never cheaper than a real measurement. The contribution is `weight·value²`, so it
pushes the solver away from the degenerate region without exploding the merit.
Successful terms are unaffected.

---

## 7. Constraint catalog

Constraints are enforced by the solver, not summed into the objective the way a
merit term is; the constraint residual is `√weight · error`, and the error is
defined per kind.

### 7.1 Constraint kinds

| Kind | Error `c` | Fields used |
|---|---|---|
| `equality` | `value − target` | `target` |
| `inequality_upper` | `max(0, value − upper)` | `upper` |
| `inequality_lower` | `max(0, lower − value)` | `lower` |
| `band` | `lower − value` if below, `value − upper` if above, else 0 | `lower`, `upper` |
| `fuzzy` | `max(0, \|value − target\| − band_width) / softness` | `target`, `band_width`, `softness` |

Multiple `equality` constraints are supported. An unreachable equality target is
reported with a warning rather than freezing the solve.

### 7.2 Constraint measures

| `measure` | Value | Uses |
|---|---|---|
| `image_height` | Chief-ray image-plane Y for the field | `field`, `wavelength` |
| `incident_angle` | Chief-ray incidence angle (deg) on the surface | `surface`, `field` |
| `thickness` | The surface's axial thickness | `surface` |
| `efl` | Signed paraxial EFL | – |
| `focal_length` | `\|EFL\|` (unsigned) | – |
| `system_length` | Sum of every surface thickness | – |
| `entrance_pupil_diameter` | Paraxial EPD, or `2×` the smallest fixed aperture radius when there is no stop | – |
| `edge_thickness` | Center thickness + back sagitta − front sagitta at `min(diameter)/2` | `surface`, `back_surface` |
| `diameter` | The surface's diameter | `surface` |
| `f_number` | Image-space F/# (infinite conjugate), fallback `EFL/EPD` | – |
| `beam_clearance` | `surfaceRadius − max beam extent` at the surface for the field | `surface`, `field` |
| `vignetting_factor` | Surviving fraction of the full prescribed pupil grid | `field` |
| `beam_diameter` | `2 × max beam extent` at the surface | `surface`, `field` |

For `edge_thickness`, `back_surface` may be omitted; the rear surface then
defaults to the next surface in system order (the surface the front's thickness
is measured to).

```yaml
optimization:
  constraints:
    - {id: efl_lock, kind: equality, measure: efl, target: 50.0, weight: 1000, active: true}
    - {id: edge3, kind: inequality_lower, measure: edge_thickness,
       surface: 3, back_surface: 4, lower: 1.0, weight: 100, active: true}
    - {id: track, kind: inequality_upper, measure: system_length, upper: 120.0,
       weight: 50, active: true}
    - {id: bfd, kind: band, measure: thickness, surface: 9,
       lower: 8.0, upper: 10.0, weight: 1000, active: true}
```

---

## 8. Constraint enforcement

### 8.1 Weighted residual and augmented Lagrangian

Each constraint contributes the augmented-Lagrangian penalty

```
p_j = λ_j · c_j + ½ · μ_j · c_j²
```

with `λ_j` the Lagrange multiplier and `μ_j` the per-constraint penalty weight,
and an equivalent augmented residual

```
r_aug = √(μ_j/2) · (c_j + λ_j/μ_j),   c_j = √weight · error
```

whose square reproduces the penalty gradient, so constraints append to the same
least-squares system as the merit terms. On an accepted step
`λ_j ← λ_j + μ_j·c_j`, and `μ_j` grows ×10 while a violation persists, up to
`optimization.mu_con_max` (default 100). An infeasible constraint can therefore
be relaxed rather than freezing the solve.

### 8.2 Region Active Method

When `optimization.region_active.enabled: true`, only a dynamically-selected
**active subset** of inequality constraints contributes to the penalty; equality
constraints are always active. Activation/deactivation uses hysteresis
(`violation > eps_activate` activates; `violation < eps_deactivate` and small
`|λ|` deactivates; defaults `1e-3` / `1e-4` mm) to prevent chattering, and only
active constraints have their multipliers updated
(`λ ← max(0, λ + alpha·g)`, capped at `max_lambda`). See
[methods/region-active.md](methods/region-active.md).

### 8.3 Reporting

The final state is checked and reported as `opt_results.constraint_violations[]`
for active constraints whose weighted residual magnitude exceeds the tolerance,
and as `opt_results.constraint_measurements[]` (the reached value of every active
constraint). `list merit` resolves constraints the same way the optimizer does:
per-config constraints when present, else `optimization.constraints` (marked
`inherited from optimization`).

---

## 9. Quick reference

### 9.1 Merit kinds

| Kind | Effect | Key params | Value |
|---|---|---|---|
| `spot_rms` | minimize RMS spot | `field`, `target` | RMS radius |
| `spot_rms_t` / `_s` | shape coma/astigmatism | `field`, `target` | tangential / sagittal RMS |
| `spot_rms_worst` | attack worst axis | `field`, `target` | `max(RMS_T, RMS_S)` |
| `spot_rms_weighted` | energy-weighted RMS | `field`, `target` | flux-weighted RMS |
| `spot_ee_radius` | tail-insensitive spot | `field`, `fraction` | EE radius |
| `field_alive` | keep fields alive | `field`, `target`=threshold | bounded deficit |
| `pupil_fill` | defeat pupil clipping | `field` | `(1−ratio)/ratio` |
| `opd_rms` | minimize OPD | `field` | RMS OPL deviation |
| `wavefront_defocus` / `_astigmatism` / `_tilt` | drive a low-order aberration | `field`, `target` | paraboloid magnitude |
| `wavefront_rms_residual` | minimize high-order residual | `field` | fit residual RMS |
| `wavefront_sphere_rms` / `_pv` | drive Strehl directly | `field`, `target` | reference-sphere residual |
| `wavefront_x2`…`constant` | drive a raw coefficient | `field`, `target` | paraboloid `a…f` |
| `distortion_pct` | minimize distortion | `field`, `target` | percent distortion |
| `lateral_color` / `longitudinal_color` | achromatize | `wavelength`, `comparison_wavelength` | height / EFL difference |
| `seidel_*` | control third-order term | `field` | Seidel coefficient |
| `glass_role` | pick correct glass | `surface_set[0]` | signed vd/nd residual |
| `focal_length` | target EFL | `target` | EFL (mm) |
| `pupil_position` / `_diameter` | pin the pupil | `field`, `target` | Z / EPD (mm) |
| `vignetting` | bound vignetting | `field`, `surface_set[0]` | vignetted fraction |
| `clear_aperture` | size a surface | `field`, `surface_set[0]` | beam-envelope diameter |
| `edge_thickness` | control glass path | `surface_set[0]` | glass-path span |
| `geometric_mtf_sag` / `_tan` | raise MTF | `frequency`, `target` | MTF (hinge) |
| `wavefront_shift_sag` / `_tan` | raise MTF at ν (not defocus) | `frequency` | Var(δW) over the pupil shear |
| `wavefront_pair_phase` | broad wavefront quality (inert in the DLS path) | `wavelength` | Σ(Δφ)² |
| `diffraction_mtf_sag` / `_tan` | gate-exact MTF | `frequency`, `target` | Huygens MTF (hinge) |

### 9.2 Constraint kinds

| Kind | Error |
|---|---|
| `equality` | `value − target` |
| `inequality_upper` | `max(0, value − upper)` |
| `inequality_lower` | `max(0, lower − value)` |
| `band` | distance outside `[lower, upper]` |
| `fuzzy` | `max(0, \|value − target\| − band_width)/softness` |

### 9.3 Constraint measures

`image_height`, `incident_angle`, `thickness`, `efl`, `focal_length`,
`system_length`, `entrance_pupil_diameter`, `edge_thickness`, `diameter`,
`f_number`, `beam_clearance`, `vignetting_factor`, `beam_diameter`.

---

## 10. References

- [optimize.md](optimize.md) — the `optimize` command, variables, and YAML.
- [methods/merit-functions.md](methods/merit-functions.md) — merit-term methods
  and the worked `glass_role` example.
- [methods/dls-optimization.md](methods/dls-optimization.md) — DLS solver,
  Jacobian, augmented-Lagrangian constraints, damping.
- [methods/region-active.md](methods/region-active.md) — Okudaira Region Active
  Method for inequality constraints.
- [contrast.md](contrast.md) — geometric MTF, wavefront shift, wavefront pair,
  and diffraction MTF derivations.
- [escape.md](escape.md) / [pso.md](pso.md) — the global optimizers that reuse
  the same merit and constraint machinery.
