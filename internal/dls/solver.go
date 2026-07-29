package dls

import (
	"math"
)

const (
	defaultMaxIter = 100
	defaultMu      = 1e-2
	defaultTol     = 1e-6
	defaultEpsilon = 1e-6
)

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

	variables := m.Variables()
	nVars := len(variables)

	scales := make([]float64, nVars)
	for i, v := range variables {
		scales[i] = v.Max - v.Min
		if scales[i] <= 0 {
			scales[i] = 1.0
		}
	}

	xPhys0 := m.InitialState()
	xNorm := make([]float64, nVars)
	for i := range xPhys0 {
		xNorm[i] = (xPhys0[i] - variables[i].Min) / scales[i]
	}

	c0 := m.ComputeConstraints(xPhys0)
	nCon := len(c0)
	hasConstraints := nCon > 0

	lambdas := make([]float64, nCon)
	cPrev := make([]float64, nCon)
	copy(cPrev, c0)
	muCon := 0.01

	merit := m.EvaluateMerit(xPhys0)
	if hasConstraints {
		for j, cj := range c0 {
			merit += lambdas[j]*cj + 0.5*muCon*cj*cj
		}
	}
	beforeMerit := merit

	mu := opts.Mu
	if mu <= 0 {
		mu = defaultMu
	}

	bestXNorm := make([]float64, nVars)
	copy(bestXNorm, xNorm)
	bestMerit := merit

	var lastDelta []float64
	status := "max_iterations"
	totalIter := 0

	for totalIter = 0; totalIter < opts.MaxIter; totalIter++ {
		J_opt, r_opt, J_con, c_con := computeJacobians(m, xNorm, variables, scales, opts.Epsilon)

		J, r := J_opt, r_opt
		if hasConstraints {
			J, r = buildAugmented(J_opt, r_opt, J_con, c_con, lambdas, muCon)
		}

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

		gNorm := 0.0
		for j := 0; j < nVars; j++ {
			gNorm += g[j] * g[j]
		}
		if math.Sqrt(gNorm) < opts.Tol {
			status = "converged_gradient"
			break
		}

		frozen := make([]bool, nVars)
		for j := 0; j < nVars; j++ {
			if (xNorm[j] < 1e-8 && g[j] >= 0) || (xNorm[j] > 1-1e-8 && g[j] <= 0) {
				frozen[j] = true
			}
		}
		for j := 0; j < nVars; j++ {
			if frozen[j] {
				for k := 0; k < nVars; k++ {
					H[j][k] = 0
					H[k][j] = 0
				}
				H[j][j] = 1
				negG[j] = 0
			}
		}

		delta := solveLinearSystem(H, negG)
		if delta == nil {
			mu *= 2.0
			continue
		}

		stepBeforeClamp := 0.0
		for _, d := range delta {
			stepBeforeClamp += d * d
		}
		stepBeforeClamp = math.Sqrt(stepBeforeClamp)
		if stepBeforeClamp > 0.5 {
			backoff := 0.5 / stepBeforeClamp
			for j := 0; j < nVars; j++ {
				delta[j] *= backoff
			}
		}

		xNormNew := make([]float64, nVars)
		for j := 0; j < nVars; j++ {
			xNormNew[j] = xNorm[j] + delta[j]
		}

		for j := 0; j < nVars; j++ {
			xNormNew[j] = sanitize(xNormNew[j])
			if xNormNew[j] < 0 {
				xNormNew[j] = 0
			} else if xNormNew[j] > 1 {
				xNormNew[j] = 1
			}
		}

		xPhysNew := denormalize(xNormNew, variables, scales)

		meritNew := m.EvaluateMerit(xPhysNew)
		var cNew []float64
		if hasConstraints {
			cNew = m.ComputeConstraints(xPhysNew)
			for j, cj := range cNew {
				meritNew += lambdas[j]*cj + 0.5*muCon*cj*cj
			}
		}

		actualReduction := merit - meritNew

		if actualReduction <= 0 {
			dirDeriv := 0.0
			for j := 0; j < nVars; j++ {
				dirDeriv += g[j] * delta[j]
			}

			alpha := 1.0
			for k := 0; k < 8; k++ {
				alpha *= 0.5
				xTestNorm := make([]float64, nVars)
				for j := 0; j < nVars; j++ {
					xTestNorm[j] = xNorm[j] + alpha*delta[j]
				}
				for j := 0; j < nVars; j++ {
					xTestNorm[j] = sanitize(xTestNorm[j])
					if xTestNorm[j] < 0 {
						xTestNorm[j] = 0
					} else if xTestNorm[j] > 1 {
						xTestNorm[j] = 1
					}
				}

				xTestPhys := denormalize(xTestNorm, variables, scales)
				meritTest := m.EvaluateMerit(xTestPhys)
				var cTest []float64
				if hasConstraints {
					cTest = m.ComputeConstraints(xTestPhys)
					for j, cj := range cTest {
						meritTest += lambdas[j]*cj + 0.5*muCon*cj*cj
					}
				}

				if meritTest <= merit+1e-4*alpha*dirDeriv {
					xNormNew = xTestNorm
					xPhysNew = xTestPhys
					meritNew = meritTest
					cNew = cTest
					actualReduction = merit - meritNew
					break
				}

				if alpha < 1e-3 {
					break
				}
			}
		}

		halfDeltaHDelta := 0.0
		for j := 0; j < nVars; j++ {
			sum := 0.0
			for k := 0; k < nVars; k++ {
				sum += H[j][k] * delta[k]
			}
			halfDeltaHDelta += delta[j] * sum
		}
		predictedReduction := 0.5 * halfDeltaHDelta

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
			copy(xNorm, xNormNew)
			merit = meritNew
			if hasConstraints && cNew != nil {
				for j, cj := range cNew {
					lambdas[j] += muCon * cj
				}
				cNewNorm := 0.0
				for _, cj := range cNew {
					cNewNorm += cj * cj
				}
				cNewNorm = math.Sqrt(cNewNorm)
				cPrevNorm := 0.0
				for _, cj := range cPrev {
					cPrevNorm += cj * cj
				}
				cPrevNorm = math.Sqrt(cPrevNorm)
				if cPrevNorm > 1e-12 && cNewNorm > 0.25*cPrevNorm {
					muCon *= 10.0
					if muCon > 1e8 {
						muCon = 1e8
					}
				}
				copy(cPrev, cNew)
			}
			if merit < bestMerit {
				bestMerit = merit
				copy(bestXNorm, xNorm)
			}
		}

		norm := 0.0
		for _, d := range delta {
			norm += d * d
		}
		stepNorm := math.Sqrt(norm)
		lastDelta = delta

		if opts.Logger != nil {
			currVars := make([]float64, nVars)
			for i := range nVars {
				currVars[i] = variables[i].Min + xNorm[i]*scales[i]
			}
			opts.Logger.LogIter(totalIter+1, merit, actualReduction, stepNorm, currVars)
		}

		if stepNorm < opts.Tol {
			status = "converged"
			break
		}
	}

	iterations := totalIter
	if status == "converged" || status == "converged_gradient" {
		iterations = totalIter + 1
	}

	if opts.Logger != nil {
		finalVars := make([]float64, nVars)
		for i := range nVars {
			finalVars[i] = variables[i].Min + bestXNorm[i]*scales[i]
		}
		finalStepNorm := 0.0
		if lastDelta != nil {
			for _, d := range lastDelta {
				finalStepNorm += d * d
			}
			finalStepNorm = math.Sqrt(finalStepNorm)
		}
		opts.Logger.LogFinal(iterations, status, bestMerit, finalStepNorm, finalVars)
	}

	vars := make([]VariableState, len(variables))
	for i, vi := range variables {
		vars[i] = VariableState{
			Name:  vi.Name,
			Param: vi.Param,
			After: vi.Min + bestXNorm[i]*scales[i],
		}
	}

	return Result{
		BeforeMerit: beforeMerit,
		AfterMerit:  bestMerit,
		Iterations:  iterations,
		Status:      status,
		Variables:   vars,
	}
}

func computeJacobians(m Model, xNorm []float64, variables []VariableInfo, scales []float64, epsilon float64) ([][]float64, []float64, [][]float64, []float64) {
	nVars := len(xNorm)
	xPhys := denormalize(xNorm, variables, scales)

	for j := 0; j < nVars; j++ {
		xPhys[j] = sanitize(xPhys[j])
	}

	r0 := m.ComputeResiduals(xPhys)
	nOpt := len(r0)

	c0 := m.ComputeConstraints(xPhys)
	nCon := len(c0)

	J_opt := make([][]float64, nOpt)
	for i := 0; i < nOpt; i++ {
		J_opt[i] = make([]float64, nVars)
	}

	J_con := make([][]float64, nCon)
	for i := 0; i < nCon; i++ {
		J_con[i] = make([]float64, nVars)
	}

	for j := 0; j < nVars; j++ {
		xPert := make([]float64, nVars)
		copy(xPert, xPhys)
		xPert[j] += epsilon * scales[j]

		rPert := m.ComputeResiduals(xPert)
		cPert := m.ComputeConstraints(xPert)

		for i := 0; i < nOpt; i++ {
			diff := rPert[i] - r0[i]
			J_opt[i][j] = sanitize(diff / epsilon)
		}
		for i := 0; i < nCon; i++ {
			diff := cPert[i] - c0[i]
			J_con[i][j] = sanitize(diff / epsilon)
		}
	}

	return J_opt, r0, J_con, c0
}

func buildAugmented(J_opt [][]float64, r_opt []float64, J_con [][]float64, c_con []float64, lambdas []float64, muCon float64) ([][]float64, []float64) {
	nOpt := len(r_opt)
	nCon := len(c_con)
	nVars := len(J_opt[0])

	rAug := make([]float64, nOpt+nCon)
	JAug := make([][]float64, nOpt+nCon)

	for i := 0; i < nOpt; i++ {
		rAug[i] = r_opt[i]
		JAug[i] = make([]float64, nVars)
		copy(JAug[i], J_opt[i])
	}

	sqrtMu := math.Sqrt(muCon)
	for j := 0; j < nCon; j++ {
		rAug[nOpt+j] = sqrtMu * (c_con[j] + lambdas[j]/muCon)
		JAug[nOpt+j] = make([]float64, nVars)
		for k := 0; k < nVars; k++ {
			JAug[nOpt+j][k] = sqrtMu * J_con[j][k]
		}
	}

	return JAug, rAug
}

func denormalize(n []float64, variables []VariableInfo, scales []float64) []float64 {
	x := make([]float64, len(n))
	for i := range n {
		x[i] = variables[i].Min + n[i]*scales[i]
	}
	return x
}
