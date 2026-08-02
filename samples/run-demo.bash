#!/bin/bash
set -euo pipefail

# =============================================================================
# run-demo.bash — end-to-end ray-tracing demo (US2645157 patent triplet)
#
# Purpose: walk through the standard pipeline `chief -> trace -> plot` plus
# spot diagrams, aberration graphs, paraxial analysis and thin-film (TMM)
# coating evaluation, all on a single input file.
#
# Pipeline steps
#   1. chief --ray-fan      : chief ray + hexagonal pupil grid per field
#                             (0/16/24 deg) -> us2645157-chief-result.yaml
#   2. spot-*.txt + gnuplot : PNG spot diagram per field
#   3. aberr-*.txt + gnuplot: transverse (EY/EX) and longitudinal (focus
#                             shift) aberration graphs per field
#   4. trace                : ray-path tables (surface-by-surface coordinates)
#   5. paraxial             : EFL, f/#, pupils (with and without chief data)
#   6. plot                 : SVG/PNG raytrace diagram
#   7. tmm                  : AR-coating (MgF2) and dielectric-mirror
#                             reflectance via the transfer-matrix method
#
# How to read the output
#   - Spot PNGs: the tighter the point cloud, the better the imaging.
#   - Aberration PNGs: flat lines through zero = perfect correction.
#   - Trace tables: y/z of the ray at every surface.
#   - Paraxial block: check the EFL (~25 mm) and the entrance/exit pupils.
#   - TMM: AR coating rs/rp ~ 0 at 550 nm; mirror rs/rp ~ 1.
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

YAML="$SCRIPT_DIR/us2645157.yaml"
OUTDIR="$SCRIPT_DIR"
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

run_trace() {
  local input=$1 idx=$2
  echo "=== Trace: ray paths through surfaces ==="
  echo
  echo "| s | x (mm) | y (mm) | z (mm) |"
  echo "|---|--------|--------|--------|"
  $RAYWEAVE trace < "$input" \
    | $RAYWEAVE query --each "results[$idx].surfaces[]:surface_id,position[0],position[1],position[2]" \
        --printf '| %d | %.4f | %.4f | %.4f |'
  echo
}

# 1. Chief ray computation and grid data extraction
echo "=== Chief ray computation (with ray fan) ==="
$RAYWEAVE chief --ray-fan < "$YAML" > "$CHIEF_RESULT"

extract_spot() {
  local field_index=$1 label=$2
  $RAYWEAVE query --csv "chief_rays[$field_index].grid_points[]:image_x,image_y,intensity" \
    < "$CHIEF_RESULT" > "$OUTDIR/spot-${label}.txt"
}

extract_spot 0 "00"
extract_spot 1 "f16"
extract_spot 2 "f24"

# 1b. Aberration data extraction (full-resolution fan rays from chief output)
extract_aberration() {
  local field_index=$1 label=$2
  $RAYWEAVE query --csv "chief_rays[$field_index].ray_fan.meridional[]:py,ey,long" \
    < "$CHIEF_RESULT" > "$OUTDIR/aberr-${label}-m.txt"
  $RAYWEAVE query --csv "chief_rays[$field_index].ray_fan.sagittal[]:px,ex,long" \
    < "$CHIEF_RESULT" > "$OUTDIR/aberr-${label}-s.txt"
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
if command -v gnuplot &>/dev/null 2>&1; then
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
  echo "  (aberration graphs skipped: gnuplot not available)"
fi

echo

# 3. Trace chief ray paths
echo "=== Trace (post-chief) ==="
run_trace "$CHIEF_RESULT" 0
run_trace "$CHIEF_RESULT" 1
run_trace "$CHIEF_RESULT" 2

echo "=== Paraxial analysis ==="
echo 'without chief data:'
$RAYWEAVE paraxial < "$YAML" | $RAYWEAVE query --yaml paraxial_result
echo 'with chief data:'
$RAYWEAVE paraxial < "$CHIEF_RESULT" | $RAYWEAVE query --yaml paraxial_result

echo
echo "=== Raytrace diagram (SVG + PNG) ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$YAML" \
  | $RAYWEAVE plot -o "$OUTDIR/us2645157.png" \
  | $RAYWEAVE plot -o "$OUTDIR/us2645157.svg" \
  | $RAYWEAVE chief --marginal-rays --ray-fan \
  | $RAYWEAVE trace > "$OUTDIR/us2645157-trace-result.yaml"
echo "Written: $OUTDIR/us2645157.svg and $OUTDIR/us2645157.png"

echo
echo "=== TMM: single-layer AR coating (MgF2 on N-SK16, lambda=550nm) ==="
AR=$($RAYWEAVE tmm < "$OUTDIR/ar-coating.yaml")
echo "rs=$($RAYWEAVE query -r rs <<< "$AR")  rp=$($RAYWEAVE query -r rp <<< "$AR")"

echo
echo "=== TMM: 9-layer dielectric mirror (SiO2/TiO2 on glass) ==="
MIRROR=$($RAYWEAVE tmm < "$OUTDIR/dielectric-mirror.yaml")
echo "rs=$($RAYWEAVE query -r rs <<< "$MIRROR")  rp=$($RAYWEAVE query -r rp <<< "$MIRROR")"
