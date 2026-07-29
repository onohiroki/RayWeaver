package dls

import (
	"math"
)

const (
	defaultMaxIter = 100
	defaultMu      = 1.0
	defaultTol     = 1e-6
	defaultEpsilon = 1e-6
)

func defaultOptions() Options {
	return Options{
		MaxIter:        defaultMaxIter,
		Mu:             defaultMu,
		Tol:            defaultTol,
		Epsilon:        defaultEpsilon,
		NumRays:        64,
		ApertureMargin: 2.0,
	}
}

func Solve(m Model) Result {
	opts := m.Options()
	if opts.MaxIter <= 0 {
		opts.MaxIter = defaultMaxIter
	}
	if opts.Tol <= 0 {
		opts.Tol = defaultTol
	}
	if opts.Epsilon <= 0 {
		opts.Epsilon = defaultEpsilon
	}
	if opts.NumRays <= 0 {
		opts.NumRays = 64
	}
	if opts.ApertureMargin <= 0 {
		opts.ApertureMargin = 2.0
	}

	x := m.InitialState()
	merit := m.EvaluateMerit(x)
	beforeMerit := merit

	mu := opts.Mu
	if mu <= 0 {
		mu = defaultMu
	}
	nVars := len(x)

	bestX := make([]float64, nVars)
	copy(bestX, x)
	bestMerit := merit

	var lastDelta []float64
	status := "max_iterations"
	totalIter := 0

	for totalIter = 0; totalIter < opts.MaxIter; totalIter++ {
		J, r := computeJacobian(m, x, opts.Epsilon)

		g := make([]float64, nVars)
		for j := 0; j < nVars; j++ {
			sum := 0.0
			for i := 0; i < len(r); i++ {
				sum += J[i][j] * r[i]
			}
			g[j] = sum
		}

		H := make([][]float64, nVars)
		for j := 0; j < nVars; j++ {
			H[j] = make([]float64, nVars)
			for k := 0; k < nVars; k++ {
				sum := 0.0
				for i := 0; i < len(r); i++ {
					sum += J[i][j] * J[i][k]
				}
				H[j][k] = sum
			}
		}
		for j := 0; j < nVars; j++ {
			H[j][j] += mu
		}

		negG := make([]float64, nVars)
		for j := 0; j < nVars; j++ {
			negG[j] = -g[j]
		}

		delta := solveLinearSystem(H, negG)
		if delta == nil {
			mu *= 2.0
			continue
		}

		xNew := make([]float64, nVars)
		for j := 0; j < nVars; j++ {
			xNew[j] = x[j] + delta[j]
		}
		projectOntoBox(xNew, m.Variables())

		meritNew := m.EvaluateMerit(xNew)
		actualReduction := merit - meritNew

		predictedReduction := 0.0
		for i := 0; i < len(r); i++ {
			sum := 0.0
			for j := 0; j < nVars; j++ {
				sum += J[i][j] * delta[j]
			}
			predictedReduction += r[i] * sum
		}
		halfDeltaHDelta := 0.0
		for j := 0; j < nVars; j++ {
			sum := 0.0
			for k := 0; k < nVars; k++ {
				sum += H[j][k] * delta[k]
			}
			halfDeltaHDelta += delta[j] * sum
		}
		predictedReduction -= 0.5 * halfDeltaHDelta

		rho := 1.0
		if predictedReduction > 1e-20 {
			rho = actualReduction / predictedReduction
		} else if predictedReduction < -1e-20 {
			rho = -1.0
		}

		if rho > 0.25 {
			mu *= math.Max(1.0/3.0, 1.0-(2.0*rho-1.0)*(2.0*rho-1.0)*(2.0*rho-1.0))
		} else {
			mu *= 2.0
		}

		if actualReduction > 0 {
			copy(x, xNew)
			merit = meritNew
			if merit < bestMerit {
				bestMerit = merit
				copy(bestX, x)
			}
		}

		norm := 0.0
		for _, d := range delta {
			norm += d * d
		}
		stepNorm := math.Sqrt(norm)
		lastDelta = delta

		if opts.Logger != nil {
			currVars := make([]float64, len(m.Variables()))
			for i := range m.Variables() {
				currVars[i] = x[i]
			}
			opts.Logger.LogIter(totalIter+1, merit, actualReduction, stepNorm, currVars)
		}

		if stepNorm < opts.Tol {
			status = "converged"
			break
		}
	}

	if opts.Logger != nil {
		finalVars := make([]float64, len(m.Variables()))
		copy(finalVars, bestX)
		finalStepNorm := 0.0
		if lastDelta != nil {
			for _, d := range lastDelta {
				finalStepNorm += d * d
			}
			finalStepNorm = math.Sqrt(finalStepNorm)
		}
		opts.Logger.LogFinal(totalIter+1, status, bestMerit, finalStepNorm, finalVars)
	}

	varInfo := m.Variables()
	vars := make([]VariableState, len(varInfo))
	for i, vi := range varInfo {
		vars[i] = VariableState{
			Name:  vi.Name,
			Param: vi.Param,
			After: bestX[i],
		}
	}

	iterations := totalIter
	if status == "converged" {
		iterations = totalIter + 1
	}

	return Result{
		BeforeMerit: beforeMerit,
		AfterMerit:  bestMerit,
		Iterations:  iterations,
		Status:      status,
		Variables:   vars,
	}
}

func computeJacobian(m Model, x []float64, epsilon float64) ([][]float64, []float64) {
	nVars := len(x)

	for j := 0; j < nVars; j++ {
		x[j] = sanitize(x[j])
	}

	r0 := m.ComputeResiduals(x)
	nResiduals := len(r0)

	J := make([][]float64, nResiduals)
	for i := 0; i < nResiduals; i++ {
		J[i] = make([]float64, nVars)
	}

	for j := 0; j < nVars; j++ {
		xPert := make([]float64, nVars)
		copy(xPert, x)
		xPert[j] += epsilon

		rPert := m.ComputeResiduals(xPert)

		for i := 0; i < nResiduals; i++ {
			diff := rPert[i] - r0[i]
			J[i][j] = sanitize(diff / epsilon)
		}
	}

	return J, r0
}
