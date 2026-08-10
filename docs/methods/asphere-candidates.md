# Asphere candidate selection and initial sag estimation

This document describes the numerical method behind `rayweave asphere`: how it
decides *which* surfaces are the best candidates for a rotationally-symmetric
even-order asphere, and how it estimates safe **initial** coefficients. For
command-line usage, flags and YAML structure, see
[the `asphere` manual](../asphere.md).

The analysis answers two questions in one pass:

1. **Where** does an asphere help most? (ranking)
2. **What coefficients** are a safe starting point? (fit)

Both are derived from a per-field, per-wavelength polar pupil grid traced
through the system, so the decisions are based on the real off-axis beams, not
on paraxial approximations.

## 1. Overview of the pipeline

```
surface.Precompute
        │
        ▼
resolve candidate surfaces (default: every non-mirror surface)
        │
        ▼
compute per-field entrance-pupil Z (stop Z, or dynamic pupil, or fixed-aperture fallback)
        │
        ▼
GenerateFootprints ── polar pupil grid per (field, wavelength) → per-surface ray hits
        │
        ▼
PreprocessOPD (piston by mean reference; optional tilt / defocus removal)
        │
        ▼
per candidate surface:
   BuildCellGrid  → ring×sector polar cells
   ComputeCellStats → common OPD, conflict, unique residual, azimuth variance,
                      radial gradient, coverage weight per cell
        │
        ▼
Phase 3 (if sensitivity_samples > 0): measured sensitivity H per surface
   base merit (traced once) vs merit with the scaled asphere inserted,
   plus per-coefficient finite-difference derivatives ∂Merit/∂c_j
        │
        ▼
RankSurfaces → composite score, sorted descending
        │
        ▼
top-K: FitAsphereCoeffs → conic + A4..A12 (physical + scaled)
        │
        ▼
(optional --validate) short DLS per fitted surface on spot RMS → validation block
(optional --apply)     insert the top validated asphere into the system
```

The rest of this document expands each step.

## 2. Footprint generation

For every (field, wavelength) the analysis traces a **polar pupil grid** with
`pupil_samples_radial` radial samples (default 21). Each grid is centred on the
field's **entrance pupil**:

- With an explicit stop (`chief.stop_surface`) every grid is centred on the
  stop surface's physical Z.
- Without a stop the grid is centred on the **dynamic pupil**: the in-lens
  crossing of the field's chief ray with field 0's chief ray (the aperture
  position), computed by a cheap `chief`-style pass. When that crossing is
  ill-conditioned (e.g. a heavily degraded starting system whose chief rays do
  not cross in-lens), the analysis falls back to the position of the tightest
  fixed aperture (`surface.FixedMinApertureRadiusZ`), where the beam is
  physically limited.

Each ray is traced through the system and recorded as a **RayHit**: the ray's
image-space OPL, its position on every surface it crossed, and its pupil
coordinates. Disabled / failed rays are flagged and excluded.

## 3. OPD preprocessing

Per field, the OPD of every valid ray is referenced to the field's **mean OPL**
(piston removal — this is always done). Optionally (`remove_tilt`,
`remove_defocus`) a best-fit plane (tilt) and/or paraboloid (defocus) is fitted
in pupil coordinates and the fitted contribution subtracted:

```
basis = [1, X, Y, X²+Y²]   (the [1] piston column is fitted for a stable
                            normal equation but only tilt/defocus are removed)
coeffs = (AᵀWA)⁻¹ AᵀWb
```

`remove_tilt` and `remove_defocus` default to `true` / `false`. The `r²` term
that dominates the asphere's low-order behaviour is therefore usually removed
from the OPD before the fit; `FitAsphereCoeffs` reports the magnitude of that
removed term as a warning. (The YAML field `remove_piston` is accepted but has
no effect — piston removal is implicit in the mean reference.)

## 4. Polar footprint cells

On each candidate surface the ray hits are binned into a polar grid of
`cell_rings` rings × `cell_angles` sectors, centred on the surface's optical
axis. The ring grid spans `[0, maxR]`, where `maxR` is the maximum radial
extent of all hits on that surface.

Each cell aggregates over the fields that occupy it:

| Statistic | Meaning |
|---|---|
| `MeanR` | weighted mean radial coordinate of the cell's hits |
| `OccupiedFields` | the set of fields with ≥ `min_rays_per_cell` hits in the cell |
| `CommonOPD` | the weighted mean OPD over the occupied fields (the field-*common* part) |
| `Conflict` | weighted variance of the per-field means about that common mean (the part the fields *disagree* on) |
| `UniqueResidual` | for single-field cells, the squared cell mean OPD (no overlap → only one field can be corrected there) |
| `AzimuthVariance` | weighted variance of OPD across all hits in the cell (rotational asymmetry) |
| `RadialGradient` | \|Δμ/Δr\| to the next ring at the same sector |
| `Weight` | cell ray weight normalised by the surface's total ray weight |

Cells with fewer than `min_rays_per_cell` hits are dropped.

## 5. Composite score

Each candidate surface `s` gets a composite score

```
S_s = w_com·E^common + w_uni·E^unique + w_fit·F + w_sens·H
      − w_conf·C − w_mfg·M − w_unstable·U
```

with the weights from `asphere_candidate.score_weights` (defaults
`common 0.35, unique 0.15, fit 0.20, sensitivity 0.15, conflict 0.10,
manufacturing 0.05`).

The terms:

- **E^common** — the shared (multi-field cell) OPD energy of the surface,
  normalised by its total cell OPD energy. A rotationally-symmetric asphere can
  correct this part jointly across fields.
- **E^unique** — the single-field-cell OPD energy fraction. Rewarded because a
  surface dominated by one field's footprint is still aspherisable, but
  penalised less than common energy (unique energy often comes from radial
  region where only the edge field reaches).
- **F** — the fit quality: R² of fitting the shared-cell common OPD to the
  rotationally-symmetric even-order basis (see §7). A surface whose common OPD
  is smoothly radial fits well.
- **H** — the sensitivity term. When the measured pass ran, H is the traced
  relative merit improvement `1 − asphere_merit/base_merit` (§8). Otherwise the
  analytic proxy `(|n2−n1| / max|Δn|) · coverage`, i.e. the index contrast
  across the surface scaled by how much OPD energy the asphere can address.
- **C** — the weighted inter-field conflict: the normalised per-cell conflict
  of the shared cells. A surface where the fields demand *different* shapes
  scores down.
- **M** — the manufacturing penalty, combining base curvature magnitude (0.6)
  and the footprint radius `meanR` (0.4, scaled against 50 mm). Steep, large
  surfaces are harder to make.
- **U** — stop-proximity instability: `exp(−dz/20)` where `dz` is the physical
  distance to the stop surface (omitted when there is no stop). A surface
  essentially at the stop ("flattest field lens") is where an asphere is most
  stable; the term is small if `dz` is small, but the sign convention penalises
  surfaces far from the stop when the sensitivity is measured through the
  frozen pupil.

`coverage` is `E^common + E^unique` (the aspherisable fraction of the surface's
OPD energy) and is reported separately.

## 6. Pupil-grid ray and ray weights

Field weights come from the config's `fields[].weight` (default 1); each grid
ray carries the field's weight, so the energies and the sensitivity merit are
all field-weighted. Wavelengths are combined by tracing a grid per wavelength;
the analysis uses the first wavelength as the "primary" for media-index lookups
of `(n1, n2)` in `mediaIndices` (mirrors get `n2 = −n1`).

## 7. Initial coefficient fit

For the top-K surfaces (those with the highest score that also yield a valid
fit) `FitAsphereCoeffs` estimates initial coefficients.

### 7.1 Radial fit of the common OPD

The shared-cell common OPD is fitted to an even-order polynomial in the cell
mean radius, normalised by the footprint max radius `rMax`:

```
basis rows:  [ρ², ρ⁴, ρ⁶, …]  with ρ = MeanR / rMax
solveRidge:  weighted least squares with Tikhonov ridge regularisation
```

The design columns are scaled to unit norm, each basis column `j` is damped by
`orderPenalty[j] = 1 + j·0.5` (higher orders are damped harder to suppress
oscillatory overfitting) and `λ = 0.05`. `max_even_order` controls how many
beyond-conic terms are used: 8 → A4..A8, 10 → A4..A10, 12 → A4..A12.

The R² measured on the r²-removed residual is reported as `fit_quality`: how
well a rotationally-symmetric asphere (excluding a removable defocus)
represents the shared OPD.

### 7.2 OPD → sag conversion

The fast normal-incidence approximation

```
dz ≈ −O / (n2 − n1)
```

converts the OPD polynomial into surface sag. With the radius normalised as
above, the physical coefficient `c_k` for the term `r^k` is

```
c_k = −coef_j / (dn · rMax^k)     with k = 4 + 2j,  dn = n2 − n1
```

The `r²` (defocus) term is not part of the asphere coefficients: with
`preserve_vertex_curvature: true` (default) it is absorbed into a report-only
warning (`removed defocus r² term (2·a2=…)`); with `false` the implied vertex
curvature change `2·a2` is still reported but the user would apply it via
`curvature`, not via the asphere coefficients.

### 7.3 Conic

If `include_conic` (default true), the conic constant is estimated by fitting
the defocus-removed sag target with a **pure-conic deformation** via a weighted
1-D grid search over `k ∈ [−20, 20]`. A conic is only reported when it
genuinely reduces the residual (`bestErr < 0.98·errAtZero`) and the optimum is
interior to the search range; otherwise it is left at 0 (an edge-saturating
optimum means a conic alone cannot represent the residual — the honest answer
is a pure polynomial).

### 7.4 Physical-sense constraints

- |A4| is capped at `1/|R|` (the surface's beam-frame radius magnitude).
- |conic| is capped at `1/|R|`.

Each applied cap adds a warning.

### 7.5 Scaling for embedding

`scaled_coefficients = coefficients × sag_scale` (default α = 0.2). The scaled
values are what `--validate` embeds and what the measured sensitivity evaluates;
the raw `coefficients` are the least-squares estimate, the scaled ones the safe
starting point.

## 8. Measured sensitivity (Phase 3)

The ranking's sensitivity term can be **measured rather than assumed**. When
`sensitivity_samples > 0`:

1. The **base merit** — weighted RMS OPD over the full pupil grid, piston
   removed (tilt/defocus per config) — is traced **once** and shared by all
   candidates.
2. For each candidate surface, a provisional fit is scaled by `sag_scale` and
   **inserted**; the merit is re-traced with the asphere applied. The relative
   improvement `1 − asphere/base` is the measured `improvement` and becomes the
   sensitivity term H.
3. Per-coefficient finite-difference derivatives `∂Merit/∂c_j` (j = A4..A12)
   are computed with a **shared frozen pupil** between base point and
   perturbations (mirroring the DLS Jacobian convention), so the Jacobian is
   consistent.

An unfit (zero) asphere gets improvement ≈ 0 and is demoted rather than
falling back to the analytic proxy. Set `sensitivity_samples: 0` to skip the
measured pass entirely and rely on the analytic index-contrast proxy
(`(|n2−n1| / max|Δn|) · coverage`).

## 9. Validation with a short DLS (`--validate`)

For each fitted top-K surface, `--validate` runs an isolated short DLS:

- The **scaled** coefficients are inserted onto the candidate surface
  (`asphere_polynomial`, conic left at 0 to avoid a degenerate discriminant on
  weakly-curved surfaces).
- The asphere coefficients `a4..a12` are the **only** optimisation variables
  (zero terms skipped), with bounds spanning 10× the scaled value.
- The merit is **spot RMS** — one term per (field × wavelength) — the same
  geometric spot the `chief` `spot_stats.rms_r` reports, so the improvement is
  coherent with the before/after spot comparison.
- The dynamic pupil is recomputed against the asphered system before the solve
  so the initial grid hits the new surface.
- `--dls-iter` defaults to 20 iterations over a `--num-rays` (default 64)-ray
  grid.

Each validated surface gains a `validation:` block with `before_merit`,
`after_merit`, `improvement`, `iterations`, `status` and the DLS-solved
`coefficients`.

## 10. OPD overlap profiles

For every candidate surface, `BuildOPDProfiles` bins the ray hits by field and
ring and emits the weight-mean OPD per ring (piston removed, tilt/defocus per
config). Each `opd_profiles[]` entry carries `max_r` (footprint max radius) and
a per-field `ring_radius` / `opd` pair.

Reading a profile: fields whose curves **overlap** share a wavefront error that
a rotationally-symmetric asphere can correct jointly; fields that pull apart
indicate inter-field conflict (the surface's `conflict` term). This is the data
behind the OPD-overlap chart in `samples/asphere-demo.bash`.

## 11. What the analysis deliberately does *not* do

- It does **not** finalise a design — the fitted coefficients are an estimate
  used to seed a subsequent `optimize`/DLS run (the demo verifies them, then
  `--apply` embeds the DLS-solved values).
- The `max_sag`, `max_slope_deg` and `max_curvature_variation` config fields are
  accepted but currently unused by the scoring/fit; slope and curvature limits
  are effectively enforced indirectly (bounded A4/conic, ridge regularisation,
  sag-scale).
- Beam-aperture clipping by fixed surfaces is not part of the footprint
  measurement; the grid is sized by the pupil and checked only for successful
  traces.