# Paraxial (first-order) analysis

This document describes how `rayweave paraxial` computes the first-order
properties of a system: focal lengths, cardinal points, pupils and f-numbers.

## 1. Paraxial model

The system is unfolded into the local (beam) frame with **positive**
thicknesses. Each surface has an index `i`, a medium index `nᵢ` (the medium
*after* the surface) and a paraxial radius `Rᵢ` (the beam-frame radius; for an
asphere it is derived from the paraxial curvature, i.e. the second derivative
of the sag at the vertex).

A ray is described by `(y, u)` — height and slope (angle). Transfer across a
thickness `t`:

```
y' = y + t·u
```

Refraction at a surface with power `φ = (n_after − n_before) / R` (for a mirror
`φ = −2 n_before / R`, with `n_after = −n_before` to fold the slope sign):

```
u' = (n_before·u − y·φ) / n_after
```

Mirrors flip the index sign, which handles the fold automatically in the
unfolded local frame.

## 2. EFL and rear cardinal points

A forward marginal ray starts at `(y=1, u=0)` (parallel to the axis). After
tracing through all surfaces, the last slope `u` in image space gives

```
EFL = −1 / (n_image · u)
```

The BFL (second principal focus) is the height ratio `−y/u` at the last lens
surface, from which the rear principal point follows as `BFL − EFL`. The rear
nodal point coincides with the rear principal point for an object-space index
of 1.

## 3. Front cardinal points

The same marginal ray is traced **reversed** (`traceReversed`, using negative
thicknesses and swapped media). From the resulting slope and height at the
first surface:

```
FFL = −1 / (n_object · u_rev)        (reported as |FFL|)
front focus = −y_rev / u_rev
first principal point = front focus + |FFL|
```

## 4. Pupils

From the aperture stop (explicit stop ID, else the smallest fixed-diameter
surface):

- **Entrance pupil:** a ray traced backward from the stop to the object side
  (`tracePupilBackward`). A chief ray entering the stop at the axis gives the
  pupil location `−y/u`; an aperture ray entering the stop at its rim gives the
  pupil radius. When `chief_rays` are available their entrance-pupil location
  is preferred.
- **Exit pupil:** the same trace in the forward direction from the stop to the
  image side.

## 5. f-numbers and NA

```
f/# (infinite conjugate)  = EFL / entrance_pupil_diameter
f/# (working)             = image-space conjugate distance / entrance_pupil_diameter
NA                        = 1 / (2 f/#)
```

## 6. Magnification (finite conjugate)

With `object_height > 0`, a marginal ray starting at `(y = h, u = 0)` is
traced; the lateral magnification is `magnification = y_image / h`, and
`minification = 1/|magnification|`.

## 7. Half angle of view

When piped after `chief`, the largest field angle among the `chief_rays` is
reported as the half angle of view.

## 8. Total track

The physical distance from the first surface vertex to the image plane. The
image plane lies at the last surface vertex advanced along its local Z by the
last thickness; after an odd number of reflections the local Z points toward
global −Z, so the sign is flipped accordingly.
