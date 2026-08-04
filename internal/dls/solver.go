package dls

import (
	"math"
	"runtime"
	"sync"
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
	if opts.MuConMax <= 0 {
		opts.MuConMax = 100.0
	}
	if opts.Workers <= 0 {
		opts.Workers = runtime.GOMAXPROCS(0)
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
	// Per-constraint penalty weight. Each constraint's penalty grows
	// independently so a satisfiable constraint is enforced tightly while an
	// infeasible one can be relaxed (see below) instead of dominating the
	// merit and freezing the solve.
	muConJ := make([]float64, nCon)
	for j := range muConJ {
		muConJ[j] = 0.01
	}
	merit := m.EvaluateMerit(xPhys0)
	meritOnly := merit
	if hasConstraints {
		for j, cj := range c0 {
			merit += constraintTerm(j, cj, lambdas, muConJ)
		}
	}
	if merit < meritOnly {
		merit = meritOnly
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
	consecStall := 0
	noImprove := 0

	bestKnownNorm := make([]float64, nVars)
	copy(bestKnownNorm, xNorm)
	bestKnownMerit := merit

	for totalIter = 0; totalIter < opts.MaxIter; totalIter++ {
		if pu, ok := m.(PupilUpdater); ok {
			pu.UpdatePupils(denormalize(xNorm, variables, scales))
		}
		J_opt, r_opt, J_con, c_con := computeJacobians(m, xNorm, variables, scales, opts.Epsilon, opts.Workers)

		J, r := J_opt, r_opt
		if hasConstraints {
			J, r = buildAugmented(J_opt, r_opt, J_con, c_con, lambdas, muConJ)
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
		meritNewOnly := meritNew
		var cNew []float64
		if hasConstraints {
			cNew = m.ComputeConstraints(xPhysNew)
			for j, cj := range cNew {
				meritNew += constraintTerm(j, cj, lambdas, muConJ)
			}
		}
		if meritNew < meritNewOnly {
			meritNew = meritNewOnly
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
				meritTestOnly := meritTest
				var cTest []float64
				if hasConstraints {
					cTest = m.ComputeConstraints(xTestPhys)
					for j, cj := range cTest {
						meritTest += constraintTerm(j, cj, lambdas, muConJ)
					}
				}
				if meritTest < meritTestOnly {
					meritTest = meritTestOnly
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
		normDelta := 0.0
		for _, d := range delta {
			normDelta += d * d
		}
		predictedReduction := 0.5*halfDeltaHDelta + 0.5*mu*normDelta

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
					lambdas[j] += muConJ[j] * cj
				}
				for j, cj := range cNew {
					// Enforce harder when this constraint's violation persists
					// or grows on an accepted step.
					if math.Abs(cj) > 0.25*math.Abs(cPrev[j]) && math.Abs(cj) > 1e-4 {
						muConJ[j] *= 10.0
						if muConJ[j] > opts.MuConMax {
							muConJ[j] = opts.MuConMax
						}
					}
				}
				copy(cPrev, cNew)
			}
			if merit < bestMerit {
				bestMerit = merit
				bestKnownMerit = merit
				copy(bestXNorm, xNorm)
				copy(bestKnownNorm, xNorm)
			}
		}

		stepNorm := math.Sqrt(normDelta)
		lastDelta = delta

		if opts.Logger != nil {
			currVars := make([]float64, nVars)
			for i := range nVars {
				currVars[i] = variables[i].Min + xNorm[i]*scales[i]
			}
			var constr []ConstraintState
			if hasConstraints {
				constr = make([]ConstraintState, len(cPrev))
				for j, cj := range cPrev {
					constr[j] = ConstraintState{Residual: cj}
				}
			}
			opts.Logger.LogIter(totalIter+1, merit, actualReduction, stepNorm, currVars, constr)
		}

		if actualReduction > 0 {
			consecStall = 0
		} else {
			consecStall++
		}

		// Converge when the merit has not improved over a window and the step
		// is small, and only while every non-relaxed constraint is satisfied
		// (a plateau from an infeasible constraint keeps constraints violated,
		// so it does not converge prematurely; the constraint is relaxed in
		// the stall-escape below and the objective then gets optimised).
		if stepNorm < opts.Tol {
			constraintsOK := true
			if hasConstraints {
				for _, cj := range cPrev {
					if math.Abs(cj) > 1e-4 {
						constraintsOK = false
						break
					}
				}
			}
			if merit >= bestMerit && constraintsOK {
				noImprove++
			} else {
				noImprove = 0
			}
			if noImprove >= 8 && constraintsOK {
				status = "converged"
				break
			}
		}

		// Stall escape: when stuck for many iterations, perturb the state and
		// reset damping. A plateau can be caused by an unsatisfiable constraint
		// (its penalty dominates the merit with no descent direction), a
		// vignetting discontinuity, or a degenerate spot evaluation.
		if consecStall >= 30 && totalIter < opts.MaxIter-10 && !opts.DisableStallEscape {
			// Reset to best known state before perturbing
			copy(xNorm, bestKnownNorm)
			merit = bestKnownMerit

			// Deterministic pseudo-random perturbation (±1% of normalized range)
			for j := 0; j < nVars; j++ {
				perturb := float64((totalIter+1)*(j+1)%53) / 53.0
				xNorm[j] += (perturb - 0.5) * 0.02
			}
			for j := 0; j < nVars; j++ {
				if xNorm[j] < 0 {
					xNorm[j] = 0
				} else if xNorm[j] > 1 {
					xNorm[j] = 1
				}
			}

			xPhys := denormalize(xNorm, variables, scales)

			meritNew := m.EvaluateMerit(xPhys)
			meritNewOnly := meritNew
			if hasConstraints {
				cNew := m.ComputeConstraints(xPhys)
				for j, cj := range cNew {
					cPrev[j] = cj
					meritNew += constraintTerm(j, cj, lambdas, muConJ)
				}
			}
			if meritNew < meritNewOnly {
				meritNew = meritNewOnly
			}
			merit = meritNew

			mu = opts.Mu
			consecStall = 0

			if merit < bestMerit {
				bestMerit = merit
				copy(bestKnownNorm, xNorm)
			}
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
		var constr []ConstraintState
		if hasConstraints {
			constr = make([]ConstraintState, len(cPrev))
			for j, cj := range cPrev {
				constr[j] = ConstraintState{Residual: cj}
			}
		}
		opts.Logger.LogFinal(iterations, status, bestMerit, finalStepNorm, finalVars, constr)
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

// computeJacobians evaluates the finite-difference Jacobian of the residuals
// and constraints with respect to every variable. Each perturbed evaluation
// writes a distinct column of J_opt/J_con, so the loop is embarrassingly
// parallel and produces bit-identical results regardless of worker count.
func computeJacobians(m Model, xNorm []float64, variables []VariableInfo, scales []float64, epsilon float64, workers int) ([][]float64, []float64, [][]float64, []float64) {
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

	column := func(j int) {
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

	if workers > 1 && nVars > 1 {
		parallelColumns(nVars, workers, column)
	} else {
		for j := 0; j < nVars; j++ {
			column(j)
		}
	}

	return J_opt, r0, J_con, c0
}

// parallelColumns runs work(j) for j in [0, n) across up to workers goroutines.
func parallelColumns(n, workers int, work func(j int)) {
	if workers > n {
		workers = n
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				work(j)
			}
		}()
	}
	for j := 0; j < n; j++ {
		jobs <- j
	}
	close(jobs)
	wg.Wait()
}

// constraintTerm returns the augmented-Lagrangian penalty contribution of
// constraint j to the merit function: lambdas[j]*cj + 0.5*muConJ[j]*cj^2.
// An infeasible constraint contributes zero so the objective is not held
// hostage by it; its violation is reported via the residuals.
func constraintTerm(j int, cj float64, lambdas []float64, muConJ []float64) float64 {
	term := lambdas[j]*cj + 0.5*muConJ[j]*cj*cj
	if term < 0 {
		lambdas[j] = 0
		term = 0.5 * muConJ[j] * cj * cj
	}
	return term
}

func buildAugmented(J_opt [][]float64, r_opt []float64, J_con [][]float64, c_con []float64, lambdas []float64, muConJ []float64) ([][]float64, []float64) {
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

	// The merit penalty term is lambdas[j]*cj + 0.5*muConJ[j]*cj^2 (see
	// Solve). Square the augmented residual to reproduce that gradient:
	//   r^2 = (muConJ[j]/2)*(cj + lambda/muConJ[j])^2
	//       = 0.5*muConJ[j]*cj^2 + lambdas[j]*cj + lambda^2/(2*muConJ[j])
	// The constant term is irrelevant for minimisation, and the gradient
	// (muConJ[j]*cj + lambdas[j]) matches the merit penalty's gradient.
	for j := 0; j < nCon; j++ {
		sqrtMu := math.Sqrt(muConJ[j] / 2.0)
		rAug[nOpt+j] = sqrtMu * (c_con[j] + lambdas[j]/muConJ[j])
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
