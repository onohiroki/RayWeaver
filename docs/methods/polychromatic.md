# Polychromatic evaluation

This document owns the definitions behind RayWeaver's wavelength sets,
wavelength weights, white spot, white best focus, axial colour and secondary
spectrum. Command syntax lives in the individual manuals
([paraxial](../paraxial.md), [chief](../chief.md), [psf](../psf.md),
[focus](../focus.md), [optimize](../optimize.md)); the merit catalog is in
[merit-and-constraints.md](../merit-and-constraints.md).

## 1. Wavelength sets

A **wavelength set** is an ordered list of `(λ, weight)` samples in
millimetres. Three modes are supported:

| Mode | Source | Weights |
|---|---|---|
| `design` | `configs[].wavelengths[]` | `weight` field, normalised to Σw = 1 |
| `spectral` | the document `spectrum` section | `SPD(λ)·Δλ`, normalised (PSF/MTF additionally multiply by the system transmittance τ) |
| `custom` | an explicit `wavelengths: []` list | the listed weights, normalised |

The design table is the primary mode (d/F/C/g, for example). A non-positive
weight **excludes** that line. When every design weight is non-positive the
legacy convention applies: all lines weigh 1. Declaration order is preserved;
duplicates within `1e-12` mm keep the first entry.

The continuous `spectrum` is sampled at `step` (default 5 nm) for evaluation
and `optimize_step` (default 10 nm) for the optimiser. The default range is
400–700 nm. Explicit `entries` replace the grid and are treated as the sample
list (their `relative` values are the raw weights). Built-in curves are `D65`
and `FLAT`; `custom` means the entries are the curve.

```yaml
spectrum:
  curve: D65
  range: [400, 700]   # nm
  step: 5             # nm, evaluation
  optimize_step: 10   # nm, optimisation

configs:
  - id: wide
    wavelengths:
      - {id: 0, value: 0.00058756, label: d, weight: 1.0}
      - {id: 1, value: 0.00048613, label: F, weight: 1.0}
      - {id: 2, value: 0.00065627, label: C, weight: 1.0}
      - {id: 3, value: 0.00043583, label: g, weight: 0.5}
```

### 1.1 Weighting rules

- **Evaluation** always uses normalised weights (Σw = 1), so a white RMS is a
  physical millimetre value and a white Strehl/MTF is directly interpretable.
- **Optimisation** expands ordinary terms with either
  `wavelength_normalization: sum` (default; `wavWeight = w_i/Σw`, so adding a
  wavelength does not rescale the term against the others) or `none` (raw
  weights, ZEMAX `WWGT` semantics).
- Aggregate `polychromatic_*` terms and `secondary_spectrum` always normalise
  internally; their `weight` is the single outer knob.
- The reference wavelength `chief.reference_wavelength` is always evaluated for
  reference quantities, even when it is excluded from the white combination. If
  it is not declared, it resolves to the line labelled `d`, else the largest
  weight, else 587.56 nm.

### 1.2 Resolving a set

`internal/spectral` owns the resolver (`design`, `spectral`, `custom`). The
optimiser calls `spectral.ExpandTerms`:

- ordinary kinds → one term per wavelength with `WavelengthWeight`;
- `polychromatic_spot_rms`, `polychromatic_ee_radius`,
  `polychromatic_strehl`, `polychromatic_mtf_sag/tan` → one term carrying the
  resolved set;
- `secondary_spectrum` → one term carrying the set, with
  `wavelength`/`comparison_wavelength` as the F/C correction pair;
- `lateral_color`, `longitudinal_color`, `axial_color`, `focal_length`,
  `glass_role` → not expandable (a clear error).

## 2. White spot

Per-wavelength spot statistics are measured about each wavelength's own
centroid. That hides **lateral colour**: a shift between wavelengths disappears
when every wavelength is re-centred. The white spot merges the ray clouds
instead:

```
c = Σ w_i c_i / Σ w_i
RMS_white² = Σ w_i (RMS_i² + |c_i − c|²) / Σ w_i
```

The first term is the monochromatic blur, the second the inter-wavelength
(lateral-colour) contribution. `chief` reports it as `white_spot_stats`, and
`polychromatic_spot_rms` uses the same formula as a merit value.
`polychromatic_ee_radius` is the encircled-energy counterpart about the common
centroid (stable index tiebreak, so it is deterministic).

A **per-wavelength** spot term still sees axial colour, because defocus grows
the spot at an off-design wavelength; it does not see lateral colour. Use a
white/aggregate term (or a `lateral_color` term) when lateral colour matters.

## 3. White best image plane

Plane conventions:

| Name | Meaning |
|---|---|
| `file` | the input document's image plane |
| `all` | reference-wavelength, all-field best focus |
| `best` | reference-wavelength, per-field best focus |
| `white` | **all wavelengths and fields**, one common plane |

The white plane is the wavelength- and field-weighted combination of the
per-(field, wavelength) focus:

- **spot** (default for optimisation): minimise
  `Σ_j u_j Σ_i w_i RMS_ij²(s)`. Because `RMS²(s)` is an exact quadratic of the
  plane shift, this is closed-form (`psf.SpotFocusShift` with wavelength
  weights) — one grid trace per (field, wavelength), no plane scan.
- **wavefront** (default for evaluation): the weighted mean of the per-(field,
  wavelength) wavefront best-focus shifts,
  `Σ u_j w_i s_ij / Σ u_j w_i`. This extends the existing wavefront
  best-focus definition to the wavelength axis; it is not the exact `argmin` of
  the weighted white RMS.

```yaml
optimization:
  back_focus_solve:
    enabled: true
    type: white
    objective: spot          # optimisation default; wavefront for evaluation
    wavelength_set: design
    weight_type: uniform
```

`psf --focus-plane white` and `focus --planes white` use the evaluation
objective (wavefront by default). The `focus` path uses the run's resolved
wavelength set and weights as a custom set for the solve.

## 4. Axial colour and secondary spectrum

- **Axial colour** (`axial_color`) is the back-focal-distance difference
  `BFL(λ₂) − BFL(λ₁)` (`paraxial.SecondPrincipalFocus`). It is a different
  quantity from `longitudinal_color`, which keeps the historical EFL
  difference.
- **Secondary spectrum** lets F and C define a straight line in the BFL-vs-λ
  plane and reports the largest departure of the other set members from that
  line. It is the residual left after achromatising the F–C pair.

`paraxial --wavelength-set design` reports the per-λ EFL/BFL/f-number, the
axial-colour shifts and the secondary spectrum together.

## 5. Evaluation vs optimisation

- **Evaluation** (`paraxial`, `chief`, `psf`, `focus`, `wavefront`) uses the
  full set and normalised weights; per-λ rows are emitted, and the white
  aggregates sit beside them.
- **Optimisation** (`optimize`, `escape`, `pso`) uses the coarse spectral grid
  when `wavelength_set: spectral` is requested. Expanding a spectral set into
  ordinary terms is expensive; prefer the aggregate kinds. The white
  back-focus solve is cheap for `objective: spot` (closed form); `wavefront`
  is for evaluation, not the inner DLS loop.
- The **white diffraction merits** (`polychromatic_strehl`,
  `polychromatic_mtf_sag/tan`) run one Huygens PSF per field and wavelength
  set, shared by the Strehl and MTF terms in one evaluation. That is still far
  more expensive than the geometric kinds: use them in a final `merit_modes`
  stage or a short local refinement, with a reduced
  `optimization.diffraction_mtf.num_rays`/`grid_size`. They are not suitable
  for a global `escape`/`pso` merit. They use the diffraction-MTF sampling
  configuration and the delivered image plane; the virtual-pupil model is
  forwarded, but the optimizer's frozen-EPD override is not (the PSF samples
  the pupil `psf.ComputeFieldGrid` would use).

## 6. Determinism

- Wavelength samples are resolved in declaration order (spectral: ascending),
  duplicates are removed with a fixed tolerance, and weights are summed in that
  fixed order.
- White focus and white spot are closed-form / index-tiebreak stable, so two
  runs agree bit-for-bit for any worker count.
- A single-wavelength document (no `wavelength_set`, no `spectrum`) keeps the
  existing per-term behaviour and output.
