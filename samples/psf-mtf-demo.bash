#!/bin/bash
set -euo pipefail

# =============================================================================
# psf-mtf-demo.bash — point-spread / OTF / MTF demo (escape-optimised triplet)
#
# Purpose: compute, for all three fields (0/16/24 deg) of the escape-optimised
# US2645157 triplet (see samples/psf-mtf-demo.yaml), the PSF and — in the same
# single `rayweave psf` run — the optical transfer function (OTF/MTF) derived
# by an FFT of each image-plane PSF grid. The pipeline is:
#   per-field polarized ray tracing -> non-uniform wavefront sampling ->
#   direct vector Huygens integral (PSF) -> FFT -> OTF/MTF.
# It then:
#   1. prints a comparison table (Strehl, FWHM, encircled-energy 50%, Airy
#      radius, sampling counts, and the MTF-50/30/10 cut-off frequencies) to
#      stdout and psf-mtf-demo-result.txt,
#   2. draws one 2D pm3d intensity map PNG per field,
#   3. draws one radial-profile overlay PNG (all fields through their peak),
#   4. draws one MTF-overlay PNG (sagittal & tangential curves per field).
#
# Input polarization is RCP+LCP (the polarization-averaged / unpolarised PSF).
#
# Options
#   --clean      remove every generated psf-mtf-demo-* artifact; the tracked
#                psf-mtf-demo.yaml input is never touched.
#   --num-rays N pupil grid rays (default 400)
#   --psf-grid N image-plane pixels per side (default 96)
#
# How to read the output
#   - 0° is nearly diffraction-limited: a tight Airy core (Strehl ~0.87), a
#     symmetric FWHM, and an MTF that stays high out to a few hundred
#     cycles/mm before dropping through 0.5/0.3/0.1.
#   - 16° / 24° are aberrated: Strehl drops to ~0.03 / ~0.04, the PSF becomes
#     a speckle pattern and the MTF collapses early (cut-offs at tens of
#     cycles/mm), with sagittal vs tangential splitting from astigmatism/coma.
#   - The MTF overlay makes the sagittal/tangential difference visible at a
#     glance; the radial overlay shows the 0° ring structure (first dark ring
#     at ~0.61·λ/NA) versus the broadened off-axis profiles.
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

YAML="$SCRIPT_DIR/psf-mtf-demo.yaml"
RESULT_YAML="$OUTDIR/psf-mtf-demo-result.yaml"
RESULT_TXT="$OUTDIR/psf-mtf-demo-result.txt"
CSV_BASE="$OUTDIR/psf-mtf-demo"
YAML_BASE="$OUTDIR/psf-mtf-demo"
RADIAL_BASE="$OUTDIR/psf-mtf-demo-radial"
MTF_BASE="$OUTDIR/psf-mtf-demo-mtf"

# Clean-only mode: remove generated files and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$RESULT_YAML" "$RESULT_TXT"
  rm -f "$CSV_BASE"_*.csv "$YAML_BASE"_*.yaml
  rm -f "$OUTDIR/psf-mtf-demo-f"*.png
  rm -f "$RADIAL_BASE"_*.csv "$RADIAL_BASE".png
  rm -f "$MTF_BASE"_*.dat "$MTF_BASE".png
  echo "  Removed: psf-mtf-demo outputs (result/csv/png/yaml/mtf)"
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

# 1. Single PSF+OTF+MTF pass: three fields, RCP+LCP. --csv writes one
#    index-suffixed intensity map per field; --yaml writes the full structured
#    result (including the sagittal/tangential OTF/MTF curves) per field; the
#    pipeline YAML carries psf_results[] with the MTF threshold summary.
echo "=== PSF + OTF + MTF computation (RCP+LCP, ${NUM_RAYS} rays, ${PSF_GRID}^2 grid) ==="
$RAYWEAVE psf --polarization RCP+LCP --num-rays "$NUM_RAYS" --psf-grid "$PSF_GRID" \
  --csv "$CSV_BASE.csv" --yaml "$YAML_BASE.yaml" \
  < "$YAML" > "$RESULT_YAML"

# 2. Comparison table (stdout + psf-mtf-demo-result.txt). The MTF cut-off
#    frequencies come from each result's sagittal thresholds (MTF 50/30/10).
{
  echo "PSF/OTF/MTF summary — escape-optimised triplet, RCP+LCP, ${NUM_RAYS} rays, ${PSF_GRID}^2 grid"
  echo "field  angle   Strehl   FWHM_x    FWHM_y    EE50      Airy      MTF50/30/10 (c/mm)  valid/total"
  for fi in 0 1 2; do
    F="psf_results[field_index=$fi]"
    ang=$($RAYWEAVE query -r "$F.field_angle" < "$RESULT_YAML")
    s=$($RAYWEAVE query -r "$F.strehl_ratio" < "$RESULT_YAML")
    fx=$($RAYWEAVE query -r "$F.fwhm_x" < "$RESULT_YAML")
    fy=$($RAYWEAVE query -r "$F.fwhm_y" < "$RESULT_YAML")
    ee=$($RAYWEAVE query -r "$F.encircled_energy_50" < "$RESULT_YAML")
    ai=$($RAYWEAVE query -r "$F.airy_radius" < "$RESULT_YAML")
    m50=$($RAYWEAVE query -r "$F.mtf.sagittal.thresholds[mtf=0.5].frequency" < "$RESULT_YAML")
    m30=$($RAYWEAVE query -r "$F.mtf.sagittal.thresholds[mtf=0.3].frequency" < "$RESULT_YAML")
    m10=$($RAYWEAVE query -r "$F.mtf.sagittal.thresholds[mtf=0.1].frequency" < "$RESULT_YAML")
    v=$($RAYWEAVE query -r "$F.valid_rays" < "$RESULT_YAML")
    t=$($RAYWEAVE query -r "$F.total_rays" < "$RESULT_YAML")
    printf '   %d  %6.1f  %7.4f  %8.5f  %8.5f  %8.5f  %8.5f  %8.1f/%6.1f/%6.1f    %d/%d\n' \
      "$fi" "${ang:-0}" "${s:-0}" "${fx:-0}" "${fy:-0}" "${ee:-0}" "${ai:-0}" \
      "${m50:-0}" "${m30:-0}" "${m10:-0}" "$v" "$t"
  done
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

# Per-field MTF helper: pull the sagittal (then tangential) (frequency, mtf)
# curve out of a per-field --yaml file into a two-column data file.
extract_mtf() {
  local yaml=$1 axis=$2 out=$3
  # Match the block: "    <axis>:" then "        curve:" then "frequency:" /
  # "mtf:" pairs (each list item carries a leading "- ").
  awk -v axis="$axis" '
    $0 ~ "^    " axis ":" { inaxis=1; next }
    inaxis && /^        curve:/ { incurve=1; next }
    inaxis && incurve && /^    [a-z]/ && /:/ { incurve=0; inaxis=0 }
    incurve && /frequency:/ { sub(/^.*frequency:/, ""); gsub(/[ \t]/, ""); f=$0; nf=1; next }
    incurve && nf && /mtf:/ { sub(/^.*mtf:/, ""); gsub(/[ \t]/, ""); print f, $0; nf=0 }
  ' "$yaml" > "$out"
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
    set output "$OUTDIR/psf-mtf-demo-${label}.png"
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
  echo "Written: $OUTDIR/psf-mtf-demo-${label}.png"

  extract_radial "${CSV_BASE}_${fi}.csv" "$RADIAL_BASE"_${fi}.csv
  extract_mtf "${YAML_BASE}_${fi}.yaml" sagittal   "$MTF_BASE"_${fi}_sag.dat
  extract_mtf "${YAML_BASE}_${fi}.yaml" tangential "$MTF_BASE"_${fi}_tan.dat
done

# Radial-profile overlay: one chart, all fields through their peak.
"$GNUPLOT" <<GPLOT 2>/dev/null
  set terminal pngcairo size 900,500
  set output "$OUTDIR/psf-mtf-demo-radial.png"
  set datafile separator ","
  set title "PSF radial profile through the peak (escape-optimised triplet, RCP+LCP)"
  set xlabel "x (mm)"
  set ylabel "intensity (normalised)"
  set key top right
  set grid xtics ytics lc rgb "#d0d0d0"
  plot "${RADIAL_BASE}_0.csv" u 1:2 with lines lw 2 lc rgb "#1f77b4" title "0°", \
       "${RADIAL_BASE}_1.csv" u 1:2 with lines lw 2 lc rgb "#d62728" title "16°", \
       "${RADIAL_BASE}_2.csv" u 1:2 with lines lw 2 lc rgb "#2ca02c" title "24°"
GPLOT
echo "Written: $OUTDIR/psf-mtf-demo-radial.png"

# 4. MTF overlay: sagittal (solid) and tangential (dashed) per field.
"$GNUPLOT" <<GPLOT 2>/dev/null
  set terminal pngcairo size 900,550
  set output "$OUTDIR/psf-mtf-demo-mtf.png"
  set title "MTF — escape-optimised triplet, RCP+LCP (sagittal solid, tangential dashed)"
  set xlabel "spatial frequency (cycles/mm)"
  set ylabel "MTF"
  set key top right
  set grid xtics ytics lc rgb "#d0d0d0"
  set yrange [0:1]
  plot "${MTF_BASE}_0_sag.dat" u 1:2 w l lw 2 lc rgb "#1f77b4" title "0° sag", \
       "${MTF_BASE}_0_tan.dat" u 1:2 w l lw 2 lc rgb "#1f77b4" dt 2 title "0° tan", \
       "${MTF_BASE}_1_sag.dat" u 1:2 w l lw 2 lc rgb "#d62728" title "16° sag", \
       "${MTF_BASE}_1_tan.dat" u 1:2 w l lw 2 lc rgb "#d62728" dt 2 title "16° tan", \
       "${MTF_BASE}_2_sag.dat" u 1:2 w l lw 2 lc rgb "#2ca02c" title "24° sag", \
       "${MTF_BASE}_2_tan.dat" u 1:2 w l lw 2 lc rgb "#2ca02c" dt 2 title "24° tan"
GPLOT
echo "Written: $OUTDIR/psf-mtf-demo-mtf.png"