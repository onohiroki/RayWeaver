#!/bin/bash
set -euo pipefail

# =============================================================================
# multi-config-zoom-demo.bash — zoom lens with equality + vignetting gates
#
# Purpose: show DLS multi-config optimisation under hard engineering gates:
# the entrance pupil is pinned by an equality constraint (EPD = 20 mm) and
# off-axis vignetting is kept above a floor (VF >= 0.5) via vignetting
# constraints, alongside the usual on-axis spot-RMS merit.
#
# Steps
#   1. optimize --verbose --log : DLS multi-config optimisation
#   2. chief                    : RMS + vignetting factor per config/field,
#                                 before vs after
#   3. paraxial                 : EFL and EPD per config
#   4. plot                     : ray-overlaid layouts before/after
#
# How to read the result
#   - Every config must pass all gates: on-axis RMS < 0.03 mm,
#     EPD = 20 +/- 0.1 mm, VF >= 0.5 on the 10/15 mm image-height fields.
#   - EFL differs per config because the lens zooms; EPD stays pinned at 20.
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

YAML="$SCRIPT_DIR/multi-config-zoom.yaml"
OUTDIR="$SCRIPT_DIR"
RESULT="$OUTDIR/multi-config-zoom-result.yaml"
LOG="$OUTDIR/multi-config-zoom-log.jsonl"
RESULT_FILE="$OUTDIR/multi-config-zoom-demo-result.txt"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  for cfg in config0 config1 config2; do
    rm -f "$OUTDIR/multi-config-zoom-${cfg}-init-rays.png"
    rm -f "$OUTDIR/multi-config-zoom-${cfg}-opt-rays.png"
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
- RMS bef/aft: geometric RMS spot radius (mm) per config and field, before
  and after DLS. The on-axis (0 deg) row is the primary imaging gate.
- EPD bef/aft: entrance pupil diameter (mm). The equality constraint pins it
  to 20 mm in every config (before and after).
- EFL (mm) per config differs because the lens changes focal length as it
  zooms, while the pupil diameter is held constant.
- Vignetting factor (VF): fraction of pupil-grid rays that transmit the
  system. 1.0000 = full aperture; a value like 0.4 means the beam is clipped
  hard (vignetting) by a clear aperture.
- Pass gates: on-axis RMS < 0.03 mm, EPD = 20 +/- 0.1 mm, and
  VF >= 0.5 on the 10/15 mm image-height fields.
EOF
}
trap append_interpretation EXIT

echo "=== Multi-Config Zoom Lens Demo ==="
echo
echo "Layout: 3 configs (config0 / config1 / config2)"
echo "Shared variables: variables following simple-zoom pattern"
echo "Local variables: 2 air gaps per config"
echo
echo "Aperture / vignetting control:"
echo "  Entrance pupil diameter = 20mm (equality constraint, all configs)"
echo "  vignetting_factor >= 0.5 on 10mm / 15mm image-height fields"
echo

echo "=== DLS Multi-Config Optimization ==="
$RAYWEAVE optimize --verbose --log "$LOG" < "$YAML" > "$RESULT"

echo
echo "--- Optimization results ---"
grep -i -E "^=== |status|merit" "$RESULT" 2>/dev/null | head -10 || true
echo

echo "--- RMS spot size comparison (before → after) ---"
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  for label in "Before" "After"; do
    [ "$label" = "Before" ] && src="$YAML" || src="$RESULT"
    echo "    $label:"
    cat "$src" | $RAYWEAVE chief --config "$cfg" 2>/dev/null \
      | $RAYWEAVE query --each 'chief_rays[]:field_angle,spot_stats.rms_r,spot_stats.traced_rays' \
          --printf '      field %.3f° RMS=%.4fmm (%d rays)'
  done
done
echo

echo "=== Ray-overlaid layout (after optimization) ==="
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  cat "$RESULT" \
    | $RAYWEAVE chief --clear-aperture --ray-fan --config "$cfg" \
    | $RAYWEAVE chief --marginal-rays --config "$cfg" \
    | $RAYWEAVE trace --config "$cfg" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/multi-config-zoom-${cfg}-opt-rays.png" 2>/dev/null
  echo "    Written: $OUTDIR/multi-config-zoom-${cfg}-opt-rays.png"
done
echo

echo "=== Ray-overlaid layout (before optimization) ==="
for cfg in config0 config1 config2; do
  echo "  Config: $cfg"
  cat "$YAML" \
    | $RAYWEAVE chief --clear-aperture --ray-fan --config "$cfg" \
    | $RAYWEAVE chief --marginal-rays --config "$cfg" \
    | $RAYWEAVE trace --config "$cfg" \
    | $RAYWEAVE plot --config "$cfg" -o "$OUTDIR/multi-config-zoom-${cfg}-init-rays.png" 2>/dev/null
  echo "    Written: $OUTDIR/multi-config-zoom-${cfg}-init-rays.png"
done
echo

# Start the result file fresh (the sections below append with tee -a).
: > "$RESULT_FILE"

{
  echo "=== Performance comparison ==="
  printf "  %-8s %6s  %10s  %10s  %9s  %9s\n" "Config" "Field" "RMS bef" "RMS aft" "EPD bef" "EPD aft"
  printf "  %-8s %6s  %10s  %10s  %9s  %9s\n" "------" "-----" "-------" "-------" "-------" "-------"
  for cfg in config0 config1 config2; do
    # Extract field angles and RMS before/after
    bef=$(cat "$YAML" | $RAYWEAVE chief --config "$cfg" 2>/dev/null \
      | $RAYWEAVE query --each 'chief_rays[]:field_angle,spot_stats.rms_r' --printf '%.3f %.4f')
    aft=$(cat "$RESULT" | $RAYWEAVE chief --config "$cfg" 2>/dev/null \
      | $RAYWEAVE query --each 'chief_rays[]:field_angle,spot_stats.rms_r' --printf '%.3f %.4f')
    efl=$(cat "$RESULT" | $RAYWEAVE paraxial --config "$cfg" 2>/dev/null \
      | $RAYWEAVE query --printf '%.1f' paraxial_result.focal_length)
    epd_bef=$(cat "$YAML" | $RAYWEAVE paraxial --config "$cfg" 2>/dev/null \
      | $RAYWEAVE query --printf '%.3f' paraxial_result.entrance_pupil_diameter)
    epd_aft=$(cat "$RESULT" | $RAYWEAVE paraxial --config "$cfg" 2>/dev/null \
      | $RAYWEAVE query --printf '%.3f' paraxial_result.entrance_pupil_diameter)
    # Show each field with before/after RMS + EPD on the header line
    line_no=0
    while IFS= read -r bline; do
      read -r fa br <<< "$bline"
      read -r _fa2 ar <<< "$(echo "$aft" | sed -n "$((line_no+1))p")"
      if [ "$line_no" -eq 0 ]; then
        printf "  %-8s %5s°  %8.4f  %8.4f  %7.3f  %7.3f    EFL=%smm\n" "$cfg" "$fa" "$br" "$ar" "$epd_bef" "$epd_aft" "$efl"
      else
        printf "  %-8s %5s°  %8.4f  %8.4f\n" "" "$fa" "$br" "$ar"
      fi
      line_no=$((line_no + 1))
    done <<< "$bef"
  done
  echo
} | tee -a "$RESULT_FILE"

# ── On-axis RMS threshold check (all configs) ──
THRESHOLD=0.03
echo "=== On-axis RMS threshold check ==="
printf "  (threshold = $THRESHOLD mm — all configs on-axis RMS must be below this)\n"

get_onaxis_rms() {
  local yaml_file="$1"
  local cfg="$2"
  $RAYWEAVE chief --config "$cfg" < "$yaml_file" 2>/dev/null \
    | $RAYWEAVE query -r "chief_rays[field_angle=0].spot_stats.rms_r"
}

failed=false
for cfg in config0 config1 config2; do
  rms_after=$(get_onaxis_rms "$RESULT" "$cfg")
  printf "  %-8s on-axis RMS = %8.4f mm" "$cfg" "$rms_after"
  if [ "$rms_after" != "-1" ] && $RAYWEAVE query --gate "rms >= $THRESHOLD" --set rms="$rms_after" < /dev/null > /dev/null; then
    echo "   ✗"
    failed=true
  else
    echo "   ✓"
  fi
done

# ── Vignetting factor comparison (before vs after) ──
get_vf() {
  local yaml_file="$1"
  local cfg="$2"
  local field="$3"
  local n d
  n=$($RAYWEAVE chief --config "$cfg" < "$yaml_file" 2>/dev/null \
      | $RAYWEAVE query --count "chief_rays[$field].grid_points[].image_x")
  d=$($RAYWEAVE chief --config "$cfg" < "$yaml_file" 2>/dev/null \
      | $RAYWEAVE query --len "chief_rays[$field].grid_points")
  if [ "$d" = "-1" ] || [ "$d" = "0" ]; then
    echo "-1"
  else
    $RAYWEAVE query --set n="$n" --set d="$d" --expr 'n/d' < /dev/null
  fi
}

{
  echo "=== Vignetting Factor Comparison (per config, primary λ=587.6nm) ==="
  echo "  (fraction of pupil-grid rays that transmit the system)"
  printf "  %-8s %6s  %10s  %10s\n" "Config" "Field" "VF before" "VF after"
  printf "  %-8s %6s  %10s  %10s\n" "------" "-----" "--------" "--------"
  for cfg in config0 config1 config2; do
    for fi in 0 1 2; do
      vf_before=$(get_vf "$YAML" "$cfg" "$fi")
      vf_after=$(get_vf "$RESULT" "$cfg" "$fi")
      printf "  %-8s %6s  %10.4f  %10.4f\n" "$cfg" "f$fi" "$vf_before" "$vf_after"
    done
  done
  echo
} | tee -a "$RESULT_FILE"

# ── Entrance pupil diameter threshold check (all configs) ──
EPD_TARGET=20
EPD_TOL=0.1
echo "=== Entrance pupil diameter threshold check ==="
printf "  (target = $EPD_TARGET mm ± $EPD_TOL — all configs EPD must be within this)\n"
for cfg in config0 config1 config2; do
  epd=$(cat "$RESULT" | $RAYWEAVE paraxial --config "$cfg" 2>/dev/null \
    | $RAYWEAVE query -r paraxial_result.entrance_pupil_diameter)
  printf "  %-8s EPD = %8.4f mm" "$cfg" "$epd"
  if $RAYWEAVE query --gate "abs(epd-$EPD_TARGET)<=$EPD_TOL" --set epd="$epd" < /dev/null > /dev/null; then
    echo "   ✓"
  else
    echo "   ✗"
    failed=true
  fi
done

# ── Vignetting threshold check (off-axis fields must keep >= 50% of center beam) ──
VIG_THRESHOLD=0.5
echo "=== Vignetting factor threshold check ==="
printf "  (threshold = $VIG_THRESHOLD — 10mm / 15mm image-height fields must keep >= this)\n"
for cfg in config0 config1 config2; do
  for fi in 1 2; do
    vf=$(get_vf "$RESULT" "$cfg" "$fi")
    printf "  %-8s field %d VF = %8.4f" "$cfg" "$fi" "$vf"
    if [ "$vf" != "-1" ] && $RAYWEAVE query --gate "vf < $VIG_THRESHOLD" --set vf="$vf" < /dev/null > /dev/null; then
      echo "   ✗"
      failed=true
    else
      echo "   ✓"
    fi
  done
done

# ── Final verdict ──
{
  echo
  if [ "$failed" = true ]; then
    echo "  >>> Optimization failed: gates not met (on-axis RMS, EPD, or vignetting factor)"
  else
    echo "  >>> Optimization passed: all configs on-axis RMS < $THRESHOLD mm, EPD ≈ ${EPD_TARGET}mm, vignetting factor >= $VIG_THRESHOLD"
  fi
  echo
} | tee -a "$RESULT_FILE"

echo "=== Iteration log saved: $LOG ==="
if [ -f "$LOG" ]; then
  echo "  Log entries:"
  wc -l "$LOG" 2>/dev/null
fi
echo

if [ "$failed" = true ]; then
  exit 1
fi

# (cleanup is handled at the top for --clean mode)
