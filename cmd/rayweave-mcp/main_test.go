package main

import (
	"strings"
	"testing"
)

func TestRunRejectsAnUnknownToolSet(t *testing.T) {
	err := run([]string{"-tools", "everything"})
	if err == nil || !strings.Contains(err.Error(), "core or all") {
		t.Fatalf("run(-tools everything) = %v, want a tool-set error", err)
	}
}

func TestRunRejectsPositionalArguments(t *testing.T) {
	err := run([]string{"serve"})
	if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("run(serve) = %v, want an unexpected-argument error", err)
	}
}

func TestRunVersion(t *testing.T) {
	if err := run([]string{"-version"}); err != nil {
		t.Fatalf("run(-version) = %v", err)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "x", "y"); got != "x" {
		t.Errorf("firstNonEmpty = %q, want x", got)
	}
	if got := firstNonEmpty("", " "); got != "" {
		t.Errorf("firstNonEmpty = %q, want empty", got)
	}
}
