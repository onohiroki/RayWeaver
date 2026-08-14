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
#   1. optimize spot_rms : DLS with the spot_rms-only merit (degraded start)
#                          -> optimize-demo-spotrms.yaml
#   2. optimize off-axis : DLS with the off-axis spot merit kinds
#                          -> optimize-demo-offaxis.yaml
#   3. vignette      : settle auto_aperture diameters + per-field vignetting
#                      on the off-axis optimum
#                      -> optimize-demo-vignette.yaml
#   4. chief         : re-evaluate the RMS spot radius per field, before /
#                      spot_rms / off-axis / vignette, and write
#                      optimize-demo-result.txt
#   5. breakdown     : per-kind final values of the off-axis merit, extracted
#                      from the optimize --log breakdown event
#
# How to read the result
#   - RMS before/spot_rms/off-axis/vignette is the geometric RMS spot radius
#     (mm) per field, measured by `chief` on the degraded start, the
#     spot_rms-only optimum, the off-axis optimum and the off-axis optimum
#     after `vignette --iterations 3 --min-glass-path 0.5` (identical
#     pupil-grid sampling, so apples-to-apples).
#   - The off-axis merit replaces spot_rms on the 16° field with
#     spot_rms_t/s/worst and adds spot_rms_worst/weighted/ee_radius on the
#     24° field, so the off-axis fields are corrected against coma/astigmatism
#     and energy concentration, not just the rotationally-symmetric RMS.
#   - Pass gate: on-axis (f0) RMS < 0.3 mm after optimisation (checked on the
#     vignetted lens).
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD
# (repo root, `cd samples`, or a copied location). All data files are read
# from and all outputs are written to this directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

OFFAX_YAML="$SCRIPT_DIR/us2645157-degraded.yaml"
SPOTRMS_YAML="$SCRIPT_DIR/us2645157-degraded-spotrms.yaml"
OUTDIR="$SCRIPT_DIR"
OPT_SPOTRMS="$OUTDIR/optimize-demo-spotrms.yaml"
OPT_OFFAX="$OUTDIR/optimize-demo-offaxis.yaml"
LOG_SPOTRMS="$OUTDIR/optimize-demo-spotrms.log"
LOG_OFFAX="$OUTDIR/optimize-demo-offaxis.log"
VIGN_YAML="$OUTDIR/optimize-demo-vignette.yaml"
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
  rm -f "$OPT_SPOTRMS" "$OPT_OFFAX" "$VIGN_YAML" "$LOG_SPOTRMS" "$LOG_OFFAX" "$RESULT_FILE"
  rm -f "$OUTDIR/optimize-demo-init.png" "$OUTDIR/optimize-demo-offaxis.png" "$OUTDIR/optimize-demo-vignette.png"
  # legacy artifact names from the pre-rename demo (old/new)
  rm -f "$OUTDIR/optimize-demo-old.yaml" "$OUTDIR/optimize-demo-old.log" "$OUTDIR/optimize-demo-old.png"
  rm -f "$OUTDIR/optimize-demo-new.yaml" "$OUTDIR/optimize-demo-new.log" "$OUTDIR/optimize-demo-new.png"
  rm -f "$OUTDIR/optimize-demo-result.yaml" "$OUTDIR/optimize-demo-opt.png"
  echo "  Removed: PNGs, spotrms/offaxis/vignette YAML+logs, $RESULT_FILE"
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
- "RMS before / spot_rms / off-axis / vignette" is the geometric RMS spot
  radius (mm) of the chief-ray pupil grid: before optimisation, after the
  spot_rms-only merit (us2645157-degraded-spotrms.yaml), after the off-axis
  merit (us2645157-degraded.yaml), and after the off-axis optimum is passed
  through `vignette --iterations 3 --min-glass-path 0.5`
  (optimize-demo-vignette.yaml). Same pupil-grid sampling for every phase.
- The vignette step re-sets auto_aperture surface diameters to the beam
  envelope and settles the per-field vignetting (glass-path edge-thickness
  >= 0.5 mm). It leaves the curvatures untouched, so the vignette column
  matches the off-axis column on-axis; off-axis the per-field vignetting
  factors clip the outer pupil rays, so a strongly vignetted field (here
  24°: vig=0.741) shows a markedly smaller RMS on the surviving bundle —
  that is the beam the sensor actually sees.
- f0/f1/f2 = 0/16/24 deg fields. The 16° field is corrected against
  tangential/sagittal (spot_rms_t/s) and its worst axis (spot_rms_worst); the
  24° field adds energy-concentration terms (spot_rms_weighted, spot_ee_radius).
- "rms->off" is (spot_rms - off-axis)/spot_rms: a positive percentage is an
  off-axis improvement from the off-axis merit. The ✓ marks an improvement
  over the degraded start. Note that trading merit weight toward the off-axis
  fields can slightly worsen the on-axis field (here f0 0.0019 -> 0.0029 mm,
  still two orders of magnitude below the 0.3 mm gate); tune the term weights
  if you need to hold f0 tighter.
- The "off-axis-kind final values" section is the per-term breakdown of the
  off-axis merit at the optimum (from optimize --log): value = sqrt(contribution /
  (weight * fieldWeight * wavWeight)), target 0.
- Pass gate: on-axis (f0) RMS < 0.3 mm after optimisation, checked on the
  vignetted lens (optimize-demo-vignette.yaml).
- If "Optimization failed" appears, DLS did not converge; try more
  iterations or relax the merit weights in the YAML.
EOF
}
trap append_interpretation EXIT

echo "=== Optimize demo: degraded US2645157 triplet, spot_rms-only vs off-axis merit ==="
echo

echo "--- Initial state (degraded curvatures) ---"
echo "=== DLS optimisation (spot_rms-only merit) ==="
$RAYWEAVE optimize --verbose --log "$LOG_SPOTRMS" < "$SPOTRMS_YAML" > "$OPT_SPOTRMS"
echo
echo "=== DLS optimisation (off-axis spot merit kinds) ==="
$RAYWEAVE optimize --verbose --log "$LOG_OFFAX" < "$OFFAX_YAML" > "$OPT_OFFAX"
echo
echo "=== Vignette: settle auto_aperture diameters + per-field vignetting ==="
$RAYWEAVE vignette --iterations 3 --min-glass-path 0.5 < "$OPT_OFFAX" > "$VIGN_YAML"
echo "Written: $VIGN_YAML"
echo "  auto_aperture diameter changes (before -> after):"
$RAYWEAVE query --each 'vignetting_result.diameters[]:surface_id,before,after' \
  --printf '    s%-2d  %8.4f -> %8.4f' < "$VIGN_YAML"
echo "  per-field vignetting (1.0 = no vignetting):"
$RAYWEAVE query --each 'vignetting_result.fields[]:field_index,angle_deg,vignetting' \
  --printf '    f%d  %5.1f deg  vig=%5.3f' < "$VIGN_YAML"
echo
echo "--- PNG diagrams ---"
echo "=== Initial diagram ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$OFFAX_YAML" | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/optimize-demo-init.png" >/dev/null
echo "Written: $OUTDIR/optimize-demo-init.png"
echo

echo "=== Optimized diagram (off-axis merit + vignette) ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$VIGN_YAML" | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/optimize-demo-vignette.png" >/dev/null
echo "Written: $OUTDIR/optimize-demo-vignette.png"
echo

# ── Spot RMS comparison (chief-measured, same sampling for both optima) ──
rms_field() {
  local yaml_file=$1 fi=$2
  $RAYWEAVE chief < "$yaml_file" 2>/dev/null \
    | $RAYWEAVE query -r "chief_rays[$fi].spot_stats.rms_r"
}
{
  echo "=== Spot RMS Comparison (chief measurement, mm) ==="
  printf "  %-8s %6s  %10s  %10s  %10s  %10s  %10s\n" "Phase" "Field" "before" "spot_rms" "off-axis" "vignette" "rms->off"
  printf "  %-8s %6s  %10s  %10s  %10s  %10s  %10s\n" "-----" "-----" "--------" "--------" "--------" "--------" "---------"
  for fi in 0 1 2; do
    rms_before=$(rms_field "$OFFAX_YAML" "$fi")
    rms_spotrms=$(rms_field "$OPT_SPOTRMS" "$fi")
    rms_offax=$(rms_field "$OPT_OFFAX" "$fi")
    rms_vignette=$(rms_field "$VIGN_YAML" "$fi")
    delta=""
    if [ "$rms_spotrms" != "-1" ] && [ "$rms_offax" != "-1" ]; then
      delta=$(printf "%.1f%%" "$(echo "scale=3; ($rms_spotrms-$rms_offax)/$rms_spotrms*100" | bc 2>/dev/null || echo 0)")
    fi
    mark=""
    if [ "$rms_before" != "-1" ] && [ "$rms_offax" != "-1" ]; then
      if $RAYWEAVE query --gate "a < b" --set a="$rms_offax" --set b="$rms_before" < /dev/null > /dev/null; then
        mark="   ✓"
      else
        mark="   ✗"
      fi
    fi
    printf "  %-8s %6s  %10.4f  %10.4f  %10.4f  %10.4f  %10s%s\n" "optimize" "f$fi" "$rms_before" "$rms_spotrms" "$rms_offax" "$rms_vignette" "$delta" "$mark"
  done
  echo
} | tee "$RESULT_FILE"

# ── Off-axis-metric breakdown of the off-axis merit's optimum ──
# contribution = weight * fieldWeight * wavWeight * (value - target)^2 (target 0),
# so value = sqrt(contribution / (weight*fieldWeight*wavWeight)).
# fieldWeight: f0/f1 = 1.0, f2 = 0.5 ; wavWeight = 1.0 ; term weights from YAML.
breakdown_value() {
  local kind=$1 angle=$2 wl=$3 contrib line pat
  # breakdown keys look like "config:0 spot_rms_worst(f16.0,0.000486)".
  pat=$(printf "%s(f%.1f,%.6f)" "$kind" "$angle" "$wl")
  line=$($RAYWEAVE query --jsonl --where 'event=="breakdown"' \
      --each 'terms:key,value' --printf '%s=%.6e' \
      < "$LOG_OFFAX" 2>/dev/null | grep -F "$pat" || true)
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
  echo "=== Off-axis-merit final values (value at the off-axis optimum, mm) ==="
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

# ── On-axis RMS threshold check (on the vignetted lens) ──
THRESHOLD=0.3
printf "  (threshold = $THRESHOLD mm — on-axis RMS must be below this; checked on the vignetted lens)\n"
  rms_onaxis=$(rms_field "$VIGN_YAML" 0)
if [ "$rms_onaxis" != "-1" ] && $RAYWEAVE query --gate "rms >= $THRESHOLD" --set rms="$rms_onaxis" < /dev/null > /dev/null; then
  msg="  >>> Optimization failed: on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm >= $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
  exit 1
else
  msg="  >>> Optimization passed: on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm < $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
fi
