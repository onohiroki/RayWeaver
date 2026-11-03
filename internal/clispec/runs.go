package clispec

// The three compute commands share most of their flag surface. The specs are
// built from one common list so a new diagnostic or phase flag lands in all of
// them at once.
func runCommonFlags() []Flag {
	return []Flag{
		{Name: "verbose", Kind: Bool, Role: RoleAction,
			Help: "print progress to stderr as compact JSONL"},
		{Name: "log", Kind: String, Role: RoleOutput,
			Help: "write the full JSONL progress stream to FILE"},
		{Name: "glass-dir", Kind: String, Role: RoleSetting,
			Help: "AGF glass catalog directory"},
		{Name: "power-solve", Kind: Bool, Role: RoleAction,
			Help: "insert the power-preserving glass phase between each global phase and clean DLS"},
		{Name: "power-solve-surfaces", Kind: String, Role: RoleAction,
			Help: "comma-separated surface IDs whose curvature is recomputed to hold the element power"},
		{Name: "glass-variables", Kind: Bool, Role: RoleAction,
			Help: "auto-generate nd/vd optimization variables for every refractive lens element"},
		{Name: "cpuprofile", Kind: String, Role: RoleOutput,
			Help: "write a CPU profile (pprof) to FILE"},
		{Name: "memprofile", Kind: String, Role: RoleOutput,
			Help: "write a heap profile (pprof) to FILE at exit"},
		{Name: "gorprofile", Kind: String, Role: RoleOutput,
			Help: "write a goroutine profile to FILE at exit"},
		{Name: "gogc", Kind: Int, Role: RoleSetting,
			Help: "GC target percentage (0 = default for compute commands, 400; overrides $GOGC)"},
	}
}

func escapeOnlyFlags() []Flag {
	return []Flag{
		{Name: "save", Kind: String, Role: RoleOutput,
			Help: "save each discovered local minimum to FILE0.yaml, FILE1.yaml, ..."},
		{Name: "keep-infeasible", Kind: Bool, Role: RoleSetting,
			Help: "include infeasible basins in stdout YAML (default: discard)"},
		{Name: "stream", Kind: BoolString, Role: RoleSetting,
			Help: "stream the pipeline document to stdout as the run progresses: true (default) or false"},
		{Name: "clean-dls", Kind: BoolString, Role: RoleSetting,
			Help: "run the clean DLS that closes each cycle: true (default) or false"},
		{Name: "mem-limit", Kind: String, Role: RoleSetting,
			Help: "cap the Go heap: 2GiB, 2048MiB, 2048 (MB) or -1 (unlimited); default optimization.mem_limit_mb"},
	}
}

// Optimize is the flag surface of `rayweave optimize`.
var Optimize = Spec{Command: "optimize", Flags: append(runCommonFlags(),
	Flag{Name: "exclude-param", Kind: String, Role: RoleAction,
		Help: "comma-separated target param names to drop from the optimization variables (e.g. conic,a4,a6)"},
)}

// OptimizeSnap is the flag surface of `rayweave optimize snap`.
var OptimizeSnap = Spec{Command: "optimize snap", Flags: []Flag{
	{Name: "glass-dir", Kind: String, Role: RoleSetting,
		Help: "AGF glass catalog directory"},
}}

// Escape is the flag surface of `rayweave escape`.
var Escape = Spec{Command: "escape", Flags: append(append(runCommonFlags(),
	Flag{Name: "debug", Kind: Bool, Role: RoleAction,
		Help: "emit all DLS-internal JSONL events plus enriched cycle diagnostics"}),
	escapeOnlyFlags()...)}

// PSO is the flag surface of `rayweave pso`: escape's flags without --debug,
// plus the swarm controls.
var PSO = Spec{Command: "pso", Flags: append(append(runCommonFlags(), escapeOnlyFlags()...),
	Flag{Name: "swarm-size", Kind: Int, Role: RoleSetting,
		Help: "number of particles per worker (overrides optimization.pso.swarm_size)"},
	Flag{Name: "pso-iterations", Kind: Int, Role: RoleSetting,
		Help: "PSO iterations per cycle (overrides optimization.pso.pso_iterations)"},
	Flag{Name: "constraint-penalty", Kind: Float, Role: RoleSetting,
		Help: "constraint penalty weight (overrides optimization.pso.constraint_penalty)"},
	Flag{Name: "gbest", Kind: String, Role: RoleSetting,
		Help: "scale the global best is ranked on: merit (default) or penalised"},
)}
