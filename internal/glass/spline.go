package glass

import "fmt"

func BuildSpline(knots []IndexEntry) ([]float64, error) {
	n := len(knots)
	if n < 3 {
		return nil, fmt.Errorf("spline requires at least 3 knots, got %d", n)
	}

	h := make([]float64, n-1)
	for i := 0; i < n-1; i++ {
		h[i] = knots[i+1].Wavelength - knots[i].Wavelength
		if h[i] <= 0 {
			return nil, fmt.Errorf("knots must be in increasing order")
		}
	}

	alpha := make([]float64, n)
	for i := 1; i < n-1; i++ {
		alpha[i] = 6.0 * ((knots[i+1].Index-knots[i].Index)/h[i] -
			(knots[i].Index-knots[i-1].Index)/h[i-1])
	}

	mu := make([]float64, n)
	z := make([]float64, n)

	for i := 1; i < n-1; i++ {
		denom := 2.0*(h[i-1]+h[i]) - h[i-1]*mu[i-1]
		mu[i] = h[i] / denom
		z[i] = (alpha[i] - h[i-1]*z[i-1]) / denom
	}

	s := make([]float64, n)

	for i := n - 2; i >= 1; i-- {
		s[i] = z[i] - mu[i]*s[i+1]
	}

	return s, nil
}

func EvalSpline(knots []IndexEntry, s []float64, lambda float64) float64 {
	n := len(knots)

	i := 0
	for i < n-2 && knots[i+1].Wavelength < lambda {
		i++
	}

	h := knots[i+1].Wavelength - knots[i].Wavelength
	t := (lambda - knots[i].Wavelength) / h
	A := 1.0 - t
	B := t

	interp := A*knots[i].Index + B*knots[i+1].Index +
		((A*A*A-A)*h*h*s[i]+(B*B*B-B)*h*h*s[i+1]) / 6.0

	return interp
}

func SplineInterpolate(knots []IndexEntry, lambda float64) (float64, error) {
	if len(knots) < 2 {
		return 0, fmt.Errorf("not enough knots")
	}
	if lambda < knots[0].Wavelength || lambda > knots[len(knots)-1].Wavelength {
		return 0, fmt.Errorf("lambda %f out of range [%f, %f]",
			lambda, knots[0].Wavelength, knots[len(knots)-1].Wavelength)
	}

	s, err := BuildSpline(knots)
	if err != nil {
		return 0, err
	}

	return EvalSpline(knots, s, lambda), nil
}
