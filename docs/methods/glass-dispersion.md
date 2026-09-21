# Glass catalog and dispersion

RayWeaver resolves every material name to a refractive index at a given
wavelength. This document describes the dispersion models and the glass hull
used to constrain glass optimization.

## 1. Catalog sources

The catalog is populated from three sources:

- **Inline entries** in the YAML `glass_catalog.entries` (`nd`/`vd`, or full
  dispersion coefficients);
- **AGF files** listed in `glass_catalog.files` or loaded from a directory
  (`--glass-dir`, or `glass_catalog.directory`);
- **AGF catalogs** scanned at runtime (e.g. `GLASS/*.agf` in the repository).

Glasses are keyed by name with aliases; `AIR` and the empty string always
resolve to `n = 1`.

## 2. Dispersion formulas

`CalcRefractiveIndex` dispatches on the glass's `type` and `dispersion_formula`:

| Formula | Form |
|---|---|
| `sellmeier_1` | `n² = 1 + Σᵢ Bᵢλ²/(λ² − Cᵢ)`, 6 coefficients |
| `schott` | `n² = A₀ + A₁λ² + A₂λ⁻² + A₃λ⁻⁴ + A₄λ⁻⁶ + A₅λ⁻⁸` |
| `extended_2` | `n² = 1 + Σᵢ Bᵢλ²/(λ² − Cᵢ)`, 5 Sellmeier terms (10 coefficients) |
| `extended_3` | polynomial `n²` in λ² and λ⁻² … λ⁻¹², 9 coefficients |
| `constant` | `n = nd` (no dispersion) |
| tabulated | cubic-spline interpolation of an `(λ, n)` table (linear below 3 entries); Cauchy extrapolation connected C0-continuously at the table edges |
| model | `nd`/`vd`-based approximation (below) |

## 3. Model glasses (nd/vd → dispersion)

A glass defined only by `nd` and `vd` — including **model glasses** whose
`nd`/`vd` are optimization variables — is turned into a full dispersion curve
as follows (`RefractiveIndexFromNDVD`). The nd/vd approximation used here is
described in detail at
<http://onohiroki.cycling.jp/2011-01-21-1#d20110121n1>:

1. The standard-line indices `nₙ`, `n_C`, `n_F`, `n_d`, `n_g`, … are derived
   from `nd`/`vd` using the industry approximation `n = 1 + (n_d − 1)(C + Aλ² +
   Bλ⁻² + …)` fitted to the known (λ, index) knots (`internal/glass/indeces.go`).
2. Between the first and last standard-line knots (≈365–2058 nm) the (λ, n)
   knots are interpolated with a **cubic spline** (`SplineInterpolate`), so the
   model is smooth and differentiable — important for the DLS Jacobian.
3. Outside that band, a **Cauchy fit** `n = A + B/λ² + C/λ⁴ (+ D/λ⁶)` is fitted
   to the same knots by least squares and evaluated. The curve is shifted so it
   meets the spline **exactly at the band edges** (C0-continuous), and values
   are clamped to `n ≥ 1`. Cauchy is well-behaved beyond the fitted band.

Tabulated glasses follow the same scheme: a natural cubic spline inside the
`(λ, n)` table (linear interpolation below 3 entries, since a spline needs at
least 3 knots) and a C0-connected Cauchy extrapolation outside the table range.

Because the dispersion is a smooth function of `nd`/`vd`, the optimizer can
differentiate merit terms with respect to glass variables.

## 4. Cauchy fit

`FitCauchy` builds the normal equations for `n(λ) = A + B x + C x²` (or a
fourth term), `x = 1/λ²`, and solves them with `raymath.SolveLinear`
(least-squares normal equations). This is the fallback dispersion model used
outside the spline band, both for model and tabulated glasses. Because a raw
least-squares fit need not reproduce the spline value exactly at the band edge,
`ConnectedCauchy` shifts only the constant term `A` so the curve passes through
the edge knot — making the overall index continuous (C0) across the spline →
Cauchy transition.

## 5. Index caching

Refractive indices are cached per (glass, nd, vd, wavelength). Model glasses
optimized by nd/vd change values between evaluations, so nd/vd are part of the
cache key; catalog glasses use stable nd/vd markers. This caching is what makes
the grid traces inside a single merit evaluation fast.

## 6. The glass hull

The **glass hull** restricts the `(nd, vd)` glass variables to a region of
commercially realizable glass. It is built **at runtime** using Andrew's
monotone chain — there is no pre-generated vertex table. The region is selected
by `optimization.glass_hull.source`:

| `source` | Hull geometry | AGF (`--glass-dir`) |
|---|---|---|
| **`union`** (default, or empty) | built-in `DefaultHullVertices` (the full real-glass region) **∪** every loaded catalogue glass | included |
| `builtin` | built-in `DefaultHullVertices` only (catalogue ignored) | ignored |
| `catalog` | the loaded catalogue only; entries are filtered to the manufacturer kinds present, ordered by `DefaultGlassKindOrder` (`SCHOTT → OHARA → HOYA → CDGM`) so only the highest-priority manufacturer's entry for a shared name enters the hull | included |
| `explicit` | the configured `glasses:` `(nd, vd)` points only | irrelevant |

Every source except `explicit`/`catalog` can always form a hull
(`DefaultHullVertices` has 13 vertices). `catalog` falls back to `builtin` when
the catalogue yields fewer than 3 hull vertices; `explicit` requires at least 3
valid points and falls back to `builtin` (with a warning) otherwise — so the
constraint never silently disappears:

```yaml
optimization:
  glass_hull:
    enabled: true
    source: explicit      # union (default) | builtin | catalog | explicit
    margin: 0.02
    weight: 1.0
    glasses:              # source: explicit only, >= 3 required
      - {nd: 1.51, vd: 64.0}
      - {nd: 1.62, vd: 36.0}
      - {nd: 1.69, vd: 49.0}
```

An **enabled** section is required for the hull to apply: a `glass_hull:`
section whose `enabled` is omitted (false) is treated as an explicit disable,
matching the previous behaviour. With `source: union` (the default) the
constraint is never tighter than the built-in real-glass region, while any
catalogue glass (inline entry or AGF loaded via `--glass-dir`) that lies outside
it is admitted too.

The hull stores its vertices (CCW), the nd/vd bounds, and a barycentric-
coordinate cache.

When `optimization.glass_hull.enabled: true`, the hull constrains model-glass
`(nd, vd)` points in two layers:

- **Smooth interior penalty** (`ConvexHull.Penalty`): near-zero well inside the
  hull, ramping smoothly to a hard `1e6` beyond the boundary (scaled by
  `glass_hull.margin` and `glass_hull.weight`). This steers the DLS interior.
- **Core-constraint check** (`ConvexHull.Contains` + `Optimizer.GlassHullViolations`):
  an O(n_vertices) barycentric point-in-hull test. During `escape`, a converged
  point whose nd/vd lies outside the hull is classified as an
  `infeasible_basin` (reason `glass_hull_violation`) and is never recorded as a
  minimum. The offending element's glass surfaces (from `Variable.SurfaceSet`)
  are reported.

This keeps optimized glasses inside the region of commercially realizable
glasses.
