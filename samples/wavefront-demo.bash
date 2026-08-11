#!/bin/bash
set -euo pipefail

# =============================================================================
# wavefront-demo.bash — wavefront analysis / Zernike / best-focus demo
#
# Purpose: demonstrate the `rayweave wavefront` subcommand. For each field it
# produces the three complementary wavefront descriptions in a single pass:
#   1. paraboloid fit  — the low-order OPD shape (defocus / astigmatism /
#                        tilt coefficients),
#   2. best-focus sphere — the geometric-spot-RMS refocus shift along the
#                        image-plane normal,
#   3. stabilized Fringe-Zernike — the high-order terms after the low-order
#                        removal (stable off-axis).
# It then draws one interpolated OPD (wavefront aberration) map per field and
# shows the best-focus correction: applying the weighted image-plane shift and
# re-analysing makes the (weighted) defocus vanish.
#
# A controlled `--defocus D` shifts the image plane by +D mm first, so the
# best-focus correction becomes visibly dramatic (defocus D -> ~0, OPD map
# rings collapse). `--lens doublegauss` analyses the 4-field double-Gauss
# (which starts ~8 mm out of focus).
#
# The analysis runs at the d-line (587.56 nm) so each angle field yields
# exactly one row.
#
# Options
#   --clean            remove every generated artifact; tracked input YAMLs are
#                      never touched.
#   --lens NAME        'triplet' (default, samples/us2645157.yaml), 'doublegauss'
#                      (samples/doublegauss-init.yaml), or a path to any input
#                      YAML with a chief section.
#   --num-rays N       pupil grid rays (default 400)
#   --zernike-order N  highest Fringe Zernike index to fit (default 15)
#   --defocus D        apply a +D mm image-plane shift first (requires yq); the
#                      best-focus shift then corrects it.
#   --focus-weight T   best-focus weighting: uniform (default) | custom
#   --focus-weights W1,...  per-field weights when --focus-weight custom
#
# How to read the output
#   - 0° is near diffraction-limited: tiny paraboloid terms, Strehl ~0.99 and a
#     nearly flat OPD map. The 16°/24° fields are coma+astigmatism dominated
#     (large Zernike coma coefficients, Strehl -> 0), clearly visible as the
#     asymmetric (non-rotationally-symmetric) OPD maps.
#   - The "defocus before / after" table shows the weighted best-focus shift
#     removing the common defocus: the after column is the residual field
#     curvature at the flat best-focus plane.
#   - With --defocus the before OPD map shows concentric defocus rings; the
#     after map (drawn in -after.png) is the corrected flat wavefront.
# =============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTDIR="$SCRIPT_DIR"

LENS="triplet"
NUM_RAYS=400
ZERNIKE_ORDER=15
DEFOCUS=""
FOCUS_WEIGHT=""
FOCUS_WEIGHTS=""
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
    --num-rays)
      NUM_RAYS="$2"; shift 2
      if ! awk -v x="$NUM_RAYS" 'BEGIN { exit !(x >= 16) }' /dev/null; then
        echo "error: --num-rays expects an integer >= 16 (got '$NUM_RAYS')" >&2
        exit 1
      fi
      ;;
    --zernike-order)
      ZERNIKE_ORDER="$2"; shift 2
      if ! awk -v x="$ZERNIKE_ORDER" 'BEGIN { exit !(x >= 7 && x <= 37) }' /dev/null; then
        echo "error: --zernike-order expects an integer in 7..37 (got '$ZERNIKE_ORDER')" >&2
        exit 1
      fi
      ;;
    --defocus)
      DEFOCUS="$2"; shift 2
      if ! awk -v x="$DEFOCUS" 'BEGIN { exit !(x > 0) }' /dev/null; then
        echo "error: --defocus expects a positive number in mm (got '$DEFOCUS')" >&2
        exit 1
      fi
      ;;
    --focus-weight)
      FOCUS_WEIGHT="$2"; shift 2
      case "$FOCUS_WEIGHT" in
        uniform|custom) ;;
        *) echo "error: --focus-weight expects 'uniform' or 'custom' (got '$FOCUS_WEIGHT')" >&2; exit 1 ;;
      esac
      ;;
    --focus-weights)
      FOCUS_WEIGHTS="$2"; shift 2 ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# Resolve the input YAML, output stem and human-readable lens name.
case "$LENS" in
  triplet)
    YAML="$SCRIPT_DIR/us2645157.yaml"
    STEM="wavefront-demo"
    LENS_NAME="US2645157 Cooke triplet (3 fields)"
    ;;
  doublegauss)
    YAML="$SCRIPT_DIR/doublegauss-init.yaml"
    STEM="wavefront-demo-doublegauss"
    LENS_NAME="6-element double-Gauss (4 fields)"
    ;;
  *)
    if [[ -f "$LENS" ]]; then
      YAML="$LENS"
      STEM="wavefront-demo"
      LENS_NAME="$(basename "$LENS")"
    else
      echo "error: --lens must be 'triplet', 'doublegauss', or a path to an input YAML (got '$LENS')" >&2
      exit 1
    fi
    ;;
esac
if [[ -n "$DEFOCUS" ]]; then
  STEM="${STEM}-defocus${DEFOCUS}"
fi

RESULT_YAML="$OUTDIR/$STEM-result.yaml"
RESULT_AFTER_YAML="$OUTDIR/$STEM-after.yaml"
RESULT_TXT="$OUTDIR/$STEM-result.txt"
YAML_BASE="$OUTDIR/$STEM"
CSV_BASE="$OUTDIR/$STEM"
DEFOCUS_YAML="$OUTDIR/$STEM-defocused.yaml"
AFTER_YAML_BASE="$OUTDIR/$STEM-after"
AFTER_CSV_BASE="$OUTDIR/$STEM-after"

# Clean-only mode: remove generated files for every known stem and exit.
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  for s in wavefront-demo wavefront-demo-doublegauss; do
    rm -f "$OUTDIR/$s"-result.yaml "$OUTDIR/$s"-result.txt "$OUTDIR/$s"-after.yaml
    rm -f "$OUTDIR/$s"-defocused.yaml
    rm -f "$OUTDIR/$s"_*.csv "$OUTDIR/$s"_*.yaml
    rm -f "$OUTDIR/$s"-after_*.csv "$OUTDIR/$s"-after_*.yaml
    rm -f "$OUTDIR/$s"-*.png
  done
  # Remove defocus-variant artifacts (wavefront-demo-defocusD*) too.
  for f in "$OUTDIR"/wavefront-demo-*-result.yaml "$OUTDIR"/wavefront-demo-*-after.yaml \
           "$OUTDIR"/wavefront-demo-*-result.txt "$OUTDIR"/wavefront-demo-*-defocused.yaml \
           "$OUTDIR"/wavefront-demo-*-*.png; do
    [[ -e "$f" ]] && rm -f "$f"
  done
  rm -f "$OUTDIR"/wavefront-demo-defocus*_*.csv "$OUTDIR"/wavefront-demo-defocus*_*.yaml \
        "$OUTDIR"/wavefront-demo-defocus*-after_*.csv "$OUTDIR"/wavefront-demo-defocus*-after_*.yaml
  echo "  Removed: wavefront-demo outputs (result/after/txt/csv/yaml/png/defocused lens)"
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

# Locate gnuplot (charts are optional) and yq (only needed by --defocus).
GNUPLOT="${GNUPLOT:-$(command -v gnuplot || true)}"
if [[ -z "$GNUPLOT" && -x /opt/homebrew/bin/gnuplot ]]; then
  GNUPLOT=/opt/homebrew/bin/gnuplot
fi
local_yq="${YQ:-$(command -v yq || true)}"
if [[ -z "$local_yq" && -x /opt/homebrew/bin/yq ]]; then
  local_yq=/opt/homebrew/bin/yq
fi

# --defocus: shift the image plane by +D mm via its last decenter Z shift
# (the same mechanism the wavefront --best-focus shift uses), so the
# best-focus correction will undo it.
if [[ -n "$DEFOCUS" ]]; then
  if [[ -z "$local_yq" ]]; then
    echo "error: --defocus requires yq (set YQ or put yq on PATH)" >&2
    exit 1
  fi
  echo "=== Applying +${DEFOCUS} mm defocus to the image plane ==="
  "$local_yq" e ".configs[0].surfaces[-1].decenter[0].shift[2] = $DEFOCUS" "$YAML" > "$DEFOCUS_YAML"
  YAML="$DEFOCUS_YAML"
fi

# Build the shared wavefront CLI options.
WF_ARGS=(--num-rays "$NUM_RAYS" --zernike-order "$ZERNIKE_ORDER" --wavelengths 0.00058756)
if [[ -n "$FOCUS_WEIGHT" ]]; then
  WF_ARGS+=(--focus-weight "$FOCUS_WEIGHT")
fi
if [[ -n "$FOCUS_WEIGHTS" ]]; then
  WF_ARGS+=(--focus-weights "$FOCUS_WEIGHTS")
fi

# 1. Single analysis pass: chief -> wavefront --best-focus. The pipeline YAML
#    carries wavefront_result (paraboloid + sphere + Zernike + statistics +
#    best_focus) and the output configs carry the applied image-plane shift.
echo "=== Wavefront analysis: $LENS_NAME (${NUM_RAYS} rays, Fringe <= $ZERNIKE_ORDER, d-line) ==="
"$RAYWEAVE" chief < "$YAML" 2>/dev/null \
  | "$RAYWEAVE" wavefront --best-focus "${WF_ARGS[@]}" \
      --yaml "$YAML_BASE.yaml" --csv "$CSV_BASE.csv" \
  > "$RESULT_YAML"

NF=$("$RAYWEAVE" query --len "wavefront_result.fields" < "$RESULT_YAML")
[[ "$NF" -gt 0 ]] || { echo "error: wavefront analysis produced no fields" >&2; exit 1; }

# qscalar reads a scalar query path, with a dash fallback when absent.
qscalar() {
  local path=$1 default="${2:--}"
  "$RAYWEAVE" query -r --default "$default" "$path" < "$RESULT_YAML"
}

# top_zernike extracts the top-N Fringe terms by |coefficient| for field $1 as
# "name(+c), name(+c), ...". It is fed from a per-field terms listing.
top_zernike() {
  local fi=$1 n=$2
  local base="wavefront_result.fields[$fi].zernike.terms"
  local nt; nt=$("$RAYWEAVE" query --len "$base" < "$RESULT_YAML")
  [[ "${nt:-0}" -gt 0 ]] || { echo "-"; return; }
  local tmp=""
  for ((k = 0; k < nt; k++)); do
    local nm c
    nm=$("$RAYWEAVE" query -r "${base}[$k].name" < "$RESULT_YAML")
    c=$("$RAYWEAVE" query -r "${base}[$k].coefficient" < "$RESULT_YAML")
    tmp+="$(awk -v c="$c" -v n="$nm" 'BEGIN { a=c<0?-c:c; printf "%e|%s|%+.3g\n", a, n, c }')\n"
  done
  printf "%b" "$tmp" | sort -gr | head -n "$n" | awk -F'|' '{ printf "%s(%s), ", $2, $3 }'
  echo ""
}

# 2. Wavefront summary table (stdout + <stem>-result.txt).
{
  echo "Wavefront analysis — $LENS_NAME, ${NUM_RAYS} rays, Fringe <= $ZERNIKE_ORDER, d-line"
  echo "field  angle  defocus     astig       tilt       RMS        PV       Strehl   shift(mm)   dominant Zernike"
  for ((i = 0; i < NF; i++)); do
    F="wavefront_result.fields[$i]"
    ang=$(qscalar "$F.field_angle" 0)
    df=$(qscalar "$F.paraboloid.defocus" 0)
    as=$(qscalar "$F.paraboloid.astigmatism" 0)
    tl=$(qscalar "$F.paraboloid.tilt" 0)
    rms=$(qscalar "$F.statistics.rms" 0)
    pv=$(qscalar "$F.statistics.pv" 0)
    st=$(qscalar "$F.statistics.strehl" 0)
    sh=$(qscalar "wavefront_result.best_focus.per_field[$i].shift_mm" 0)
    zn=$(top_zernike "$i" 3)
    awk -v i="$i" -v a="$ang" -v d="$df" -v as="$as" -v tl="$tl" -v r="$rms" \
        -v p="$pv" -v s="$st" -v sh="$sh" -v zn="$zn" \
        'BEGIN { printf "  %2d   %6.1f  %9.3e %9.3e %9.3e %9.3e %9.3e  %8.4f  %+9.4f   %s\n",
                 i, a, d, as, tl, r, p, s, sh, zn }'
  done
  echo "weighted best-focus shift: $(qscalar 'wavefront_result.best_focus.weighted_average.shift_mm' 0) mm"
  echo "(positive shift = move the image plane away from the lens, +Z)"
} | tee "$RESULT_TXT"
echo "Written: $RESULT_TXT"
echo

# 3. Best-focus before/after: re-analyse the shifted (best-focus) configs.
#    The weighted-mean defocus is removed; the residual is field curvature at
#    the flat best-focus plane. The after --csv maps (--defocus only) feed the
#    -after.png charts.
echo "=== Best-focus correction (re-analysis at the shifted image plane) ==="
AFTER_WF_ARGS=("${WF_ARGS[@]}")
if [[ -n "$DEFOCUS" ]]; then
  AFTER_WF_ARGS+=(--yaml "$AFTER_YAML_BASE.yaml" --csv "$AFTER_CSV_BASE.csv")
fi
"$RAYWEAVE" wavefront "${AFTER_WF_ARGS[@]}" \
  < "$RESULT_YAML" > "$RESULT_AFTER_YAML"
{
  echo "field  angle   defocus before    defocus after    RMS before    RMS after"
  for ((i = 0; i < NF; i++)); do
    F="wavefront_result.fields[$i]"
    ang=$(qscalar "$F.field_angle" 0)
    db=$(qscalar "$F.paraboloid.defocus" 0)
    rb=$(qscalar "$F.statistics.rms" 0)
    da=$("$RAYWEAVE" query -r --default "-" "$F.paraboloid.defocus" < "$RESULT_AFTER_YAML")
    ra=$("$RAYWEAVE" query -r --default "-" "$F.statistics.rms" < "$RESULT_AFTER_YAML")
    awk -v i="$i" -v a="$ang" -v db="$db" -v da="$da" -v rb="$rb" -v ra="$ra" \
        'BEGIN { printf "  %2d   %6.1f  %13.4e %13.4e %12.4e %12.4e\n",
                 i, a, db, da, rb, ra }'
  done
  echo
  echo "Note: the weighted shift removes the common (on-axis) defocus. For"
  echo "off-axis angle fields the paraboloid defocus includes the field-launch"
  echo "geometry, so it does not vanish with a flat-plane shift — the on-axis"
  echo "row is the clean indicator. Use --defocus D to see the correction"
  echo "directly."
} | tee -a "$RESULT_TXT"
echo

if [[ -z "$GNUPLOT" ]]; then
  echo "(charts skipped: gnuplot not available)"
  exit 0
fi

export GNUTERM=pngcairo

# draw_map renders CSV_BASE_N.csv as an OPD pm3d map PNG, diverging blue-white-
# red centered on 0. The scale is the max |OPD| over the field's own samples.
draw_map() {
  local csv=$1 out=$2 title=$3
  local mx
  mx=$(awk -F, 'NR>1 && NF>=3 && $3+0==$3 { a=$3<0?-$3:$3; if (a>m) m=a } END { print m+0 }' "$csv")
  if awk -v m="$mx" 'BEGIN { exit !(m <= 0) }' /dev/null; then mx=1; fi
  "$GNUPLOT" <<GPLOT 2>/dev/null
    set terminal pngcairo size 640,600
    set output "$out"
    set datafile separator ","
    set pm3d map
    set palette defined (0 "#0000ff", 0.5 "#ffffff", 1 "#ff0000")
    set size square
    set title "$title"
    set xlabel "x (mm)"
    set ylabel "y (mm)"
    set cblabel "OPD (mm)"
    set cbrange [-$mx:$mx]
    splot "$csv" u 1:2:3 with pm3d
GPLOT
}

# 4. Per-field OPD maps (wavefront aberration, reference-sphere residual).
for ((i = 0; i < NF; i++)); do
  ang=$(qscalar "wavefront_result.fields[$i].field_angle" 0)
  strehl=$(qscalar "wavefront_result.fields[$i].statistics.strehl" 0)
  draw_map "${CSV_BASE}_${i}.csv" "$OUTDIR/$STEM-${i}.png" \
    "${ang}° field — Strehl ${strehl}"
  echo "Written: $OUTDIR/$STEM-${i}.png"
done

# 5. With --defocus, also draw the corrected (after) OPD maps side by side.
if [[ -n "$DEFOCUS" ]]; then
  for ((i = 0; i < NF; i++)); do
    ang=$(qscalar "wavefront_result.fields[$i].field_angle" 0)
    strehl=$("$RAYWEAVE" query -r --default "-" "wavefront_result.fields[$i].statistics.strehl" < "$RESULT_AFTER_YAML")
    if [[ -f "${AFTER_CSV_BASE}_${i}.csv" ]]; then
      draw_map "${AFTER_CSV_BASE}_${i}.csv" "$OUTDIR/$STEM-${i}-after.png" \
        "${ang}° field after best focus — Strehl ${strehl}"
      echo "Written: $OUTDIR/$STEM-${i}-after.png"
    fi
  done
fi

echo
echo "See $RESULT_TXT for the full table."
