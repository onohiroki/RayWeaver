#!/bin/bash
set -uo pipefail

# =============================================================================
# glass-optimize-demo.bash — swapped flint/crown recovery via the conditional
# merit schedule (smooth blend, Option B)
#
# Purpose: show that a SINGLE `rayweave optimize` run recovers a deliberately
# swapped flint/crown arrangement on the real US2645157 Cooke triplet, using
# the `optimization.merit_schedule` smooth blend:
#
#   - The geometry is FROZEN (only the two glass elements' nd/vd are variables),
#     so the swap can not be hidden by re-bending the surfaces.
#   - The system starts with the negative-power element carrying a crown (SK18
#     values) and the positive-power element a flint (SF12 values).
#   - `merit_schedule` starts in the colour-only `color_first` mode (no spot
#     terms — the imagery is deliberately not converged) and, as the colour
#     errors collapse, blends to the `full` mode (spot RMS + colour).
#
# Steps
#   1. optimize --verbose --log : one DLS run with the scheduled blend
#   2. query                    : schedule state (active_mode, mode_weights,
#                                 mode_changes) and the JSONL weights events
#   3. chief                    : spot RMS per field, before vs after
#   4. gnuplot                  : the weight-transition curve + PNG diagrams
#
# How to read the result
#   - The weight-transition curve shows the blend fading from colour_first to
#     full as the colour merit collapses.
#   - Gates: S3.vd < 45 (flint) AND S6.vd > 45 (crown); on-axis RMS improves;
#     the run ends in the `full` mode.
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

YAML="$SCRIPT_DIR/glass-optimize-demo.yaml"
OUTDIR="$SCRIPT_DIR"
OPT_RESULT="$OUTDIR/glass-optimize-result.yaml"
OPT_LOG="$OUTDIR/glass-optimize-log.jsonl"
RESULT_FILE="$OUTDIR/glass-optimize-demo-result.txt"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$OPT_RESULT" "$OPT_LOG" "$OUTDIR/glass-optimize-stderr.txt"
  rm -f "$OUTDIR"/glass-optimize-init.png "$OUTDIR"/glass-optimize-opt.png
  rm -f "$OUTDIR"/glass-schedule-weights.png "$OUTDIR"/glass-schedule-weights.txt "$RESULT_FILE"
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
- The system is the real US2645157 Cooke triplet with the second and third
  element glasses swapped: the negative-power element carries a crown (SK18
  values) and the positive-power element a flint (SF12 values). The geometry
  is FROZEN — only the two glasses' nd/vd are optimised, so the swap cannot be
  hidden by re-bending the surfaces.
- "RMS before / RMS after" is the geometric RMS spot radius (mm) per field at
  the primary wavelength (d = 587.6 nm), measured on the fixed image plane.
- The merit_schedule started in the colour-only "color_first" mode (no spot
  terms — imagery deliberately not converged) and blended to "full" as the
  colour errors collapsed. The weight-transition curve shows this fade.
- Gates: S3.vd < 45 (the negative-power element became a flint) AND
  S6.vd > 45 (the positive-power element became a crown); the on-axis RMS
  improved; the run ended in the full mode.
- A single optimize run recovers the roles: no chained two-stage runs.
EOF
}
trap append_interpretation EXIT

echo "=== Glass optimization demo: swapped flint/crown recovery ==="
echo
echo "Optical system: the US2645157 Cooke triplet, glasses SWAPPED:"
echo "  Surface 3-4: negative-power element, carries a crown (nd=1.63854, vd=55.42)"
echo "  Surface 6-7: positive-power element, carries a flint (nd=1.64831, vd=33.84)"
echo "  Geometry frozen: variables are only the two elements' nd/vd (4 variables)"
echo "  Fields: 0/16/24 degrees — 3 fields, wavelengths g/F/d/C — 4 colours"
echo
echo "Merit schedule (optimization.merit_schedule, smooth blend):"
echo "  metric: merit_ratio  curve: linear  anchor 1.0 -> 0.05"
echo "  color_first: lateral_color + longitudinal_color only (imagery unconverged)"
echo "  full: spot_rms (4 wl x 3 fields) + lateral_color + longitudinal_color"
echo

echo "=== DLS optimization (single run) ==="
OPT_STDERR="$OUTDIR/glass-optimize-stderr.txt"
$RAYWEAVE optimize --verbose --log "$OPT_LOG" < "$YAML" > "$OPT_RESULT" 2> "$OPT_STDERR"

echo
echo "--- Optimization results ---"
echo -n "  Status:      "
$RAYWEAVE query --jsonl --where 'has("status")' -r status < "$OPT_LOG"
echo -n "  Iterations:  "
$RAYWEAVE query --jsonl --where 'has("status")' -r iter < "$OPT_LOG"
BEFORE_MERIT=$(grep -oE 'Before: +[0-9.e+-]+' "$OPT_STDERR" | grep -oE '[0-9.e+-]+$')
AFTER_MERIT=$(grep -oE 'After: +[0-9.e+-]+' "$OPT_STDERR" | grep -oE '[0-9.e+-]+$')
echo "  Before:      ${BEFORE_MERIT:-?}"
echo "  After:       ${AFTER_MERIT:-?}"
echo
echo "--- Merit schedule state ---"
ACTIVE=$( $RAYWEAVE query -r opt_results.active_mode < "$OPT_RESULT" )
CHANGES=$( $RAYWEAVE query -r opt_results.mode_changes < "$OPT_RESULT" )
WCOLOR=$( $RAYWEAVE query -r 'opt_results.mode_weights.color_first' < "$OPT_RESULT" )
WFULL=$(  $RAYWEAVE query -r 'opt_results.mode_weights.full' < "$OPT_RESULT" )
echo "  Active mode:   $ACTIVE"
echo "  Mode changes:  $CHANGES"
echo "  Final weights: color_first=$WCOLOR  full=$WFULL"
FIRST_W=$( grep '"weights"' "$OPT_LOG" | head -1 )
LAST_W=$( grep '"weights"' "$OPT_LOG" | tail -1 )
echo "  First weights: $FIRST_W"
echo "  Last weights:  $LAST_W"
echo

echo "--- Glass before → after (surfaces 3 / 6) ---"
extract_surface_glass() {
  local yaml="$1"
  local sid="$2"
  local field="$3"
  $RAYWEAVE query -r "configs[0].surfaces[id=$sid].material.$field" < "$yaml"
}
for sid in 3 6; do
  ND0=$(extract_surface_glass "$YAML" "$sid" nd)
  VD0=$(extract_surface_glass "$YAML" "$sid" vd)
  ND1=$(extract_surface_glass "$OPT_RESULT" "$sid" nd)
  VD1=$(extract_surface_glass "$OPT_RESULT" "$sid" vd)
  ROLE="?"
  if $RAYWEAVE query --gate "vd < 45" --set vd="$VD1" < /dev/null > /dev/null 2>&1; then
    ROLE="flint"
  else
    ROLE="crown"
  fi
  printf "  S%d: nd %-8s -> %-8s   vd %-8s -> %-8s   (%s)\n" "$sid" "$ND0" "$ND1" "$VD0" "$VD1" "$ROLE"
done
echo

# ── Spot RMS comparison (before vs after, primary λ=587.6nm) ──
BEFORE_CHIEF=$($RAYWEAVE chief < "$YAML" 2>/dev/null)
AFTER_CHIEF=$($RAYWEAVE chief < "$OPT_RESULT" 2>/dev/null)
rms_field() {
  local chief="$1" fi="$2"
  echo "$chief" | $RAYWEAVE query -r "chief_rays[$fi].spot_stats.rms_r"
}
RMS_ONAXIS_BEFORE=$(rms_field "$BEFORE_CHIEF" 0)
RMS_ONAXIS_AFTER=$(rms_field "$AFTER_CHIEF" 0)
{
  echo "=== Spot RMS Comparison (primary λ=587.6nm) ==="
  printf "  %-8s %6s  %10s  %10s\n" "Phase" "Field" "RMS before" "RMS after"
  printf "  %-8s %6s  %10s  %10s\n" "-----" "-----" "--------" "--------"
  for fi in 0 1 2; do
    rms_before=$(rms_field "$BEFORE_CHIEF" "$fi")
    rms_after=$(rms_field "$AFTER_CHIEF" "$fi")
    printf "  %-8s %6s  %10.4f  %10.4f" "optimize" "f$fi" "$rms_before" "$rms_after"
    if $RAYWEAVE query --gate "a < b" --set a="$rms_after" --set b="$rms_before" < /dev/null > /dev/null 2>&1; then
      echo "   ✓"
    else
      echo "   ✗"
    fi
  done
  echo
} | tee "$RESULT_FILE"

# ── Gates ──
echo "=== Pass gates ==="
FAIL=0
check_gate() {
  local desc="$1"; shift
  if "$@"; then
    echo "  PASS  $desc"
  else
    echo "  FAIL  $desc"
    FAIL=1
  fi
}

# Gate 1/2: roles recovered (--gate supports single comparisons only).
VD3=$(extract_surface_glass "$OPT_RESULT" 3 vd)
VD6=$(extract_surface_glass "$OPT_RESULT" 6 vd)
gate_s3() { $RAYWEAVE query --gate "vd < 45" --set vd="$VD3" < /dev/null > /dev/null 2>&1; }
gate_s6() { $RAYWEAVE query --gate "vd > 45" --set vd="$VD6" < /dev/null > /dev/null 2>&1; }
# Gate 3: on-axis RMS improved.
gate_rms() { $RAYWEAVE query --gate "a < b" --set a="$RMS_ONAXIS_AFTER" --set b="$RMS_ONAXIS_BEFORE" < /dev/null > /dev/null 2>&1; }
# Gate 4: ended in the full mode.
gate_full() { [ "$ACTIVE" = "full" ]; }

check_gate "S3.vd < 45 (negative element is a flint, vd=$VD3)"  gate_s3
check_gate "S6.vd > 45 (positive element is a crown, vd=$VD6)"  gate_s6
check_gate "on-axis RMS improved ($RMS_ONAXIS_BEFORE -> $RMS_ONAXIS_AFTER mm)" gate_rms
check_gate "run ended in the full mode (active_mode=$ACTIVE)" gate_full

if [ "$FAIL" = 1 ]; then
  echo "  >>> Demo failed one or more gates" | tee -a "$RESULT_FILE"
  exit 1
fi
echo "  >>> All gates passed" | tee -a "$RESULT_FILE"
echo

echo "=== PNG diagrams ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$YAML" 2>/dev/null \
  | $RAYWEAVE chief --marginal-rays 2>/dev/null \
  | $RAYWEAVE trace 2>/dev/null \
  | $RAYWEAVE plot -o "$OUTDIR/glass-optimize-init.png" > /dev/null 2>/dev/null || true
echo "Written: $OUTDIR/glass-optimize-init.png"

$RAYWEAVE chief --clear-aperture --ray-fan < "$OPT_RESULT" 2>/dev/null \
  | $RAYWEAVE chief --marginal-rays 2>/dev/null \
  | $RAYWEAVE trace 2>/dev/null \
  | $RAYWEAVE plot -o "$OUTDIR/glass-optimize-opt.png" > /dev/null 2>/dev/null || true
echo "Written: $OUTDIR/glass-optimize-opt.png"
echo

if command -v gnuplot &>/dev/null 2>&1; then
  echo "=== Merit-schedule weight transition (color_first vs full) ==="
  grep '"weights"' "$OPT_LOG" \
    | sed -E 's/.*"iter":([0-9]+),.*"color_first":([0-9.eE+-]+).*/\1 \2/' \
    > "$OUTDIR/glass-schedule-weights.txt"
  grep '"weights"' "$OPT_LOG" \
    | sed -E 's/.*"iter":([0-9]+),.*"full":([0-9.eE+-]+).*/\1 \2/' \
    > "$OUTDIR/glass-schedule-weights-full.txt"
  export GNUTERM=pngcairo
  gnuplot 2>/dev/null <<GPLOT
    set terminal pngcairo size 800,400
    set output "$OUTDIR/glass-schedule-weights.png"
    set xlabel "DLS iteration"
    set ylabel "mode weight"
    set yrange [0:1.05]
    set key top right
    plot "$OUTDIR/glass-schedule-weights.txt" using 1:2 with lines lw 2 title "color_first", \
         "$OUTDIR/glass-schedule-weights-full.txt" using 1:2 with lines lw 2 title "full"
GPLOT
  echo "Written: $OUTDIR/glass-schedule-weights.png"
  rm -f "$OUTDIR/glass-schedule-weights-full.txt"
  echo
else
  echo "  (weight-transition plot skipped: gnuplot not available)"
fi

echo "=== Iteration log saved: $OPT_LOG ==="
