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
#     EPD = 20 +/- 0.1 mm, optimizer-reported VF >= 0.5 on the 10/15 mm
#     image-height fields.
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
- Vignetting factor (VF): fraction of the pupil beam that transmits the
  system; 1.0 = full aperture. The pass gate uses the value the optimizer
  enforced (opt_results.constraints vignetting_factor, reported in the
  result YAML); "VF aft(chief)" is the chief-grid reference measurement.
- Pass gates: on-axis RMS < 0.03 mm, EPD = 20 +/- 0.1 mm, and
  optimizer-reported VF >= 0.5 on the 10/15 mm image-height fields.
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
      | awk '
        BEGIN{ang=""; in_spot=0}
        /field_angle:/{ ang = $NF }
        /spot_stats:/{ in_spot = 1; rms = "" }
        in_spot && /rms_r:/{ rms = $NF }
        in_spot && /traced_rays:/ && rms != "" {
          tr = $NF
          if (tr + 0 > 0)
            printf "      field %.3f° RMS=%.4fmm (%d rays)\n", ang, rms + 0, tr
          in_spot = 0
        }'
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
      | awk 'BEGIN{ang=""; r=0} /field_angle:/{ang=$NF} /spot_stats:/{in_spot=1; r=0} in_spot&&/rms_r:/{r=$NF} in_spot&&/traced_rays:/{if(r+0>0 && $NF+0>0)printf "%.3f %.4f\n", ang, r; in_spot=0}')
    aft=$(cat "$RESULT" | $RAYWEAVE chief --config "$cfg" 2>/dev/null \
      | awk 'BEGIN{ang=""; r=0} /field_angle:/{ang=$NF} /spot_stats:/{in_spot=1; r=0} in_spot&&/rms_r:/{r=$NF} in_spot&&/traced_rays:/{if(r+0>0 && $NF+0>0)printf "%.3f %.4f\n", ang, r; in_spot=0}')
    efl=$(cat "$RESULT" | $RAYWEAVE paraxial --config "$cfg" 2>/dev/null \
      | awk -F': ' '/focal_length:/{printf "%.1f",$2; exit}')
    epd_bef=$(cat "$YAML" | $RAYWEAVE paraxial --config "$cfg" 2>/dev/null \
      | awk -F': ' '/entrance_pupil_diameter:/{printf "%.3f",$2; exit}')
    epd_aft=$(cat "$RESULT" | $RAYWEAVE paraxial --config "$cfg" 2>/dev/null \
      | awk -F': ' '/entrance_pupil_diameter:/{printf "%.3f",$2; exit}')
    # Show each field with before/after RMS + EPD on the header line
    line_no=0
    while IFS= read -r bline; do
      aline=$(echo "$aft" | sed -n "$((line_no+1))p")
      fa=$(echo "$bline" | awk '{print $1}')
      br=$(echo "$bline" | awk '{print $2}')
      ar=$(echo "$aline" | awk '{print $2}')
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
  python3 -c "
import sys, yaml
with open('/dev/stdin') as f:
    data = yaml.safe_load(f)
if data and 'chief_rays' in data:
    for ray in data['chief_rays']:
        if ray.get('field_angle') == 0 or abs(ray.get('field_angle', 1)) < 1e-10:
            ss = ray.get('spot_stats', {})
            rms = ss.get('rms_r', -1)
            if rms > 0:
                print(rms)
                sys.exit(0)
print(-1)
" < <($RAYWEAVE chief --config "$cfg" < "$yaml_file" 2>/dev/null)
}

failed=false
for cfg in config0 config1 config2; do
  rms_after=$(get_onaxis_rms "$RESULT" "$cfg")
  printf "  %-8s on-axis RMS = %8.4f mm" "$cfg" "$rms_after"
  if [ "$rms_after" != "-1" ] && [ "$(python3 -c "print('1' if $rms_after >= $THRESHOLD else '0')")" = "1" ]; then
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
  python3 -c "
import sys, yaml
d = yaml.safe_load(sys.stdin)
r = d['chief_rays']
p = r[$field].get('grid_points', [])
ok = [g for g in p if g.get('image_x') is not None]
print(len(ok)/len(p) if p else -1)
" < <($RAYWEAVE chief --config "$cfg" < "$yaml_file" 2>/dev/null)
}

# get_reported_vf reads the vignetting factor the optimizer enforced, as
# reported in opt_results.constraints of the optimize output (the metric the
# DLS constraint was evaluated on; this is what the pass gate checks).
get_reported_vf() {
  local yaml_file="$1"
  local cfg="$2"
  local field="$3"
  python3 -c "
import sys, yaml
d = yaml.safe_load(open('$yaml_file'))
or_ = d.get('opt_results')
if not or_:
    print(-1); sys.exit(0)
for c in or_.get('constraints', []):
    if c.get('measure') == 'vignetting_factor' and c.get('field') == $field and c.get('config') == '$cfg':
        print(c.get('value', -1)); sys.exit(0)
print(-1)
"
}

{
  echo "=== Vignetting Factor (per config, primary λ=587.6nm) ==="
  echo "  (VF bef / aft(chief) = fraction of chief pupil-grid rays transmitted;"
  echo "   VF aft(report) = vignetting factor the optimizer enforced — the pass gate)"
  printf "  %-8s %6s  %10s  %10s  %10s\n" "Config" "Field" "VF bef" "VF aft(rep)" "VF aft(chief)"
  printf "  %-8s %6s  %10s  %10s  %10s\n" "------" "-----" "-------" "----------" "----------"
  for cfg in config0 config1 config2; do
    for fi in 0 1 2; do
      vf_before=$(get_vf "$YAML" "$cfg" "$fi")
      vf_reported=$(get_reported_vf "$RESULT" "$cfg" "$fi")
      vf_chief=$(get_vf "$RESULT" "$cfg" "$fi")
      if [ "$vf_reported" = "-1" ]; then vf_reported="-"; fi
      printf "  %-8s %6s  %10.4f  %10s  %10.4f\n" "$cfg" "f$fi" "$vf_before" "$vf_reported" "$vf_chief"
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
    | awk -F': ' '/entrance_pupil_diameter:/{printf "%.4f",$2; exit}')
  epd_ok=$(echo "$epd" | python3 -c "import sys; v=float(sys.stdin.read()); print('1' if abs(v-$EPD_TARGET)<=$EPD_TOL else '0')")
  printf "  %-8s EPD = %8.4f mm" "$cfg" "$epd"
  if [ "$epd_ok" = "1" ]; then
    echo "   ✓"
  else
    echo "   ✗"
    failed=true
  fi
done

# ── Vignetting threshold check (off-axis fields; gate on the optimizer-reported VF) ──
VIG_THRESHOLD=0.5
echo "=== Vignetting factor threshold check ==="
printf "  (threshold = $VIG_THRESHOLD — optimizer-reported VF on 10mm / 15mm image-height fields must be >= this)\n"
for cfg in config0 config1 config2; do
  for fi in 1 2; do
    vf=$(get_reported_vf "$RESULT" "$cfg" "$fi")
    printf "  %-8s field %d VF = %8.4f" "$cfg" "$fi" "$vf"
    if [ "$vf" != "-1" ] && [ "$(python3 -c "print('1' if $vf < $VIG_THRESHOLD else '0')")" = "1" ]; then
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
