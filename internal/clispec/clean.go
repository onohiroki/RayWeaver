package clispec

// Clean is the flag surface of `rayweave clean`.
var Clean = Spec{Command: "clean", Flags: []Flag{
	{Name: "verbose", Kind: Bool, Role: RoleAction,
		Help: "print removed fields as JSONL to stderr"},
}}
