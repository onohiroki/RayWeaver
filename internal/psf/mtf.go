package psf

import (
	"math"

	"github.com/hiroki/rayweaver/internal/fft"
	"github.com/hiroki/rayweaver/internal/types"
)

// ComputeMTF derives the OTF/MTF of a raw (unnormalized) image-plane PSF
// grid via a direct FFT of the sampled point-spread function:
//
//   - zero-pad the intensity grid to the next power of two,
//   - forward FFT (radix-2),
//   - fftshift so DC sits at the array centre,
//   - divide by the DC magnitude,
//   - apply a linear phase correction exp(-2πi·f·c) referencing the phase
//     (and therefore the PTF) to the grid centroid.
//
// The sagittal axis is X (frequency along the grid x direction), the
// tangential axis is Y (the image-height direction). cfg may be nil to use
// the defaults (thresholds 0.50/0.30/0.10, frequency samples = M/2, full
// range up to the Nyquist frequency).
func ComputeMTF(intensity []float64, spec ImageGridSpec, cfg *types.PSFMTFConfig) *types.PSFMTFSummary {
	n := spec.NX
	if n <= 1 || len(intensity) < n*n || spec.DX <= 0 {
		return nil
	}
	m := fft.NextPow2(n)
	if m < 2 {
		m = 2
	}
	dx := spec.DX
	nyquist := 1.0 / (2 * dx)

	cx, cy := gridCentroid(intensity, spec)

	// Zero-pad + forward FFT of the PSF (DC lands at index (0,0)).
	grid := make([]complex128, m*m)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			grid[j*m+i] = complex(intensity[j*n+i], 0)
		}
	}
	fft.FFT2D(grid, m, -1)
	// fftshift the spectrum so DC (index (0,0)) lands at the array centre:
	// after the shift, index (k, l) carries the frequency (Δf·(k-M/2),
	// Δf·(l-M/2)) with Δf = 1/(M·DX).
	fft.FFTShift2D(grid, m)
	df := 1.0 / (float64(m) * dx)
	shift := m / 2

	// Positive-frequency OTF along each axis (frequency origin at the centre
	// index k = shift). Sagittal = centre row (fy = 0, fx varies); tangential
	// = centre column (fx = 0, fy varies). The phase is referenced to the
	// grid centroid: the fftshifted FFT value F[k] equals the physical OTF up
	// to a linear phase from the pixel placement, corrected below.
	samples := m - shift
	sagOTF := make([]complex128, samples)
	tanOTF := make([]complex128, samples)
	for s := 0; s < samples; s++ {
		k := shift + s
		f := df * float64(k-shift)
		sag := grid[shift*m+k]
		tan := grid[k*m+shift]
		// OTF_c(f) = F[k]·exp(-2πi·f·(origin - c)), where the FFT "origin"
		// is the first pixel's centre (X0 + 0.5·DX) and c is the centroid.
		sagOTF[s] = sag * exp2pi(f*(spec.X0+0.5*spec.DX-cx))
		tanOTF[s] = tan * exp2pi(f*(spec.Y0+0.5*spec.DY-cy))
	}

	dcSag := cmag(sagOTF[0])
	dcTan := cmag(tanOTF[0])
	return &types.PSFMTFSummary{
		Sagittal:   axisFromOTF(sagOTF, df, nyquist, dcSag, cfg),
		Tangential: axisFromOTF(tanOTF, df, nyquist, dcTan, cfg),
	}
}

// gridCentroid returns the intensity-weighted centroid (global mm) of a
// row-major square grid.
func gridCentroid(intensity []float64, spec ImageGridSpec) (cx, cy float64) {
	var sw, wx, wy float64
	for j := 0; j < spec.NY; j++ {
		for i := 0; i < spec.NX; i++ {
			w := intensity[j*spec.NX+i]
			if w <= 0 {
				continue
			}
			sw += w
			wx += w * (spec.X0 + (float64(i)+0.5)*spec.DX)
			wy += w * (spec.Y0 + (float64(j)+0.5)*spec.DY)
		}
	}
	if sw > 0 {
		return wx / sw, wy / sw
	}
	return spec.X0 + 0.5*spec.DX*float64(spec.NX), spec.Y0 + 0.5*spec.DY*float64(spec.NY)
}

// axisFromOTF turns one axis' positive-frequency OTF samples (index s ↔
// frequency df·s) into the reported curve, threshold crossings and evaluated
// points.
func axisFromOTF(otf []complex128, df, nyquist, dc float64, cfg *types.PSFMTFConfig) types.PSFMTFAxis {
	var ax types.PSFMTFAxis
	if dc <= 0 {
		return ax
	}
	n := len(otf)
	mtf := make([]float64, n)
	for k := 0; k < n; k++ {
		mtf[k] = cmag(otf[k]) / dc
	}

	thresh := []float64{0.50, 0.30, 0.10}
	maxFreq := nyquist
	fp := n
	if cfg != nil {
		if len(cfg.Thresholds) > 0 {
			thresh = cfg.Thresholds
		}
		if cfg.MaxFrequency > 0 {
			maxFreq = cfg.MaxFrequency
		}
		if cfg.FrequencyPoints > 0 && cfg.FrequencyPoints < fp {
			fp = cfg.FrequencyPoints
		}
	}
	if maxFreq > nyquist {
		maxFreq = nyquist
	}

	// Curve: uniform samples across [0, maxFreq].
	steps := maxInt(fp-1, 1)
	for k := 0; k < fp; k++ {
		f := maxFreq * float64(k) / float64(steps)
		v := interpMTF(mtf, df, f)
		if v > 1 {
			v = 1
		}
		ax.Curve = append(ax.Curve, types.PSFMTFPoint{
			Frequency: f,
			OTFReal:   otfRealAt(otf, df, f, dc),
			OTFImag:   otfImagAt(otf, df, f, dc),
			MTF:       v,
			PTF:       ptfAt(otf, df, f),
		})
	}

	// Threshold crossings within [0, maxFreq].
	for _, th := range thresh {
		if f, ok := crossingMTF(mtf, df, th, maxFreq); ok {
			ax.Thresholds = append(ax.Thresholds, types.PSFMTFCross{MTF: th, Frequency: f})
		}
	}

	// User-selected evaluation points.
	if cfg != nil {
		for _, f := range cfg.Frequencies {
			if f < 0 {
				continue
			}
			v := interpMTF(mtf, df, f)
			if v > 1 {
				v = 1
			}
			ax.Evaluated = append(ax.Evaluated, types.PSFMTFPoint{
				Frequency: f,
				OTFReal:   otfRealAt(otf, df, f, dc),
				OTFImag:   otfImagAt(otf, df, f, dc),
				MTF:       v,
				PTF:       ptfAt(otf, df, f),
			})
		}
	}
	return ax
}

// interpMTF returns the MTF at frequency f by linear interpolation of the
// positive-frequency samples f_k = df·k (k = 0..n-1). Frequencies beyond the
// last sample are clamped to the last sample's value.
func interpMTF(mtf []float64, df, f float64) float64 {
	if len(mtf) == 0 {
		return 0
	}
	if f <= 0 {
		return mtf[0]
	}
	idx := f / df
	i := int(idx)
	if i >= len(mtf)-1 {
		return mtf[len(mtf)-1]
	}
	t := idx - float64(i)
	return mtf[i] + t*(mtf[i+1]-mtf[i])
}

// crossingMTF finds the smallest frequency in [0, maxFreq] at which the MTF
// crosses the given level (linear interpolation between adjacent samples).
func crossingMTF(mtf []float64, df, level, maxFreq float64) (float64, bool) {
	if len(mtf) == 0 || level <= 0 {
		return 0, false
	}
	j := 0
	for j+1 < len(mtf) && df*float64(j+1) <= maxFreq && mtf[j+1] >= level {
		j++
	}
	if j+1 >= len(mtf) {
		return 0, false
	}
	if j == 0 && mtf[0] < level {
		return 0, false
	}
	f0 := df * float64(j)
	f1 := df * float64(j+1)
	if f1 > maxFreq {
		f1 = maxFreq
	}
	m0, m1 := mtf[j], mtf[j+1]
	if m0 == m1 {
		return 0, false
	}
	fc := f0 + (f1-f0)*(level-m0)/(m1-m0)
	if fc < 0 || fc > maxFreq {
		return 0, false
	}
	return fc, true
}

func otfRealAt(otf []complex128, df, f, dc float64) float64 {
	return real(otfComplexAt(otf, df, f)) / dc
}

func otfImagAt(otf []complex128, df, f, dc float64) float64 {
	return imag(otfComplexAt(otf, df, f)) / dc
}

func ptfAt(otf []complex128, df, f float64) float64 {
	c := otfComplexAt(otf, df, f)
	return math.Atan2(imag(c), real(c))
}

func otfComplexAt(otf []complex128, df, f float64) complex128 {
	if len(otf) == 0 {
		return 0
	}
	if f <= 0 {
		return otf[0]
	}
	idx := f / df
	i := int(idx)
	if i >= len(otf)-1 {
		return otf[len(otf)-1]
	}
	t := idx - float64(i)
	return otf[i] + complex(real(otf[i+1]-otf[i])*t, imag(otf[i+1]-otf[i])*t)
}

// exp2pi returns exp(-2πi·x).
func exp2pi(x float64) complex128 {
	return complex(math.Cos(-2*math.Pi*x), math.Sin(-2*math.Pi*x))
}

func cmag(c complex128) float64 {
	return math.Hypot(real(c), imag(c))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
