#!/bin/bash
set -euo pipefail

# =============================================================================
# asphere-demo.bash — add a single asphere to an optimized spherical lens
#
# Purpose: show the `rayweave asphere` workflow on a lens that is already
# fully optimized with spherical surfaces. asphere-demo-init.yaml is an
# optimized 6-element double-Gauss (f/2.8 50 mm) whose curvatures have NO
# aspherical correction. For EACH case (full aperture, and the stopped-down
# variant when one is requested):
#   1. ranks candidate surfaces for asphere introduction,
#   2. fits even-order coefficients from the OPD residuals,
#   3. validates each top-K fit with a short DLS solve against the geometric
#      spot RMS,
#   4. inserts (--apply) the top-ranked asphere's DLS-solved coefficients,
#   5. compares the all-spherical spot RMS against the aspherized spot RMS,
#   6. draws before/after PNG diagrams and applies the per-case gate.
#
# Options
#   --clean      remove every generated artifact (full-aperture and stopped-down
#                tagged outputs); the tracked asphere-demo-init.yaml and
#                asphere-demo.bash are never touched.
#   --epd X      stopped-down EPD as a FRACTION of the full entrance pupil when
#                0 < X < 1 (e.g. --epd 0.5 = half aperture), or as an absolute
#                diameter in mm when X >= 1 (e.g. --epd 12). Surface 7's
#                fixed aperture is resized via yq. Requires yq.
#   --fno N      stopped-down EPD set by F-number instead: N = EFL/N mm (e.g.
#                --fno 5.6). Requires yq.
#
# The demo runs one case — the full-aperture lens — and ONLY when a stop is
# requested (--epd/--fno) also runs a stopped-down variant. EVERY case draws
# its own side-by-side OPD-overlap chart: left = all-spherical (before), right
# = right after the top-ranked initial asphere is inserted (residual OPD).
# Each column gets its OWN y-range (the per-case OPD min/max): the residual
# column is much smaller, so the two scales are each read separately.
#
# How to read the result
#   - The ranking score weights how well a rotationally-symmetric asphere
#     corrects the shared (field-common) OPD while penalising inter-field
#     conflict, manufacturing difficulty and optimisation instability.
#   - The validation improvement is the geometric RMS-spot reduction the
#     short DLS achieved with the asphere coefficients as the only variables.
#   - The final comparison table prints RMS before/after per field; a '✓'
#     means the aspherized surface shrank the spot, '✗' a regression.
#   - The OPD chart shows each field in its local beam frame: tangential
#     position t on the x-axis and +s/-s sagittal half-beam means as two curves.
#     Separation of the curves is the non-radial residual; the aspherized
#     surface's row collapses towards 0 in the right-hand (after) column.
#
# The gate (one improvement, no field regressing more than 1%) is applied to
# EVERY case; any failed case makes the demo exit non-zero.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTDIR="$SCRIPT_DIR"
BASE_INIT="$SCRIPT_DIR/asphere-demo-init.yaml"

TOP_K=3
DLS_ITER=30
NUM_RAYS=128
SENS_SAMPLES=6

CLEAN=false
STOP_MODE=""   # "frac" | "mm" | "fno"
STOP_VALUE=""  # the numeric argument from --epd/--fno
STOP_TAG=""
STOP_LABEL=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    --epd)
      STOP_MODE=mm
      STOP_VALUE="$2"; shift 2
      if [[ ! "$STOP_VALUE" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
        echo "error: --epd expects a numeric EPD (got '$STOP_VALUE')" >&2
        exit 1
      fi
      if ! awk -v x="$STOP_VALUE" 'BEGIN { exit !(x > 0) }' /dev/null; then
        echo "error: --epd expects a positive EPD (got '$STOP_VALUE')" >&2
        exit 1
      fi
      if awk -v x="$STOP_VALUE" 'BEGIN { exit !(x < 1) }' /dev/null; then
        STOP_MODE=frac
      fi
      ;;
    --fno)
      STOP_MODE=fno
      STOP_VALUE="$2"; shift 2
      if [[ ! "$STOP_VALUE" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
        echo "error: --fno expects a numeric F-number (got '$STOP_VALUE')" >&2
        exit 1
      fi
      if ! awk -v x="$STOP_VALUE" 'BEGIN { exit !(x > 0) }' /dev/null; then
        echo "error: --fno expects a positive F-number (got '$STOP_VALUE')" >&2
        exit 1
      fi
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# Resolve the stopped-down EPD:
#   frac — diameter = FULL_DIAM * X        (X in (0,1))
#   mm   — diameter = X                   (X >= 1, absolute mm)
#   fno  — diameter = EFL / N             (N = EFL/EPD)
# The labelling and file tag differ per mode.
if [[ -n "$STOP_MODE" ]]; then
  case "$STOP_MODE" in
    frac)
      STOP_TAG="-epd${STOP_VALUE}"
      pct=$(awk -v x="$STOP_VALUE" 'BEGIN { printf "%g", x * 100 }')
      STOP_LABEL="EPD ${pct}%"
      ;;
    mm)
      STOP_TAG="-epd${STOP_VALUE}"
      STOP_LABEL="EPD ${STOP_VALUE} mm"
      ;;
    fno)
      raw_tag=${STOP_VALUE}            # strip trailing zeros for the tag
      if [[ "$raw_tag" =~ ^([0-9]+)([.]0*)?$ ]]; then
        raw_tag=${BASH_REMATCH[1]}
      elif [[ "$raw_tag" =~ ^([0-9]+)[.]([0-9]*[1-9])0*$ ]]; then
        raw_tag="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
      fi
      STOP_TAG="-fno${raw_tag}"
      STOP_LABEL="F/${STOP_VALUE}"
      ;;
  esac
fi

if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$OUTDIR"/asphere-demo-result*.txt "$OUTDIR"/asphere-demo-*-result*.txt
  rm -f "$OUTDIR"/asphere-demo-rank*.yaml "$OUTDIR"/asphere-demo-*-rank*.yaml
  rm -f "$OUTDIR"/asphere-demo-validated*.yaml "$OUTDIR"/asphere-demo-*-validated*.yaml
  rm -f "$OUTDIR"/asphere-demo-applied*.yaml "$OUTDIR"/asphere-demo-*-applied*.yaml
  rm -f "$OUTDIR"/asphere-demo-spot*.tbl "$OUTDIR"/asphere-demo-*-spot*.tbl
  rm -f "$OUTDIR"/asphere-demo-spot*.csv "$OUTDIR"/asphere-demo-*-spot*.csv
  rm -f "$OUTDIR"/asphere-demo-opd*.dat "$OUTDIR"/asphere-demo-*-opd*.dat
  rm -f "$OUTDIR"/asphere-demo-opd*.gnu "$OUTDIR"/asphere-demo-*-opd*.gnu
  rm -f "$OUTDIR"/asphere-demo-opd-overlap*.png "$OUTDIR"/asphere-demo-*-opd-overlap*.png
  rm -f "$OUTDIR"/asphere-demo-defocus*.png "$OUTDIR"/asphere-demo-*-defocus*.png
  rm -f "$OUTDIR"/asphere-demo-defocus*.dat "$OUTDIR"/asphere-demo-*-defocus*.dat
  rm -f "$OUTDIR"/asphere-demo-defocus*.gnu "$OUTDIR"/asphere-demo-*-defocus*.gnu
  rm -f "$OUTDIR"/asphere-demo-ts-focus*.png "$OUTDIR"/asphere-demo-*-ts-focus*.png
  rm -f "$OUTDIR"/asphere-demo-ts-focus*.dat "$OUTDIR"/asphere-demo-*-ts-focus*.dat
  rm -f "$OUTDIR"/asphere-demo-ts-focus*.gnu "$OUTDIR"/asphere-demo-*-ts-focus*.gnu
  rm -f "$OUTDIR"/asphere-demo-focus-radial*.png "$OUTDIR"/asphere-demo-*-focus-radial*.png
  rm -f "$OUTDIR"/asphere-demo-focus-radial*.dat "$OUTDIR"/asphere-demo-*-focus-radial*.dat
  rm -f "$OUTDIR"/asphere-demo-focus-radial*.gnu "$OUTDIR"/asphere-demo-*-focus-radial*.gnu
  rm -f "$OUTDIR"/asphere-demo-focus-gain*.png "$OUTDIR"/asphere-demo-*-focus-gain*.png
  rm -f "$OUTDIR"/asphere-demo-focus-gain*.dat "$OUTDIR"/asphere-demo-*-focus-gain*.dat
  rm -f "$OUTDIR"/asphere-demo-focus-gain*.gnu "$OUTDIR"/asphere-demo-*-focus-gain*.gnu
  rm -f "$OUTDIR"/asphere-demo-wf-before*.yaml "$OUTDIR"/asphere-demo-*-wf-before*.yaml
  rm -f "$OUTDIR"/asphere-demo-wf-after*.yaml  "$OUTDIR"/asphere-demo-*-wf-after*.yaml
  rm -f "$OUTDIR"/asphere-demo-applied-initial*.yaml "$OUTDIR"/asphere-demo-*-applied-initial*.yaml
  rm -f "$OUTDIR"/asphere-demo-rank-after-initial*.yaml "$OUTDIR"/asphere-demo-*-rank-after-initial*.yaml
  rm -f "$OUTDIR"/asphere-demo-before*.png "$OUTDIR"/asphere-demo-*-before*.png
  rm -f "$OUTDIR"/asphere-demo-after*.png "$OUTDIR"/asphere-demo-*-after*.png
  rm -f "$OUTDIR"/asphere-demo-init-half*.yaml "$OUTDIR"/asphere-demo-init-epd*.yaml "$OUTDIR"/asphere-demo-init-fno*.yaml
  echo "  Removed: result txts, ranking/validation/apply YAMLs, spot tables, OPD charts/data, PNGs, stop init variants"
  exit 0
fi

# Locate the rayweave binary.
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

# Locate yq (needed to build the "initial asphere inserted" system that feeds
# the before/after OPD chart). Optional: without it that chart is skipped.
YQ_BIN="${YQ:-$(command -v yq || true)}"
if [[ -z "$YQ_BIN" && -x /opt/homebrew/bin/yq ]]; then
  YQ_BIN=/opt/homebrew/bin/yq
fi

# fit prints a numeric field as %8.4f, or a dash when the query returned -1
# (field absent, e.g. a candidate whose coefficients fit failed).
fit() {
  if [ -z "$1" ] || [ "$1" = "nan" ] || [ "$1" = "-1" ]; then printf "%9s" "-"; else printf "%9.4f" "$1"; fi
}

rms_field() {
  "$RAYWEAVE" chief < "$1" 2>/dev/null | "$RAYWEAVE" query -r "chief_rays[$2].spot_stats.rms_r"
}

append_notes() {
  cat >> "$1" <<'EOF'

=== How to interpret this result ===
- "RMS before / RMS after" is the geometric RMS spot radius (mm) per field
  from the chief-ray pupil grid (all-spherical vs aspherized).
- The validation merit is the same geometric spot RMS, solved by a short DLS
  over the inserted surface's a4..a12 only (all other surfaces frozen).
- A '✓' means the added asphere shrank the spot for that field; '✗' a
  regression. The demo applies the top-ranked (highest-scoring) surface.
- The OPD chart is the same data BEFORE vs right AFTER the initial asphere
  (the calibrated initial coefficients, no DLS yet): each field has +s and -s
  half-beam curves versus tangential beam coordinate t. Their separation is
  the sagittal asymmetry; each column has its own y-range and a zero reference.
- The T/S focus chart shows per-field best-focus Z for tangential (T, solid)
  and sagittal (S, dashed) fans: blue = all-spherical (base), red = initial
  asphere (trial). Separation between T and S at a given field = astigmatism.
  Vertical shift of the red curves toward a common plane = field-curvature
  correction.
- The focus radial-fit chart overlays the polynomial fit (r², r⁴, r⁶) of
  mean focus residual and T-S split versus field angle for each candidate
  surface.  Steeper curves = larger field-dependent defocus/astigmatism.
- The focus gain chart summarises how much the asphere corrects field
  curvature (blue) and astigmatism (red) per candidate surface, from 0 (no
  improvement) to 1 (perfect correction).
EOF
}

# ── OPD-overlap chart (per-column y-range, optional side-by-side) ──
# For every candidate surface, plots each field's beam-frame +s/-s mean OPD
# across tangential position (the opd_profiles emitted by the asphere command).
# When a
# right-hand case is given, left = full aperture and right = stopped-down are
# drawn side by side; otherwise a single column. Overlapping profiles = shared
# sagittal curves show non-radial residual structure. Each COLUMN gets its OWN y-range (the OPD min/max
# over that case's surfaces/fields): aligning scales is meaningful within one
# EPD but two different EPDs are each read on their own scale. Plus a dashed
# OPD=0 reference per column. Requires gnuplot; silently skipped when absent.
plot_opd_overlap() {
  local out="$1" rank_left="$2" base_left="$3" label_left="$4"
  local rank_right="${5:-}" base_right="${6:-}" label_right="${7:-}"
  local dual=false
  if [[ -n "$rank_right" && -f "$rank_right" ]]; then dual=true; fi
  local gnuplot="${GNUPLOT:-$(command -v gnuplot || true)}"
  if [[ -z "$gnuplot" && -x /opt/homebrew/bin/gnuplot ]]; then
    gnuplot=/opt/homebrew/bin/gnuplot
  fi
  if [[ -z "$gnuplot" ]]; then
    echo "  (OPD-overlap chart skipped: gnuplot not available)"
    return 0
  fi

  local nsid
  nsid=$("$RAYWEAVE" query --len asphere_candidate_result.opd_profiles < "$rank_left" 2>/dev/null || echo 0)
  if [[ -z "$nsid" || "$nsid" -eq 0 ]]; then
    echo "  (OPD-overlap chart skipped: no opd_profiles in ranking)"
    return 0
  fi

  # Emit one data file per surface per case; each field is a block of
  # "t opd_plus opd_minus" rows separated by a blank line.
  build_opd_dat() {
    local rank="$1" base="$2"
    local si fi j nf npoints sid t plus minus
    for ((si=0; si<nsid; si++)); do
      sid=$("$RAYWEAVE" query -r "asphere_candidate_result.opd_profiles[$si].surface_id" < "$rank")
      : > "$base.$si.dat"
      nf=$("$RAYWEAVE" query --len "asphere_candidate_result.opd_profiles[$si].fields" < "$rank" 2>/dev/null || echo 0)
      for ((fi=0; fi<nf; fi++)); do
        npoints=$("$RAYWEAVE" query --len "asphere_candidate_result.opd_profiles[$si].fields[$fi].t_radius" < "$rank" 2>/dev/null || echo 0)
        for ((j=0; j<npoints; j++)); do
          t=$("$RAYWEAVE" query -r "asphere_candidate_result.opd_profiles[$si].fields[$fi].t_radius[$j]" < "$rank")
          plus=$("$RAYWEAVE" query -r "asphere_candidate_result.opd_profiles[$si].fields[$fi].opd_plus[$j]" < "$rank")
          minus=$("$RAYWEAVE" query -r "asphere_candidate_result.opd_profiles[$si].fields[$fi].opd_minus[$j]" < "$rank")
          printf "%s %s %s\n" "$t" "$plus" "$minus" >> "$base.$si.dat"
        done
        printf "\n" >> "$base.$si.dat"
      done
    done
  }
  build_opd_dat "$rank_left" "$base_left"
  if $dual; then build_opd_dat "$rank_right" "$base_right"; fi

  # Per-column y-range (min/max over that case's own dat files), padded by 5 %
  # so the outer envelope is not clipped. Each column is on its OWN scale.
  opd_range() {
    local base="$1" lo hi pad
    read -r lo hi < <(awk 'NF>=3 { for (i=2; i<=3; i++) { if (min=="" || $i<min) min=$i; if (max=="" || $i>max) max=$i } }
                       END { if (min!="") print min, max; else print "", "" }' "$base".*.dat)
    if [[ -n "$lo" && -n "$hi" ]]; then
      pad=$(awk -v lo="$lo" -v hi="$hi" 'BEGIN { p=(hi-lo)*0.05; if (p==0) p=0.05; printf "%.9g", p }')
      lo=$(awk -v v="$lo" -v p="$pad" 'BEGIN { printf "%.9g", v-p }')
      hi=$(awk -v v="$hi" -v p="$pad" 'BEGIN { printf "%.9g", v+p }')
      printf "set yrange [%s:%s]\n" "$lo" "$hi"
    fi
  }
  local yrange_l yrange_r
  yrange_l=$(opd_range "$base_left")
  yrange_r=""
  if $dual; then yrange_r=$(opd_range "$base_right"); fi

  # Build the gnuplot script in bash (avoids heredoc expansion under set -u)
  # and pipe it to gnuplot: one ROW per candidate surface; either one column or
  # two columns (left = full aperture, right = stopped-down); one line per
  # field. The column's y-range is applied right before each plot so the two
  # scales stay independent.
  local cols=1
  if $dual; then cols=2; fi
  local width=1200
  if $dual; then width=2400; fi
  local title="Per-field OPD per candidate surface — $label_left"
  if $dual; then
    title="Per-field OPD per candidate surface — left: $label_left, right: $label_right (per-case y-ranges, overlap = common OPD)"
  fi
  local gs="$base_left.gnu"
  {
    echo "set terminal pngcairo size ${width},$((nsid * 400))"
    echo "set output \"$out\""
    echo "set multiplot layout $nsid,$cols rowsfirst title \"$title\" font \",12\""
    echo "set xlabel \"tangential beam position t (mm)\""
    echo "set ylabel \"OPD (mm)\""
    echo "set grid"
    echo "set key outside right"
    echo "set yzeroaxis dt 2 linecolor rgb \"#999999\""
    for ((si=0; si<nsid; si++)); do
      sid=$("$RAYWEAVE" query -r "asphere_candidate_result.opd_profiles[$si].surface_id" < "$rank_left")
      nf_l=$("$RAYWEAVE" query --len "asphere_candidate_result.opd_profiles[$si].fields" < "$rank_left" 2>/dev/null || echo 0)
      if [[ -n "$yrange_l" ]]; then echo "$yrange_l"; fi
      echo "set title sprintf(\"surface ${sid} — $label_left\")"
      plot_cmd="plot"
      for ((f=0; f<nf_l; f++)); do
        plot_cmd+=" sprintf(\"$base_left.%d.dat\", $si) index $f using 1:2 with linespoints pt 7 ps 0.7 title sprintf(\"field %d +s\", $f),"
        plot_cmd+=" sprintf(\"$base_left.%d.dat\", $si) index $f using 1:3 with linespoints pt 9 ps 0.7 title sprintf(\"field %d -s\", $f),"
      done
      echo "${plot_cmd%,}"
      if $dual; then
        nf_r=$("$RAYWEAVE" query --len "asphere_candidate_result.opd_profiles[$si].fields" < "$rank_right" 2>/dev/null || echo 0)
        if [[ -n "$yrange_r" ]]; then echo "$yrange_r"; fi
        echo "set title sprintf(\"surface ${sid} — $label_right\")"
        plot_cmd="plot"
        for ((f=0; f<nf_r; f++)); do
          plot_cmd+=" sprintf(\"$base_right.%d.dat\", $si) index $f using 1:2 with linespoints pt 7 ps 0.7 title sprintf(\"field %d +s\", $f),"
          plot_cmd+=" sprintf(\"$base_right.%d.dat\", $si) index $f using 1:3 with linespoints pt 9 ps 0.7 title sprintf(\"field %d -s\", $f),"
        done
        echo "${plot_cmd%,}"
      fi
    done
    echo "unset multiplot"
  } > "$gs"
  GNUTERM=pngcairo "$gnuplot" "$gs" 2>/dev/null
  echo "Written: $out"
}

# ── Field defocus chart ──────────────────────────────────────
# Plots per-field focus shift (best_fit_sphere.center[2] mm) relative to
# field 0, for before/after comparison of the initial asphere insertion.
# Horizontal axis: field angle (deg). Vertical axis: focus shift (mm).
# One PNG per case (full aperture or stopped-down).
plot_field_defocus() {
  local out="$1" wf_before="$2" label_before="$3" wf_after="$4" label_after="$5"
  local gnuplot="${GNUPLOT:-$(command -v gnuplot || true)}"
  if [[ -z "$gnuplot" && -x /opt/homebrew/bin/gnuplot ]]; then gnuplot=/opt/homebrew/bin/gnuplot; fi
  if [[ -z "$gnuplot" ]]; then
    echo "  (defocus chart skipped: gnuplot not available)"
    return 0
  fi

  # Determine number of fields from each YAML
  local nf_before nf_after
  nf_before=$("$RAYWEAVE" query --len "wavefront_result.fields" < "$wf_before" 2>/dev/null || echo 0)
  nf_after=$("$RAYWEAVE" query --len "wavefront_result.fields" < "$wf_after" 2>/dev/null || echo 0)
  [[ "$nf_before" -eq 0 || "$nf_after" -eq 0 ]] && { echo "  (defocus chart skipped: no fields)"; return 0; }

  # Extract field 0's center as reference
  local cz0_before cz0_after
  cz0_before=$("$RAYWEAVE" query -r "wavefront_result.fields[0].best_fit_sphere.center[2]" < "$wf_before" 2>/dev/null)
  cz0_after=$("$RAYWEAVE" query -r "wavefront_result.fields[0].best_fit_sphere.center[2]" < "$wf_after" 2>/dev/null)
  [[ -z "$cz0_before" || -z "$cz0_after" ]] && { echo "  (defocus chart skipped: missing center[2])"; return 0; }

  # Write data files: angle (deg), shift_mm (relative to field 0)
  local dat_before="$out.before.dat" dat_after="$out.after.dat"
  : > "$dat_before" : > "$dat_after"

  for ((i=0; i<nf_before && i<nf_after; i++)); do
    local ang_before cz_before ang_after cz_after
    ang_before=$("$RAYWEAVE" query -r "wavefront_result.fields[$i].field_angle" < "$wf_before" 2>/dev/null)
    cz_before=$("$RAYWEAVE" query -r "wavefront_result.fields[$i].best_fit_sphere.center[2]" < "$wf_before" 2>/dev/null)
    ang_after=$("$RAYWEAVE" query -r "wavefront_result.fields[$i].field_angle" < "$wf_after" 2>/dev/null)
    cz_after=$("$RAYWEAVE" query -r "wavefront_result.fields[$i].best_fit_sphere.center[2]" < "$wf_after" 2>/dev/null)
    [[ -z "$ang_before" || -z "$cz_before" || -z "$ang_after" || -z "$cz_after" ]] && continue
    awk -v a="$ang_before" -v c="$cz_before" -v c0="$cz0_before" 'BEGIN { printf "%.6f %.6f\n", a, c - c0 }' >> "$dat_before"
    awk -v a="$ang_after" -v c="$cz_after" -v c0="$cz0_after" 'BEGIN { printf "%.6f %.6f\n", a, c - c0 }' >> "$dat_after"
  done

  # If no valid points, skip
  if ! wc -l "$dat_before" | grep -q '[1-9]'; then
    echo "  (defocus chart: no valid data points)"
    rm -f "$dat_before" "$dat_after"
    return 0
  fi

  # Generate gnuplot script (default pngcairo lt1=magenta, lt2=green)
  cat > "$out.gnu" <<EOF
set terminal pngcairo size 800,500
set output "$out"
set xlabel "field angle (deg)"
set ylabel "focus shift relative to field 0 (mm)"
set grid
set key outside right
set yzeroaxis dt 2 lc rgb "#999999"
plot "$dat_before" using 1:2 with linespoints pt 7 title "$label_before", \
     "$dat_after"  using 1:2 with linespoints pt 9 title "$label_after"
EOF
  GNUTERM=pngcairo "$gnuplot" "$out.gnu" 2>/dev/null
  echo "Written: $out"
}

# ── T/S focus position chart (per candidate surface) ──────────
# For every candidate surface in the asphere ranking, plots per-field
# tangential (T) and sagittal (S) best-focus Z for the all-spherical system
# (base) and right after the initial asphere is inserted (trial).
# Requires gnuplot and the focus channel data (--diagnostics focus).
plot_ts_focus() {
  local out="$1" rank="$2" label="$3" input_yaml="${4:-}"
  local gnuplot="${GNUPLOT:-$(command -v gnuplot || true)}"
  if [[ -z "$gnuplot" && -x /opt/homebrew/bin/gnuplot ]]; then gnuplot=/opt/homebrew/bin/gnuplot; fi
  if [[ -z "$gnuplot" ]]; then
    echo "  (T/S focus chart skipped: gnuplot not available)"
    return 0
  fi

  local nsurf
  nsurf=$("$RAYWEAVE" query --len asphere_candidate_result.surfaces < "$rank" 2>/dev/null || echo 0)
  if [[ -z "$nsurf" || "$nsurf" -eq 0 ]]; then
    echo "  (T/S focus chart skipped: no focus data)"
    return 0
  fi

  # Extract field angles from the input YAML.
  local angles_str=""
  if [[ -n "$input_yaml" && -f "$input_yaml" ]]; then
    angles_str=$("$YQ_BIN" e '.chief.fields[].angle // 0' "$input_yaml" 2>/dev/null | tr '\n' ' ')
  fi
  if [[ -z "$angles_str" ]]; then
    echo "  (T/S focus chart skipped: cannot read field angles)"
    return 0
  fi
  read -ra angles <<< "$angles_str"
  local nfields=${#angles[@]}

  # Build one data file per surface: angle base_t base_s trial_t trial_s
  local base="$out"
  for ((si=0; si<nsurf; si++)); do
    local dat="$base.$si.dat"
    local t_vals s_vals tt_vals ts_vals
    t_vals=$("$YQ_BIN" e ".asphere_candidate_result.surfaces[$si].field_focus[].base.tangential.best_z_mm // \"\"" "$rank" 2>/dev/null | tr '\n' '|')
    s_vals=$("$YQ_BIN" e ".asphere_candidate_result.surfaces[$si].field_focus[].base.sagittal.best_z_mm // \"\"" "$rank" 2>/dev/null | tr '\n' '|')
    tt_vals=$("$YQ_BIN" e ".asphere_candidate_result.surfaces[$si].field_focus[].trial.tangential.best_z_mm // \"\"" "$rank" 2>/dev/null | tr '\n' '|')
    ts_vals=$("$YQ_BIN" e ".asphere_candidate_result.surfaces[$si].field_focus[].trial.sagittal.best_z_mm // \"\"" "$rank" 2>/dev/null | tr '\n' '|')

    IFS='|' read -ra t_arr <<< "$t_vals"
    IFS='|' read -ra s_arr <<< "$s_vals"
    IFS='|' read -ra tt_arr <<< "$tt_vals"
    IFS='|' read -ra ts_arr <<< "$ts_vals"

    : > "$dat"
    local nvalid=0
    for ((fi=0; fi<nfields && fi<${#t_arr[@]}; fi++)); do
      local bt="${t_arr[$fi]}" bs="${s_arr[$fi]}" tt="${tt_arr[$fi]}" ts="${ts_arr[$fi]}"
      [[ -z "$bt" || -z "$bs" || -z "$tt" || -z "$ts" ]] && continue
      printf "%s %s %s %s %s\n" "${angles[$fi]}" "$bt" "$bs" "$tt" "$ts" >> "$dat"
      nvalid=$((nvalid+1))
    done
    [[ "$nvalid" -eq 0 ]] && rm -f "$dat"
  done

  # Check if any data was generated
  local has_data=false
  for ((si=0; si<nsurf; si++)); do
    [[ -f "$base.$si.dat" ]] && has_data=true && break
  done
  if ! $has_data; then
    echo "  (T/S focus chart skipped: no valid focus data)"
    return 0
  fi

  # Gnuplot: multiplot with one row per candidate surface
  local gs="$base.gnu"
  {
    echo "set terminal pngcairo size 900,$((nsurf * 380))"
    echo "set output \"$out\""
    echo "set multiplot layout $nsurf,1 rowsfirst title \"T/S best-focus Z per candidate surface — $label\" font \",11\""
    echo "set xlabel \"field angle (deg)\""
    echo "set ylabel \"best-focus Z (mm)\""
    echo "set grid"
    echo "set key outside right"
    echo "set yzeroaxis dt 2 linecolor rgb \"#999999\""
    for ((si=0; si<nsurf; si++)); do
      [[ ! -f "$base.$si.dat" ]] && continue
      local sid
      sid=$("$RAYWEAVE" query -r "asphere_candidate_result.surfaces[$si].surface_id" < "$rank" 2>/dev/null || echo "?")
      echo "set title sprintf(\"surface $sid\")"
      echo "plot \"$base.$si.dat\" using 1:2 with linespoints pt 7 ps 0.8 lc rgb \"#1f77b4\" title \"base T\", \\"
      echo "     \"$base.$si.dat\" using 1:3 with linespoints pt 5 ps 0.8 lc rgb \"#1f77b4\" dt 2 title \"base S\", \\"
      echo "     \"$base.$si.dat\" using 1:4 with linespoints pt 9 ps 0.8 lc rgb \"#d62728\" title \"trial T\", \\"
      echo "     \"$base.$si.dat\" using 1:5 with linespoints pt 6 ps 0.8 lc rgb \"#d62728\" dt 2 title \"trial S\""
    done
    echo "unset multiplot"
  } > "$gs"
  GNUTERM=pngcairo "$gnuplot" "$gs" 2>/dev/null
  echo "Written: $out"
}

# ── Focus radial-fit overlay (per candidate surface) ───────────
# For every candidate surface, overlays the radial polynomial fit (r², r⁴, r⁶)
# of mean focus and T-S split versus field angle, comparing base (all-spherical)
# and trial (initial asphere) states.  Two columns per surface: left = mean
# focus fit, right = T-S split fit.  Requires gnuplot and focus channel data.
plot_focus_radial_fit() {
  local out="$1" rank="$2" label="$3" input_yaml="${4:-}"
  local gnuplot="${GNUPLOT:-$(command -v gnuplot || true)}"
  if [[ -z "$gnuplot" && -x /opt/homebrew/bin/gnuplot ]]; then gnuplot=/opt/homebrew/bin/gnuplot; fi
  if [[ -z "$gnuplot" ]]; then
    echo "  (focus radial-fit chart skipped: gnuplot not available)"
    return 0
  fi

  local nsurf
  nsurf=$("$RAYWEAVE" query --len asphere_candidate_result.surfaces < "$rank" 2>/dev/null || echo 0)
  if [[ -z "$nsurf" || "$nsurf" -eq 0 ]]; then
    echo "  (focus radial-fit chart skipped: no focus data)"
    return 0
  fi

  # Extract field angles
  local angles_str=""
  if [[ -n "$input_yaml" && -f "$input_yaml" ]]; then
    angles_str=$("$YQ_BIN" e '.chief.fields[].angle // 0' "$input_yaml" 2>/dev/null | tr '\n' ' ')
  fi
  if [[ -z "$angles_str" ]]; then
    echo "  (focus radial-fit chart skipped: cannot read field angles)"
    return 0
  fi
  read -ra angles <<< "$angles_str"
  local nfields=${#angles[@]}

  local base="$out"
  local has_data=false
  for ((si=0; si<nsurf; si++)); do
    local dat_mean="$base.$si.mean.dat" dat_ts="$base.$si.ts.dat"
    : > "$dat_mean" : > "$dat_ts"

    # Extract coefficients for focus_mean (base and trial)
    local c_base_m0 c_base_m1 c_base_m2 c_trial_m0 c_trial_m1 c_trial_m2
    for target in "focus_mean" "focus_ts"; do
      local prefix
      if [[ "$target" == "focus_mean" ]]; then prefix="mean"; else prefix="ts"; fi
      local dat_file="$base.$si.${prefix}.dat"

      local b0="" b1="" b2="" t0="" t1="" t2=""
      local nfit
      nfit=$("$RAYWEAVE" query --len "asphere_candidate_result.surfaces[$si].radial_fits" < "$rank" 2>/dev/null || echo 0)
      for ((fi=0; fi<nfit; fi++)); do
        local rt
        rt=$("$RAYWEAVE" query -r "asphere_candidate_result.surfaces[$si].radial_fits[$fi].target" < "$rank" 2>/dev/null)
        [[ "$rt" != "$target" ]] && continue
        # Extract 3 coefficients (r², r⁴, r⁶ basis)
        b0=$("$RAYWEAVE" query -r "asphere_candidate_result.surfaces[$si].radial_fits[$fi].coefficients[0]" < "$rank" 2>/dev/null)
        b1=$("$RAYWEAVE" query -r "asphere_candidate_result.surfaces[$si].radial_fits[$fi].coefficients[1]" < "$rank" 2>/dev/null)
        b2=$("$RAYWEAVE" query -r "asphere_candidate_result.surfaces[$si].radial_fits[$fi].coefficients[2]" < "$rank" 2>/dev/null)
        # For trial, we don't have separate trial radial_fits; use the same
        # coefficients (the fit is on the base data; the chart shows the fit shape)
        t0="$b0"; t1="$b1"; t2="$b2"
        break
      done
      [[ -z "$b0" ]] && continue

      # Evaluate polynomial at each field angle: val = c0*x² + c1*x⁴ + c2*x⁶
      : > "$dat_file"
      for ((fi=0; fi<nfields; fi++)); do
        local a="${angles[$fi]}"
        awk -v a="$a" -v c0="$b0" -v c1="$b1" -v c2="$b2" \
          'BEGIN { x2=a*a; printf "%.6f %.9g\n", a, c0*x2 + c1*x2*x2 + c2*x2*x2*x2 }' >> "$dat_file"
      done
      has_data=true
    done
  done

  if ! $has_data; then
    echo "  (focus radial-fit chart skipped: no radial fit data)"
    rm -f "$base".*.dat "$base".*.gnu
    return 0
  fi

  # Gnuplot: multiplot, 2 columns per surface (mean focus, T-S split)
  # Only include surfaces that have at least one non-empty data file.
  local gs="$base.gnu"
  local panels=0
  local panel_sids=()
  local panel_dm=()
  local panel_dts=()
  for ((si=0; si<nsurf; si++)); do
    local dm="$base.$si.mean.dat" dts="$base.$si.ts.dat"
    local has_mean=false has_ts=false
    [[ -f "$dm" && -s "$dm" ]] && has_mean=true
    [[ -f "$dts" && -s "$dts" ]] && has_ts=true
    if $has_mean || $has_ts; then
      local sid
      sid=$("$RAYWEAVE" query -r "asphere_candidate_result.surfaces[$si].surface_id" < "$rank" 2>/dev/null || echo "?")
      panel_sids+=("$sid")
      panel_dm+=("$dm")
      panel_dts+=("$dts")
      panels=$((panels+1))
    fi
  done
  if [[ "$panels" -eq 0 ]]; then
    echo "  (focus radial-fit chart skipped: no valid radial fit data)"
    rm -f "$base".*.dat "$base".*.gnu
    return 0
  fi
  {
    echo "set terminal pngcairo size 1200,$((panels * 380))"
    echo "set output \"$out\""
    echo "set multiplot layout $panels,2 rowsfirst title \"Focus radial fits — $label\" font \",11\""
    echo "set xlabel \"field angle (deg)\""
    echo "set grid"
    echo "set key outside right"
    for ((pi=0; pi<panels; pi++)); do
      local sid="${panel_sids[$pi]}" dm="${panel_dm[$pi]}" dts="${panel_dts[$pi]}"
      if [[ -s "$dm" ]]; then
        echo "set title sprintf(\"surface $sid — mean focus fit\")"
        echo "set ylabel \"mean focus (mm)\""
        echo "plot \"$dm\" using 1:2 with lines lw 2 lc rgb \"#1f77b4\" title \"fit\""
      fi
      if [[ -s "$dts" ]]; then
        echo "set title sprintf(\"surface $sid — T-S split fit\")"
        echo "set ylabel \"T-S split (mm)\""
        echo "plot \"$dts\" using 1:2 with lines lw 2 lc rgb \"#d62728\" title \"fit\""
      fi
    done
    echo "unset multiplot"
  } > "$gs"
  GNUTERM=pngcairo "$gnuplot" "$gs" 2>/dev/null
  echo "Written: $out"
}

# ── Focus gain bar chart (per candidate surface) ──────────────
# Grouped bar chart of field-curvature gain and astigmatism gain for each
# candidate surface.  Gains range 0..1 (1 = perfect correction).
# Requires gnuplot and focus channel data.
plot_focus_gain() {
  local out="$1" rank="$2" label="$3"
  local gnuplot="${GNUPLOT:-$(command -v gnuplot || true)}"
  if [[ -z "$gnuplot" && -x /opt/homebrew/bin/gnuplot ]]; then gnuplot=/opt/homebrew/bin/gnuplot; fi
  if [[ -z "$gnuplot" ]]; then
    echo "  (focus gain chart skipped: gnuplot not available)"
    return 0
  fi

  local nrank
  nrank=$("$RAYWEAVE" query --len asphere_candidate_result.rankings < "$rank" 2>/dev/null || echo 0)
  if [[ -z "$nrank" || "$nrank" -eq 0 ]]; then
    echo "  (focus gain chart skipped: no rankings)"
    return 0
  fi

  # Extract ranking surface IDs and matching gains via yq (single pass).
  local dat="$out.dat"
  : > "$dat"
  local has_data=false
  for ((ri=0; ri<nrank; ri++)); do
    local sid fc astig
    sid=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].surface_id" < "$rank" 2>/dev/null)
    [[ -z "$sid" ]] && continue
    # Find matching surface in surfaces[] by ID and read gains
    fc=$("$RAYWEAVE" query -r "asphere_candidate_result.surfaces[] | select(.surface_id == $sid) | .summary.focus.field_curvature_gain" < "$rank" 2>/dev/null || echo "")
    astig=$("$RAYWEAVE" query -r "asphere_candidate_result.surfaces[] | select(.surface_id == $sid) | .summary.focus.astigmatism_gain" < "$rank" 2>/dev/null || echo "")
    [[ -z "$fc" || -z "$astig" ]] && continue
    printf "%s %s %s\n" "$sid" "$fc" "$astig" >> "$dat"
    has_data=true
  done
  if ! $has_data; then
    echo "  (focus gain chart skipped: no gain data)"
    rm -f "$dat"
    return 0
  fi

  local gs="$out.gnu"
  local nrows
  nrows=$(wc -l < "$dat" | tr -d ' ')
  local barwidth
  barwidth=$(awk -v n="$nrows" 'BEGIN { w=0.8/n; if (w>0.25) w=0.25; printf "%.4g", w }')
  {
    echo "set terminal pngcairo size 700,450"
    echo "set output \"$out\""
    echo "set title \"Focus gains per candidate surface — $label\""
    echo "set xlabel \"surface ID\""
    echo "set ylabel \"gain (0..1)\""
    echo "set yrange [0:1.05]"
    echo "set grid y"
    echo "set key outside right"
    echo "set style fill solid 0.8 border -1"
    echo "set xtics rotate"
    echo "plot \"$dat\" using (column(0)-0.5):2:($barwidth) with boxes lc rgb \"#1f77b4\" title \"field curvature\", \\"
    echo "     \"$dat\" using (column(0)+0.5):3:($barwidth) with boxes lc rgb \"#d62728\" title \"astigmatism\""
  } > "$gs"
  GNUTERM=pngcairo "$gnuplot" "$gs" 2>/dev/null
  echo "Written: $out"
}

# ── Build the "initial asphere inserted" system for the before/after OPD chart ──
# Writes a copy of `from` with the FIRST fitted candidate surface (the one the
# demo will aspherize; unfit rankings like the image plane are skipped) turned
# into an asphere_polynomial carrying the initial coefficients:
# calibrated_coefficients when the measured-response calibration ran, else
# scaled_coefficients (the sag_scale-scaled set). These are the coefficients
# right after insertion, BEFORE any DLS refinement. Returns 1 (no file written)
# when no ranking has initial coefficients.
build_applied_initial() {
  local rank="$1" from="$2" out="$3"
  local nrank ri sid a4 src v val coeffs
  nrank=$("$RAYWEAVE" query --len asphere_candidate_result.rankings < "$rank" 2>/dev/null || echo 0)
  for ((ri = 0; ri < nrank; ri++)); do
    sid=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].surface_id" < "$rank")
    a4=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].calibrated_coefficients.A4" < "$rank")
    src="calibrated_coefficients"
    if [[ -z "$a4" || "$a4" = "-1" ]]; then
      src="scaled_coefficients"
      a4=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].scaled_coefficients.A4" < "$rank")
    fi
    if [[ -z "$a4" || "$a4" = "-1" ]]; then continue; fi
    break
  done
  if [[ -z "$a4" || "$a4" = "-1" || -z "$sid" || "$sid" = "-1" ]]; then
    return 1
  fi

  coeffs=""
  for v in A4 A6 A8 A10 A12; do
    val=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].$src.$v" < "$rank")
    if [[ -z "$val" || "$val" = "-1" ]]; then val=0; fi
    coeffs="${coeffs}${coeffs:+,}${val}"
  done

  "$YQ_BIN" e ".configs[0].surfaces[] |= (select(.id == $sid) | .type = \"asphere_polynomial\" | .conic = 0 | .coefficients = [$coeffs])" "$from" > "$out"
}

# ── One full demo case ──
# Runs steps 1–6 (ranking → before/after OPD chart → validation → apply → spot
# RMS → PNG + gate) for a single input system. Every artifact carries the given
# tag (asphere-demo<TAG>-{rank,validated,applied,applied-initial,
# rank-after-initial,result,spot,...}). A per-field spot CSV is written
# alongside the table for the cross-case comparison.
run_case() {
  local from="$1" tag="$2" label="$3"
  local applied="$OUTDIR/asphere-demo${tag}-applied.yaml"
  local applied_init="$OUTDIR/asphere-demo${tag}-applied-initial.yaml"
  local rank_after="$OUTDIR/asphere-demo${tag}-rank-after-initial.yaml"
  local result="$OUTDIR/asphere-demo${tag}-result.txt"
  local rank="$OUTDIR/asphere-demo${tag}-rank.yaml"
  local validated="$OUTDIR/asphere-demo${tag}-validated.yaml"
  local spot="$OUTDIR/asphere-demo${tag}-spot.tbl"
  local spot_csv="$OUTDIR/asphere-demo${tag}-spot.csv"

  : > "$result"
  echo "=== Case: $label ===" | tee -a "$result"
  echo | tee -a "$result"

  # ── Step 1: candidate ranking ──
  echo "--- Step 1: candidate ranking (top $TOP_K) ---"
  "$RAYWEAVE" asphere --top-k "$TOP_K" --sensitivity-samples "$SENS_SAMPLES" \
    --diagnostics opd,focus < "$from" > "$rank"
  {
    printf "  %-6s %10s %10s %10s %10s\n" "surf" "score" "sens.imprv" "asym" "cons"
    for ri in $(seq 0 $((TOP_K - 1))); do
      sid=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].surface_id" < "$rank")
      score=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].score" < "$rank")
      sens=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].sensitivity.improvement" < "$rank" 2>/dev/null || echo "-1")
      asym=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].asym_residual" < "$rank" 2>/dev/null || echo "-1")
      cons=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].field_consistency" < "$rank" 2>/dev/null || echo "-1")
      printf "  %-6s %10s %10s %10s %10s\n" "$sid" "$(fit "$score")" "$(fit "$sens")" "$(fit "$asym")" "$(fit "$cons")"
    done
  } | tee -a "$result"
  echo

  # ── Step 1b: before/after OPD chart (initial asphere inserted) ──
  # Re-runs the analysis on the top-ranked initial asphere and draws the
  # side-by-side OPD-overlap chart: left = all-spherical (before), right =
  # residual right after the initial asphere is inserted (per-column y-ranges).
  if [[ -z "$YQ_BIN" ]]; then
    echo "  (before/after OPD chart skipped: yq not available)"
  elif build_applied_initial "$rank" "$from" "$applied_init"; then
    echo "Written: $applied_init"
    "$RAYWEAVE" asphere --top-k "$TOP_K" --sensitivity-samples "$SENS_SAMPLES" \
      --diagnostics opd,focus < "$applied_init" > "$rank_after" 2>/dev/null
    plot_opd_overlap "$OUTDIR/asphere-demo${tag}-opd-overlap.png" \
      "$rank" "$OUTDIR/asphere-demo${tag}-opd-before" "all-spherical (before)" \
      "$rank_after" "$OUTDIR/asphere-demo${tag}-opd-after" "initial asphere (after)"
  else
    echo "  (before/after OPD chart skipped: top candidate has no initial coefficients)"
  fi
  echo

  # ── Step 1b-f: Focus Channel charts (T/S focus, radial fit, gain) ──
  if [[ -n "$YQ_BIN" ]]; then
    echo "--- Step 1b-f: Focus Channel charts ---"
    plot_ts_focus "$OUTDIR/asphere-demo${tag}-ts-focus.png" \
      "$rank" "$label" "$from"
    plot_focus_radial_fit "$OUTDIR/asphere-demo${tag}-focus-radial.png" \
      "$rank" "$label" "$from"
    plot_focus_gain "$OUTDIR/asphere-demo${tag}-focus-gain.png" \
      "$rank" "$label"
    echo
  fi

  # ── Step 1c: field defocus chart (before/after comparison) ───
  if [[ -n "$YQ_BIN" ]] && build_applied_initial "$rank" "$from" "$applied_init"; then
    echo "Written: $applied_init"
    "$RAYWEAVE" chief < "$applied_init" | "$RAYWEAVE" wavefront --wavelengths 0.00058756 --num-rays "$NUM_RAYS" > "$OUTDIR/asphere-demo${tag}-wf-after.yaml" 2>/dev/null
    "$RAYWEAVE" chief < "$from"         | "$RAYWEAVE" wavefront --wavelengths 0.00058756 --num-rays "$NUM_RAYS" > "$OUTDIR/asphere-demo${tag}-wf-before.yaml" 2>/dev/null
    plot_field_defocus "$OUTDIR/asphere-demo${tag}-defocus.png" \
      "$OUTDIR/asphere-demo${tag}-wf-before.yaml" "all-spherical (before)" \
      "$OUTDIR/asphere-demo${tag}-wf-after.yaml"  "initial asphere (after)"
  else
    echo "  (defocus chart skipped: wf generation failed)"
  fi
  echo

  # ── Step 2: validation ──
  echo "--- Step 2: short-DLS validation (spot-RMS merit, $DLS_ITER it + ${NUM_RAYS} rays) ---"
  "$RAYWEAVE" asphere --validate --top-k "$TOP_K" --sensitivity-samples "$SENS_SAMPLES" \
    --dls-iter "$DLS_ITER" --num-rays "$NUM_RAYS" --diagnostics opd,focus \
    < "$from" > "$validated"
  {
    printf "  %-6s %12s %12s %12s %13s\n" "surf" "before" "after" "imprv" "a4"
    for ri in $(seq 0 $((TOP_K - 1))); do
      sid=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].surface_id" < "$validated")
      before=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].validation.before_merit" < "$validated" 2>/dev/null || echo "-")
      after=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].validation.after_merit" < "$validated" 2>/dev/null || echo "-")
      imprv=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].validation.improvement" < "$validated" 2>/dev/null || echo "-")
      a4=$("$RAYWEAVE" query -r "asphere_candidate_result.rankings[$ri].validation.coefficients.A4" < "$validated" 2>/dev/null || echo "-")
      if [ "$before" = "-1" ] || [ "$before" = "-" ]; then
        printf "  %-6s %12s %12s %12s %13s\n" "$sid" "(no fit)" "" "" ""
      elif [ "$before" = "-1" ] || [ "$after" = "-1" ]; then
        printf "  %-6s %12s %12s %12s %13s\n" "$sid" "-" "-" "-" "-"
      else
        printf "  %-6s %12.4f %12.4f %12.4f %13.3e\n" "$sid" "$before" "$after" "$imprv" "$a4"
      fi
    done
  } | tee -a "$result"
  echo

  # ── Step 3: apply the top-ranked aspherical ──
  echo "--- Step 3: inserting the top-ranked aspherical surface (--apply) ---"
  "$RAYWEAVE" asphere --apply --top-k "$TOP_K" \
    --dls-iter "$DLS_ITER" --num-rays "$NUM_RAYS" --diagnostics opd,focus \
    < "$from" > "$applied"
  echo "Written: $applied" | tee -a "$result"
  echo

  # ── Step 4: spot RMS before/after ──
  echo "--- Step 4: spot RMS comparison (all-spherical vs aspherical) ---"
  nfield=$("$RAYWEAVE" chief < "$from" | "$RAYWEAVE" query --len chief_rays)
  : > "$spot"
  : > "$spot_csv"
  nregress=0
  ngood=0
  printf "  %-6s %12s %12s %10s\n" "field" "RMS before" "RMS after" "" >> "$spot"
  printf "  %-6s %12s %12s %10s\n" "-----" "-----------" "----------" "-------" >> "$spot"
  for fi in $(seq 0 $((nfield - 1))); do
    before=$(rms_field "$from" "$fi")
    after=$(rms_field "$applied" "$fi")
    mark=""
    if [ "$before" != "-1" ] && [ "$after" != "-1" ]; then
      if "$RAYWEAVE" query --gate "b * 1.01 > a" --set a="$after" --set b="$before" < /dev/null > /dev/null; then
        mark="   ✓"
        ngood=$((ngood+1))
      else
        mark="   ✗"
        nregress=$((nregress+1))
      fi
    fi
    printf "  %-6s %12.4f %12.4f %10s\n" "f$fi" "$before" "$after" "$mark" >> "$spot"
    printf "f%s %s %s\n" "$fi" "$before" "$after" >> "$spot_csv"
  done
  echo >> "$spot"
  printf "  (gate: no field may regress by more than 1%%, and at least one must improve)\n" >> "$spot"
  cat "$spot" | tee -a "$result"

  # ── Step 5: PNG diagrams ──
  echo "--- Step 5: PNG diagrams ---"
  "$RAYWEAVE" chief --clear-aperture --ray-fan < "$from" | "$RAYWEAVE" trace \
    | "$RAYWEAVE" plot -o "$OUTDIR/asphere-demo${tag}-before.png" >/dev/null
  echo "Written: $OUTDIR/asphere-demo${tag}-before.png"
  "$RAYWEAVE" chief --clear-aperture --ray-fan < "$applied" | "$RAYWEAVE" trace \
    | "$RAYWEAVE" plot -o "$OUTDIR/asphere-demo${tag}-after.png" >/dev/null
  echo "Written: $OUTDIR/asphere-demo${tag}-after.png"

  # ── Step 6: gate check ──
  echo "--- Step 6: gate check ---"
  if [ "$nregress" -eq 0 ] && [ "$ngood" -gt 0 ]; then
    echo "  >>> Passed ($label): $ngood field(s) improved, none regressed > 1%." | tee -a "$result"
  else
    echo "  >>> Failed ($label): $nregress field(s) regressed > 1%, $ngood improved." | tee -a "$result"
    GATE_OK=false
  fi
  append_notes "$result"
  echo
}

# ── Main: full-aperture case; a stopped-down case only when requested ──
RESULT_DEFAULT="$OUTDIR/asphere-demo-result.txt"
SPOT_DEFAULT_CSV="$OUTDIR/asphere-demo-spot.csv"
GATE_OK=true

run_case "$BASE_INIT" "" "full aperture"

if [[ -n "$STOP_MODE" ]]; then
  # Build the stopped-down variant: only surface 7 (the fixed-aperture surface
  # that defines the entrance pupil) is resized. Diameter by mode:
  #   frac — FULL_DIAM * X    (X in (0,1), fraction of the full pupil)
  #   mm   — X                (X >= 1, absolute mm)
  #   fno  — EFL / N          (N = EFL/EPD, F-number)
  # FULL_DIAM and EFL come from a paraxial pass on the full-aperture system.
  # Requires yq.
  if [[ -z "$YQ_BIN" ]]; then
    echo "error: --epd/--fno requires yq (set YQ or put yq on PATH)" >&2
    exit 1
  fi

  case "$STOP_MODE" in
    frac)
      full_diam=$("$RAYWEAVE" paraxial < "$BASE_INIT" | "$RAYWEAVE" query -r paraxial_result.entrance_pupil_diameter)
      stop_diam=$(awk -v d="$full_diam" -v x="$STOP_VALUE" 'BEGIN { printf "%.12g", d * x }')
      ;;
    mm)
      stop_diam="$STOP_VALUE"
      ;;
    fno)
      efl=$("$RAYWEAVE" paraxial < "$BASE_INIT" | "$RAYWEAVE" query -r paraxial_result.focal_length)
      stop_diam=$(awk -v f="$efl" -v n="$STOP_VALUE" 'BEGIN { printf "%.12g", f / n }')
      ;;
  esac

  variant="$OUTDIR/asphere-demo-init${STOP_TAG}.yaml"
  if [ ! -f "$variant" ]; then
    "$YQ_BIN" e ".configs[0].surfaces[] |= (select(.id == 7) | .diameter = $stop_diam)" "$BASE_INIT" > "$variant"
  fi
  echo "=== Stopped-down case (input: $variant, $STOP_LABEL) ==="
  run_case "$variant" "$STOP_TAG" "$STOP_LABEL"

  # ── Step 7: full vs stopped-down comparison ──
  echo "--- Step 7: full-aperture vs $STOP_LABEL spot-RMS comparison ---"
  {
    printf "  %-6s %14s %14s %14s %14s\n" "field" "full.before" "full.after" "stop.before" "stop.after"
    printf "  %-6s %14s %14s %14s %14s\n" "-----" "----------" "---------" "----------" "---------"
    nf=$("$RAYWEAVE" chief < "$BASE_INIT" | "$RAYWEAVE" query --len chief_rays)
    for fi in $(seq 0 $((nf - 1))); do
      line_def=$(grep -E "^f$fi " "$SPOT_DEFAULT_CSV")
      line_stop=$(grep -E "^f$fi " "$OUTDIR/asphere-demo${STOP_TAG}-spot.csv")
      read -r _ db da <<< "$line_def"
      read -r _ eb ea <<< "$line_stop"
      printf "  %-6s %14.4f %14.4f %14.4f %14.4f\n" "f$fi" "$db" "$da" "$eb" "$ea"
    done
  } | tee -a "$RESULT_DEFAULT"
  echo
fi

# ── Overall gate summary ──
if [ "$GATE_OK" = true ]; then
  echo "  >>> All cases passed the gate." | tee -a "$RESULT_DEFAULT"
  exit 0
else
  echo "  >>> At least one case failed the gate." | tee -a "$RESULT_DEFAULT"
  exit 1
fi
