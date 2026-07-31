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
  rm -f "$OUTDIR"/aberr-*.txt "$OUTDIR"/aberr-*.png
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
echo "=== Chief ray computation (with ray fan) ==="
./rayweave chief --ray-fan < "$YAML" > "$CHIEF_RESULT"

extract_spot() {
  local field_index=$1 label=$2
  yq eval ".chief_rays[$field_index].grid_points[] | select(.image_x != null) | [.image_x, .image_y, .intensity]" \
    "$CHIEF_RESULT" -o=csv 2>/dev/null | tail -n +2 > "$OUTDIR/spot-${label}.txt"
}

extract_spot 0 "00"
extract_spot 1 "f16"
extract_spot 2 "f24"

# 1b. Aberration data extraction (full-resolution fan rays from chief output)
extract_aberration() {
  local field_index=$1 label=$2
  yq eval ".chief_rays[$field_index].ray_fan.meridional[] | [.py, .ey, (.long // 0)]" \
    "$CHIEF_RESULT" -o=csv 2>/dev/null | tail -n +2 > "$OUTDIR/aberr-${label}-m.txt"
  yq eval ".chief_rays[$field_index].ray_fan.sagittal[] | [.px, .ex, (.long // 0)]" \
    "$CHIEF_RESULT" -o=csv 2>/dev/null | tail -n +2 > "$OUTDIR/aberr-${label}-s.txt"
}

extract_aberration 0 "00"
extract_aberration 1 "f16"
extract_aberration 2 "f24"

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

# 2b. Aberration graphs: transverse (横収差) + longitudinal (縦収差)
if command -v yq &>/dev/null && command -v gnuplot &>/dev/null 2>&1; then
  for field in 00 f16 f24; do
    case $field in
      00)  title="0° field";;
      f16) title="16° field";;
      f24) title="24° field";;
    esac
    gnuplot 2>/dev/null <<GPLOT
      set terminal pngcairo size 900,900
      set output "$OUTDIR/aberr-${field}.png"
      set datafile separator ","
      set multiplot layout 2,1 title "$title aberration"

      set key top left
      set grid xtics ytics lc rgb "#d0d0d0"

      # Transverse ray aberration (横収差): pupil vs image error
      set title "Transverse ray aberration"
      set xlabel "pupil position (mm)"
      set ylabel "image error EY/EX (mm)"
      plot "$OUTDIR/aberr-${field}-m.txt" using 1:2 with lines lw 2 lc rgb "#1f77b4" title "meridional EY", \
           "$OUTDIR/aberr-${field}-s.txt" using 1:2 with lines lw 2 lc rgb "#d62728" title "sagittal EX"

      # Longitudinal aberration (縦収差): pupil vs axial focus shift
      set title "Longitudinal aberration"
      set xlabel "pupil position (mm)"
      set ylabel "focus shift (mm)"
      plot "$OUTDIR/aberr-${field}-m.txt" using 1:3 with lines lw 2 lc rgb "#1f77b4" title "meridional LONG", \
           "$OUTDIR/aberr-${field}-s.txt" using 1:3 with lines lw 2 lc rgb "#d62728" title "sagittal LONG"

      unset multiplot
GPLOT
    echo "Written: $OUTDIR/aberr-${field}.png"
  done
else
  echo "  (aberration graphs skipped: yq or gnuplot not available)"
fi

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
./rayweave chief --clear-aperture --ray-fan < "$YAML" \
  | ./rayweave chief --marginal-rays --ray-fan \
  | ./rayweave trace \
  | tee "$OUTDIR/us2645157-trace-result.yaml" \
        >(./rayweave plot -o "$OUTDIR/us2645157.png" > /dev/null) \
  | ./rayweave plot -o "$OUTDIR/us2645157.svg" > /dev/null
echo "Written: $OUTDIR/us2645157.svg and $OUTDIR/us2645157.png"

echo
echo "=== TMM: single-layer AR coating (MgF2 on N-SK16, lambda=550nm) ==="
./rayweave tmm < "$OUTDIR/ar-coating.yaml" | yq '.rs, .rp'

echo
echo "=== TMM: 9-layer dielectric mirror (SiO2/TiO2 on glass) ==="
./rayweave tmm < "$OUTDIR/dielectric-mirror.yaml" | yq '.rs, .rp'
