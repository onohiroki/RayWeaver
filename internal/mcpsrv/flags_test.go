package mcpsrv

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/clispec"
)

// The MCP tools re-declare the CLI's flag surface one line per flag (floatFlag,
// intFlag, ...). A flag renamed or removed in cmd/rayweave would leave the tool
// building an argument the CLI rejects, and nothing would notice until a client
// called it. This test reads both sides from source and fails on drift.

var (
	cliFlagFirstArg = regexp.MustCompile(`fs\.\w+\(\s*"([a-z0-9][a-z0-9-]*)"`)
	cliFlagVarArg   = regexp.MustCompile(`fs\.\w+Var\(\s*[^,\n]+,\s*"([a-z0-9][a-z0-9-]*)"`)
	cliFlagVarCall  = regexp.MustCompile(`fs\.Var\(\s*[^,\n]+,\s*"([a-z0-9][a-z0-9-]*)"`)
	cliFlagConst    = regexp.MustCompile(`fs\.(?:\w+Var|Var)\(\s*[^,\n]+,\s*([A-Za-z_]\w*)`)
	cliStringConst  = regexp.MustCompile(`(?m)^\s*(?:const\s+)?(\w+)\s*=\s*"([a-z0-9][a-z0-9-]*)"`)
	// floatFlag(args, in.X, "--max-iter") and friends.
	mcpFlagUse = regexp.MustCompile(`(?:boolFlag|strFlag|floatFlag|intFlag|boolPtrFlag|listFlag)\([^,\n]+,\s*(?:[^,\n]+,\s*)?"(--[a-z0-9-]+)"`)
)

func readGoFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	out := map[string]string{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		out[f] = string(data)
	}
	return out
}

// TestMCPFlagsExistInCLI checks every flag the MCP tools pass against the
// flags cmd/rayweave actually registers.
func TestMCPFlagsExistInCLI(t *testing.T) {
	cli := readGoFiles(t, filepath.Join("..", "..", "cmd", "rayweave"))
	mcp := readGoFiles(t, ".")

	consts := map[string]string{}
	for _, s := range cli {
		if strings.HasSuffix(s, "_test.go") {
			continue
		}
		for _, m := range cliStringConst.FindAllStringSubmatch(s, -1) {
			consts[m[1]] = m[2]
		}
	}

	registered := map[string]bool{}
	for path, s := range cli {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		for _, m := range cliFlagFirstArg.FindAllStringSubmatch(s, -1) {
			registered[m[1]] = true
		}
		for _, m := range cliFlagVarArg.FindAllStringSubmatch(s, -1) {
			registered[m[1]] = true
		}
		for _, m := range cliFlagVarCall.FindAllStringSubmatch(s, -1) {
			registered[m[1]] = true
		}
		for _, m := range cliFlagConst.FindAllStringSubmatch(s, -1) {
			if name, ok := consts[m[1]]; ok {
				registered[name] = true
			}
		}
	}

	// Flags migrated to a clispec table no longer appear as fs.*("name")
	// literals in cmd/rayweave; the table is the registration.
	for _, spec := range clispec.All {
		for _, f := range spec.Flags {
			registered[f.Name] = true
			if f.Short != "" {
				registered[f.Short] = true
			}
			if f.Alias != "" {
				registered[f.Alias] = true
			}
		}
	}

	used := map[string]bool{}
	for path, s := range mcp {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		for _, m := range mcpFlagUse.FindAllStringSubmatch(s, -1) {
			used[strings.TrimPrefix(m[1], "--")] = true
		}
	}

	var missing []string
	for name := range used {
		if !registered[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("MCP tools pass flags the CLI does not register: %v", missing)
	}
	if len(used) == 0 {
		t.Fatal("no flag uses found; the scanner is out of date with the tool helpers")
	}
}
