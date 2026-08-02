#!/bin/bash
set -euo pipefail

# =============================================================================
# ghost-demo.bash — ghost-ray tracing (Ono et al. surface-sequence encoding)
#
# Purpose: trace a double-reflection ghost path through the optimised
# double-Gauss and quantify how much fainter it is than the normal image.
# Each ray carries an ordered surface-ID list; a direction reversal in the
# list means reflection, so [0,1,2,3,4,3,2,3,4,...,14] reflects at surface 4,
# refracts backwards through surface 3, reflects at surface 2, then returns
# forward to the image (surface 14).
#
# Steps
#   1. chief --clear-aperture --shrink --preserve-rays : re-size lens
#      diameters to the beam while keeping the ghost/normal rays
#   2. trace                           : trace the ghost + normal reference rays
#   3. query                           : per-surface table + Fresnel intensities
#   4. plot                            : SVG diagram of the ghost path
#
# How to read the result
#   - Ghost relative intensity = product of the two Fresnel reflectances
#     (~0.004): a double reflection is ~250x fainter than the image.
#   - The ghost lands near the on-axis image point (the double reflection
#     keeps it close to the axis), so it appears as a faint halo.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD
# (repo root, `cd samples`, or a copied location). All data files are read
# from and all outputs are written to this directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# CLI options
CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

YAML="$SCRIPT_DIR/doublegauss-ghost.yaml"
OUTDIR="$SCRIPT_DIR"
TRACE_RESULT="$OUTDIR/doublegauss-ghost-trace-result.yaml"
RESULT_FILE="$OUTDIR/ghost-demo-result.txt"
SVG="$OUTDIR/doublegauss-ghost.svg"

# Clean-only mode
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$TRACE_RESULT" "$RESULT_FILE" "$SVG"
  echo "  Removed generated files"
  exit 0
fi

# Locate the rayweave binary: an explicit RAYWEAVE env value wins, then a
# binary next to the script or one directory up (samples/ -> repo root),
# then any rayweave on PATH.
if [[ -z "${RAYWEAVE:-}" ]]; then
  for cand in "$SCRIPT_DIR/rayweave" "$SCRIPT_DIR/../rayweave"; do
    if [[ -x "$cand" ]]; then RAYWEAVE="$cand"; break; fi
  done
  RAYWEAVE="${RAYWEAVE:-$(command -v rayweave || true)}"
  if [[ -z "${RAYWEAVE:-}" ]]; then
    echo "error: rayweave binary not found; set RAYWEAVE or put rayweave on PATH" >&2
    exit 1
  fi
fi

echo "=== Double-Gauss ghost ray tracing demo ==="
echo
echo "Optical system: 6-element symmetric double-Gauss (50 mm f/2.8), optimised."
echo "Ghost path: [0,1,2,3,4,3,2,3,4,...,14]"
echo "  - forward to surface 4, reflect there (pattern 3,4,3)"
echo "  - reversed refraction through surface 3 (backward pass)"
echo "  - reflect at surface 2 (pattern 3,2,3)"
echo "  - forward again to the image (surface 14)"
echo

# ── Re-adjust lens clear apertures to the beam footprint ──
# `chief --clear-aperture --shrink --preserve-rays` re-computes every
# surface's effective diameter from the traced grid beams (the aperture stop
# keeps its diameter), while keeping the ghost/normal rays for the trace.
echo "=== Re-adjusting lens effective diameters (chief --clear-aperture --shrink --preserve-rays) ==="
$RAYWEAVE chief --clear-aperture --shrink --clear-aperture-rays 1024 --preserve-rays < "$YAML" \
  | $RAYWEAVE trace > "$TRACE_RESULT"
echo "  Written: $TRACE_RESULT"
echo

# ── Adjusted diameters ──
echo "--- Effective diameters after adjustment ---"
while read -r sid before_d; do
  after_d=$( $RAYWEAVE query --default 0 -r "configs[0].surfaces[id=$sid].diameter" < "$TRACE_RESULT" )
  if $RAYWEAVE query --gate "abs(a-b) > 1e-6" --set a="$after_d" --set b="$before_d" < /dev/null > /dev/null; then
    printf "  s%-2d  %6.2f -> %6.2f mm\n" "$sid" "$before_d" "$after_d"
  fi
done < <( $RAYWEAVE query --each 'configs[0].surfaces[]:id,diameter' --default 0 --printf '%d %f' < "$YAML" )
echo

# ── Per-surface table and ghost intensity ──
{
  for ridx in 0 1; do
    id=$( $RAYWEAVE query -r "results[$ridx].id" < "$TRACE_RESULT" )
    path=$( $RAYWEAVE query -r "rays.rays[$ridx].path" < "$TRACE_RESULT" )
    path="${path// /, }"
    echo "ray: $id"
    echo "  path: $path"
    printf "  %-3s %-9s %12s %12s %12s %10s %10s\n" \
      "surf" "interact" "y (mm)" "z (mm)" "thick" "Is" "Ip"
    $RAYWEAVE query --each "results[$ridx].surfaces[]:surface_id,interaction,position[1],position[2],thickness,intensity_s,intensity_p" \
        --printf '%d %s %f %f %f %f %f' < "$TRACE_RESULT" \
      | while read -r sid inter y z thk is ip; do
          marker=""
          [ "$inter" = "REFLECT" ] && marker="  <-- ghost reflection"
          printf "  %-3s %-9s %12.4f %12.4f %12.4f %10.4f %10.4f%s\n" \
            "$sid" "$inter" "$y" "$z" "$thk" "$is" "$ip" "$marker"
        done
    is_refl=$( $RAYWEAVE query --product "results[$ridx].surfaces[interaction=REFLECT].intensity_s" --printf '%.3e' < "$TRACE_RESULT" )
    ip_refl=$( $RAYWEAVE query --product "results[$ridx].surfaces[interaction=REFLECT].intensity_p" --printf '%.3e' < "$TRACE_RESULT" )
    cum_s=$( $RAYWEAVE query --product "results[$ridx].surfaces[].intensity_s" --printf '%.4e' < "$TRACE_RESULT" )
    cum_p=$( $RAYWEAVE query --product "results[$ridx].surfaces[].intensity_p" --printf '%.4e' < "$TRACE_RESULT" )
    case "$id" in
      ghost*)
        echo "  ghost relative intensity (product of Fresnel reflectances):"
        echo "    Is = $is_refl   Ip = $ip_refl"
        ;;
    esac
    echo "  cumulative intensity (all surfaces): Is = $cum_s  Ip = $cum_p"
    echo
  done
  echo
  echo "=== How to interpret this result ==="
  echo "- Two rays through the re-sized double-Gauss:"
  echo "  - ghost_reflect_s4_s2: forward to surface 4, reflect there, reversed"
  echo "    refraction through surface 3, reflect at surface 2, then forward to"
  echo "    the image (surface 14)."
  echo "  - normal_reference: the same field's normal refraction-only ray."
  echo "- The two REFLECT rows are the ghost reflections; every other surface is"
  echo "  a Fresnel transmission (Is/Ip ~ 0.94)."
  echo "- Ghost relative intensity (Is/Ip) is the product of the two Fresnel"
  echo "  reflectances (~0.004): the ghost is a few hundredths of one percent of"
  echo "  the normal image brightness."
  echo "- Cumulative intensity: transmitted light left after all surfaces"
  echo "  (~0.48 for the normal ray vs ~0.0015 for the ghost)."
} > "$RESULT_FILE"
echo "Written: $RESULT_FILE"
echo

# ── Console summary ──
echo "--- Console summary ---"
for ridx in 0 1; do
  id=$( $RAYWEAVE query -r "results[$ridx].id" < "$TRACE_RESULT" )
  case "$id" in
    ghost*)
      err=$( $RAYWEAVE query --default '' -r "results[$ridx].error" < "$TRACE_RESULT" )
      if [ -n "$err" ]; then
        echo "  ghost ray: error: $err"
      else
        echo "  ghost ray: traced OK"
      fi
      $RAYWEAVE query --each "results[$ridx].surfaces[interaction=REFLECT]:surface_id,intensity_s,intensity_p" \
          --printf '%d %f %f' < "$TRACE_RESULT" \
        | while read -r sid is ip; do
            printf "    reflect at surface %-2d  Is = %.4f  Ip = %.4f\n" "$sid" "$is" "$ip"
          done
      last_sid=$( $RAYWEAVE query -r "results[$ridx].surfaces[-1].surface_id" < "$TRACE_RESULT" )
      last_y=$( $RAYWEAVE query -r "results[$ridx].surfaces[-1].position[1]" < "$TRACE_RESULT" )
      last_z=$( $RAYWEAVE query -r "results[$ridx].surfaces[-1].position[2]" < "$TRACE_RESULT" )
      printf "    final hit: surface %d at (y, z) = (%.2f, %.2f) mm\n" "$last_sid" "$last_y" "$last_z"
      ;;
  esac
done
echo

# ── Diagram ──
echo "=== Diagram ==="
$RAYWEAVE plot -o "$SVG" < "$TRACE_RESULT" >/dev/null 2>&1
echo "Written: $SVG"
echo
echo "=== Done. See $RESULT_FILE for the full trace table ==="
