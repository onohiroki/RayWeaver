#!/bin/bash
set -euo pipefail

# =============================================================================
# simple-zoom-demo.bash — multi-config (shared-variable) zoom optimisation
#
# Purpose: demonstrate a 3-configuration zoom lens (wide / mid / tele) being
# optimised with one set of shared variables plus per-config local air gaps.
#
# Configs: config0 (S2=20, S4=80), config1 (S2=50, S4=50), config2 (S2=80, S4=20)
#
# Steps
#   1. optimize --verbose --log : DLS multi-config optimisation
#   2. chief | trace | plot     : ray-overlaid layout per config, before/after
#   3. paraxial                 : EFL per config, before/after
#   4. chief                    : on-axis RMS per config, before/after
#   5. geometry check           : config0 lens1-lens2 air gap >= 5 mm
#
# How to read the result
#   - All configs must be simultaneously good: on-axis RMS < 0.3 mm each.
#   - The gap check guards against lens elements colliding.
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

YAML="$SCRIPT_DIR/simple-zoom.yaml"
OUTDIR="$SCRIPT_DIR"
RESULT="$OUTDIR/simple-zoom-optimized.yaml"
LOG="$OUTDIR/simple-zoom-log.jsonl"
RESULT_FILE="$OUTDIR/simple-zoom-demo-result.txt"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  for cfg in config0 config1 config2; do
    rm -f "$OUTDIR/simple-zoom-${cfg}-init-rays.png"
    rm -f "$OUTDIR/simple-zoom-${cfg}-opt-rays.png"
  done
  rm -f "$RESULT" "$LOG" "$RESULT_FILE"
  echo "  Removed: PNGs, $RESULT, $LOG, $RESULT_FILE"
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
- Simple 3-config zoom lens; config0/1/2 are the wide / mid / tele positions.
- RMS before/after = on-axis (0 deg) geometric RMS spot radius (mm) per
  config, evaluated before and after the shared-variable DLS run.
- Pass gate: every config on-axis RMS < 0.3 mm.
- The lens1-lens2 gap check confirms the config0 air gap stays >= 5 mm
  (a geometric anti-collision guard).
- The point of multi-config optimisation is that all three configs must be
  good simultaneously, not just one.
EOF
}
trap append_interpretation EXIT

echo "=== Simple Zoom Lens Optimization Demo ==="
echo
echo "Configs: config0 (S2=20, S4=80), config1 (S2=50, S4=50), config2 (S2=80, S4=20)"
echo
echo "Constraint: config0 air gap between lens 1 and lens 2 >= 5mm"
echo

# ── 1. DLS multi-config optimization ──
echo "=== DLS Multi-Config Optimization ==="
echo "  Merit: on-axis + off-axis spot RMS (center weight 1.0, mid 0.3, edge 0.1)"
$RAYWEAVE optimize --verbose --log "$LOG" < "$YAML" > "$RESULT"

echo
echo "--- Optimization results ---"
echo "  (see stderr for iteration details)"
echo

# ── 2. Ray-overlaid layout (before optimization) ──
echo "=== Initial ray-overlaid layout ==="
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  cat "$YAML" \
    | $RAYWEAVE chief --clear-aperture --ray-fan --config "$cfg" \
    | $RAYWEAVE chief --marginal-rays --config "$cfg" \
    | $RAYWEAVE trace --config "$cfg" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/simple-zoom-${cfg}-init-rays.png" 2>/dev/null
  echo "    PNG: $OUTDIR/simple-zoom-${cfg}-init-rays.png"
done
echo

# ── 3. Ray-overlaid layout (after optimization) ──
echo "=== Optimized ray-overlaid layout ==="
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  cat "$RESULT" \
    | $RAYWEAVE chief --clear-aperture --ray-fan --config "$cfg" \
    | $RAYWEAVE chief --marginal-rays --config "$cfg" \
    | $RAYWEAVE trace --config "$cfg" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/simple-zoom-${cfg}-opt-rays.png" 2>/dev/null
  echo "    PNG: $OUTDIR/simple-zoom-${cfg}-opt-rays.png"
done
echo

echo "=== Iteration log saved: $LOG ==="
if [ -f "$LOG" ]; then
  echo "  Log entries:"
  wc -l "$LOG" 2>/dev/null
fi
echo

# ── 4. Focal length (before and after) ──
echo "=== Focal Length (EFL) ==="
get_efl() {
  local yaml_file="$1" cfg="$2"
  cat "$yaml_file" | $RAYWEAVE paraxial --config "$cfg" 2>/dev/null \
    | $RAYWEAVE query -r paraxial_result.focal_length
}
for cfg in config0 config1 config2; do
  efl_before=$(get_efl "$YAML" "$cfg")
  efl_after=$(get_efl "$RESULT" "$cfg")
  printf "  %-8s before=%8.2f mm  after=%8.2f mm\n" "$cfg" "$efl_before" "$efl_after"
done
echo

# ── 5. Spot RMS comparison (before vs after) ──
echo "=== Spot RMS Comparison (on-axis, field 0°) ==="
printf "  %-8s %12s %12s\n" "Config" "RMS before" "RMS after"

# Extract on-axis RMS from chief output (field_angle: 0)
get_onaxis_rms() {
  local yaml_file="$1"
  local cfg="$2"
  $RAYWEAVE chief --config "$cfg" < "$yaml_file" 2>/dev/null \
    | $RAYWEAVE query -r "chief_rays[field_angle=0].spot_stats.rms_r"
}

THRESHOLD=0.3
echo "  (threshold = $THRESHOLD mm — all configs on-axis RMS must be below this)"

failed=false
for cfg in config0 config1 config2; do
  rms_before=$(get_onaxis_rms "$YAML" "$cfg")
  rms_after=$(get_onaxis_rms "$RESULT" "$cfg")
  printf "  %-8s %12.4f %12.4f" "$cfg" "$rms_before" "$rms_after"
  if [ "$rms_before" != "-1" ] && [ "$rms_after" != "-1" ]; then
    if $RAYWEAVE query --gate "a < b" --set a="$rms_after" --set b="$rms_before" < /dev/null > /dev/null; then
      printf "   ✓"
    else
      printf "   ✗"
    fi
  fi
  echo
  if [ "$rms_after" != "-1" ] && $RAYWEAVE query --gate "rms >= $THRESHOLD" --set rms="$rms_after" < /dev/null > /dev/null; then
    failed=true
  fi
done

# ── Save RMS comparison to result file ──
{
  echo "=== Spot RMS Comparison (on-axis, field 0°) ==="
  printf "  %-8s %12s %12s\n" "Config" "RMS before" "RMS after"
  echo "  (threshold = $THRESHOLD mm)"
  for cfg in config0 config1 config2; do
    rms_before=$(get_onaxis_rms "$YAML" "$cfg")
    rms_after=$(get_onaxis_rms "$RESULT" "$cfg")
    printf "  %-8s %12.4f %12.4f\n" "$cfg" "$rms_before" "$rms_after"
  done
  echo
} > "$RESULT_FILE"
echo "  (RMS comparison saved to $RESULT_FILE)"

if [ "$failed" = true ]; then
  echo
  echo "  >>> Optimization failed: not all configs on-axis RMS < $THRESHOLD mm"
  echo "  >>> Optimization failed: not all configs on-axis RMS < $THRESHOLD mm" >> "$RESULT_FILE"
  exit 1
fi
echo
echo "  >>> Optimization passed: all configs on-axis RMS < $THRESHOLD mm"
echo "  >>> Optimization passed: all configs on-axis RMS < $THRESHOLD mm" >> "$RESULT_FILE"

# ── Lens 1-2 gap threshold check (config0) ──
get_gap12() {
  local yaml_file="$1"
  local cfg="$2"
  $RAYWEAVE query -r "configs[id=$cfg].surfaces[id=2].thickness" < "$yaml_file"
}

GAP_TARGET=5.0
echo
echo "=== Lens1-Lens2 gap threshold check (config0) ==="
printf "  (threshold = $GAP_TARGET mm — air gap between lens 1 and lens 2 must be >= this)\n"
gap_before=$(get_gap12 "$YAML" "config0")
gap_after=$(get_gap12 "$RESULT" "config0")
printf "  %-8s gap before = %8.4f mm\n" "config0" "$gap_before"
printf "  %-8s gap after  = %8.4f mm" "config0" "$gap_after"
{
  echo "=== Lens1-Lens2 gap check (config0) ==="
  echo "  (threshold = $GAP_TARGET mm)"
  printf "  %-8s gap before = %8.4f mm\n" "config0" "$gap_before"
  printf "  %-8s gap after  = %8.4f mm\n" "config0" "$gap_after"
  echo
} >> "$RESULT_FILE"
if [ "$gap_after" != "-1" ] && $RAYWEAVE query --gate "gap < $GAP_TARGET" --set gap="$gap_after" < /dev/null > /dev/null; then
  echo "   ✗"
  echo "  >>> Optimization failed: config0 lens1-lens2 gap $(printf '%.4f' "$gap_after") mm < $GAP_TARGET mm" >> "$RESULT_FILE"
  exit 1
else
  echo "   ✓"
  echo "  >>> Optimization passed: config0 lens1-lens2 gap $(printf '%.4f' "$gap_after") mm >= $GAP_TARGET mm" >> "$RESULT_FILE"
fi

# (cleanup is handled at the top for --clean mode)
