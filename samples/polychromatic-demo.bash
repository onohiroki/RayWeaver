#!/usr/bin/env bash
# Polychromatic evaluation: the four-line design table (d/F/C/g) is used with
# its weights by paraxial (colour sweep), chief (white spot + lateral colour),
# psf (design-weight white PSF at the white best focus), focus (file vs white
# plane) and a short weighted optimisation.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RAYWEAVE="${RAYWEAVE:-}"
if [ -z "$RAYWEAVE" ]; then
  if [ -x "$SCRIPT_DIR/rayweave" ]; then
    RAYWEAVE="$SCRIPT_DIR/rayweave"
  elif [ -x "$SCRIPT_DIR/../rayweave" ]; then
    RAYWEAVE="$SCRIPT_DIR/../rayweave"
  else
    RAYWEAVE="rayweave"
  fi
fi

INPUT="$SCRIPT_DIR/polychromatic-4wl.yaml"
PARAXIAL="$SCRIPT_DIR/polychromatic-paraxial.yaml"
CHIEF="$SCRIPT_DIR/polychromatic-chief.yaml"
PSF="$SCRIPT_DIR/polychromatic-psf.yaml"
FOCUS="$SCRIPT_DIR/polychromatic-focus.yaml"
OPTIMIZED="$SCRIPT_DIR/polychromatic-optimized.yaml"

clean() {
  rm -f "$PARAXIAL" "$CHIEF" "$PSF" "$FOCUS" "$OPTIMIZED"
}

if [ "${1:-}" = "--clean" ]; then
  clean
  exit 0
fi

if [ "${1:-}" = "--lens" ] && [ $# -ge 2 ]; then
  INPUT="$2"
  shift 2
fi

echo "== paraxial: design set (d/F/C/g), axial colour and secondary spectrum =="
"$RAYWEAVE" paraxial --wavelength-set design < "$INPUT" > "$PARAXIAL"
"$RAYWEAVE" list paraxial < "$PARAXIAL" || true
"$RAYWEAVE" query -r 'paraxial_result.color.axial_color[2].shift_mm' < "$PARAXIAL" || true
"$RAYWEAVE" query -r 'paraxial_result.color.white_back_focal' < "$PARAXIAL" || true

echo
echo "== chief: per-wavelength chief height, white spot and lateral colour =="
"$RAYWEAVE" chief < "$INPUT" > "$CHIEF"
"$RAYWEAVE" query -r 'chief_rays[0].white_spot_stats.rms_r' < "$CHIEF" || true
"$RAYWEAVE" query -r 'chief_rays[1].lateral_color.max_mm' < "$CHIEF" || true

echo
echo "== psf: design-weighted white PSF at the white best focus =="
"$RAYWEAVE" psf --wavelength-set design --focus-plane white \
  --psf-grid 16 --num-rays 64 --fields 0 --converge-check false \
  < "$INPUT" > "$PSF"
"$RAYWEAVE" query -r 'psf_results[0].strehl_ratio' < "$PSF" || true
"$RAYWEAVE" query -r 'psf_results[0].spectral_curve' < "$PSF" || true

echo
echo "== focus: file plane vs the wavelength-weighted white plane =="
"$RAYWEAVE" focus mtf --planes file,white --wavelength-set design \
  --num-rays 64 --psf-grid 16 --fields 0 --frequencies 30 \
  < "$INPUT" > "$FOCUS"
"$RAYWEAVE" list focus < "$FOCUS" || true

echo
echo "== optimize: weighted white merit and the per-wavelength summary =="
"$RAYWEAVE" optimize < "$INPUT" > "$OPTIMIZED"
"$RAYWEAVE" query -r 'opt_results.back_focus.method' < "$OPTIMIZED" || true
"$RAYWEAVE" query --len 'opt_results.wavelength_summary' < "$OPTIMIZED" || true

echo
echo "Outputs written to:"
echo "  $PARAXIAL"
echo "  $CHIEF"
echo "  $PSF"
echo "  $FOCUS"
echo "  $OPTIMIZED"
