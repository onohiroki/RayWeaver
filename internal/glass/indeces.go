package glass

type IndexEntry struct {
	Wavelength float64
	Index      float64
	Accuracy   int
}

type polyCoeff struct {
	A, B, C float64
}

var indexCoeffs = []polyCoeff{
	{A: -0.0000583850240651572, B: 0.00963062864477342, C: 0.455082920643784},   // 0: C-t/F-C
	{A: -0.0000072362389179955, B: 0.00114895547270874, C: 0.261774306273274},   // 1: d-C/F-C
	{A: -0.00000857041520043334, B: 0.00134945596551881, C: 0.493084062868116},   // 2: e-C/F-C
	{A: 0.0000318533097940724, B: -0.00488347374582586, C: 0.71998657703516},    // 3: g-F/F-C
	{A: 0.0000390840876470875, B: -0.00603177936127578, C: 1.45818772107994},    // 4: g-d/F-C
	{A: 0.0000488921337348262, B: -0.00735206734664444, C: 0.713222567189907},   // 5: h-g/F-C
	{A: 0.00017607838239413, B: -0.0267015534159699, C: 2.17820279075923},       // 6: i-g/F-C
	{A: 0.00000107034178228145, B: 0.000667885809865637, C: 0.0320256732063829}, // 7: t-1064/F-C
	{A: 0.00000335206345471141, B: 0.000727674251657583, C: 0.0368321510326376}, // 8: 1064-1129/F-C
	{A: 0.0000322443634205704, B: 0.00453596687778952, C: 0.13574120129307},     // 9: 1129-1530/F-C
	{A: 0.0000585557120323515, B: 0.00548118401740362, C: 0.0743976977850314},   // 10: 1530-1970/F-C
	{A: 0.0000138970959874907, B: 0.00127614144999278, C: 0.00947589847118399},  // 11: 1970-2058/F-C
}

func IndecesFromNdVd(nd, vd float64) map[string]IndexEntry {
	if vd == 0 {
		return allNd(nd)
	}

	common := (nd - 1.0) / vd

	y := make([]float64, 12)
	for i, c := range indexCoeffs {
		y[i] = c.A*vd*vd + c.B*vd + c.C
	}

	nC := nd - y[1]*common
	ng := nd + y[4]*common
	nt := nC - y[0]*common
	ne := nC + y[2]*common
	nF := ng - y[3]*common
	nh := ng + y[5]*common
	ni := ng + y[6]*common

	n1064 := nt - y[7]*common
	n1129 := n1064 - y[8]*common
	n1530 := n1129 - y[9]*common
	n1970 := n1530 - y[10]*common
	n2058 := n1970 - y[11]*common

	return map[string]IndexEntry{
		"i":    {Wavelength: 0.000365015, Index: ni, Accuracy: 2},
		"h":    {Wavelength: 0.000404656, Index: nh, Accuracy: 2},
		"g":    {Wavelength: 0.000435835, Index: ng, Accuracy: 1},
		"F":    {Wavelength: 0.000486133, Index: nF, Accuracy: 1},
		"e":    {Wavelength: 0.000546074, Index: ne, Accuracy: 1},
		"d":    {Wavelength: 0.000587562, Index: nd, Accuracy: 0},
		"C":    {Wavelength: 0.000656273, Index: nC, Accuracy: 1},
		"t":    {Wavelength: 0.00101398, Index: nt, Accuracy: 2},
		"1064": {Wavelength: 0.00106414, Index: n1064, Accuracy: 2},
		"1129": {Wavelength: 0.00112864, Index: n1129, Accuracy: 2},
		"1530": {Wavelength: 0.001529582, Index: n1530, Accuracy: 2},
		"1970": {Wavelength: 0.00197009, Index: n1970, Accuracy: 2},
		"2058": {Wavelength: 0.00205809, Index: n2058, Accuracy: 2},
	}
}

func allNd(nd float64) map[string]IndexEntry {
	return map[string]IndexEntry{
		"i":    {Wavelength: 0.000365015, Index: nd, Accuracy: 0},
		"h":    {Wavelength: 0.000404656, Index: nd, Accuracy: 0},
		"g":    {Wavelength: 0.000435835, Index: nd, Accuracy: 0},
		"F":    {Wavelength: 0.000486133, Index: nd, Accuracy: 0},
		"e":    {Wavelength: 0.000546074, Index: nd, Accuracy: 0},
		"d":    {Wavelength: 0.000587562, Index: nd, Accuracy: 0},
		"C":    {Wavelength: 0.000656273, Index: nd, Accuracy: 0},
		"t":    {Wavelength: 0.00101398, Index: nd, Accuracy: 0},
		"1064": {Wavelength: 0.00106414, Index: nd, Accuracy: 0},
		"1129": {Wavelength: 0.00112864, Index: nd, Accuracy: 0},
		"1530": {Wavelength: 0.001529582, Index: nd, Accuracy: 0},
		"1970": {Wavelength: 0.00197009, Index: nd, Accuracy: 0},
		"2058": {Wavelength: 0.00205809, Index: nd, Accuracy: 0},
	}
}
