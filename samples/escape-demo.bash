#!/bin/bash
set -euo pipefail

# =============================================================================
# escape-demo.bash — global optimisation with the escape function
#
# Purpose: show the Ishiki-Ono style escape-function optimiser: DLS cycles
# with merit-function bumps at discovered local minima, so the result is a
# list of local minima and the best one wins.
#
# Two lenses are supported:
#   escape-demo.bash                     : degraded US2645157 triplet (default)
#   escape-demo.bash --lens doublegauss  : 6-element double-Gauss (f/2.8 50 mm)
# The double-Gauss run is much slower (36 variables, 256 rays) but uses the
# same escape section baked into samples/doublegauss-init.yaml.
#
# Steps
#   1. escape                  : global search -> <prefix>result.yaml
#   2. doublegauss --save/--log: every discovered minimum written to a clean
#                                <prefix>min1.yaml, <prefix>min2.yaml, ... as
#                                it is found (interrupt/kill safe), progress
#                                streamed to <prefix>progress.jsonl
#      triplet                 : escape extract --index N pulls one full
#                                local-minimum system out
#   3. plot                    : diagrams of the initial and best systems
#
# How to read the result
#   - Best merit is the lowest DLS merit among the discovered minima.
#   - The result YAML holds every minimum's full surfaces; use
#     `escape extract --index N` to re-optimise from a chosen one. With
#     --save the per-minimum clean lens files are already on disk.
# =============================================================================

# Resolve the script's own directory so the demo runs from any CWD
# (repo root, `cd samples`, or a copied location). All data files are read
# from and all outputs are written to this directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTDIR="$SCRIPT_DIR"

# CLI options
LENS="triplet"
CLEAN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=true; shift ;;
    --lens)
      shift
      case "${1:-}" in
        triplet|doublegauss) LENS="$1"; shift ;;
        *) echo "Error: --lens must be 'triplet' or 'doublegauss' (got '${1:-}')"; exit 1 ;;
      esac
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

case "$LENS" in
  triplet)
    YAML="$SCRIPT_DIR/escape-demo.yaml"
    PREFIX="escape-demo-"
    LENS_NAME="degraded US2645157 triplet"
    SAVE_BASE=""   # triplet: keep the demo light, no per-minimum save/log
    LOG_FILE=""
    ;;
  doublegauss)
    YAML="$SCRIPT_DIR/doublegauss-init.yaml"
    PREFIX="escape-demo-doublegauss-"
    LENS_NAME="6-element double-Gauss (f/2.8 50 mm)"
    # Long run: save every discovered minimum to a clean lens file (--save) and
    # stream the JSONL progress to a log (--log), so a killed run never loses
    # already-found minima and the progress is inspectable afterwards.
    SAVE_BASE="$OUTDIR/${PREFIX}min"
    LOG_FILE="$OUTDIR/${PREFIX}progress.jsonl"
    ;;
esac
RESULT="$OUTDIR/${PREFIX}result.yaml"
RESULT_FILE="$OUTDIR/${PREFIX}result.txt"

if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$OUTDIR"/escape-demo-result.yaml "$OUTDIR"/escape-demo-result.txt
  rm -f "$OUTDIR"/escape-demo-init.png "$OUTDIR"/escape-demo-best.png "$OUTDIR"/escape-demo-min1.png "$OUTDIR"/escape-demo-min1.yaml
  rm -f "$OUTDIR"/escape-demo-doublegauss-result.yaml "$OUTDIR"/escape-demo-doublegauss-result.txt
  rm -f "$OUTDIR"/escape-demo-doublegauss-progress.jsonl
  rm -f "$OUTDIR"/escape-demo-doublegauss-min*.yaml
  rm -f "$OUTDIR"/escape-demo-doublegauss-init.png "$OUTDIR"/escape-demo-doublegauss-best.png "$OUTDIR"/escape-demo-doublegauss-min1.png
  echo "  Removed: triplet and double-Gauss escape outputs"
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

# ── Interpretation notes: appended to the result file on exit. ──
append_interpretation() {
cat >> "$RESULT_FILE" <<EOF

=== How to interpret this result ===
- Escape-function global optimisation of the $LENS_NAME.
- Best merit is the lowest DLS merit among all discovered local minima
  (lower = better); [0] is the best minimum, [1] a worse secondary one.
- The '*' marks the best minimum. $(basename "$RESULT") contains the full
  surfaces of every minimum; \`escape extract --index N\` pulls one out
  (e.g. ${PREFIX}min1.yaml) to re-optimise from.
- \`element_powers\` (per minimum, per config) is the thin-lens power of each
  lens element at the d-line — a fingerprint for comparing the minima against
  each other.
- If the best merit is only marginally better than a plain local DLS run,
  the merit landscape is effectively single-modal and escape is unnecessary.
EOF
if [[ -n "$SAVE_BASE" ]]; then
cat >> "$RESULT_FILE" <<EOF
- This double-Gauss run used --save: every discovered minimum was written to a
  clean lens file (${PREFIX}min1.yaml, ${PREFIX}min2.yaml, ...) as it was found
  (atomic writes, so even a killed run keeps them). When a minimum is improved,
  the previous version is archived as ${PREFIX}minN.<version>.yaml. The JSONL
  progress went to ${PREFIX}progress.jsonl (read it with \`query --jsonl\`).
EOF
fi
}
trap append_interpretation EXIT

echo "=== Escape demo: global optimisation of the $LENS_NAME ==="
echo

echo "--- Running escape-function global optimisation (JSONL progress on stderr) ---"
ESCAPE_ARGS=(--verbose)
if [[ -n "$LOG_FILE" ]]; then
  ESCAPE_ARGS+=(--log "$LOG_FILE")
fi
if [[ -n "$SAVE_BASE" ]]; then
  ESCAPE_ARGS+=(--save "$SAVE_BASE")
fi
$RAYWEAVE escape "${ESCAPE_ARGS[@]}" < "$YAML" > "$RESULT"
echo

echo "--- Local minima summary ---"
{
  BEST_IDX=$($RAYWEAVE query -r escape_result.best_index < "$RESULT")
  BEST_MERIT=$($RAYWEAVE query --printf '%.6e' escape_result.best_merit < "$RESULT")
  echo "  Best index: $BEST_IDX  Best merit: $BEST_MERIT"
  $RAYWEAVE query --each 'escape_result.minima[]:index,merit' --printf '%d %.6e' < "$RESULT" \
    | while read -r idx merit; do
        mark=" "
        [ "$idx" = "$BEST_IDX" ] && mark="*"
        powers=$($RAYWEAVE query --each "escape_result.minima[$idx].features[0].element_powers[]" \
          --printf '%.4g' < "$RESULT" | paste -sd ',' -)
        if [[ -n "$SAVE_BASE" ]]; then
          file=$(basename "$($RAYWEAVE query -r "escape_result.minima[$idx].file" < "$RESULT")")
          printf "  %s[%s] merit=%s  file=%s  element_powers=%s\n" "$mark" "$idx" "$merit" "$file" "$powers"
        else
          printf "  %s[%s] merit=%s  element_powers=%s\n" "$mark" "$idx" "$merit" "$powers"
        fi
      done
} | tee "$RESULT_FILE"
echo

echo "--- PNG diagrams ---"
echo "=== Initial diagram ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$YAML" | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/${PREFIX}init.png" >/dev/null
echo "Written: $OUTDIR/${PREFIX}init.png"

echo "=== Best-solution diagram ==="
$RAYWEAVE chief --clear-aperture --ray-fan < "$RESULT" | $RAYWEAVE trace \
  | $RAYWEAVE plot -o "$OUTDIR/${PREFIX}best.png" >/dev/null
echo "Written: $OUTDIR/${PREFIX}best.png"

if [[ -n "$SAVE_BASE" ]]; then
  echo "=== Saved local minima (--save) ==="
  echo "  Every discovered minimum was written as a clean lens file:"
  ls -1 "$OUTDIR"/${PREFIX}min[0-9]*.yaml 2>/dev/null | sed 's#.*/##' | sed 's/^/    /' || true
else
  echo "=== Extracting local minimum 1 ==="
  $RAYWEAVE escape extract --index 1 < "$RESULT" > "$OUTDIR/${PREFIX}min1.yaml"
  echo "Written: $OUTDIR/${PREFIX}min1.yaml"
fi
