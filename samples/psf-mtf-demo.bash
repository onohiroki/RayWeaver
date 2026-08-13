#!/bin/bash
set -euo pipefail

# =============================================================================
# psf-mtf-demo.bash — point-spread / OTF / MTF demo
#
# Purpose: compute the PSF and — in the same single `rayweave psf` run — the
# optical transfer function (OTF/MTF) derived by an FFT of each image-plane PSF
# grid. The pipeline is:
#   per-field polarized ray tracing -> non-uniform wavefront sampling ->
#   direct vector Huygens integral (PSF) -> FFT -> OTF/MTF.
# It then:
#   1. prints a comparison table (Strehl, FWHM, encircled-energy 50%, Airy
#      radius, sampling counts, and the MTF-50/30/10 cut-off frequencies) to
#      stdout and <stem>-result.txt,
#   2. draws one 2D pm3d intensity map PNG per field,
#   3. draws one radial-profile overlay PNG (all fields through their peak),
#   4. draws one MTF-overlay PNG (sagittal & tangential curves per field).
#
# The default lens is the escape-optimised US2645157 triplet restricted to the
# center field (0 deg) for a clean single-field Airy-core / MTF walkthrough
# (see samples/psf-mtf-demo.yaml). The 16°/24° fields now trace correctly too
# (Strehl ~0.11 / ~0.86 at best focus, thanks to the wavefront-plane launch),
# but the demo keeps the single-field YAML so the charts are easy to read. Any
# other lens YAML with a `chief` section can be substituted with --lens; the MTF
# frequency cap keeps working via the --max-freq CLI flag (default 200 c/mm)
# even if that YAML carries no `psf:` section, because --max-freq overrides
# psf.mtf_config.max_frequency.
#
# Input polarization is RCP+LCP (the polarization-averaged / unpolarised PSF).
#
# Options
#   --clean       remove every generated artifact; the tracked input YAMLs are
#                 never touched.
#   --lens NAME   select the lens: 'triplet' (default, psf-mtf-demo.yaml) or
#                 'doublegauss' (samples/doublegauss-init.yaml), or a path to
#                 any input YAML with a chief section.
#   --max-freq N  MTF frequency cap in cycles/mm (default 200); passed to
#                 `rayweave psf` so it works regardless of the lens YAML.
#   --num-rays N  pupil grid rays (default 400)
#   --psf-grid N  image-plane pixels per side (default 96)
#
# How to read the output
#   - 0° is nearly diffraction-limited: a tight Airy core (Strehl ~0.87), a
#     symmetric FWHM, and an MTF that stays high out to ~200 cycles/mm before
#     dropping through the 0.5/0.3/0.1 cut-off frequencies. Thresholds that
#     fall beyond the --max-freq chart cap are printed as '-' in the table.
#   - The MTF overlay makes the sagittal/tangential difference visible at a
#     glance; the radial overlay shows the 0° ring structure (first dark ring
#     at ~0.61·λ/NA).
#   - With --lens doublegauss the 4 fields (0/10/16/23 deg) are all aberrated
#     to varying degrees; the same table and charts adapt to their count.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTDIR="$SCRIPT_DIR"

LENS="triplet"
MAXFREQ=200
NUM_RAYS=400
PSF_GRID=96
CLEAN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    --lens)
      shift
      LENS="${1:-}"
      if [[ -z "$LENS" ]]; then
        echo "error: --lens expects 'triplet', 'doublegauss', or a YAML path" >&2
        exit 1
      fi
      shift
      ;;
    --max-freq)
      MAXFREQ="$2"; shift 2
      if ! awk -v x="$MAXFREQ" 'BEGIN { exit !(x >= 0) }' /dev/null; then
        echo "error: --max-freq expects a number >= 0 (got '$MAXFREQ')" >&2
        exit 1
      fi
      ;;
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

# Resolve the input YAML and the output stem for the chosen lens.
case "$LENS" in
  triplet)
    YAML="$SCRIPT_DIR/psf-mtf-demo.yaml"
    STEM="psf-mtf-demo"
    LENS_NAME="escape-optimised US2645157 triplet (center field)"
    ;;
  doublegauss)
    YAML="$SCRIPT_DIR/doublegauss-init.yaml"
    STEM="psf-mtf-demo-doublegauss"
    LENS_NAME="6-element double-Gauss (f/2.8 50 mm)"
    ;;
  *)
    if [[ -f "$LENS" ]]; then
      YAML="$LENS"
      STEM="psf-mtf-demo"
      LENS_NAME="$(basename "$LENS")"
    else
      echo "error: --lens must be 'triplet', 'doublegauss', or a path to an input YAML (got '$LENS')" >&2
      exit 1
    fi
    ;;
esac

RESULT_YAML="$OUTDIR/$STEM-result.yaml"
RESULT_TXT="$OUTDIR/$STEM-result.txt"
CSV_BASE="$OUTDIR/$STEM"
YAML_BASE="$OUTDIR/$STEM"
RADIAL_BASE="$OUTDIR/$STEM-radial"
MTF_BASE="$OUTDIR/$STEM-mtf"

# Clean-only mode: remove generated files for every known stem and exit
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  for s in psf-mtf-demo psf-mtf-demo-doublegauss; do
    rm -f "$OUTDIR/$s-result.yaml" "$OUTDIR/$s-result.txt"
    rm -f "$OUTDIR/$s"_*.csv "$OUTDIR/$s"_*.yaml
    rm -f "$OUTDIR/$s"-*.png
    rm -f "$OUTDIR/$s-radial"*.csv "$OUTDIR/$s-radial".png
    rm -f "$OUTDIR/$s-mtf"*.dat "$OUTDIR/$s-mtf".png
  done
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

# 1. Single PSF+OTF+MTF pass, RCP+LCP, MTF capped at --max-freq. --csv writes
#    one index-suffixed intensity map per field; --yaml writes the full
#    structured result (including the sagittal/tangential OTF/MTF curves) per
#    field; the pipeline YAML carries psf_results[] with the MTF summary.
echo "=== PSF + OTF + MTF computation: $LENS_NAME (RCP+LCP, ${NUM_RAYS} rays, ${PSF_GRID}^2 grid, MTF cap ${MAXFREQ} c/mm) ==="
$RAYWEAVE psf --polarization RCP+LCP --num-rays "$NUM_RAYS" --psf-grid "$PSF_GRID" \
  --max-freq "$MAXFREQ" --csv "$CSV_BASE.csv" --yaml "$YAML_BASE.yaml" \
  < "$YAML" > "$RESULT_YAML"

# 2. Comparison table (stdout + <stem>-result.txt). The MTF cut-off
#    frequencies come from each result's sagittal thresholds (MTF 50/30/10);
#    a threshold beyond the frequency cap is printed as '-'.
NF=$($RAYWEAVE query --len "psf_results" < "$RESULT_YAML")
{
  echo "PSF/OTF/MTF summary — $LENS_NAME, RCP+LCP, ${NUM_RAYS} rays, ${PSF_GRID}^2 grid, MTF cap ${MAXFREQ} c/mm"
  echo "field  angle   Strehl   FWHM_x    FWHM_y    EE50      Airy      MTF50/30/10 (c/mm)  valid/total"
  for ((i = 0; i < NF; i++)); do
    F="psf_results[$i]"
    fi=$($RAYWEAVE query -r "$F.field_index" < "$RESULT_YAML")
    ang=$($RAYWEAVE query -r "$F.field_angle" < "$RESULT_YAML")
    s=$($RAYWEAVE query -r "$F.strehl_ratio" < "$RESULT_YAML")
    fx=$($RAYWEAVE query -r "$F.fwhm_x" < "$RESULT_YAML")
    fy=$($RAYWEAVE query -r "$F.fwhm_y" < "$RESULT_YAML")
    ee=$($RAYWEAVE query -r "$F.encircled_energy_50" < "$RESULT_YAML")
    ai=$($RAYWEAVE query -r "$F.airy_radius" < "$RESULT_YAML")
    m50=$($RAYWEAVE query -r --default "" "$F.mtf.sagittal.thresholds[mtf=0.5].frequency" < "$RESULT_YAML")
    m30=$($RAYWEAVE query -r --default "" "$F.mtf.sagittal.thresholds[mtf=0.3].frequency" < "$RESULT_YAML")
    m10=$($RAYWEAVE query -r --default "" "$F.mtf.sagittal.thresholds[mtf=0.1].frequency" < "$RESULT_YAML")
    v=$($RAYWEAVE query -r "$F.valid_rays" < "$RESULT_YAML")
    t=$($RAYWEAVE query -r "$F.total_rays" < "$RESULT_YAML")
    [[ -n "$m50" ]] && mtf50=$(printf '%6.1f' "$m50") || mtf50=$(printf '%6s' '-')
    [[ -n "$m30" ]] && mtf30=$(printf '%6.1f' "$m30") || mtf30=$(printf '%6s' '-')
    [[ -n "$m10" ]] && mtf10=$(printf '%6.1f' "$m10") || mtf10=$(printf '%6s' '-')
    printf '  %2d   %6.1f  %7.4f  %8.5f  %8.5f  %8.5f  %8.5f  %s/%s/%s   %s/%s\n' \
      "${fi:-$i}" "${ang:-0}" "${s:-0}" "${fx:-0}" "${fy:-0}" "${ee:-0}" "${ai:-0}" \
      "$mtf50" "$mtf30" "$mtf10" "${v:-0}" "${t:-0}"
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
# curve out of a per-field --yaml file into a two-column data file. Uses the
# `rayweave query --csv` path through the real result parser so the curve is
# always frequency-sorted and never includes stray threshold/evaluated rows.
extract_mtf() {
  local yaml=$1 axis=$2 out=$3
  $RAYWEAVE query --csv "mtf.$axis.curve:frequency,mtf" < "$yaml" > "$out"
}

# 3. Per-field 2D intensity maps + the radial/MTF overlay charts.
export GNUTERM=pngcairo

COLORS=("#1f77b4" "#d62728" "#2ca02c" "#ff7f0e" "#9467bd")
ANGLES=()
for ((i = 0; i < NF; i++)); do
  label="f${i}"
  ang=$($RAYWEAVE query -r "psf_results[$i].field_angle" < "$RESULT_YAML")
  ANGLES+=("$ang")
  strehl=$($RAYWEAVE query -r "psf_results[$i].strehl_ratio" < "$RESULT_YAML")

  "$GNUPLOT" <<GPLOT 2>/dev/null
    set terminal pngcairo size 600,600
    set output "$OUTDIR/$STEM-${label}.png"
    set datafile separator ","
    set pm3d map
    set palette gray
    set size square
    set title "${ang}° field — Strehl ${strehl}"
    set xlabel "x (mm)"
    set ylabel "y (mm)"
    set cblabel "log10(I)"
    splot "${CSV_BASE}_${i}.csv" u 1:2:(log10(\$3<1e-8?1e-8:\$3)) with pm3d
GPLOT
  echo "Written: $OUTDIR/$STEM-${label}.png"

  extract_radial "${CSV_BASE}_${i}.csv" "$RADIAL_BASE"_${i}.csv
  extract_mtf "${YAML_BASE}_${i}.yaml" sagittal   "$MTF_BASE"_${i}_sag.dat
  extract_mtf "${YAML_BASE}_${i}.yaml" tangential "$MTF_BASE"_${i}_tan.dat
done

# Radial-profile overlay: one chart, all fields through their peak.
radial_plots=()
for ((i = 0; i < NF; i++)); do
  c="${COLORS[$((i % ${#COLORS[@]}))]}"
  radial_plots+=("'${RADIAL_BASE}_${i}.csv' u 1:2 with lines lw 2 lc rgb '${c}' title '${ANGLES[$i]}°'")
done
RADIAL_SPEC=$(IFS=','; printf '%s' "${radial_plots[*]}")

if [[ -n "$RADIAL_SPEC" ]]; then
  "$GNUPLOT" <<GPLOT 2>/dev/null
    set terminal pngcairo size 900,500
    set output "$OUTDIR/$STEM-radial.png"
    set datafile separator ","
    set title "PSF radial profile through the peak ($LENS_NAME, RCP+LCP)"
    set xlabel "x (mm)"
    set ylabel "intensity (normalised)"
    set key top right
    set grid xtics ytics lc rgb "#d0d0d0"
    plot $RADIAL_SPEC
GPLOT
  echo "Written: $OUTDIR/$STEM-radial.png"
fi

# 4. MTF overlay: sagittal (solid) and tangential (dashed) per field.
mtf_plots=()
for ((i = 0; i < NF; i++)); do
  c="${COLORS[$((i % ${#COLORS[@]}))]}"
  mtf_plots+=("'${MTF_BASE}_${i}_sag.dat' u 1:2 with lines lw 2 lc rgb '${c}' title '${ANGLES[$i]}° sag'")
  mtf_plots+=("'${MTF_BASE}_${i}_tan.dat' u 1:2 with lines lw 2 lc rgb '${c}' dt 2 title '${ANGLES[$i]}° tan'")
done
MTF_SPEC=$(IFS=','; printf '%s' "${mtf_plots[*]}")

if [[ -n "$MTF_SPEC" ]]; then
  "$GNUPLOT" <<GPLOT 2>/dev/null
    set terminal pngcairo size 900,550
    set output "$OUTDIR/$STEM-mtf.png"
    set title "MTF — $LENS_NAME, RCP+LCP (sagittal solid, tangential dashed)"
    set xlabel "spatial frequency (cycles/mm)"
    set ylabel "MTF"
    set key top right
    set grid xtics ytics lc rgb "#d0d0d0"
    set yrange [0:1]
    set xrange [0:$MAXFREQ]
    set datafile separator ","
    plot $MTF_SPEC
GPLOT
  echo "Written: $OUTDIR/$STEM-mtf.png"
fi