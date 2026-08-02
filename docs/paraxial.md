# `rayweave paraxial` — first-order analysis

Performs a paraxial (first-order) ray trace and cardinal analysis: effective
focal length, principal points, focal points, entrance/exit pupils, f/numbers
and (for finite conjugates) magnification.

```
rayweave paraxial [--config ID] [--glass-dir DIR] < system.yaml
```

## Options

| Flag | Description |
|---|---|
| `--config ID` | select a config by id (multi-config mode); defaults to `configs[0]` |
| `--glass-dir DIR` | AGF glass catalog directory |

## Input

Standard system YAML (`glass_catalog` + `configs[].surfaces`). Optional:

```yaml
paraxial:
  object_height: 10.0        # object height in mm (0 = infinite conjugate)
```

When piped after `rayweave chief`, the `chief_rays` section is used for the
entrance-pupil location and the field-of-view (half angle of view).

## Output

Augmented YAML with a `paraxial_result:` section:

| Field | Meaning |
|---|---|
| `focal_length` | effective focal length (mm) |
| `image_space_f/#` | working f-number |
| `inf_conj_image_space_f/#` | infinite-conjugate f-number |
| `entrance_pupil_diameter` / `entrance_pupil_location` | mm / mm from first surface |
| `exit_pupil_diameter` / `exit_pupil_location` | mm / mm from last surface |
| `magnification` / `minification` | lateral mag / 1\|mag\| (finite conjugate) |
| `half_angle_of_view` | degrees (from chief rays, if present) |
| `total_track` | mm, first to last surface (image plane) |
| `first_focal_length` / `second_focal_length` | front / rear focal lengths |
| `first_principal_focus` | mm from first surface |
| `first_principal_point` | mm from first surface |
| `second_principal_focus` | BFL in mm from last surface |
| `second_principal_point` | mm from last surface |

## Examples

```sh
rayweave paraxial < samples/us2645157.yaml
rayweave chief < lens.yaml | rayweave paraxial

# Read one quantity with query
rayweave paraxial < lens.yaml | rayweave query -r paraxial_result.focal_length
```

## Method

The first-order theory and the ray-transfer/power-matrix approach used here are
described in [methods/paraxial.md](methods/paraxial.md).
