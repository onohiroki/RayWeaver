#!/bin/bash
set -euo pipefail

# =============================================================================
# optimize-demo.bash — DLS optimisation of a deliberately degraded triplet
#
# Purpose: show how `rayweave optimize` (damped least-squares) recovers a
# good lens from a broken starting point, and how the off-axis spot merit
# kinds (spot_rms_t/s/worst, spot_rms_weighted, spot_ee_radius) change the
# result compared with the plain spot_rms-only merit.
#
# Steps
#   1. optimize old  : DLS with the spot_rms-only merit (degraded start)
#                      -> optimize-demo-old.yaml
#   2. optimize new  : DLS with the off-axis spot merit kinds
#                      -> optimize-demo-new.yaml
#   3. chief         : re-evaluate the RMS spot radius per field, before /
#                      old / new, and write optimize-demo-result.txt
#   4. breakdown     : per-kind final values of the new merit, extracted from
#                      the optimize --log breakdown event
#
# How to read the result
#   - RMS before/old/new is the geometric RMS spot radius (mm) per field,
#     measured by `chief` on the degraded start, the old-merit optimum and the
#     new-merit optimum (identical pupil-grid sampling, so apples-to-apples).
#   - The new merit replaces spot_rms on the 16° field with spot_rms_t/s/worst
#     and adds spot_rms_worst/weighted/ee_radius on the 24° field, so the
#     off-axis fields are corrected against coma/astigmatism and energy
#     concentration, not just the rotationally-symmetric RMS.
#   - Pass gate: on-axis (f0) RMS < 0.3 mm after optimisation.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD
# (repo root, `cd samples`, or a copied location). All data files are read
# from and all outputs are written to this directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

NEW_YAML="$SCRIPT_DIR/us2645157-degraded.yaml"
OLD_YAML="$SCRIPT_DIR/us2645157-degraded-spotrms.yaml"
OUTDIR="$SCRIPT_DIR"
OPT_OLD="$OUTDIR/optimize-demo-old.yaml"
OPT_NEW="$OUTDIR/optimize-demo-new.yaml"
LOG_OLD="$OUTDIR/optimize-demo-old.log"
LOG_NEW="$OUTDIR/optimize-demo-new.log"
RESULT_FILE="$OUTDIR/optimize-demo-result.txt"

# CLI options
CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$OPT_OLD" "$OPT_NEW" "$LOG_OLD" "$LOG_NEW" "$RESULT_FILE"
  rm -f "$OUTDIR/optimize-demo-init.png" "$OUTDIR/optimize-demo-old.png" "$OUTDIR/optimize-demo-new.png"
  # legacy artifact names from the pre-comparison demo
  rm -f "$OUTDIR/optimize-demo-result.yaml" "$OUTDIR/optimize-demo-opt.png"
  echo "  Removed: PNGs, old/new YAML+logs, $RESULT_FILE"
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
- "RMS before / old / new" is the geometric RMS spot radius (mm) of the
  chief-ray pupil grid: before optimisation, after the spot_rms-only merit
  (us2645157-degraded-spotrms.yaml) and after the off-axis-merit
  (us2645157-degraded.yaml). Same pupil-grid sampling for both optima.
- f0/f1/f2 = 0/16/24 deg fields. The 16° field is corrected against
  tangential/sagittal (spot_rms_t/s) and its worst axis (spot_rms_worst); the
  24° field adds energy-concentration terms (spot_rms_weighted, spot_ee_radius).
- "old->new" is (old - new)/old: a positive percentage is an off-axis
  improvement from the new merit. The ✓ marks an improvement over the degraded
  start. Note that trading merit weight toward the off-axis fields can
  slightly worsen the on-axis field (here f0 0.0019 -> 0.0029 mm, still two
  orders of magnitude below the 0.3 mm gate); tune the term weights if you
  need to hold f0 tighter.
- The "new-kind final values" section is the per-term breakdown of the new
  merit at the optimum (from optimize --log): value = sqrt(contribution /
  (weight * fieldWeight * wavWeight)), target 0.
- Pass gate: on-axis (f0) RMS < 0.3 mm after optimisation.
- If "Optimization failed" appears, DLS did not converge; try more
  iterations or relax the merit weights in the YAML.
EOF
}
trap append_interpretation EXIT

echo "=== Optimize demo: degraded US2645157 triplet, old vs new merit ==="
echo

echo "--- Initial state (degraded curvatures) ---"
echo "=== DLS optimisation (spot_rms-only merit) ==="
$RAYWEAVE optimize --verbose --log "$LOG_OLD" < "$OLD_YAML" > "$OPT_OLD"
echo
echo "=== DLS optimisation (off-axis spot merit kinds) ==="
$RAYWEAVE optimize --verbose --log "$LOG_NEW" < "$NEW_YAML" > "$OPT_NEW"
echo

echo "--- PNG diagrams ---"
echo "=== Initial diagram ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$NEW_YAML" | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/optimize-demo-init.png" >/dev/null
echo "Written: $OUTDIR/optimize-demo-init.png"
echo

echo "=== Optimized diagram (old merit) ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$OPT_OLD" | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/optimize-demo-old.png" >/dev/null
echo "Written: $OUTDIR/optimize-demo-old.png"
echo

echo "=== Optimized diagram (new merit) ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$OPT_NEW" | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/optimize-demo-new.png" >/dev/null
echo "Written: $OUTDIR/optimize-demo-new.png"
echo

# ── Spot RMS comparison (chief-measured, same sampling for old/new) ──
rms_field() {
  local yaml_file=$1 fi=$2
  $RAYWEAVE chief < "$yaml_file" 2>/dev/null \
    | $RAYWEAVE query -r "chief_rays[$fi].spot_stats.rms_r"
}
{
  echo "=== Spot RMS Comparison (chief measurement, mm) ==="
  printf "  %-8s %6s  %10s  %10s  %10s  %10s\n" "Phase" "Field" "before" "old" "new" "old->new"
  printf "  %-8s %6s  %10s  %10s  %10s  %10s\n" "-----" "-----" "--------" "--------" "--------" "---------"
  for fi in 0 1 2; do
    rms_before=$(rms_field "$NEW_YAML" "$fi")
    rms_old=$(rms_field "$OPT_OLD" "$fi")
    rms_new=$(rms_field "$OPT_NEW" "$fi")
    delta=""
    if [ "$rms_old" != "-1" ] && [ "$rms_new" != "-1" ]; then
      delta=$(printf "%.1f%%" "$(echo "scale=3; ($rms_old-$rms_new)/$rms_old*100" | bc 2>/dev/null || echo 0)")
    fi
    mark=""
    if [ "$rms_before" != "-1" ] && [ "$rms_new" != "-1" ]; then
      if $RAYWEAVE query --gate "a < b" --set a="$rms_new" --set b="$rms_before" < /dev/null > /dev/null; then
        mark="   ✓"
      else
        mark="   ✗"
      fi
    fi
    printf "  %-8s %6s  %10.4f  %10.4f  %10.4f  %10s%s\n" "optimize" "f$fi" "$rms_before" "$rms_old" "$rms_new" "$delta" "$mark"
  done
  echo
} | tee "$RESULT_FILE"

# ── New-metric breakdown of the new merit's optimum ──
# contribution = weight * fieldWeight * wavWeight * (value - target)^2 (target 0),
# so value = sqrt(contribution / (weight*fieldWeight*wavWeight)).
# fieldWeight: f0/f1 = 1.0, f2 = 0.5 ; wavWeight = 1.0 ; term weights from YAML.
breakdown_value() {
  local kind=$1 angle=$2 wl=$3 contrib line pat
  # breakdown keys look like "config:0 spot_rms_worst(f16.0,0.000486)".
  pat=$(printf "%s(f%.1f,%.6f)" "$kind" "$angle" "$wl")
  line=$($RAYWEAVE query --jsonl --where 'event=="breakdown"' \
      --each 'terms:key,value' --printf '%s=%.6e' \
      < "$LOG_NEW" 2>/dev/null | grep -F "$pat" || true)
  if [ -z "$line" ]; then
    echo "n/a"
    return
  fi
  contrib=${line##*=}
  # weight products: field1 worst 1.0; field1 t/s 0.5; field2 worst 0.5;
  # field2 weighted/ee 0.3. fieldWeight 1.0 (f1) / 0.5 (f2).
  local wprod
  case "$angle:$kind" in
    16:spot_rms_worst) wprod=1.0 ;;
    16:spot_rms_t|16:spot_rms_s) wprod=0.5 ;;
    24:spot_rms_worst) wprod=0.25 ;;   # 0.5 * fieldWeight 0.5
    24:spot_rms_weighted|24:spot_ee_radius) wprod=0.15 ;;  # 0.3 * 0.5
  esac
  echo "scale=8; sqrt($contrib / $wprod)" | bc -l 2>/dev/null || echo "n/a"
}
{
  echo "=== New-merit final values (value at the new optimum, mm) ==="
  echo "  (from the optimize --log breakdown; value = sqrt(contrib/(w*fw*wlw)), target 0)"
  printf "  %-20s %6s  %12s  %12s  %12s\n" "kind" "field" "0.4861um" "0.5876um" "0.6563um"
  printf "  %-20s %6s  %12s  %12s  %12s\n" "----" "-----" "-------" "-------" "-------"
  for spec in "spot_rms_t:16" "spot_rms_s:16" "spot_rms_worst:16" "spot_rms_worst:24" "spot_rms_weighted:24" "spot_ee_radius:24"; do
    kind="${spec%%:*}"; angle="${spec##*:}"
    v1=$(breakdown_value "$kind" "$angle" 0.0004861)
    v2=$(breakdown_value "$kind" "$angle" 0.0005876)
    v3=$(breakdown_value "$kind" "$angle" 0.0006563)
    printf "  %-20s %6s  %12s  %12s  %12s\n" "$kind" "$angle" "$v1" "$v2" "$v3"
  done
  echo
} | tee -a "$RESULT_FILE"

# ── On-axis RMS threshold check ──
THRESHOLD=0.3
printf "  (threshold = $THRESHOLD mm — on-axis RMS must be below this)\n"
rms_onaxis=$(rms_field "$OPT_NEW" 0)
if [ "$rms_onaxis" != "-1" ] && $RAYWEAVE query --gate "rms >= $THRESHOLD" --set rms="$rms_onaxis" < /dev/null > /dev/null; then
  msg="  >>> Optimization failed: on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm >= $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
  exit 1
else
  msg="  >>> Optimization passed: on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm < $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
fi
