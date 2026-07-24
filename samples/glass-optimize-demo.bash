#!/bin/bash
set -euo pipefail

YAML="samples/glass-optimize-demo.yaml"
OUTDIR="samples"
OPT_RESULT="$OUTDIR/glass-optimize-result.yaml"
OPT_CHIEF="$OUTDIR/glass-optimize-chief.yaml"
OPT_WITH_CHIEF="$OUTDIR/glass-opt-with-chief.yaml"
RAYWEAVE="${RAYWEAVE:-./rayweave}"

echo "=== Glass optimization demo: 35mm-format 3-lens system ==="
echo
echo "Optical system (35mm full-frame):"
echo "  Surface 1-2: Singlet lens (model1, nd=1.5, vd=60)"
echo "  Surface 3-4: model2 lens (stop)"
echo "  Surface 5-6: model3 lens"
echo "  Surface 7:   Image plane"
echo "  Fields: 0° (on-axis), 14°, 20° (full 35mm coverage)"
echo
echo "Vignetting control:"
echo "  auto_aperture on surfaces 1,2,4,5,6 (dynamic clear aperture)"
echo "  Surface 3: fixed stop (22mm)"
echo "  min_glass_path: 2.0mm (lens 1), 1.0mm (lenses 2, 3)"
echo
echo "Optimization variables:"
echo "  - s1_curvature   (lens 1 front)"
echo "  - s2_curvature   (lens 1 back)"
echo "  - s3_curvature   (lens 2 front = stop)"
echo "  - s4_curvature   (lens 2 back)"
echo "  - s5_curvature   (lens 3 front)"
echo "  - s6_curvature   (lens 3 back)"
echo "  - lens_nd        (lens 1 refractive index nd)"
echo "  - lens_vd        (lens 1 Abbe number vd)"
echo "  - s2_thickness   (air gap lens1→lens2)"
echo "  - s4_thickness   (air gap lens2→lens3)"
echo

echo "=== DLS optimization ==="
$RAYWEAVE optimize --verbose < "$YAML" > "$OPT_RESULT"

echo
echo "--- Optimized 3-lens system ---"
OPT_GLASS_ND=$(grep -E "^\s+nd: [0-9]" "$OPT_RESULT" | tail -1 | sed 's/.*nd: //')
OPT_GLASS_VD=$(grep "vd:" "$OPT_RESULT" | tail -1 | sed 's/.*vd: //')
echo "  Lens 1 glass: nd=$OPT_GLASS_ND  vd=$OPT_GLASS_VD"
echo
echo "  Surface  curv       diameter  material"
for ID in 1 2 3 4 5 6; do
  BLOCK=$(grep -A8 "id: $ID$" "$OPT_RESULT")
  CV=$(echo "$BLOCK" | grep -E "curvature:|radius:" | sed 's/.*: //')
  DIAM=$(echo "$BLOCK" | grep "diameter:" | sed 's/.*diameter: //')
  MAT=$(echo "$BLOCK" | grep "material:" | sed 's/.*material: //')
  printf "  %-7s %-10s %-8s  %s\n" "S$ID" "$CV" "${DIAM:-?}" "$MAT"
done
echo

echo "=== Initial SVG diagram ==="
$RAYWEAVE chief --clear-aperture < "$YAML" | $RAYWEAVE chief --marginal-rays \
  | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/glass-optimize-init.svg" 2>/dev/null
echo "Written: $OUTDIR/glass-optimize-init.svg"
echo

echo "=== Optimized SVG diagram ==="
cat > "$OPT_CHIEF" <<CHIEFEOF
chief:
  fields:
    - angle: 0.0
      direction: [0, 1]
    - angle: 14.0
      direction: [0, 1]
    - angle: 20.0
      direction: [0, 1]
  reference_surface: 7
  num_rays: 512
  grid_type: hex
  dump_map: false
CHIEFEOF
# merge chief into optimized result
$RAYWEAVE chief < "$OPT_RESULT" > /dev/null 2>&1 && HAVE_CHIEF=1 || HAVE_CHIEF=0
if [ "$HAVE_CHIEF" -eq 1 ]; then
  $RAYWEAVE chief --clear-aperture < "$OPT_RESULT" | $RAYWEAVE chief --marginal-rays \
    | $RAYWEAVE trace \
    | $RAYWEAVE plot -o "$OUTDIR/glass-optimize-opt.svg" 2>/dev/null
  echo "Written: $OUTDIR/glass-optimize-opt.svg"
else
  echo "(skipping optimized SVG - chief requires traceable rays)"
fi
echo
