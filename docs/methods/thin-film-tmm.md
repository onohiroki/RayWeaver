# Thin-film transfer-matrix method (TMM)

This document describes how `rayweave tmm` (and coated-surface intensity in the
ray tracer) computes the reflectance and transmittance of a stack of thin-film
layers.

## 1. The characteristic matrix

Each layer `i` with refractive index `nᵢ`, thickness `dᵢ` (nm) and angle
`θᵢ` contributes a 2×2 characteristic matrix

```
         ⎡ cos δᵢ        (i/ηᵢ) sin δᵢ ⎤
Mᵢ =     ⎢                              ⎥
         ⎣ i ηᵢ sin δᵢ         cos δᵢ  ⎦
```

where the phase thickness is

```
δᵢ = (2π/λ) nᵢ dᵢ cos θᵢ
```

and `ηᵢ` is the optical admittance, which depends on polarisation:

```
η = nᵢ cos θᵢ        (s-polarisation)
η = nᵢ / cos θᵢ      (p-polarisation)
```

The angles in the layers follow Snell's law from the angle of incidence:
`nᵢ sin θᵢ = n₀ sin θ₀` (constant across the stack). If any layer would require
`sin θᵢ > 1` (total internal reflection in the stack) the stack is treated as
fully reflecting.

## 2. Stack combination

The stack matrix is the ordered product of the layer matrices:

```
M = M₁ · M₂ · … · M_N
```

with complex arithmetic (absorption could be included via complex indices; the
current implementation uses real indices, so R + T = 1 at the substrate side).

## 3. Reflectance and transmittance

With `B = M[0][0] + M[0][1]·η_s` and `C = M[1][0] + M[1][1]·η_s` (where `η_s`
is the substrate admittance), the amplitude reflection and transmission
coefficients are

```
r = (η₀ − C/B) / (η₀ + C/B)
t = 2η₀ / (η₀·B + C)
```

and the (intensity) reflectance / transmittance are

```
R = |r|²
T = (η_s / η₀) · |t|²
```

The `η_s/η₀` factor accounts for the change of medium between incident and
substrate.

## 4. s- and p-polarisation

The computation is performed twice, once per polarisation, and reported as
`Rs/Ts` and `Rp/Tp`. For the common unpolarised case the average is
`(R_s + R_p)/2`.

## 5. Use inside the ray tracer

When a traced ray hits a surface whose `coating` is in the coating catalog, the
engine calls `ComputeTMM(n₁, n₂, layers, λ, θ₁)` at the actual angle of
incidence `θ₁` and replaces (reflection) or scales (transmission) the Fresnel
intensities with the coating's R/T. Coating layer indices may be given directly
or resolved from the glass catalog at the ray's wavelength.

## Example: quarter-wave stack

A 9-layer alternating high/low stack (see `samples/dielectric-mirror.yaml`)
approximates a Bragg reflector: each layer has optical thickness λ/4, so the
partial reflections add in phase and `R → 1` across the design band.
