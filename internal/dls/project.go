package dls

func projectOntoBox(x []float64, variables []VariableInfo) {
	for i, v := range variables {
		x[i] = sanitize(x[i])
		if x[i] < v.Min {
			x[i] = v.Min
		} else if x[i] > v.Max {
			x[i] = v.Max
		}
	}
}
