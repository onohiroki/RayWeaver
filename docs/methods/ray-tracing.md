# Sequential ray tracing

This document describes how `rayweave trace` (and every higher-level command
that traces rays) propagates a ray through the optical system.

A ray is a start point, a direction and a **path** — the ordered list of
surface IDs to visit. Surface 0 is the implicit object plane: the trace records
it but performs no intersection or refraction. Each subsequent surface in the
path is handled as follows.

## 1. Frame transformation

Every surface carries a precomputed local coordinate frame
(`GlobalToLocal` / `LocalToGlobal` 4×4 matrices) built from its `decenter`
steps (translation plus X/Y/Z rotations). The incoming origin and direction are
transformed into the surface's local frame, where the surface vertex is at the
origin and the surface is symmetric about the local Z axis.

## 2. Intersection

The ray parameter `t` (distance along the direction, in local coordinates) at
which the ray meets the surface is found.

**Sphere** (`type: sphere`): a quadratic solved in closed form,
`IntersectSphere`. For radius 0 (a plane) the intersection is the plane at the
vertex. The smallest positive root is chosen, with a tolerance guard
(`t > 1e-12`) so a ray already sitting on a surface does not immediately "miss".

**Asphere** (`type: asphere_polynomial`, `asphere_zernike`): the sag is
`z = sag(h)`, and the intersection is solved with Newton iteration
(`IntersectAsphere`, max 50 iterations, tolerance 1e-12). The sag functions are

```
sag(h) = c h² / (1 + √(1 − (1+κ) c² h²)) + Σ aᵢ h^(2i+4)      (polynomial)

sag(h) = base + c₁ + c₂ ρ² + c₃ (2ρ⁴ − ρ²),  ρ = h / r_norm      (zernike)
```

where `c` is curvature, `κ` the conic constant, `h² = x² + y²`, `aᵢ` the even
polynomial coefficients, and `r_norm` the normalization radius. If the
intersection point exceeds the surface `diameter`, the ray is rejected with
`aperture_stop` (unless aperture checks are skipped).

## 3. Normal

The outward surface normal at the hit point is computed analytically for a
sphere, and from the numerical slope of the sag function for aspheres
(`AsphereNormal`). The normal is oriented so that the ray direction has a
positive dot product with it (`cos θ₁ > 0`).

## 4. Interaction type

The interaction is `TRANSMIT` unless the surface is a fold mirror
(`decenter[].reflect: true`) or the path encodes a reflection
(`DetermineInteraction` detects a direction reversal: `prev → current → next`
changing sign).

## 5. Media and refraction

The incident medium `n₁` is the material of the surface that precedes the
current one in the sequence (skipping fold mirrors, which do not separate
media), and the emergent medium `n₂` is the current surface's material. For
backward-travelling ghost rays (after a path-encoded reflection) the two are
swapped. Indices are looked up in the glass catalog at the ray's wavelength.

**Refraction** uses the vector form of Snell's law:

```
η = n₁/n₂
cos θ₂ = √(1 − η² (1 − cos² θ₁))
d' = η d + (η cos θ₁ − cos θ₂) n
```

If `1 − η²(1 − cos² θ₁) < 0` the ray is totally internally reflected
(`total_internal_reflection` error).

**Reflection** uses `d' = d − 2 (d·n) n`. Fold mirrors reflect ideally
(intensity 1); path-encoded ghost reflections use the Fresnel coefficients.

## 6. Fresnel coefficients and intensity

The amplitude coefficients (s- and p-polarisation) are

```
r_s = (η₁ₛ − η₂ₛ) / (η₁ₛ + η₂ₛ),   ηₛ = n cos θ
r_p = (η₁ₚ − η₂ₚ) / (η₁ₚ + η₂ₚ),   ηₚ = n / cos θ
t = 2 η / (η₁ + η₂)
```

Intensity (power) is the square of the amplitude, with the transmittance scaled
by the projection ratio `(n₂ cos θ₂)/(n₁ cos θ₁)` so that `R + T = 1` for an
uncoated interface. If the surface carries a coating, the per-interface Fresnel
intensities are multiplied by the coating's TMM transmittance (or replaced by
its reflectance for reflections) — see [thin-film-tmm.md](thin-film-tmm.md).

## 7. Optical path length

Each segment contributes `OPL = |t| · n₁`; the cumulative OPL is accumulated
per surface and stored on the result. The total OPL of a ray is used for
wavefront (OPD RMS) analysis.

## 8. Glass-path constraints

Optional `min_glass_path` / `max_glass_path` on the entry surface of a glass
element bound the path length travelled inside the glass. A violation reports
`glass_path_too_short` / `glass_path_too_long`. The entry position is tracked
between the first non-air material surface and the next reflection.

## Error handling

| Code | Cause |
|---|---|
| `surface_not_found` | path references an unknown surface ID |
| `missed_surface` | no intersection found |
| `aperture_stop` | intersection lies outside the surface diameter |
| `total_internal_reflection` | Snell's law has no real solution |
| `glass_path_too_short` / `glass_path_too_long` | glass-path constraint violated |

## Parallelism

`trace` processes rays concurrently (one goroutine pool sized to
`GOMAXPROCS`). The engine is read-only after catalog loading, so concurrent
traces are race-free.

## Fold model

Folded (mirror) systems use **positive thicknesses** only. A mirror is created
by `decenter: [{tilt: [0, 180, 0], reflect: true}]`. The fold walk
(`internal/surface/precompute.go`) keeps rays travelling +Z locally after a
fold; the beam-frame radius after an odd number of reflections is the negation
of the physical radius. `materialBefore` skips fold mirrors when resolving
media, because a mirror does not separate two different media.
