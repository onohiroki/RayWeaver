#!/bin/bash
set -euo pipefail

# CLI options
CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

YAML="samples/us2645157.yaml"
OUTDIR="samples"
CHIEF_RESULT="$OUTDIR/us2645157-chief-result.yaml"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$CHIEF_RESULT"
  rm -f "$OUTDIR"/us2645157-trace-result.yaml
  rm -f "$OUTDIR"/us2645157.svg "$OUTDIR"/us2645157.png
  rm -f "$OUTDIR"/spot-*.txt "$OUTDIR"/spot-*.png
  echo "  Removed generated files"
  exit 0
fi

run_trace() {
  local input=$1 idx=$2
  echo "=== Trace: ray paths through surfaces ==="
  ./rayweave trace < "$input" \
    | yq '[.results[]|[.surfaces[] | {"s": .surface_id, "x": .position[0], "y": .position[1], "z": .position[2] }]] | .['$idx']' -o=csv \
    | csvtk csv2md
  echo
}

# 1. Chief ray computation and grid data extraction
echo "=== Chief ray computation ==="
./rayweave chief < "$YAML" > "$CHIEF_RESULT"

extract_spot() {
  local field_index=$1 label=$2
  yq eval ".chief_rays[$field_index].grid_points[] | select(.image_x != null) | [.image_x, .image_y, .intensity]" \
    "$CHIEF_RESULT" -o=csv 2>/dev/null | tail -n +2 > "$OUTDIR/spot-${label}.txt"
}

extract_spot 0 "00"
extract_spot 1 "f16"
extract_spot 2 "f24"

# 2. PNG output
export GNUTERM=pngcairo

for field in 00 f16 f24; do
  case $field in
    00)  title="0° field";;
    f16) title="16° field";;
    f24) title="24° field";;
  esac
  gnuplot -e "
    set terminal pngcairo size 600,600;
    set output '$OUTDIR/spot-${field}.png';
    set datafile separator comma;
    set title '$title - spot diagram';
    set xlabel 'image X (mm)';
    set ylabel 'image Y (mm)';
    set cbrange [0.95:1.00];
    plot '$OUTDIR/spot-${field}.txt' using 1:2:3 \
      with points pt 7 ps 2 palette title ''
  " 2>/dev/null || true
done

echo

# 3. Trace chief ray paths
echo "=== Trace (post-chief) ==="
run_trace "$CHIEF_RESULT" 0
run_trace "$CHIEF_RESULT" 1
run_trace "$CHIEF_RESULT" 2

echo "=== Paraxial analysis ==="
echo 'without chief data:'
./rayweave paraxial < "$YAML" | yq '.paraxial_result'
echo 'with chief data:'
./rayweave paraxial < "$CHIEF_RESULT" | yq '.paraxial_result'

echo
echo "=== Raytrace diagram (SVG + PNG) ==="
./rayweave chief --clear-aperture < "$YAML" \
  | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | tee "$OUTDIR/us2645157-trace-result.yaml" \
        >(./rayweave plot -o "$OUTDIR/us2645157.png") \
  | ./rayweave plot -o "$OUTDIR/us2645157.svg"
echo "Written: $OUTDIR/us2645157.svg and $OUTDIR/us2645157.png"

echo
echo "=== TMM: single-layer AR coating (MgF2 on N-SK16, lambda=550nm) ==="
./rayweave tmm < "$OUTDIR/ar-coating.yaml" | yq '.rs, .rp'

echo
echo "=== TMM: 9-layer dielectric mirror (SiO2/TiO2 on glass) ==="
./rayweave tmm < "$OUTDIR/dielectric-mirror.yaml" | yq '.rs, .rp'
