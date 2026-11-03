package main

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sweepZMX is a minimal two-surface Zemax file that imports cleanly, so the
// sweep writes a real (not just parse-failed) record for it.
const sweepZMX = `! Test
VERS 2023-01-01
MODE SEQ
NAME Test
UNIT MM
STOP 3
WAVL 0.58756
FIELD 0 0 0.0 1.0
SURF 0
  TYPE STANDARD
  CURV 0.0
  THIC INFINITY
  GLAS AIR
  DIAM 0.0
SURF 1
  TYPE STANDARD
  CURV 0.02
  THIC 5.0
  GLAS BK7
  DIAM 20.0
SURF 2
  TYPE STANDARD
  CURV -0.01
  THIC 20.0
  GLAS AIR
  DIAM 20.0
SURF 3
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
  GLAS AIR
  DIAM 10.0
`

// TestImportsweepCLIHelper re-executes the test binary as importsweep. The
// sweep exits the process on argument errors, so it cannot run in-process.
func TestImportsweepCLIHelper(t *testing.T) {
	if os.Getenv("GO_IMPORTSWEEP_HELPER") != "1" {
		return
	}
	var args []string
	_ = json.Unmarshal([]byte(os.Getenv("GO_IMPORTSWEEP_ARGS")), &args)
	os.Args = append([]string{"importsweep"}, args...)
	main()
	os.Exit(0)
}

// TestRecordRowMatchesHeader guards the CSV contract: the record row and the
// header must stay column-for-column, or the report silently shifts.
func TestRecordRowMatchesHeader(t *testing.T) {
	var r record
	if got, want := len(r.row()), len(header); got != want {
		t.Fatalf("record row has %d cells, header has %d", got, want)
	}
}

// TestCollectFiles pins the directory walk: only the three supported lens
// extensions are swept, Zemax archives are reported as skipped, and unrelated
// files (including the CSVs the sweep itself writes) are ignored.
func TestCollectFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.zmx", "b.len", "c.seq", "d.zda", "notes.txt", "sweep_results.csv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "e.zmx"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, skipped := collectFiles(dir)
	if len(files) != 4 {
		t.Fatalf("collected %d files, want 4: %+v", len(files), files)
	}
	for i := 1; i < len(files); i++ {
		if files[i-1].abs > files[i].abs {
			t.Fatalf("files are not sorted: %v", files)
		}
	}
	if len(skipped) != 1 || filepath.Ext(skipped[0]) != ".zda" {
		t.Fatalf("skipped = %v, want the single .zda", skipped)
	}
}

// TestSweepWritesCSV runs the whole tool on a one-lens directory and checks the
// result report rather than the tool's internal helpers.
func TestSweepWritesCSV(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lens.zmx"), []byte(sweepZMX), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "result.csv")
	summary := filepath.Join(dir, "summary.csv")

	args := []string{"-root", dir, "-out", out, "-summary", summary}
	cmd := exec.Command(os.Args[0], "-test.run=^TestImportsweepCLIHelper$")
	raw, _ := json.Marshal(args)
	cmd.Env = append(os.Environ(), "GO_IMPORTSWEEP_HELPER=1", "GO_IMPORTSWEEP_ARGS="+string(raw))
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("importsweep failed: %v\n%s", err, msg)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open result csv: %v", err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read result csv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("result csv has %d rows, want header + 1 lens\n%v", len(rows), rows)
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	for _, name := range header {
		if _, ok := col[name]; !ok {
			t.Fatalf("result csv is missing column %q", name)
		}
	}
	row := rows[1]
	if !strings.HasSuffix(row[col["path"]], "lens.zmx") {
		t.Errorf("path = %q, want lens.zmx", row[col["path"]])
	}
	if row[col["parse_ok"]] != "yes" {
		t.Errorf("parse_ok = %q, want yes (parse_error %q)", row[col["parse_ok"]], row[col["parse_error"]])
	}
	if row[col["surfaces"]] == "0" {
		t.Errorf("surfaces = 0, want the imported surface count")
	}
	if _, err := os.Stat(summary); err != nil {
		t.Errorf("summary csv not written: %v", err)
	}
}
