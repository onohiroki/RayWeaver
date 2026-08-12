#!/bin/bash
set -euo pipefail

# =============================================================================
# schmidt-psf-demo.bash — Schmidt camera PSF / OTF / MTF demo (all field angles)
#
# Purpose: compute the PSF and — in the same single `rayweave psf` run — the
# optical transfer function (OTF/MTF) derived by an FFT of each image-plane PSF
# grid. The pipeline is:
#   per-field polarized ray tracing -> non-uniform wavefront sampling ->
#   direct vector Huygens integral (PSF) -> FFT -> OTF/MTF.
# It then:
#   1. prints a comparison table (Strehl, FWHM, encircled-energy 50%, Airy
#      radius, effective grid, sampling counts, and the MTF-50/30/10 cut-off
#      frequencies) to stdout and <stem>-result.txt,
#   2. draws one 2D pm3d intensity map PNG per field,
#   3. draws one radial-profile overlay PNG (all fields through their peak),
#   4. draws one MTF-overlay PNG (sagittal & tangential curves per field).
#
# The default lens is the folded D=200 F/1.93 Schmidt camera
# (samples/schmidt-flattener.yaml): BK7 corrector plate + spherical primary +
# 2-element field flattener, with the flat sensor folded back to Z=400. Unlike
# the psf-mtf-demo triplet (whose 16°/24° fields are far too aberrated), the
# Schmidt is evaluated at ALL four chief fields — 0/1/2/3.22° — i.e. across the
# full 35 mm full-frame half-diagonal (21.63 mm). Any other lens YAML with a
# `chief` section can be substituted with --lens; the MTF frequency cap keeps
# working via the --max-freq CLI flag even if that YAML carries no `psf:`
# section, because --max-freq overrides psf.mtf_config.max_frequency.
#
# Input polarization is RCP+LCP (the polarization-averaged / unpolarised PSF).
#
# Options
#   --clean       remove every generated artifact; the tracked input YAMLs are
#                 never touched.
#   --lens NAME   select the lens: 'schmidt' (default,
#                 samples/schmidt-flattener.yaml), 'my-schmidt'
#                 (samples/my-schmidt.yaml, the DLS+escape-optimised version
#                 with an aspheric field flattener), 'triplet'
#                 (psf-mtf-demo.yaml), 'doublegauss'
#                 (samples/doublegauss-init.yaml), or a path to any input YAML
#                 with a chief section.
#   --max-freq N  MTF frequency cap in cycles/mm (default 100); passed to
#                 `rayweave psf` so it works regardless of the lens YAML.
#   --num-rays N  pupil grid rays (default 400)
#   --psf-grid N  requested image-plane pixels per side (default 96)
#   --best-focus  evaluate each field at its best-focus image plane (psf
#                 --best-focus): the applied shift is listed per field in a
#                 shift column and in the PNG titles as "shift +x.xxx mm"
#
# How to read the output
#   - The full-aperture F/1.93 Schmidt is NOT diffraction limited: the fast
#     mirror leaves a ~lambda/4-level residual spherical aberration even after
#     the two-term corrector, so Strehl is ~0.03-0.08 and the MTF-50 cut-off
#     lands at a few c/mm — far below the ~880 c/mm diffraction cut-off for
#     this aperture. The 0° field is the best (Strehl ~0.08).
#   - The geometric spot (~30 um) dominates the auto-sized evaluation window,
#     so the image grid is auto-enlarged until the Airy core (1.4 um) is
#     resolved: the effective grid (Grid column, ~180-450) exceeds the
#     requested --psf-grid 96. This is the auto-enlargement feature at work.
#   - The 1/2/3.22° fields show a mild sagittal/tangential MTF split and a
#     gradually larger residual — the field-curvature / astigmatism + residual
#     SA limit at full frame.
#   - Strehl is a coherent-speckle peak and therefore --num-rays sensitive;
#     raise it (900..1600) for tighter values. The spot dominates anyway, so
#     the numbers here are qualitative.
#   - Default (no --best-focus) is the as-built flat sensor: field curvature
#     and defocus appear in the PSF. With --best-focus the per-field best-focus
#     plane is used instead (the same shift the wavefront command reports, with
#     opposite sign), so the Strehls improve where defocus dominated — e.g. the
#     on-axis field climbs from ~0.08 to ~0.10. The shift column shows the
#     ~0.33 mm best-focus span across the field curvature.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTDIR="$SCRIPT_DIR"

LENS="schmidt"
MAXFREQ=100
NUM_RAYS=400
PSF_GRID=96
BEST_FOCUS=false
CLEAN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    --best-focus) BEST_FOCUS=true; shift ;;
    --lens)
      shift
      LENS="${1:-}"
      if [[ -z "$LENS" ]]; then
        echo "error: --lens expects 'schmidt', 'triplet', 'doublegauss', or a YAML path" >&2
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
  schmidt)
    YAML="$SCRIPT_DIR/schmidt-flattener.yaml"
    STEM="schmidt-psf-demo"
    LENS_NAME="folded Schmidt camera (D=200, F/1.93, 35mm full-frame)"
    ;;
  my-schmidt)
    YAML="$SCRIPT_DIR/my-schmidt.yaml"
    STEM="schmidt-psf-demo-my-schmidt"
    LENS_NAME="DLS+escape-optimised Schmidt (aspheric field flattener)"
    ;;
  triplet)
    YAML="$SCRIPT_DIR/psf-mtf-demo.yaml"
    STEM="schmidt-psf-demo-triplet"
    LENS_NAME="escape-optimised US2645157 triplet (center field)"
    ;;
  doublegauss)
    YAML="$SCRIPT_DIR/doublegauss-init.yaml"
    STEM="schmidt-psf-demo-doublegauss"
    LENS_NAME="6-element double-Gauss (f/2.8 50 mm)"
    ;;
  *)
    if [[ -f "$LENS" ]]; then
      YAML="$LENS"
      STEM="schmidt-psf-demo-$(basename "$LENS" .yaml)"
      LENS_NAME="$(basename "$LENS")"
    else
      echo "error: --lens must be 'schmidt', 'my-schmidt', 'triplet', 'doublegauss', or a path to an input YAML (got '$LENS')" >&2
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
  for s in schmidt-psf-demo schmidt-psf-demo-my-schmidt schmidt-psf-demo-triplet schmidt-psf-demo-doublegauss; do
    rm -f "$OUTDIR/$s-result.yaml" "$OUTDIR/$s-result.txt"
    rm -f "$OUTDIR/$s"_*.csv "$OUTDIR/$s"_*.yaml
    rm -f "$OUTDIR/$s"-*.png
    rm -f "$OUTDIR/$s-radial"*.csv "$OUTDIR/$s-radial".png
    rm -f "$OUTDIR/$s-mtf"*.dat "$OUTDIR/$s-mtf".png
  done
  echo "  Removed: schmidt-psf-demo outputs (result/csv/png/yaml/mtf)"
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
BF_FLAG=""
if [ "$BEST_FOCUS" = true ]; then BF_FLAG="--best-focus"; fi
$RAYWEAVE psf --polarization RCP+LCP --num-rays "$NUM_RAYS" --psf-grid "$PSF_GRID" \
  --max-freq "$MAXFREQ" --csv "$CSV_BASE.csv" --yaml "$YAML_BASE.yaml" \
  $BF_FLAG \
  < "$YAML" > "$RESULT_YAML"

# 2. Comparison table (stdout + <stem>-result.txt). The MTF cut-off
#    frequencies come from each result's sagittal thresholds (MTF 50/30/10);
#    a threshold beyond the frequency cap is printed as '-'.
NF=$($RAYWEAVE query --len "psf_results" < "$RESULT_YAML")
{
  echo "PSF/OTF/MTF summary — $LENS_NAME, RCP+LCP, ${NUM_RAYS} rays, ${PSF_GRID}^2 requested grid, MTF cap ${MAXFREQ} c/mm${BEST_FOCUS:+ (best-focus planes)}"
  if [ "$BEST_FOCUS" = true ]; then
    HDR="field  angle   Strehl   Shift     FWHM_x    FWHM_y    EE50      Airy      Grid   MTF50/30/10 (c/mm)  valid/total"
  else
    HDR="field  angle   Strehl   FWHM_x    FWHM_y    EE50      Airy      Grid   MTF50/30/10 (c/mm)  valid/total"
  fi
  echo "$HDR"
  for ((i = 0; i < NF; i++)); do
    F="psf_results[$i]"
    fi=$($RAYWEAVE query -r "$F.field_index" < "$RESULT_YAML")
    ang=$($RAYWEAVE query -r "$F.field_angle" < "$RESULT_YAML")
    s=$($RAYWEAVE query -r "$F.strehl_ratio" < "$RESULT_YAML")
    fx=$($RAYWEAVE query -r "$F.fwhm_x" < "$RESULT_YAML")
    fy=$($RAYWEAVE query -r "$F.fwhm_y" < "$RESULT_YAML")
    ee=$($RAYWEAVE query -r "$F.encircled_energy_50" < "$RESULT_YAML")
    ai=$($RAYWEAVE query -r "$F.airy_radius" < "$RESULT_YAML")
    g=$($RAYWEAVE query -r "$F.grid_size" < "$RESULT_YAML")
    m50=$($RAYWEAVE query -r --default "" "$F.mtf.sagittal.thresholds[mtf=0.5].frequency" < "$RESULT_YAML")
    m30=$($RAYWEAVE query -r --default "" "$F.mtf.sagittal.thresholds[mtf=0.3].frequency" < "$RESULT_YAML")
    m10=$($RAYWEAVE query -r --default "" "$F.mtf.sagittal.thresholds[mtf=0.1].frequency" < "$RESULT_YAML")
    v=$($RAYWEAVE query -r "$F.valid_rays" < "$RESULT_YAML")
    t=$($RAYWEAVE query -r "$F.total_rays" < "$RESULT_YAML")
    [[ -n "$m50" ]] && mtf50=$(printf '%6.1f' "$m50") || mtf50=$(printf '%6s' '-')
    [[ -n "$m30" ]] && mtf30=$(printf '%6.1f' "$m30") || mtf30=$(printf '%6s' '-')
    [[ -n "$m10" ]] && mtf10=$(printf '%6.1f' "$m10") || mtf10=$(printf '%6s' '-')
    if [ "$BEST_FOCUS" = true ]; then
      sh=$($RAYWEAVE query -r "$F.best_focus_shift_mm" < "$RESULT_YAML")
      sh=$(awk -v x="${sh:-0}" 'BEGIN { printf "%+.4f", x }')
      printf '  %2d   %6.1f  %7.4f  %s  %8.5f  %8.5f  %8.5f  %8.5f  %4d  %s/%s/%s   %s/%s\n' \
        "${fi:-$i}" "${ang:-0}" "${s:-0}" "$sh" "${fx:-0}" "${fy:-0}" "${ee:-0}" "${ai:-0}" \
        "${g:-0}" "$mtf50" "$mtf30" "$mtf10" "${v:-0}" "${t:-0}"
    else
      printf '  %2d   %6.1f  %7.4f  %8.5f  %8.5f  %8.5f  %8.5f  %4d  %s/%s/%s   %s/%s\n' \
        "${fi:-$i}" "${ang:-0}" "${s:-0}" "${fx:-0}" "${fy:-0}" "${ee:-0}" "${ai:-0}" \
        "${g:-0}" "$mtf50" "$mtf30" "$mtf10" "${v:-0}" "${t:-0}"
    fi
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
  ang_raw=$($RAYWEAVE query -r "psf_results[$i].field_angle" < "$RESULT_YAML")
  ang=$(awk -v a="$ang_raw" 'BEGIN { printf "%.1f", a }')
  ANGLES+=("$ang")
  strehl=$($RAYWEAVE query -r "psf_results[$i].strehl_ratio" < "$RESULT_YAML")
  strehl=$(awk -v s="$strehl" 'BEGIN { printf "%.4f", s }')

  ttl_extra=""
  if [ "$BEST_FOCUS" = true ]; then
    sh=$($RAYWEAVE query -r "psf_results[$i].best_focus_shift_mm" < "$RESULT_YAML")
    sh=$(awk -v x="${sh:-0}" 'BEGIN { printf "%+.4f", x }')
    ttl_extra=", shift ${sh} mm"
  fi

  "$GNUPLOT" <<GPLOT 2>/dev/null
    set terminal pngcairo size 600,600
    set output "$OUTDIR/$STEM-${label}.png"
    set datafile separator ","
    set pm3d map
    set palette gray
    set size square
    set title "${ang}° field — Strehl ${strehl}${ttl_extra}"
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
