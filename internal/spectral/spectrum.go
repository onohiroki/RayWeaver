// Package spectral provides light-source spectral power distributions (SPD)
// used to combine monochromatic PSFs into a polychromatic ("white") PSF. The
// built-in curves are CIE standard illuminant D65 and a flat (equal-power)
// curve; arbitrary curves can be supplied as (wavelength nm, relative power)
// points.
package spectral

import "math"

// Curve is a relative spectral power distribution sampled at discrete
// wavelengths (stored internally in mm) with linear interpolation between
// samples and zero power outside the covered range.
type Curve struct {
	mm  []float64 // wavelengths in mm
	rel []float64
}

// NewCurve builds a Curve from parallel wavelength (nm) / relative-power
// arrays. The samples are sorted by wavelength and duplicate wavelengths keep
// the last value.
func NewCurve(nm, rel []float64) *Curve {
	n := len(nm)
	if n > len(rel) {
		n = len(rel)
	}
	c := &Curve{mm: make([]float64, n), rel: make([]float64, n)}
	for i := 0; i < n; i++ {
		c.mm[i] = nm[i] * 1e-6
		c.rel[i] = rel[i]
	}
	// Insertion sort by wavelength (curves are short); collapse duplicates.
	for i := 1; i < len(c.mm); i++ {
		for j := i; j > 0 && c.mm[j-1] > c.mm[j]; j-- {
			c.mm[j-1], c.mm[j] = c.mm[j], c.mm[j-1]
			c.rel[j-1], c.rel[j] = c.rel[j], c.rel[j-1]
		}
	}
	// In-place compaction: keep the last value of each duplicate wavelength.
	write := 0
	for i := 0; i < len(c.mm); i++ {
		if write > 0 && c.mm[i] == c.mm[write-1] {
			c.rel[write-1] = c.rel[i]
			continue
		}
		c.mm[write] = c.mm[i]
		c.rel[write] = c.rel[i]
		write++
	}
	c.mm = c.mm[:write]
	c.rel = c.rel[:write]
	return c
}

// MinNM / MaxNM return the covered wavelength range in nm.
func (c *Curve) MinNM() float64 {
	if len(c.mm) == 0 {
		return 0
	}
	return c.mm[0] * 1e6
}

func (c *Curve) MaxNM() float64 {
	if len(c.mm) == 0 {
		return 0
	}
	return c.mm[len(c.mm)-1] * 1e6
}

// Weight returns the relative spectral power at wavelength (mm), linearly
// interpolated between samples and zero outside the covered range. Negative
// interpolated values are clamped to zero.
func (c *Curve) Weight(wavelengthMM float64) float64 {
	if len(c.mm) == 0 || wavelengthMM < c.mm[0] || wavelengthMM > c.mm[len(c.mm)-1] {
		return 0
	}
	lo := 0
	hi := len(c.mm) - 1
	for lo < hi {
		mid := (lo + hi) / 2
		if c.mm[mid] < wavelengthMM {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	i := lo
	// The curve's runtime nm→mm conversion and a caller's mm literal can
	// differ by one ULP; treat near-equal samples on either side as exact hits.
	if i > 0 && math.Abs(c.mm[i-1]-wavelengthMM) <= 1e-12 {
		if v := c.rel[i-1]; v > 0 {
			return v
		}
		return 0
	}
	if math.Abs(c.mm[i]-wavelengthMM) <= 1e-12 {
		if v := c.rel[i]; v > 0 {
			return v
		}
		return 0
	}
	if i == 0 {
		return 0
	}
	w0, w1 := c.mm[i-1], c.mm[i]
	if w1 <= w0 {
		return 0
	}
	t := (wavelengthMM - w0) / (w1 - w0)
	v := c.rel[i-1] + t*(c.rel[i]-c.rel[i-1])
	if v < 0 {
		return 0
	}
	return v
}

// Flat returns a Curve with unit relative power over [minNM, maxNM].
func Flat(minNM, maxNM float64) *Curve {
	if maxNM <= minNM {
		return NewCurve([]float64{minNM}, []float64{1})
	}
	return NewCurve([]float64{minNM, maxNM}, []float64{1, 1})
}

// D65 returns CIE standard illuminant D65 (correlated colour temperature
// 6504 K) sampled at 5 nm from 300 nm to 830 nm.
func D65() *Curve {
	mm := make([]float64, len(d65NM))
	for i := range d65NM {
		mm[i] = d65NM[i] * 1e-6
	}
	return &Curve{mm: mm, rel: d65Rel}
}

// d65NM / d65Rel are the CIE D65 relative spectral power distribution at 5 nm
// steps (wavelengths in nm).
var d65NM = []float64{
	300, 305, 310, 315, 320, 325, 330, 335, 340, 345,
	350, 355, 360, 365, 370, 375, 380, 385, 390, 395,
	400, 405, 410, 415, 420, 425, 430, 435, 440, 445,
	450, 455, 460, 465, 470, 475, 480, 485, 490, 495,
	500, 505, 510, 515, 520, 525, 530, 535, 540, 545,
	550, 555, 560, 565, 570, 575, 580, 585, 590, 595,
	600, 605, 610, 615, 620, 625, 630, 635, 640, 645,
	650, 655, 660, 665, 670, 675, 680, 685, 690, 695,
	700, 705, 710, 715, 720, 725, 730, 735, 740, 745,
	750, 755, 760, 765, 770, 775, 780, 785, 790, 795,
	800, 805, 810, 815, 820, 825, 830,
}

var d65Rel = []float64{
	0.0341, 1.6643, 3.2945, 11.7652, 20.2360, 28.6447, 37.0535, 38.5011, 39.9488, 42.4302,
	44.9117, 45.7750, 46.6383, 49.2837, 51.9292, 51.8592, 51.7892, 54.4591, 57.1290, 58.8597,
	60.5904, 61.5941, 62.5979, 63.5707, 64.5435, 66.1209, 67.6982, 69.0288, 70.3594, 70.9900,
	71.6207, 72.6074, 73.5941, 74.5626, 75.5312, 76.4180, 77.3049, 78.0403, 78.7757, 79.5091,
	80.2425, 81.0976, 81.9527, 82.9127, 83.8727, 84.4738, 85.0749, 85.4692, 85.8635, 86.0831,
	86.3027, 86.4890, 86.6753, 86.8243, 86.9734, 87.1216, 87.2698, 87.1848, 87.0998, 86.9040,
	86.7082, 86.5912, 86.4742, 86.3471, 86.2200, 86.1990, 86.1780, 86.1599, 86.1417, 85.9423,
	85.7429, 85.6310, 85.5191, 85.4160, 85.3128, 85.1136, 84.9144, 84.8447, 84.7750, 84.5943,
	84.4136, 84.6436, 84.8737, 85.0740, 85.2743, 85.4135, 85.5527, 85.5403, 85.5279, 85.8874,
	86.2469, 86.2846, 86.3224, 86.4005, 86.4786, 86.5350, 86.5914, 86.6400, 86.6886, 86.7290,
	86.7695, 86.7912, 86.8129, 86.7683, 86.7237, 86.5553, 86.3870,
}