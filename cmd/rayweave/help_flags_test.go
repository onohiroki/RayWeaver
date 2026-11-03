package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The help text is hand-written (help.go), so a flag can drift out of it in two
// directions: a flag that no longer exists stays documented, or a new flag is
// never documented. These scanners recover both sets from the source and the
// test fails on either difference.

var (
	// fs.String("name", ...), fs.Bool("name", ...), ...
	flagFirstArg = regexp.MustCompile(`fs\.\w+\(\s*"([a-z0-9][a-z0-9-]*)"`)
	// fs.StringVar(&v, "name", ...), fs.BoolVar(&v, "name", ...)
	flagVarSecondArg = regexp.MustCompile(`fs\.\w+Var\(\s*[^,\n]+,\s*"([a-z0-9][a-z0-9-]*)"`)
	// fs.Var(&v, "name", ...)
	flagVarMethod = regexp.MustCompile(`fs\.Var\(\s*[^,\n]+,\s*"([a-z0-9][a-z0-9-]*)"`)
	// fs.StringVar(&v, constName, ...) — the name held in a const.
	flagConstName = regexp.MustCompile(`fs\.(?:\w+Var|Var)\(\s*[^,\n]+,\s*([A-Za-z_]\w*)`)
	// const phaseCleanDLSFlag = "clean-dls"
	stringConst = regexp.MustCompile(`(?m)^\s*(?:const\s+)?(\w+)\s*=\s*"([a-z0-9][a-z0-9-]*)"`)

	documentedFlag = regexp.MustCompile(`--([a-z0-9][a-z0-9-]*)`)
)

// cliSource reads the non-test Go files of the package.
func cliSource(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob sources: %v", err)
	}
	src := make(map[string]string, len(files))
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		src[f] = string(data)
	}
	return src
}

func TestHelpDocumentsEveryFlag(t *testing.T) {
	src := cliSource(t)
	help, ok := src["help.go"]
	if !ok {
		t.Fatal("help.go not found")
	}

	consts := map[string]string{}
	for _, s := range src {
		for _, m := range stringConst.FindAllStringSubmatch(s, -1) {
			consts[m[1]] = m[2]
		}
	}

	registered := map[string]bool{}
	add := func(name string) {
		if name != "" {
			registered[name] = true
		}
	}
	for _, s := range src {
		for _, m := range flagFirstArg.FindAllStringSubmatch(s, -1) {
			add(m[1])
		}
		for _, m := range flagVarSecondArg.FindAllStringSubmatch(s, -1) {
			add(m[1])
		}
		for _, m := range flagVarMethod.FindAllStringSubmatch(s, -1) {
			add(m[1])
		}
		for _, m := range flagConstName.FindAllStringSubmatch(s, -1) {
			add(consts[m[1]])
		}
	}

	documented := map[string]bool{}
	for _, m := range documentedFlag.FindAllStringSubmatch(help, -1) {
		documented[m[1]] = true
	}

	// Prose in help.go mentions "--flag" generically and "--help"; they are not
	// command flags. -o and -r are single-dash aliases documented as such.
	helpOnlyAllowed := map[string]bool{"flag": true, "help": true}
	registeredOnlyAllowed := map[string]bool{"o": true, "r": true}

	var stale, undocumented []string
	for name := range documented {
		if !registered[name] && !helpOnlyAllowed[name] {
			stale = append(stale, name)
		}
	}
	for name := range registered {
		if !documented[name] && !registeredOnlyAllowed[name] {
			undocumented = append(undocumented, name)
		}
	}
	sort.Strings(stale)
	sort.Strings(undocumented)

	if len(stale) > 0 {
		t.Errorf("help.go documents flags that are not registered: %v", stale)
	}
	if len(undocumented) > 0 {
		t.Errorf("flags registered without a help entry: %v", undocumented)
	}
}
