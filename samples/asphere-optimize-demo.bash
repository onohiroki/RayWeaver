#!/bin/bash
set -euo pipefail

# =============================================================================
# asphere-optimize-demo.bash — aspheric-surface optimisation (two stages)
#
# Purpose: compare a spherical-only optimisation with one that also varies the
# asphere coefficients (conic, a4, a6) of the front surface of a singlet. The
# front (aspheric) surface is the explicit aperture stop (chief.stop_surface: 1)
# and the chief ray of every field passes through its centre
# (chief.pass_through.surface: 1), so the pupil sits at the first surface.
#
# Steps
#   1. Stage 1 (spherical): optimize curvatures only (asphere vars excluded
#      via --exclude-param)
#   2. Stage 2 (asphere):   additionally optimize conic/a4/a6
#   3. chief                : spot RMS and OPD RMS per field for before /
#                             spherical / asphere
#
# How to read the result
#   - The coefficient table (result.txt) shows the surface-1 shape parameters
#     curvature / conic / a4 / a6 before, after the spherical stage and after
#     the asphere stage, plus a delta (asphere-opt minus initial) column.
#   - Spot RMS (mm) and OPD RMS (mm) should both drop before -> asphere.
#   - OPD RMS ~ 1e-3 mm is near the diffraction limit at 587.6 nm.
#   - Gate: asphere-opt on-axis RMS < 0.3 mm.
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

YAML="$SCRIPT_DIR/asphere-optimize.yaml"
OUTDIR="$SCRIPT_DIR"
SPH_RESULT="$OUTDIR/asphere-optimize-spherical-result.yaml"
SPH_LOG="$OUTDIR/asphere-optimize-spherical-log.jsonl"
ASP_RESULT="$OUTDIR/asphere-optimize-result.yaml"
ASP_LOG="$OUTDIR/asphere-optimize-log.jsonl"
RESULT_FILE="$OUTDIR/asphere-optimize-demo-result.txt"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$SPH_RESULT" "$SPH_LOG" "$ASP_RESULT" "$ASP_LOG" "$RESULT_FILE"
  rm -f "$OUTDIR"/asphere-optimize-init.png "$OUTDIR"/asphere-optimize-spherical.png "$OUTDIR"/asphere-optimize-opt.png
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
- Two optimisation stages on a singlet with an aspheric front surface:
  spherical-opt varies only curvatures; asphere-opt also varies conic/a4/a6.
  The front (aspheric) surface is the explicit aperture stop
  (chief.stop_surface: 1) and the chief ray passes through its centre
  (chief.pass_through.surface: 1), so the pupil sits at the first surface.
- Coef table: the asphere coefficients stay 0 in the spherical stage (they
  are not variables) and pick up small non-zero values in the asphere stage;
  the "delta (asp-before)" column is the asphere-opt value minus the initial
  value of each surface-1 shape parameter (curv / conic / a4 / a6).
- Spot RMS (mm): geometric RMS spot radius; the asphere stage reaches the
  smallest on-axis value.
- OPD RMS (mm): RMS of (optical path length - mean OPL) across the pupil, a
  wavefront-quality measure. Values ~1e-3 mm are near the diffraction limit
  at 587.6 nm (about lambda/4).
- Pass gate: asphere-opt on-axis RMS < 0.3 mm.
EOF
}
trap append_interpretation EXIT

echo "=== Asphere optimization demo: singlet with aspheric front surface ==="
echo
echo "Two-stage comparison:"
echo "  Stage 1 (spherical): optimize s1/s2 curvature only — the asphere"
echo "    coefficients (conic, a4, a6) stay fixed at 0."
echo "  Stage 2 (asphere):   additionally optimize conic / a4 / a6."
echo
echo "Optical system:"
echo "  Surface 1: asphere_polynomial (N-BK7, explicit aperture stop),"
echo "  Surface 2: sphere,  Surface 3: image"
echo "  Fields: 0 deg / 5 deg, wavelength d (587.6 nm)"
echo

# ── Stage 1: spherical-only (asphere parameters are excluded from the
# optimization variables via --exclude-param) ──
echo "=== Stage 1: optimize with curvature only (no asphere variables) ==="
$RAYWEAVE optimize --exclude-param conic,a4,a6,coefficient_0,coefficient_1 --log "$SPH_LOG" < "$YAML" > "$SPH_RESULT"
echo "  Written: $SPH_RESULT"
echo

echo "=== Stage 2: optimize with asphere variables (conic, a4, a6) ==="
$RAYWEAVE optimize --verbose --log "$ASP_LOG" < "$YAML" > "$ASP_RESULT"
echo "  Written: $ASP_RESULT"
echo

# ── Final merit / status from both logs (the log's first "merit" line is
# already after the first step, so only the final merit is shown) ──
log_summary() {
  local logfile=$1
  local merit status
  merit=$($RAYWEAVE query --jsonl --where 'has("merit")' -r merit < "$logfile")
  status=$($RAYWEAVE query --jsonl --where 'has("status")' -r status < "$logfile")
  printf '%s %s\n' "$merit" "$status"
}
read -r SPH_AFTER SPH_STATUS < <(log_summary "$SPH_LOG")
read -r ASP_AFTER ASP_STATUS < <(log_summary "$ASP_LOG")

# ── Asphere coefficients (before / spherical-opt / asphere-opt) ──
extract_coeffs() {
  local yaml="$1"
  $RAYWEAVE query --default 0 --printf '%.6f' 'configs[0].surfaces[0].curvature' < "$yaml"
  $RAYWEAVE query --default 0 --printf '%.6f' 'configs[0].surfaces[0].conic' < "$yaml"
  $RAYWEAVE query --default 0 --printf '%.6e' 'configs[0].surfaces[0].coefficients[0]' < "$yaml"
  $RAYWEAVE query --default 0 --printf '%.6e' 'configs[0].surfaces[0].coefficients[1]' < "$yaml"
}
# bash 3.2 (macOS default) has no readarray; read the four lines one by one.
{
  read CUR_BEFORE; read CON_BEFORE; read A4_BEFORE; read A6_BEFORE;
} < <(extract_coeffs "$YAML")
{
  read CUR_SPH; read CON_SPH; read A4_SPH; read A6_SPH;
} < <(extract_coeffs "$SPH_RESULT")
{
  read CUR_ASP; read CON_ASP; read A4_ASP; read A6_ASP;
} < <(extract_coeffs "$ASP_RESULT")
coef_delta() {
  awk -v a="$1" -v b="$2" 'BEGIN { printf "%.6e", b - a }'
}
DELTA_CUR=$(coef_delta "$CUR_BEFORE" "$CUR_ASP")
DELTA_CON=$(coef_delta "$CON_BEFORE" "$CON_ASP")
DELTA_A4=$(coef_delta "$A4_BEFORE" "$A4_ASP")
DELTA_A6=$(coef_delta "$A6_BEFORE" "$A6_ASP")

# ── Spot RMS (before / spherical-opt / asphere-opt) ──
BEFORE_CHIEF=$($RAYWEAVE chief < "$YAML" 2>/dev/null)
SPH_CHIEF=$($RAYWEAVE chief < "$SPH_RESULT" 2>/dev/null)
ASP_CHIEF=$($RAYWEAVE chief < "$ASP_RESULT" 2>/dev/null)
rms_field() {
  local chief="$1" fi="$2"
  echo "$chief" | $RAYWEAVE query -r "chief_rays[$fi].spot_stats.rms_r"
}

# OPD RMS = population stddev of OPL over the accepted pupil-grid rays.
opd_field() {
  local chief="$1" fi="$2"
  echo "$chief" | $RAYWEAVE query --stdev "chief_rays[$fi].grid_points[].opl"
}

{
  echo "=== Asphere optimization demo: two-stage comparison ==="
  echo
  echo "--- Asphere coefficient (surface 1) ---"
  printf "  %-8s %14s %14s %14s %16s\n" "Coef" "before" "spherical-opt" "asphere-opt" "delta (asp-before)"
  printf "  %-8s %14s %14s %14s %16s\n" "----" "-------" "-------------" "-----------" "------------------"
  printf "  %-8s %14s %14s %14s %16s\n" "curv" "$CUR_BEFORE" "$CUR_SPH" "$CUR_ASP" "$DELTA_CUR"
  printf "  %-8s %14s %14s %14s %16s\n" "conic" "$CON_BEFORE" "$CON_SPH" "$CON_ASP" "$DELTA_CON"
  printf "  %-8s %14s %14s %14s %16s\n" "a4"    "$A4_BEFORE" "$A4_SPH" "$A4_ASP" "$DELTA_A4"
  printf "  %-8s %14s %14s %14s %16s\n" "a6"    "$A6_BEFORE" "$A6_SPH" "$A6_ASP" "$DELTA_A6"
  echo
  echo "--- Spot RMS (d=587.6nm, chief evaluation) ---"
  printf "  %-6s %6s  %10s  %10s  %10s\n" "Field" "Angle" "before" "spherical" "asphere"
  printf "  %-6s %6s  %10s  %10s  %10s\n" "-----" "-----" "--------" "---------" "-------"
  for fi in 0 1; do
    ang=$(echo "$BEFORE_CHIEF" | $RAYWEAVE query --default '?' -r "chief_rays[$fi].field_angle")
    printf "  %-6s %5s°  %10.4f  %10.4f  %10.4f\n" "f$fi" "$ang" \
      "$(rms_field "$BEFORE_CHIEF" "$fi")" \
      "$(rms_field "$SPH_CHIEF" "$fi")" \
      "$(rms_field "$ASP_CHIEF" "$fi")"
  done
  echo
  echo "--- OPD RMS (d=587.6nm, chief evaluation) ---"
  printf "  %-6s %6s  %12s  %12s  %12s\n" "Field" "Angle" "before" "spherical" "asphere"
  printf "  %-6s %6s  %12s  %12s  %12s\n" "-----" "-----" "--------" "---------" "-------"
  for fi in 0 1; do
    ang=$(echo "$BEFORE_CHIEF" | $RAYWEAVE query --default '?' -r "chief_rays[$fi].field_angle")
    printf "  %-6s %5s°  %12.3e  %12.3e  %12.3e\n" "f$fi" "$ang" \
      "$(opd_field "$BEFORE_CHIEF" "$fi")" \
      "$(opd_field "$SPH_CHIEF" "$fi")" \
      "$(opd_field "$ASP_CHIEF" "$fi")"
  done
  echo
} | tee "$RESULT_FILE"

# ── Console summary ──
echo "--- Stage summaries (DLS-internal merit; spot RMS above is the reference) ---"
printf "  %-18s status=%-15s final merit=%.3e\n" "spherical-opt" "$SPH_STATUS" "$SPH_AFTER"
printf "  %-18s status=%-15s final merit=%.3e\n" "asphere-opt"   "$ASP_STATUS" "$ASP_AFTER"
echo

# ── Merit breakdown (asphere stage) from the log ──
if [ -f "$ASP_LOG" ]; then
  echo "--- Merit breakdown (asphere stage, final state) ---"
  $RAYWEAVE query --jsonl --where 'event=="breakdown"' \
    --each 'terms:key,value' --printf '  %s: %.6e' < "$ASP_LOG" 2>/dev/null || true
  echo
fi

# ── Diagrams ──
echo "=== Diagrams ==="
$RAYWEAVE chief --clear-aperture < "$YAML" \
  | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/asphere-optimize-init.png" >/dev/null 2>&1 || true
echo "Written: $OUTDIR/asphere-optimize-init.png"

$RAYWEAVE chief --clear-aperture < "$SPH_RESULT" \
  | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/asphere-optimize-spherical.png" >/dev/null 2>&1 || true
echo "Written: $OUTDIR/asphere-optimize-spherical.png"

$RAYWEAVE chief --clear-aperture < "$ASP_RESULT" \
  | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/asphere-optimize-opt.png" >/dev/null 2>&1 || true
echo "Written: $OUTDIR/asphere-optimize-opt.png"
echo

# ── Threshold check: asphere stage on-axis RMS ──
THRESHOLD=0.3
printf "  (threshold = $THRESHOLD mm — asphere-opt on-axis RMS must be below this)\n"
rms_onaxis=$(rms_field "$ASP_CHIEF" 0)
if [ "$rms_onaxis" != "-1" ] && $RAYWEAVE query --gate "rms >= $THRESHOLD" --set rms="$rms_onaxis" < /dev/null > /dev/null; then
  msg="  >>> Optimization failed: asphere on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm >= $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
  exit 1
else
  msg="  >>> Optimization passed: asphere on-axis RMS = $(printf '%.4f' "$rms_onaxis") mm < $THRESHOLD mm"
  echo "$msg" | tee -a "$RESULT_FILE"
fi

echo
echo "=== Iteration logs: $SPH_LOG, $ASP_LOG ==="
echo "=== Results saved: $RESULT_FILE ==="
