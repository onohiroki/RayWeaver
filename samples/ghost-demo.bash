#!/bin/bash
set -euo pipefail

# =============================================================================
# ghost-demo.bash — ghost-ray tracing (Ono et al. surface-sequence encoding)
#
# Purpose: trace a double-reflection ghost path through the optimised
# double-Gauss and quantify how much fainter it is than the normal image.
# Each ray carries an ordered surface-ID list; a direction reversal in the
# list means reflection, so [0,1,2,3,4,3,2,3,4,...,14] reflects at surface 4,
# refracts backwards through surface 3, reflects at surface 2, then returns
# forward to the image (surface 14).
#
# Steps
#   1. chief --clear-aperture --shrink : re-size lens diameters to the beam
#   2. trace                           : trace the ghost + normal reference rays
#   3. python                          : per-surface table + Fresnel intensities
#   4. plot                            : SVG diagram of the ghost path
#
# How to read the result
#   - Ghost relative intensity = product of the two Fresnel reflectances
#     (~0.004): a double reflection is ~250x fainter than the image.
#   - The ghost lands near the on-axis image point (the double reflection
#     keeps it close to the axis), so it appears as a faint halo.
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

YAML="$SCRIPT_DIR/doublegauss-ghost.yaml"
OUTDIR="$SCRIPT_DIR"
TRACE_RESULT="$OUTDIR/doublegauss-ghost-trace-result.yaml"
RESULT_FILE="$OUTDIR/ghost-demo-result.txt"
SVG="$OUTDIR/doublegauss-ghost.svg"

# Clean-only mode
if [ "$CLEAN" = true ]; then
  echo "=== Cleaning up generated files ==="
  rm -f "$TRACE_RESULT" "$RESULT_FILE" "$SVG"
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

echo "=== Double-Gauss ghost ray tracing demo ==="
echo
echo "Optical system: 6-element symmetric double-Gauss (50 mm f/2.8), optimised."
echo "Ghost path: [0,1,2,3,4,3,2,3,4,...,14]"
echo "  - forward to surface 4, reflect there (pattern 3,4,3)"
echo "  - reversed refraction through surface 3 (backward pass)"
echo "  - reflect at surface 2 (pattern 3,2,3)"
echo "  - forward again to the image (surface 14)"
echo

# ── Re-adjust lens clear apertures to the beam footprint ──
# `chief --clear-aperture --shrink` re-computes every surface's effective
# diameter from the traced grid beams (the aperture stop keeps its diameter).
# It replaces the `rays` section with its own chief rays, so the ghost/normal
# rays are re-attached afterwards before tracing.
TMP_ADJ="$(mktemp "${TMPDIR:-/tmp}/doublegauss-ghost-adjusted.XXXXXX.yaml")"
trap 'rm -f "$TMP_ADJ"' EXIT
echo "=== Re-adjusting lens effective diameters (chief --clear-aperture --shrink) ==="
$RAYWEAVE chief --clear-aperture --shrink --clear-aperture-rays 1024 < "$YAML" > "$TMP_ADJ"
python3 - "$TMP_ADJ" "$YAML" <<'PYEOF' | $RAYWEAVE trace > "$TRACE_RESULT"
import sys
import yaml

adj = yaml.safe_load(open(sys.argv[1]))
orig = yaml.safe_load(open(sys.argv[2]))
adj["rays"] = orig["rays"]
adj.pop("chief_rays", None)
yaml.safe_dump(adj, sys.stdout, default_flow_style=None, sort_keys=False)
PYEOF
echo "  Written: $TRACE_RESULT"
echo

# ── Adjusted diameters ──
echo "--- Effective diameters after adjustment ---"
python3 - "$YAML" "$TRACE_RESULT" <<'PYEOF'
import sys
import yaml

before = {s["id"]: s.get("diameter", 0) for s in yaml.safe_load(open(sys.argv[1]))["configs"][0]["surfaces"]}
after = {s["id"]: s.get("diameter", 0) for s in yaml.safe_load(open(sys.argv[2]))["configs"][0]["surfaces"]}
for sid in sorted(before):
    if after.get(sid) != before.get(sid):
        print("  s%-2d  %6.2f -> %6.2f mm" % (sid, before[sid], after[sid]))
PYEOF
echo

# ── Per-surface table and ghost intensity ──
python3 - "$TRACE_RESULT" > "$RESULT_FILE" <<'PYEOF'
import sys
import yaml

d = yaml.safe_load(open(sys.argv[1]))
paths = {r["id"]: r.get("path", []) for r in d.get("rays", {}).get("rays", [])}

for r in d.get("results", []):
    rid = r["id"]
    print("ray: %s" % rid)
    print("  path: %s" % paths.get(rid))
    print("  %-3s %-9s %12s %12s %12s %10s %10s" %
          ("surf", "interact", "y (mm)", "z (mm)", "thick", "Is", "Ip"))
    prod_s, prod_p = 1.0, 1.0
    reflect = []
    for s in r.get("surfaces", []):
        marker = ""
        if s["interaction"] == "REFLECT":
            marker = "  <-- ghost reflection"
            reflect.append(s)
        print("  %-3d %-9s %12.4f %12.4f %12.4f %10.4f %10.4f%s" %
              (s["surface_id"], s["interaction"], s["position"][1], s["position"][2],
               s["thickness"], s["intensity_s"], s["intensity_p"], marker))
        prod_s *= s["intensity_s"]
        prod_p *= s["intensity_p"]
    if rid.startswith("ghost"):
        refl_prod_s = 1.0
        refl_prod_p = 1.0
        for s in reflect:
            refl_prod_s *= s["intensity_s"]
            refl_prod_p *= s["intensity_p"]
        print("  ghost relative intensity (product of Fresnel reflectances):")
        print("    Is = %.3e   Ip = %.3e" % (refl_prod_s, refl_prod_p))
    print("  cumulative intensity (all surfaces): Is = %.4e  Ip = %.4e" % (prod_s, prod_p))
    print()
print()
print("=== How to interpret this result ===")
print("- Two rays through the re-sized double-Gauss:")
print("  - ghost_reflect_s4_s2: forward to surface 4, reflect there, reversed")
print("    refraction through surface 3, reflect at surface 2, then forward to")
print("    the image (surface 14).")
print("  - normal_reference: the same field's normal refraction-only ray.")
print("- The two REFLECT rows are the ghost reflections; every other surface is")
print("  a Fresnel transmission (Is/Ip ~ 0.94).")
print("- Ghost relative intensity (Is/Ip) is the product of the two Fresnel")
print("  reflectances (~0.004): the ghost is a few hundredths of one percent of")
print("  the normal image brightness.")
print("- Cumulative intensity: transmitted light left after all surfaces")
print("  (~0.48 for the normal ray vs ~0.0015 for the ghost).")
PYEOF
echo "Written: $RESULT_FILE"
echo

# ── Console summary ──
echo "--- Console summary ---"
python3 - "$TRACE_RESULT" <<'PYEOF'
import sys
import yaml

d = yaml.safe_load(open(sys.argv[1]))
for r in d.get("results", []):
    if not r["id"].startswith("ghost"):
        continue
    err = r.get("error")
    status = "traced OK" if not err else "error: %s" % err
    print("  ghost ray: %s" % status)
    for s in r.get("surfaces", []):
        if s["interaction"] == "REFLECT":
            print("    reflect at surface %-2d  Is = %.4f  Ip = %.4f" %
                  (s["surface_id"], s["intensity_s"], s["intensity_p"]))
    last = r.get("surfaces", [])[-1]
    print("    final hit: surface %d at (y, z) = (%.2f, %.2f) mm" %
          (last["surface_id"], last["position"][1], last["position"][2]))
PYEOF
echo

# ── Diagram ──
echo "=== Diagram ==="
$RAYWEAVE plot -o "$SVG" < "$TRACE_RESULT" >/dev/null 2>&1
echo "Written: $SVG"
echo
echo "=== Done. See $RESULT_FILE for the full trace table ==="
