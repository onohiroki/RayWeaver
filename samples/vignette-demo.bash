#!/bin/bash
set -euo pipefail

# =============================================================================
# vignette-demo.bash — dynamic pupil + iterative vignetting / diameter sizing
#
# Purpose: show the `vignette` subcommand on the double-Gauss start system
# (samples/vignette-demo.yaml, f/2.8 50 mm). The chief rays are found from the
# surviving-grid centroid; the entrance and exit pupils are derived per field
# from the chief-ray crossings (no explicit stop — chief.stop_surface is
# omitted). Vignetting comes from the fixed aperture (surface 7) and the
# glass-path (edge-thickness) check, and auto_aperture: true surfaces are
# re-sized to the surviving-beam envelope.
#
# Steps
#   1. chief        : dynamic-pupil chief (per-field entrance/exit pupil)
#   2. clear-aperture + plot : initial diagram
#   3. vignette     : 3 passes settling diameters + vignetting -> result.yaml
#   4. plot         : final diagram (chief + marginal rays)
# =============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTDIR="$SCRIPT_DIR"

YAML="$SCRIPT_DIR/vignette-demo.yaml"
PREFIX="vignette-demo-"
RESULT="$OUTDIR/${PREFIX}result.yaml"

CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$OUTDIR"/vignette-demo-chief.yaml "$OUTDIR"/vignette-demo-result.yaml
  rm -f "$OUTDIR"/vignette-demo-init.png "$OUTDIR"/vignette-demo-final.png
  echo "  Removed: vignette demo outputs"
  exit 0
fi

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

echo "=== Vignette demo: double-Gauss, dynamic pupil, iterative vignetting ==="
echo

echo "--- 1. Dynamic-pupil chief (chief.stop_surface omitted -> per-field pupils) ---"
$RAYWEAVE chief < "$YAML" > "$OUTDIR/${PREFIX}chief.yaml"
echo "  Written: $OUTDIR/${PREFIX}chief.yaml"
echo "  (per-field entrance/exit pupils are reported in vignetting_result below)"

echo "--- 2. Initial diagram (clear-aperture on auto_aperture surfaces) ---"
$RAYWEAVE chief --clear-aperture < "$YAML" | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/${PREFIX}init.png" >/dev/null
echo "  Written: $OUTDIR/${PREFIX}init.png"

echo "--- 3. Vignette: 3 passes settling diameters + vignetting ---"
$RAYWEAVE vignette --iterations 3 --min-glass-path 0.5 < "$YAML" > "$RESULT"
echo "  iterations     = $($RAYWEAVE query -r vignetting_result.iterations < "$RESULT")"
echo "  min_glass_path = $($RAYWEAVE query -r vignetting_result.min_glass_path < "$RESULT")"
echo "  Per-field vignetting / entrance pupil / pupil-plane envelope / marginals:"
$RAYWEAVE query --each \
  'vignetting_result.fields[]:field_index,angle_deg,vignetting,entrance_pupil_z,bound_lower,bound_upper,marginal_y_lower,marginal_y_upper' \
  --printf '    f%d  %5.1f deg  vig=%5.3f  epZ=%7.3f  bound=[%7.3f, %7.3f]  marginal=[%7.3f, %7.3f]' \
  < "$RESULT"
echo "  auto_aperture diameter changes:"
$RAYWEAVE query --each 'vignetting_result.diameters[]:surface_id,before,after' \
  --printf '    s%-2d  %8.4f -> %8.4f' < "$RESULT"
echo "  Written: $RESULT"

echo "--- 4. Final diagram (chief + marginal rays) ---"
$RAYWEAVE vignette --iterations 3 --min-glass-path 0.5 < "$YAML" | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/${PREFIX}final.png" >/dev/null
echo "  Written: $OUTDIR/${PREFIX}final.png"

cat >> "$RESULT" <<EOF

=== How to interpret this result ===
- vignetting_result.fields[].vignetting is the surviving fraction of each
  field's pupil grid (1.0 = no vignetting). The on-axis field is the reference
  envelope; wide fields are vignetted by the fixed aperture (s7) and by the
  glass-path (edge-thickness) check.
- entrance_pupil_z is the per-field dynamic entrance pupil derived from the
  chief-ray crossings (no physical stop surface). exit_pupil_z is available in
  the YAML (omitted when the outgoing-ray crossing is unreliable).
- bound_lower/bound_upper is field 0's marginal-ray envelope at the field's
  entrance pupil plane; marginal_y_lower/upper are this field's marginal rays
  there (within the bounds means the beam is not additionally vignetted).
- diameters[] list the auto_aperture: true surfaces before and after; fixed
  (auto_aperture: false) surfaces are never re-sized.
- Pipe the result into trace/plot to reproduce the final diagram.
EOF

echo
echo "=== Vignette demo complete ==="
