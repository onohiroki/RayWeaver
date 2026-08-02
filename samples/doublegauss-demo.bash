#!/bin/bash
set -euo pipefail

# =============================================================================
# doublegauss-demo.bash — DLS design of a 6-element 50 mm f/2.8 double-Gauss
#
# Purpose: optimise a realistic standard lens (36 variables including glass
# nd/vd) from a synthesised starting point and report the key performance
# figures — EFL, f-number, spot RMS and distortion — before and after.
#
# Steps
#   1. paraxial           : EFL / f# before
#   2. chief              : spot RMS + distortion per field before
#   3. optimize --verbose : DLS (256 rays, 500 iterations)
#   4. paraxial + chief   : the same figures after
#   5. plot               : raytrace diagrams before/after
#
# How to read the result
#   - Spot RMS per field is the geometric spot radius; on-axis < 0.1 mm gate.
#   - Distortion is the deviation of the chief image height from f*tan(theta);
#     negative = barrel, positive = pincushion. A little distortion is the
#     normal trade-off for a fast standard lens.
#   - EFL/f# staying near 50 / 2.8 confirms the constraints held.
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

YAML="$SCRIPT_DIR/doublegauss-init.yaml"
OUTDIR="$SCRIPT_DIR"
OPT_RESULT="$OUTDIR/doublegauss-result.yaml"
OPT_LOG="$OUTDIR/doublegauss-log.jsonl"
RESULT_FILE="$OUTDIR/doublegauss-demo-result.txt"

# Clean-only mode
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$OPT_RESULT" "$OPT_LOG" "$RESULT_FILE"
  rm -f "$OUTDIR"/doublegauss-init.png "$OUTDIR"/doublegauss-opt.png
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

# ── Interpretation notes: appended to the result file on exit, so they stay
# as the closing section even when a gate check exits early. ──
append_interpretation() {
cat >> "$RESULT_FILE" <<'EOF'

=== How to interpret this result ===
- EFL (mm) and f/#: DLS keeps them near the design targets (50 mm, f/2.8).
  f/# = EFL / entrance-pupil diameter, so it confirms the aperture held.
- Spot RMS (mm): geometric RMS spot radius per field (0/10/16/23 deg).
  On-axis should drop below 0.1 mm; the off-axis fields improve too.
- Distortion (%): deviation of the chief-ray image height from the paraxial
  value f*tan(theta). Negative = barrel (image smaller than paraxial),
  positive = pincushion. A fast standard lens normally trades a little
  distortion for better spot sizes.
- Pass gate: on-axis RMS < 0.1 mm.
EOF
}
trap append_interpretation EXIT

echo "=== Double-Gauss optimization demo: 6-element 50 mm f/2.8 standard lens ==="
echo
echo "Optical system:"
echo "  6-element symmetric double-Gauss (front: crown/flint/meniscus |"
echo "  stop | meniscus/flint/crown). Total 14 surfaces."
echo "  Fields: 0 deg / 10 deg / 16 deg / 23 deg (35 mm format half-diagonal)"
echo "  Wavelengths: F (486nm) / d (588nm) / C (656nm)"
echo "  36 variables: curvatures, thicknesses, glass nd/vd"
echo "  Constraints: abs_efl band 50±0.5 mm, EPD band 17.86±0.3 mm"
echo "  Merit: spot RMS (12 terms) + lateral colour + OPD RMS"
echo "  Target threshold: on-axis RMS < 0.1 mm"
echo

# ── Evaluate before state ──
echo "--- Initial state ---"
EFL_BEFORE=$( $RAYWEAVE paraxial < "$YAML" 2>/dev/null | $RAYWEAVE query -r paraxial_result.focal_length )
FNO_BEFORE=$( $RAYWEAVE paraxial < "$YAML" 2>/dev/null | $RAYWEAVE query -r paraxial_result.image_space_f_number )
BEFORE_CHIEF=$( $RAYWEAVE chief < "$YAML" 2>/dev/null )

rms_field() {
  local chief="$1" fi="$2"
  echo "$chief" | $RAYWEAVE query -r "chief_rays[$fi].spot_stats.rms_r"
}
distortion() {
  local chief="$1" fi="$2" efl="$3"
  echo "$chief" | $RAYWEAVE query \
    --set a="chief_rays[$fi].field_angle" \
    --set ih="chief_rays[$fi].image_height[1]" \
    --set efl="$efl" \
    --expr 'a < 1e-9 ? 0 : 100*(ih-efl*tan(radians(a)))/(efl*tan(radians(a)))'
}

for fi in 0 1 2 3; do
  ang=$(echo "$BEFORE_CHIEF" | $RAYWEAVE query --default '?' -r "chief_rays[$fi].field_angle")
  printf "  field %s°  RMS = %.4f mm\n" "$ang" "$(rms_field "$BEFORE_CHIEF" "$fi")"
done
printf "  EFL = %.2f mm   f/%.2f\n" "$EFL_BEFORE" "$FNO_BEFORE"
echo

# ── DLS optimization ──
echo "=== DLS optimization (256 rays, 500 iterations) ==="
$RAYWEAVE optimize --verbose --log "$OPT_LOG" < "$YAML" > "$OPT_RESULT"
echo "  Written: $OPT_RESULT"
echo

# ── Evaluate after state ──
echo "--- Optimized state ---"
EFL_AFTER=$( $RAYWEAVE paraxial < "$OPT_RESULT" 2>/dev/null | $RAYWEAVE query -r paraxial_result.focal_length )
FNO_AFTER=$( $RAYWEAVE paraxial < "$OPT_RESULT" 2>/dev/null | $RAYWEAVE query -r paraxial_result.image_space_f_number )
AFTER_CHIEF=$( $RAYWEAVE chief < "$OPT_RESULT" 2>/dev/null )

for fi in 0 1 2 3; do
  ang=$(echo "$AFTER_CHIEF" | $RAYWEAVE query --default '?' -r "chief_rays[$fi].field_angle")
  printf "  field %s°  RMS = %.4f mm\n" "$ang" "$(rms_field "$AFTER_CHIEF" "$fi")"
done
printf "  EFL = %.2f mm   f/%.2f\n" "$EFL_AFTER" "$FNO_AFTER"
echo

# ── Stage summary from log ──
echo "--- Stage summary ---"
{
  status=$( $RAYWEAVE query --jsonl --where 'has("status")' -r status < "$OPT_LOG" 2>/dev/null )
  merit_first=$( $RAYWEAVE query --jsonl --where 'has("merit")' --first -r merit < "$OPT_LOG" 2>/dev/null )
  merit_last=$( $RAYWEAVE query --jsonl --where 'has("merit")' -r merit < "$OPT_LOG" 2>/dev/null )
  merit_count=$( $RAYWEAVE query --jsonl --where 'has("merit")' --count '[]' < "$OPT_LOG" 2>/dev/null )
  improvement=$( $RAYWEAVE query --set f="$merit_first" --set l="$merit_last" \
      --expr 'f > 0 ? (1-l/f)*100 : 0' < /dev/null )
  echo "  Status:      ${status:-unknown}"
  echo "  Iterations:  ${merit_count:-0}"
  printf "  Merit init:  %.6e\n" "$merit_first"
  printf "  Merit final: %.6e\n" "$merit_last"
  printf "  Improvement: %.1f%%\n" "$improvement"
}
echo

# ── Result file ──
{
  echo "=== Double-Gauss 6-element f/2.8 — optimization result ==="
  echo
  echo "--- Lens parameters ---"
  printf "  %-12s %12s %12s\n" "Quantity" "before" "after"
  printf "  %-12s %12s %12s\n" "---------" "-------" "------"
  printf "  %-12s %12.3f %12.3f\n" "EFL (mm)" "$EFL_BEFORE" "$EFL_AFTER"
  printf "  %-12s %12.3f %12.3f\n" "f/#" "$FNO_BEFORE" "$FNO_AFTER"
  echo
  echo "--- Spot RMS (d=587.6nm, chief evaluation) ---"
  printf "  %-6s %6s  %10s  %10s\n" "Field" "Angle" "before" "after"
  printf "  %-6s %6s  %10s  %10s\n" "-----" "-----" "--------" "-------"
  for fi in 0 1 2 3; do
    ang=$(echo "$BEFORE_CHIEF" | $RAYWEAVE query --default '?' -r "chief_rays[$fi].field_angle")
    bef=$(rms_field "$BEFORE_CHIEF" "$fi")
    aft=$(rms_field "$AFTER_CHIEF" "$fi")
    printf "  %-6s %5s°  %10.4f  %10.4f\n" "f$fi" "$ang" "$bef" "$aft"
  done
  echo
  echo "--- Distortion (d=587.6nm, chief vs paraxial image height) ---"
  printf "  %-6s %6s  %10s  %10s\n" "Field" "Angle" "before" "after"
  printf "  %-6s %6s  %10s  %10s\n" "-----" "-----" "--------" "-------"
  for fi in 0 1 2 3; do
    ang=$(echo "$BEFORE_CHIEF" | $RAYWEAVE query --default '?' -r "chief_rays[$fi].field_angle")
    d1=$(distortion "$BEFORE_CHIEF" "$fi" "$EFL_BEFORE")
    d2=$(distortion "$AFTER_CHIEF" "$fi" "$EFL_AFTER")
    printf "  %-6s %5s°  %9.2f%%  %9.2f%%\n" "f$fi" "$ang" "$d1" "$d2"
  done
  echo
} > "$RESULT_FILE"

# ── Console summary ──
echo "--- Lens parameters ---"
printf "  %-12s %12s %12s\n" "Quantity" "before" "after"
printf "  %-12s %12.3f %12.3f\n" "EFL (mm)" "$EFL_BEFORE" "$EFL_AFTER"
printf "  %-12s %12.3f %12.3f\n" "f/#" "$FNO_BEFORE" "$FNO_AFTER"
echo
echo "  (full spot RMS and distortion tables in $RESULT_FILE)"

# ── Merit breakdown ──
if [ -f "$OPT_LOG" ]; then
  echo "--- Merit breakdown (final state) ---"
  $RAYWEAVE query --jsonl --where 'event=="breakdown"' \
    --each 'terms:key,value' --printf '  %s: %.6e' < "$OPT_LOG" 2>/dev/null || true
  echo
fi

# ── Diagrams ──
echo "=== Diagrams ==="
$RAYWEAVE chief --clear-aperture --shrink --ray-fan < "$YAML" \
  | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/doublegauss-init.png" >/dev/null 2>&1 || true
echo "Written: $OUTDIR/doublegauss-init.png"

$RAYWEAVE chief --clear-aperture --shrink --ray-fan < "$OPT_RESULT" \
  | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/doublegauss-opt.png" >/dev/null 2>&1 || true
echo "Written: $OUTDIR/doublegauss-opt.png"
echo

# ── Threshold check: on-axis RMS < 0.1 mm ──
THRESHOLD=0.1
printf "  (threshold = %.1f mm — on-axis RMS must be below this)\n" "$THRESHOLD"
rms_onaxis=$(rms_field "$AFTER_CHIEF" 0)
if [ "$rms_onaxis" != "-1" ] && $RAYWEAVE query --gate "rms >= $THRESHOLD" --set rms="$rms_onaxis" < /dev/null > /dev/null; then
  msg="  >>> Optimization failed: on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm >= $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
  exit 1
else
  msg="  >>> Optimization passed: on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm < $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
fi

echo
echo "=== Iteration log saved: $OPT_LOG ==="
echo "=== Results saved: $RESULT_FILE ==="
