package main

import (
	"math"
	"testing"
)

// TestParseMemLimitMB verifies the --mem-limit size parser: a bare number is
// MB, binary/decimal suffixes are accepted, and <= 0 means unlimited.
func TestParseMemLimitMB(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		err  bool
	}{
		{"2GiB", 2048, false},
		{"2048MiB", 2048, false},
		{"2048", 2048, false},
		{"2GB", 2000, false},
		{"1.5GiB", 1536, false},
		{"512m", 512, false},
		{"-1", -1, false},
		{"0", -1, false},
		{"", 0, true},
		{"abc", 0, true},
		{"2XiB", 0, true},
	}
	for _, c := range cases {
		got, err := parseMemLimitMB(c.in)
		if (err != nil) != c.err {
			t.Errorf("parseMemLimitMB(%q) error = %v, want err=%v", c.in, err, c.err)
			continue
		}
		if !c.err && math.Abs(got-c.want) > 1e-9 {
			t.Errorf("parseMemLimitMB(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
