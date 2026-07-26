#!/bin/bash
set -uo pipefail

# CLI options
CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

YAML="samples/glass-optimize-demo.yaml"
OUTDIR="samples"
OPT_RESULT="$OUTDIR/glass-optimize-result.yaml"
OPT_CHIEF="$OUTDIR/glass-optimize-chief.yaml"
OPT_LOG="$OUTDIR/glass-optimize-log.jsonl"
RAYWEAVE="${RAYWEAVE:-./rayweave}"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  for f in "$OPT_RESULT" "$OPT_CHIEF" "$OPT_LOG"; do
    rm -f "$f"
  done
  rm -f "$OUTDIR"/glass-optimize-init.png "$OUTDIR"/glass-optimize-opt.png
  rm -f "$OUTDIR"/glass-chief-*.yaml "$OUTDIR"/glass-spot-*.txt "$OUTDIR"/glass-spot-*.png
  echo "  Removed generated files"
  exit 0
fi

echo "=== Glass optimization demo: 35mm-format 3-lens system ==="
echo
echo "Optical system:"
echo "  Surface 1-2: Singlet lens (model1, nd=1.5, vd=60)"
echo "  Surface 3-4: model2 lens (stop, 9mm)"
echo "  Surface 5-6: model3 lens"
echo "  Surface 7:   Image plane"
echo "  Fields: 0/10/16 degrees — 3 fields" 
echo "  Wavelengths: g(436nm) F(486nm) d(588nm) C(656nm) — 4 colors"
echo
echo "Vignetting control:"
echo "  auto_aperture on surfaces 1,2,4,5,6"
echo "  Surface 3: fixed stop (9mm)"
echo "  min_glass_path: 1.0mm (model1), 0.3mm (model2, model3)"
echo
echo "Initial glass values (deliberately wrong):"
echo "  model1: nd=1.500  vd=60.0  (crown)"
echo "  model2: nd=1.700  vd=30.0  ← extreme flint, wrong for this position"
echo "  model3: nd=1.500  vd=70.0  ← extreme crown, wrong for this position"
echo
echo "Optimization variables (14 total):"
echo "  curvatures: s1/s2/s3/s4/s5/s6"
echo "  glasses:    model1 nd/vd, model2 nd/vd, model3 nd/vd"
echo "  air gaps:   s2_thickness, s4_thickness"
echo

echo "=== DLS optimization ==="
$RAYWEAVE optimize --verbose --log "$OPT_LOG" < "$YAML" > "$OPT_RESULT"

echo
echo "--- Optimization results ---"
echo -n "  Status:      "
grep '"status"' "$OPT_LOG" | tail -1 | sed 's/.*"status":"//;s/".*//'
echo -n "  Iterations:  "
grep "finalLog\|status" "$OPT_LOG" | tail -1 | sed 's/.*"iter"://;s/,.*//'
grep -E "^=== |Status|Merit|Improvement" "$OPT_RESULT" 2>/dev/null | head -10
echo

echo "--- Glass before → after ---"
extract_glass_value() {
  local yaml="$1"
  local label="$2"
  local field="$3"
  grep -A3 "name: \"$label\"" "$yaml" | grep "$field:" | sed 's/.*'"$field"': *//' | head -1
}
INIT_ND1=$(extract_glass_value "$YAML" model1 nd)
INIT_VD1=$(extract_glass_value "$YAML" model1 vd)
INIT_ND2=$(extract_glass_value "$YAML" model2 nd)
INIT_VD2=$(extract_glass_value "$YAML" model2 vd)
INIT_ND3=$(extract_glass_value "$YAML" model3 nd)
INIT_VD3=$(extract_glass_value "$YAML" model3 vd)
# Optimized glasses: extract the last 3 nd/vd pairs from the result
OPT_NDS=$(grep -E "^\s+nd: [0-9]" "$OPT_RESULT" | tail -3)
OPT_VDS=$(grep -E "^\s+vd: [0-9]" "$OPT_RESULT" | tail -3)
OPT_ND1=$(echo "$OPT_NDS" | sed -n '1p' | sed 's/.*nd: *//')
OPT_VD1=$(echo "$OPT_VDS" | sed -n '1p' | sed 's/.*vd: *//')
OPT_ND2=$(echo "$OPT_NDS" | sed -n '2p' | sed 's/.*nd: *//')
OPT_VD2=$(echo "$OPT_VDS" | sed -n '2p' | sed 's/.*vd: *//')
OPT_ND3=$(echo "$OPT_NDS" | sed -n '3p' | sed 's/.*nd: *//')
OPT_VD3=$(echo "$OPT_VDS" | sed -n '3p' | sed 's/.*vd: *//')
echo "  model1: nd $INIT_ND1 → $OPT_ND1  vd $INIT_VD1 → $OPT_VD1"
echo "  model2: nd $INIT_ND2 → $OPT_ND2  vd $INIT_VD2 → $OPT_VD2"
echo "  model3: nd $INIT_ND3 → $OPT_ND3  vd $INIT_VD3 → $OPT_VD3"
echo

echo "--- Surface curvatures and diameters ---"
echo "  Surface  curv       diameter  material"
for ID in 1 2 3 4 5 6; do
  BLOCK=$(grep -A8 "id: $ID$" "$OPT_RESULT" 2>/dev/null || true)
  CV=$(echo "$BLOCK" | grep -E "curvature:|radius:" | sed 's/.*: //' 2>/dev/null || echo "?")
  DIAM=$(echo "$BLOCK" | grep "diameter:" | sed 's/.*diameter: //' 2>/dev/null || echo "?")
  MAT=$(echo "$BLOCK" | grep "material:" | sed 's/.*material: //' 2>/dev/null || echo "?")
  printf "  %-7s %-10s %-8s  %s\n" "S$ID" "$CV" "${DIAM:-?}" "$MAT"
done
echo

# Compute diffraction limit (best-effort)
FNO=$($RAYWEAVE paraxial < "$OPT_RESULT" 2>/dev/null | grep "inf_conj_image_space_f_number" | sed 's/.*f_number: //' || echo "")
if [ -n "$FNO" ]; then
  AIRY=$(echo "scale=6; 1.22 * 0.0005876 * $FNO" | bc -l 2>/dev/null || echo "0")
  MERIT=$(grep '"merit"' "$OPT_LOG" 2>/dev/null | tail -2 | head -1 | sed 's/.*"merit"://;s/,.*//' || echo "0")
  NTERMS=12
  RMS_R=$(echo "scale=6; sqrt($MERIT / $NTERMS)" | bc -l 2>/dev/null || echo "0")
  if [ "$RMS_R" != "0" ]; then
    echo "--- Diffraction limit ---"
    printf "  F-number:         %s\n" "$FNO"
    printf "  Airy disk radius: %.6f mm (1.22λF#)\n" "$AIRY"
    printf "  RMS spot radius:  %.6f mm\n" "$RMS_R"
    RATIO=$(echo "scale=1; $RMS_R / $AIRY" | bc -l 2>/dev/null || echo "0")
    echo "  Spot / Airy:      ${RATIO}x"
    echo
  fi
fi

# ── Spot RMS comparison (before vs after) ──
echo "=== Spot RMS Comparison ==="
printf "  %-8s %6s  %10s  %10s\n" "Phase" "Field" "RMS before" "RMS after"
printf "  %-8s %6s  %10s  %10s\n" "-----" "-----" "--------" "--------"
rms_field() {
  local yaml_file="$1"
  local fi="$2"
  python3 -c "
import sys, yaml
with open('/dev/stdin') as f:
    data = yaml.safe_load(f)
if data and 'chief_rays' in data and $fi < len(data['chief_rays']):
    ss = data['chief_rays'][$fi].get('spot_stats', {})
    rms = ss.get('rms_r', -1)
    if rms > 0:
        print(rms)
    else:
        print(-1)
else:
    print(-1)
" < <($RAYWEAVE chief < "$yaml_file" 2>/dev/null)
}
for fi in 0 1 2; do
  rms_before=$(rms_field "$YAML" "$fi")
  rms_after=$(rms_field "$OPT_RESULT" "$fi")
  printf "  %-8s %6s  %10.4f  %10.4f" "optimize" "f$fi" "$rms_before" "$rms_after"
  if [ "$(echo "$rms_after < $rms_before" | bc -l 2>/dev/null)" = "1" ]; then
    echo "   ✓"
  else
    echo "   ✗"
  fi
done
echo

echo "=== PNG diagrams ==="
$RAYWEAVE chief --clear-aperture < "$YAML" 2>/dev/null \
  | $RAYWEAVE chief --marginal-rays 2>/dev/null \
  | $RAYWEAVE trace 2>/dev/null \
  | $RAYWEAVE plot -o "$OUTDIR/glass-optimize-init.png" 2>/dev/null || true
echo "Written: $OUTDIR/glass-optimize-init.png"

$RAYWEAVE chief --clear-aperture < "$OPT_RESULT" 2>/dev/null \
  | $RAYWEAVE chief --marginal-rays 2>/dev/null \
  | $RAYWEAVE trace 2>/dev/null \
  | $RAYWEAVE plot -o "$OUTDIR/glass-optimize-opt.png" 2>/dev/null || true
echo "Written: $OUTDIR/glass-optimize-opt.png"
echo

if command -v yq &>/dev/null && command -v gnuplot &>/dev/null 2>&1; then

echo "=== Spot diagrams (4 wavelengths, before vs after) ==="

WL_NAMES=("g" "F" "d" "C")
WL_VALUES=(0.0004358 0.0004861 0.0005876 0.0006563)
WL_COLORS=("#1a237e" "#1565c0" "#2e7d32" "#c62828")
NTN_NAMES=("g(436nm)" "F(486nm)" "d(588nm)" "C(656nm)")

# For each wavelength, run chief and extract spots
for wli in 0 1 2 3; do
  wl="${WL_VALUES[$wli]}"
  for phase in init opt; do
    case $phase in
      init) INPUT="$YAML";;
      opt)  INPUT="$OPT_RESULT";;
    esac
    CHIEF_OUT="$OUTDIR/glass-chief-${phase}-wl${wli}.yaml"
    $RAYWEAVE chief --wl "$wl" < "$INPUT" > "$CHIEF_OUT" 2>/dev/null || true
    for fi in 0 1 2; do
      yq eval ".chief_rays[$fi].grid_points[] | select(.image_x != null) | [.image_x, .image_y, $wli]" \
        "$CHIEF_OUT" -o=csv 2>/dev/null | tail -n +2 \
        >> "$OUTDIR/glass-spot-${phase}-f${fi}-all.txt" || true
    done
  done
done

export GNUTERM=pngcairo
for fi in 0 1 2; do
  case $fi in
    0) FLABEL="on-axis";;
    1) FLABEL="10deg";;
    2) FLABEL="16deg";;
  esac
  gnuplot 2>/dev/null <<GPLOT
    set terminal pngcairo size 1000,450
    set output "$OUTDIR/glass-spot-f${fi}.png"

    set datafile separator ","

    # palette: g=darkblue, F=blue, d=green, C=red
    set palette defined (0 "#1a237e", 1 "#1565c0", 2 "#2e7d32", 3 "#c62828")
    set cbrange [0:3]

    # compute centroid (all wavelengths combined)
    stats "$OUTDIR/glass-spot-init-f${fi}-all.txt" using 1:2 nooutput
    cx_init = STATS_mean_x; cy_init = STATS_mean_y
    stats "$OUTDIR/glass-spot-opt-f${fi}-all.txt" using 1:2 nooutput
    cx_opt = STATS_mean_x; cy_opt = STATS_mean_y

    # global range from centered data (all wl)
    set term push; set terminal unknown
    plot "$OUTDIR/glass-spot-init-f${fi}-all.txt" using (\$1-cx_init):(\$2-cy_init)
    xmin = GPVAL_DATA_X_MIN; xmax = GPVAL_DATA_X_MAX
    ymin = GPVAL_DATA_Y_MIN; ymax = GPVAL_DATA_Y_MAX
    plot "$OUTDIR/glass-spot-opt-f${fi}-all.txt" using (\$1-cx_opt):(\$2-cy_opt)
    if (GPVAL_DATA_X_MIN < xmin) { xmin = GPVAL_DATA_X_MIN }
    if (GPVAL_DATA_X_MAX > xmax) { xmax = GPVAL_DATA_X_MAX }
    if (GPVAL_DATA_Y_MIN < ymin) { ymin = GPVAL_DATA_Y_MIN }
    if (GPVAL_DATA_Y_MAX > ymax) { ymax = GPVAL_DATA_Y_MAX }
    set term pop
    dx = xmax - xmin; dy = ymax - ymin
    range = (dx > dy ? dx : dy) * 0.6; if (range < 0.005) { range = 0.005 }

    set multiplot layout 1,2 title "$FLABEL spot diagram (g/F/d/C, centered)"
    set xlabel "dx (mm)"; set ylabel "dy (mm)"
    set xrange [-range:range]; set yrange [-range:range]
    set size square; set key outside right

    # before
    set title "before"
    plot "$OUTDIR/glass-spot-init-f${fi}-all.txt" using (\$1-cx_init):(\$2-cy_init):3 \
      with points pt 7 ps 1.5 lc palette title "", \
      keyentry with lines lc rgb "#1a237e" title "g(436nm)", \
      keyentry with lines lc rgb "#1565c0" title "F(486nm)", \
      keyentry with lines lc rgb "#2e7d32" title "d(588nm)", \
      keyentry with lines lc rgb "#c62828" title "C(656nm)"

    # after
    set title "after"
    plot "$OUTDIR/glass-spot-opt-f${fi}-all.txt" using (\$1-cx_opt):(\$2-cy_opt):3 \
      with points pt 7 ps 1.5 lc palette title "", \
      keyentry with lines lc rgb "#1a237e" title "g(436nm)", \
      keyentry with lines lc rgb "#1565c0" title "F(486nm)", \
      keyentry with lines lc rgb "#2e7d32" title "d(588nm)", \
      keyentry with lines lc rgb "#c62828" title "C(656nm)"
    unset multiplot
GPLOT
  if [ -f "$OUTDIR/glass-spot-f${fi}.png" ]; then
    echo "Written: $OUTDIR/glass-spot-f${fi}.png (4 wavelengths)"
  fi
done
echo

else
  echo "  (spot diagrams skipped: yq or gnuplot not available)"
fi

echo "=== Iteration log saved: $OPT_LOG ==="