package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hiroki/rayweaver/internal/runctl"
)

func TestStopFileEnvMatchesTheLauncher(t *testing.T) {
	if stopFileEnv != runctl.StopFileEnv {
		t.Fatalf("the CLI watches %q but runctl sets %q", stopFileEnv, runctl.StopFileEnv)
	}
}

func TestStageRunnerIsIdempotentAndOrdered(t *testing.T) {
	var got []int
	r := newStageRunner(
		func() { got = append(got, 1) },
		func() { got = append(got, 2) },
		func() { got = append(got, 3) },
	)
	from, to := r.request(1)
	if from != 0 || to != 1 || len(got) != 1 || got[0] != 1 {
		t.Fatalf("request(1) = %d..%d, got %v", from, to, got)
	}
	if from, to := r.request(1); from != 1 || to != 1 || len(got) != 1 {
		t.Errorf("duplicate request(1) applied again: %v", got)
	}
	if from, to := r.request(3); from != 1 || to != 3 || len(got) != 3 {
		t.Errorf("request(3): %d..%d, got %v", from, to, got)
	}
	if from, to := r.request(2); from != 3 || to != 3 {
		t.Errorf("a late stage-2 request rewound the sequence: %d..%d", from, to)
	}
}

func TestWatchStopFile(t *testing.T) {
	prev := stopFilePoll
	stopFilePoll = 5 * time.Millisecond
	t.Cleanup(func() { stopFilePoll = prev })

	path := filepath.Join(t.TempDir(), "stop")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stages := make(chan int, 8)
	stop := watchStopFile(path, func(n int) { stages <- n })

	appendStage := func(s string) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	wait := func(want int) {
		t.Helper()
		select {
		case got := <-stages:
			if got != want {
				t.Fatalf("stage = %d, want %d", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("stage %d was not requested", want)
		}
	}

	appendStage("1\n")
	wait(1)
	appendStage("not-a-number\n1\n")
	select {
	case got := <-stages:
		t.Fatalf("junk or a duplicate produced stage %d", got)
	case <-time.After(50 * time.Millisecond):
	}
	appendStage("2\n")
	wait(2)

	// A file that jumps straight to 2 is reported as 2; the stage runner (not
	// the watcher) is responsible for filling in stage 1.
	stop()
	path2 := filepath.Join(t.TempDir(), "stop2")
	if err := os.WriteFile(path2, []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	jumped := make(chan int, 1)
	stop2 := watchStopFile(path2, func(n int) { jumped <- n })
	defer stop2()
	select {
	case got := <-jumped:
		if got != 2 {
			t.Fatalf("jumped stage = %d, want 2", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a file that starts at stage 2 was not read")
	}
}
