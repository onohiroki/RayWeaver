# Merit functions

The optimizer minimizes a scalar **merit function** assembled from merit terms,
each weighted and summed. This document lists the term kinds, how each is
evaluated, and how weights combine across fields, wavelengths and configs.

## 1. Assembly

In multi-config mode each config contributes its own merit. The total merit is

```
M = Σ_configs  w_cfg · M_cfg
```

and within a config

```
M_cfg = Σ_terms  w_term · r(term)
```

where `r(term)` is the per-term residual (a positive quantity to be made
small). Weighting lets the designer balance fields, wavelengths and configs
(zoom positions).

## 2. Term kinds

| Kind | Residual |
|---|---|
| `spot_rms` | RMS radial spot size on the reference surface (see below) |
| `opd_rms` | RMS optical path difference across the pupil grid |
| `distortion_pct` | percent distortion |
| `lateral_color` | lateral colour |
| `longitudinal_color` | longitudinal colour |
| `seidel_spherical` / `seidel_coma` / `seidel_astigmatism` / `seidel_distortion` | third-order Seidel coefficients |

### spot_rms

A field/wavelength grid of rays is traced and the intensity-weighted spot RMS
radius about the centroid is computed (see
[chief-rays-and-spot.md](chief-rays-and-spot.md), §5). The grid density follows
`optimization.num_rays`, and the pupil is scaled by
`optimization.aperture_margin` (clamped ≥ 1). Polychromatic spot RMS uses the
per-wavelength grid re-trace.

### opd_rms

The same pupil grid is traced and each ray's **optical path length** (OPL) is
recorded. The residual is the RMS of `OPL − ⟨OPL⟩` over the accepted rays, i.e.
the wavefront error expressed as an optical path difference:

```
OPD_RMS = √( (1/N) Σ (OPLᵢ − OPL̄)² )
```

This is a convenient aberration measure that does not require a chosen
reference sphere.

### distortion_pct

```
distortion = 100 · (y_chief − y_paraxial) / y_paraxial
```

where `y_chief` is the traced chief-ray image height for the field and
`y_paraxial = EFL · tan θ` is the paraxial (perfect) height.

### lateral_color

The difference between the chief-ray image heights at two wavelengths:

```
lateral_color = y_chief(λ₂) − y_chief(λ₁)
```

### longitudinal_color

The difference in paraxial EFL between two wavelengths:

```
longitudinal_color = EFL(λ₂) − EFL(λ₁)
```

### Seidel coefficients

Third-order (Seidel) aberration coefficients computed for a field and
wavelength (see `internal/paraxial/seidel.go`). Each kind targets one
coefficient (spherical, coma, astigmatism, distortion).

## 3. Constraints

Constraints are *not* merit terms: they are enforced by the DLS solver through
an augmented-Lagrangian penalty (see
[dls-optimization.md](dls-optimization.md), §5). The solver adds
`λ·c + ½μ·c²` per constraint residual `c`, so a constraint pushes the solution
toward `c = 0` while the penalty weight is managed automatically. Constraint
kinds: `equality`, `inequality_upper`, `inequality_lower`, `band`, `fuzzy`.

## 4. Notes

- An evaluation that degenerates (all rays missing, division by zero) returns a
  large merit (1e6) so the solver is pushed away rather than misled.
- `distortion`/`colour`/`seidel` terms are evaluated from chief-ray / paraxial
  traces rather than a full grid, so they are cheap.
- The `breakdown` event in `optimize --log`/`--verbose` JSONL output lists the
  per-term residual values, which helps find which term dominates the merit.
