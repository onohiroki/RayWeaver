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
echo "  Fields: 0/12/18 degrees — 3 fields" 
echo "  Wavelengths: g(436nm) F(486nm) d(588nm) C(656nm) — 4 colors"
echo
echo "Vignetting control:"
echo "  auto_aperture on surfaces 1,2,4,5,6"
echo "  Surface 3: fixed stop (9mm)"
echo "  min_glass_path: 1.0mm (model1), 0.5mm (model2, model3)"
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

$RAYWEAVE chief --clear-aperture < "$OPT_RESULT" 2>/dev/null \
  | $RAYWEAVE chief --marginal-rays 2>/dev/null \
  | $RAYWEAVE trace 2>/dev/null \
  | $RAYWEAVE plot -o "$OUTDIR/glass-optimize-opt.svg" 2>/dev/null || true
echo "Written: $OUTDIR/glass-optimize-opt.svg"
echo

echo "=== Iteration log saved: $OPT_LOG ==="