#!/bin/bash
set -euo pipefail

# =============================================================================
# psf-demo.bash — point-spread function demo (US2645157 patent triplet)
#
# Purpose: compute the PSF of all three fields (0/16/24 deg) of the
# US2645157 triplet on the fixed flat image plane via the `psf` subcommand
# (per-field polarized ray tracing -> non-uniform wavefront sampling ->
# direct vector Huygens integral, no FFT), then:
#   1. print a comparison table (Strehl, FWHM, encircled-energy 50%, Airy
#      radius, sampling counts) to stdout and psf-demo-result.txt,
#   2. draw a 2D pm3d intensity map PNG per field,
#   3. draw one radial-profile overlay PNG (all fields through their peak).
#
# Input polarization is RCP+LCP (the polarization-averaged / unpolarised PSF).
#
# Options
#   --clean      remove every generated psf-demo-* artifact; the tracked
#                us2645157.yaml input is never touched.
#   --num-rays N pupil grid rays (default 400)
#   --psf-grid N image-plane pixels per side (default 96)
#
# How to read the output
#   - 0° is near diffraction-limited: a tight Airy core with a bright Strehl
#     (~0.96) and symmetric FWHM.
#   - 16° / 24° are strongly aberrated: Strehl drops to ~0.02 / ~0.004, the
#     FWHM becomes asymmetric (astigmatism / coma), and the encircled-energy
#     50% radius grows well beyond the Airy radius.
#   - The radial overlay shows the 0° ring structure (first dark ring at
#     ~0.61·λ/NA) versus the broadened off-axis profiles.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTDIR="$SCRIPT_DIR"

NUM_RAYS=400
PSF_GRID=96
CLEAN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    --num-rays)
      NUM_RAYS="$2"; shift 2
      if ! awk -v x="$NUM_RAYS" 'BEGIN { exit !(x >= 16) }' /dev/null; then
        echo "error: --num-rays expects an integer >= 16 (got '$NUM_RAYS')" >&2
        exit 1
      fi
      ;;
    --psf-grid)
      PSF_GRID="$2"; shift 2
      if ! awk -v x="$PSF_GRID" 'BEGIN { exit !(x >= 16) }' /dev/null; then
        echo "error: --psf-grid expects an integer >= 16 (got '$PSF_GRID')" >&2
        exit 1
      fi
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

YAML="$SCRIPT_DIR/us2645157.yaml"
RESULT_YAML="$OUTDIR/psf-demo-result.yaml"
RESULT_TXT="$OUTDIR/psf-demo-result.txt"
CSV_BASE="$OUTDIR/psf-demo"
YAML_BASE="$OUTDIR/psf-demo"
RADIAL_BASE="$OUTDIR/psf-demo-radial"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$RESULT_YAML" "$RESULT_TXT"
  rm -f "$CSV_BASE"_*.csv "$YAML_BASE"_*.yaml
  rm -f "$OUTDIR/psf-demo-f"*.png
  rm -f "$RADIAL_BASE"_*.csv "$RADIAL_BASE".png
  echo "  Removed: psf-demo outputs (result/csv/png/yaml)"
  exit 0
fi

# Locate the rayweave binary: an explicit RAYWEAVE env value wins, then a
# binary next to the script or one directory up, then any rayweave on PATH.
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

# Locate gnuplot (charts are optional).
GNUPLOT="${GNUPLOT:-$(command -v gnuplot || true)}"
if [[ -z "$GNUPLOT" && -x /opt/homebrew/bin/gnuplot ]]; then
  GNUPLOT=/opt/homebrew/bin/gnuplot
fi

# 1. PSF computation: three fields, RCP+LCP. --csv/--yaml write one
#    index-suffixed file per field; the pipeline YAML carries psf_results[].
echo "=== PSF computation (RCP+LCP, ${NUM_RAYS} rays, ${PSF_GRID}^2 grid) ==="
$RAYWEAVE psf --polarization RCP+LCP --num-rays "$NUM_RAYS" --psf-grid "$PSF_GRID" \
  --csv "$CSV_BASE.csv" --yaml "$YAML_BASE.yaml" \
  < "$YAML" > "$RESULT_YAML"

# 2. Comparison table (stdout + psf-demo-result.txt).
{
  echo "PSF summary — US2645157 triplet, RCP+LCP, ${NUM_RAYS} rays, ${PSF_GRID}^2 grid"
  echo "field  angle   Strehl   FWHM_x    FWHM_y    EE50      Airy      valid/total"
  $RAYWEAVE query --each "psf_results[]:field_index,field_angle,strehl_ratio,fwhm_x,fwhm_y,encircled_energy_50,airy_radius,valid_rays,total_rays" \
    --printf '%5d  %6.1f  %7.4f  %8.5f  %8.5f  %8.5f  %8.5f  %d/%d' \
    < "$RESULT_YAML"
} | tee "$RESULT_TXT"
echo "Written: $RESULT_TXT"
echo

if [[ -z "$GNUPLOT" ]]; then
  echo "(charts skipped: gnuplot not available)"
  exit 0
fi

# Per-field chart helper: extract the row through the intensity peak as a
# radial (x, intensity) profile.
extract_radial() {
  local csv=$1 out=$2
  local py
  py=$(awk -F, 'NR>1 && NF>=3 && $3+0==$3 { if (m=="" || $3>m) { m=$3; py=$2 } } END { print py }' "$csv")
  awk -F, -v py="$py" '$2==py { print $1","$3 }' "$csv" > "$out"
}

# 3. Per-field 2D intensity maps + the radial overlay chart.
export GNUTERM=pngcairo

for fi in 0 1 2; do
  case $fi in
    0) label="f0"; angle="0";;
    1) label="f16"; angle="16";;
    2) label="f24"; angle="24";;
  esac
  strehl=$($RAYWEAVE query -r "psf_results[$fi].strehl_ratio" < "$RESULT_YAML")

  "$GNUPLOT" <<GPLOT 2>/dev/null
    set terminal pngcairo size 600,600
    set output "$OUTDIR/psf-demo-${label}.png"
    set datafile separator ","
    set pm3d map
    set palette gray
    set size square
    set title "${angle}° field — Strehl ${strehl}"
    set xlabel "x (mm)"
    set ylabel "y (mm)"
    set cblabel "log10(I)"
    splot "${CSV_BASE}_${fi}.csv" u 1:2:(log10(\$3<1e-8?1e-8:\$3)) with pm3d
GPLOT
  echo "Written: $OUTDIR/psf-demo-${label}.png"

  extract_radial "${CSV_BASE}_${fi}.csv" "$RADIAL_BASE"_${fi}.csv
done

# Radial-profile overlay: one chart, all fields through their peak.
"$GNUPLOT" <<GPLOT 2>/dev/null
  set terminal pngcairo size 900,500
  set output "$OUTDIR/psf-demo-radial.png"
  set datafile separator ","
  set title "PSF radial profile through the peak (US2645157, RCP+LCP)"
  set xlabel "x (mm)"
  set ylabel "intensity (normalised)"
  set key top right
  set grid xtics ytics lc rgb "#d0d0d0"
  plot "${RADIAL_BASE}_0.csv" u 1:2 with lines lw 2 lc rgb "#1f77b4" title "0°", \
       "${RADIAL_BASE}_1.csv" u 1:2 with lines lw 2 lc rgb "#d62728" title "16°", \
       "${RADIAL_BASE}_2.csv" u 1:2 with lines lw 2 lc rgb "#2ca02c" title "24°"
GPLOT
echo "Written: $OUTDIR/psf-demo-radial.png"
