#!/bin/bash
set -euo pipefail

# =============================================================================
# vignette-demo.bash — dynamic pupil + iterative vignetting / diameter sizing
#
# Purpose: show the `vignette` subcommand on the OPTIMIZED double-Gauss with
# deliberately oversized effective diameters (samples/vignette-demo.yaml —
# f/2.8 50 mm, base = the DLS-optimized lens from doublegauss-result.yaml).
# Every auto_aperture surface is inflated to 36 mm, so the "before" state is
# clearly wasteful glass; because the base lens is optimized, the rays already
# come to a tight focus (on-axis spot RMS ~0.06 mm) and the marginal rays draw
# cleanly through the oversized glass. The chief rays are found from the
# surviving-grid centroid; the entrance and exit pupils are derived per field
# from the chief-ray crossings (no explicit stop — chief.stop_surface is
# omitted). Vignetting comes from the fixed aperture (surface 7) and the
# glass-path (edge-thickness) check, and auto_aperture: true surfaces are
# re-sized to the surviving-beam envelope.
#
# Steps
#   1. chief        : dynamic-pupil chief (per-field entrance/exit pupil)
#   2. marginal-rays + plot : initial diagram — marginal rays through the
#                             oversized (36 mm) lens, rays in focus
#   3. vignette     : 3 passes shrinking diameters to the beam envelope,
#                     settling per-field vignetting and applying the
#                     min_glass_path (edge-thickness >= 0.5 mm) constraint
#   4. plot         : final diagram (chief + marginal rays) — just-right
#                     effective apertures, edge thickness maintained
#   5. spot RMS     : before/after (vignette leaves curvatures untouched, so
#                     the rays stay focused)
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

echo "=== Vignette demo: optimized double-Gauss, dynamic pupil, iterative vignetting ==="
echo "  Lens: f/2.8 50 mm double-Gauss (DLS-optimized base), effective diameters"
echo "  deliberately inflated to 36 mm — the beam only fills the center of each element."
echo

echo "--- 1. Dynamic-pupil chief (chief.stop_surface omitted -> per-field pupils) ---"
$RAYWEAVE chief < "$YAML" > "$OUTDIR/${PREFIX}chief.yaml"
echo "  Written: $OUTDIR/${PREFIX}chief.yaml"
echo "  (per-field entrance/exit pupils are reported in vignetting_result below)"

echo "--- 2. Initial diagram (marginal rays through the oversized lens) ---"
$RAYWEAVE chief --marginal-rays < "$YAML" | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/${PREFIX}init.png" >/dev/null
echo "  Written: $OUTDIR/${PREFIX}init.png"
echo "  (rays are in focus; the 36 mm lens bodies are clearly oversized)"

echo "--- 3. Vignette: 3 passes settling diameters + vignetting ---"
$RAYWEAVE vignette --iterations 3 --min-glass-path 0.5 < "$YAML" > "$RESULT"
echo "  iterations     = $($RAYWEAVE query -r vignetting_result.iterations < "$RESULT")"
echo "  min_glass_path = $($RAYWEAVE query -r vignetting_result.min_glass_path < "$RESULT")"
echo "  Per-field vignetting / entrance pupil / pupil-plane envelope / marginals:"
$RAYWEAVE query --each \
  'vignetting_result.fields[]:field_index,angle_deg,vignetting,entrance_pupil_z,bound_lower,bound_upper,marginal_y_lower,marginal_y_upper' \
  --printf '    f%d  %5.1f deg  vig=%5.3f  epZ=%7.3f  bound=[%7.3f, %7.3f]  marginal=[%7.3f, %7.3f]' \
  < "$RESULT"
echo "  auto_aperture diameter changes (oversized -> beam envelope):"
$RAYWEAVE query --each 'vignetting_result.diameters[]:surface_id,before,after' \
  --printf '    s%-2d  %8.3f -> %8.3f' < "$RESULT"
echo "  Written: $RESULT"

echo "--- 4. Final diagram (chief + marginal rays) ---"
$RAYWEAVE vignette --iterations 3 --min-glass-path 0.5 < "$YAML" | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/${PREFIX}final.png" >/dev/null
echo "  Written: $OUTDIR/${PREFIX}final.png"
echo "  (effective diameters now fit the beam; edge thickness >= 0.5 mm enforced)"

echo "--- 5. Spot RMS before/after (vignette does not touch curvatures) ---"
BEFORE_CHIEF=$( $RAYWEAVE chief < "$YAML" 2>/dev/null )
AFTER_CHIEF=$( $RAYWEAVE chief < "$RESULT" 2>/dev/null )
for fi in 0 1 2 3; do
  ang=$(echo "$BEFORE_CHIEF" | $RAYWEAVE query --default '?' -r "chief_rays[$fi].field_angle")
  before=$(echo "$BEFORE_CHIEF" | $RAYWEAVE query --default '?' -r "chief_rays[$fi].spot_stats.rms_r")
  after=$(echo "$AFTER_CHIEF" | $RAYWEAVE query --default '?' -r "chief_rays[$fi].spot_stats.rms_r")
  printf "    f%d  %5.1f deg  RMS=%.4f -> %.4f mm\n" "$fi" "$ang" "$before" "$after"
done

cat >> "$RESULT" <<EOF

=== How to interpret this result ===
- The base lens is the DLS-optimized double-Gauss (on-axis spot RMS ~0.06 mm),
  so the rays come to a tight focus in both diagrams; vignette only re-sizes
  the effective diameters (curvatures untouched, spot RMS unchanged).
- vignetting_result.fields[].vignetting is the surviving fraction of each
  field's pupil grid (1.0 = no vignetting). The on-axis field is the reference
  envelope; wide fields are vignetted by the fixed aperture (s7) and by the
  glass-path (edge-thickness) check.
- diameters[] list the auto_aperture: true surfaces before and after: every
  one starts at the deliberate 36.0 mm (oversized / wasteful glass) and is
  shrunk to 2x its surviving-beam extent + margin (~16 .. 25 mm). Fixed
  (auto_aperture: false) surfaces are never re-sized.
- min_glass_path: 0.5 is applied to every glass element's entry surface; rays
  that would bring the edge thickness below it are vignetted, so the "after"
  lens is manufacturable (proper koba / edge thickness).
- entrance_pupil_z is the per-field dynamic entrance pupil derived from the
  chief-ray crossings (no physical stop surface). exit_pupil_z is available in
  the YAML (omitted when the outgoing-ray crossing is unreliable).
- bound_lower/bound_upper is field 0's marginal-ray envelope at the field's
  entrance pupil plane; marginal_y_lower/upper are this field's marginal rays
  there (within the bounds means the beam is not additionally vignetted).
- Pipe the result into trace/plot to reproduce the final diagram.
EOF

echo
echo "=== Vignette demo complete ==="
