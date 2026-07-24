#!/bin/bash

YAML="samples/glass-optimize-demo.yaml"
OUTDIR="samples"
OPT_RESULT="$OUTDIR/glass-optimize-result.yaml"
OPT_CHIEF="$OUTDIR/glass-optimize-chief.yaml"
OPT_LOG="$OUTDIR/glass-optimize-log.jsonl"
RAYWEAVE="${RAYWEAVE:-./rayweave}"

echo "=== Glass optimization demo: 35mm-format 3-lens system ==="
echo
echo "Optical system:"
echo "  Surface 1-2: Singlet lens (model1, nd=1.5, vd=60)"
echo "  Surface 3-4: model2 lens (stop, 9mm)"
echo "  Surface 5-6: model3 lens"
echo "  Surface 7:   Image plane"
echo "  Fields: 0/10/15 degrees — 3 fields" 
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
# Extract initial glass values from YAML
INIT_ND1=$(grep -A2 "name: \"model1\"" "$YAML" | grep "nd:" | sed 's/.*nd: //')
INIT_VD1=$(grep -A2 "name: \"model1\"" "$YAML" | grep "vd:" | sed 's/.*vd: //')
INIT_ND2=$(grep -A2 "name: \"model2\"" "$YAML" | grep "nd:" | sed 's/.*nd: //')
INIT_VD2=$(grep -A2 "name: \"model2\"" "$YAML" | grep "vd:" | sed 's/.*vd: //')
INIT_ND3=$(grep -A2 "name: \"model3\"" "$YAML" | grep "nd:" | sed 's/.*nd: //')
INIT_VD3=$(grep -A2 "name: \"model3\"" "$YAML" | grep "vd:" | sed 's/.*vd: //')
# Optimized glasses are the last 3 nd/vd pairs in the glass entries
OPT_ND3=$(grep -E "^\s+nd: [0-9]" "$OPT_RESULT" | tail -1 | sed 's/.*nd: //')
OPT_VD3=$(grep -E "^\s+vd: [0-9]" "$OPT_RESULT" | tail -1 | sed 's/.*vd: //')
OPT_ND2=$(grep -E "^\s+nd: [0-9]" "$OPT_RESULT" | tail -3 | sed -n '2p' | sed 's/.*nd: //')
OPT_VD2=$(grep -E "^\s+vd: [0-9]" "$OPT_RESULT" | tail -3 | sed -n '2p' | sed 's/.*vd: //')
OPT_ND1=$(grep -E "^\s+nd: [0-9]" "$OPT_RESULT" | tail -3 | sed -n '1p' | sed 's/.*nd: //')
OPT_VD1=$(grep -E "^\s+vd: [0-9]" "$OPT_RESULT" | tail -3 | sed -n '1p' | sed 's/.*vd: //')
echo "  model1: nd $INIT_ND1 → $OPT_ND1  vd $INIT_VD1 → $OPT_VD1"
echo "  model2: nd $INIT_ND2 → $OPT_ND2  vd $INIT_VD2 → $OPT_VD2"
echo "  model3: nd $INIT_ND3 → $OPT_ND3  vd $INIT_VD3 → $OPT_VD3"
echo

echo "--- Surface curvatures and diameters ---"
echo "  Surface  curv       diameter  material"
for ID in 1 2 3 4 5 6; do
  BLOCK=$(grep -A8 "id: $ID$" "$OPT_RESULT")
  CV=$(echo "$BLOCK" | grep -E "curvature:|radius:" | sed 's/.*: //')
  DIAM=$(echo "$BLOCK" | grep "diameter:" | sed 's/.*diameter: //')
  MAT=$(echo "$BLOCK" | grep "material:" | sed 's/.*material: //')
  printf "  %-7s %-10s %-8s  %s\n" "S$ID" "$CV" "${DIAM:-?}" "$MAT"
done
echo

# Compute diffraction limit
FNO=$($RAYWEAVE paraxial < "$OPT_RESULT" 2>/dev/null | grep "inf_conj_image_space_f_number" | sed 's/.*f_number: //')
if [ -n "$FNO" ]; then
  AIRY=$(echo "scale=6; 1.22 * 0.0005876 * $FNO" | bc -l 2>/dev/null || echo "0")
  # Extract final merit from the log file (last iter before final entry)
  MERIT=$(grep '"merit"' "$OPT_LOG" | tail -2 | head -1 | sed 's/.*"merit"://;s/,.*//' 2>/dev/null || echo "0")
  NTERMS=12
  RMS_R=$(echo "scale=6; sqrt($MERIT / $NTERMS)" | bc -l 2>/dev/null || echo "0")
  echo "--- Diffraction limit ---"
  printf "  F-number:         %s\n" "$FNO"
  printf "  Airy disk radius: %.6f mm (1.22λF#)\n" "$AIRY"
  printf "  RMS spot radius:  %.6f mm\n" "$RMS_R"
  if [ "$(echo "$RMS_R > 0" | bc -l 2>/dev/null)" = "1" ]; then
    RATIO=$(echo "scale=1; $RMS_R / $AIRY" | bc -l 2>/dev/null || echo "0")
    echo "  Spot / Airy:      ${RATIO}x"
  fi
  echo
fi

echo "=== SVG diagrams ==="
$RAYWEAVE chief --clear-aperture < "$YAML" 2>/dev/null \
  | $RAYWEAVE chief --marginal-rays 2>/dev/null \
  | $RAYWEAVE trace 2>/dev/null \
  | $RAYWEAVE plot -o "$OUTDIR/glass-optimize-init.svg" 2>/dev/null || true
echo "Written: $OUTDIR/glass-optimize-init.svg"
if command -v rsvg-convert >/dev/null 2>&1; then
  rsvg-convert "$OUTDIR/glass-optimize-init.svg" -o "$OUTDIR/glass-optimize-init.png" 2>/dev/null && echo "  PNG: $OUTDIR/glass-optimize-init.png"
fi

$RAYWEAVE chief --clear-aperture < "$OPT_RESULT" 2>/dev/null \
  | $RAYWEAVE chief --marginal-rays 2>/dev/null \
  | $RAYWEAVE trace 2>/dev/null \
  | $RAYWEAVE plot -o "$OUTDIR/glass-optimize-opt.svg" 2>/dev/null || true
echo "Written: $OUTDIR/glass-optimize-opt.svg"
if command -v rsvg-convert >/dev/null 2>&1; then
  rsvg-convert "$OUTDIR/glass-optimize-opt.svg" -o "$OUTDIR/glass-optimize-opt.png" 2>/dev/null && echo "  PNG: $OUTDIR/glass-optimize-opt.png"
fi
echo

echo "=== Spot diagrams (d-line, before vs after) ==="
# Collect spot data for initial design
INIT_CHIEF="$OUTDIR/glass-init-chief.yaml"
$RAYWEAVE chief < "$YAML" > "$INIT_CHIEF" 2>/dev/null || true

# Extract spot data for each field
for fi in 0 1 2; do
  # initial spots
  yq eval ".chief_rays[$fi].grid_points[] | select(.image_x != null) | [.image_x, .image_y, .intensity]" \
    "$INIT_CHIEF" -o=csv 2>/dev/null | tail -n +2 > "$OUTDIR/glass-spot-init-f${fi}.txt" || true
  # optimized spots
  OPT_CHIEF_FILE="$OUTDIR/glass-opt-chief.yaml"
  $RAYWEAVE chief < "$OPT_RESULT" > "$OPT_CHIEF_FILE" 2>/dev/null || true
  yq eval ".chief_rays[$fi].grid_points[] | select(.image_x != null) | [.image_x, .image_y, .intensity]" \
    "$OPT_CHIEF_FILE" -o=csv 2>/dev/null | tail -n +2 > "$OUTDIR/glass-spot-opt-f${fi}.txt" || true
done

# Determine global scale from both init and opt spots
XMIN=999; XMAX=-999; YMIN=999; YMAX=-999; HAS_DATA=0
for fi in 0 1 2; do
  for phase in init opt; do
    F="$OUTDIR/glass-spot-${phase}-f${fi}.txt"
    if [ -f "$F" ] && [ -s "$F" ]; then
      read xm xx ym yx <<<$(awk -F, 'BEGIN{xm=999;xx=-999;ym=999;yx=-999}{if($1<xm)xm=$1;if($1>xx)xx=$1;if($2<ym)ym=$2;if($2>yx)yx=$2} END{print xm,xx,ym,yx}' "$F")
      XMIN=$(echo "scale=6; if($xm<$XMIN) $xm else $XMIN" | bc -l 2>/dev/null || echo "-0.5")
      XMAX=$(echo "scale=6; if($xx>$XMAX) $xx else $XMAX" | bc -l 2>/dev/null || echo "0.5")
      YMIN=$(echo "scale=6; if($ym<$YMIN) $ym else $YMIN" | bc -l 2>/dev/null || echo "-0.5")
      YMAX=$(echo "scale=6; if($yx>$YMAX) $yx else $YMAX" | bc -l 2>/dev/null || echo "0.5")
      HAS_DATA=1
    fi
  done
done
if [ "$HAS_DATA" -eq 0 ]; then
  XMIN=-0.5; XMAX=0.5; YMIN=-0.5; YMAX=0.5
fi
# Add 10% margin
RANGE=$(echo "scale=6; dx=$XMAX-$XMIN; dy=$YMAX-$YMIN; m=sqrt(dx*dx+dy*dy); if(m<0.001)m=0.1; m*0.55" | bc -l 2>/dev/null || echo "0.1")
XLOW=$(echo "$XMIN - $RANGE" | bc -l 2>/dev/null || echo "-0.6")
XHIGH=$(echo "$XMAX + $RANGE" | bc -l 2>/dev/null || echo "0.6")
YLOW=$(echo "$YMIN - $RANGE" | bc -l 2>/dev/null || echo "-0.6")
YHIGH=$(echo "$YMAX + $RANGE" | bc -l 2>/dev/null || echo "0.6")

export GNUTERM=pngcairo
for fi in 0 1 2; do
  case $fi in
    0) FLABEL="on-axis";;
    1) FLABEL="12deg";;
    2) FLABEL="18deg";;
  esac
  gnuplot 2>/dev/null <<GPLOT
    set terminal pngcairo size 800,400
    set output "$OUTDIR/glass-spot-f${fi}.png"
    set datafile separator comma
    set multiplot layout 1,2 title "$FLABEL spot diagram (d-line)"
    set xlabel "image X (mm)"
    set ylabel "image Y (mm)"
    set cbrange [0.95:1.00]
    set xrange [$XLOW:$XHIGH]; set yrange [$YLOW:$YHIGH]
    set title "before"
    plot "$OUTDIR/glass-spot-init-f${fi}.txt" using 1:2:3 \
      with points pt 7 ps 2 palette title ""
    set title "after"
    plot "$OUTDIR/glass-spot-opt-f${fi}.txt" using 1:2:3 \
      with points pt 7 ps 2 palette title ""
    unset multiplot
GPLOT
  if [ -f "$OUTDIR/glass-spot-f${fi}.png" ]; then
    echo "Written: $OUTDIR/glass-spot-f${fi}.png"
  fi
done
echo

echo "=== Iteration log saved: $OPT_LOG ==="