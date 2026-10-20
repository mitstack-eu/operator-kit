package main

import (
	"strings"
	"testing"
)

const testZeroSHA = "0000000000000000000000000000000000000000"

// An unbounded git log walks every ancestor, so every row also checks that the range is bounded.
func TestResolveRange(t *testing.T) {
	const cur = "curSHA"
	cases := []struct {
		name, anchor, before, want string
	}{
		{"a usable anchor wins", "anchorSHA", "beforeSHA", "anchorSHA..curSHA"},
		{"a usable anchor wins over an empty before", "anchorSHA", "", "anchorSHA..curSHA"},
		{"a usable anchor wins over an all-zero before", "anchorSHA", testZeroSHA, "anchorSHA..curSHA"},
		{"a usable anchor wins over a before equal to current", "anchorSHA", cur, "anchorSHA..curSHA"},
		{"an empty anchor falls through to before", "", "beforeSHA", "beforeSHA..curSHA"},
		{"an all-zero anchor falls through to before", testZeroSHA, "beforeSHA", "beforeSHA..curSHA"},
		{"an anchor equal to current falls through to before", cur, "beforeSHA", "beforeSHA..curSHA"},
		{"empty anchor and before fall back to the tip", "", "", "curSHA~1..curSHA"},
		{"all-zero anchor and before fall back to the tip", testZeroSHA, testZeroSHA, "curSHA~1..curSHA"},
		{"anchor and before equal to current fall back to the tip", cur, cur, "curSHA~1..curSHA"},
		{"an empty anchor and an all-zero before fall back to the tip", "", testZeroSHA, "curSHA~1..curSHA"},
		{"an all-zero anchor and an empty before fall back to the tip", testZeroSHA, "", "curSHA~1..curSHA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveRange(tc.anchor, tc.before, cur)

			if got != tc.want {
				t.Errorf("resolveRange(%q, %q, %q) = %q, want %q", tc.anchor, tc.before, cur, got, tc.want)
			}
			if !strings.Contains(got, "..") || got == cur {
				t.Errorf("resolveRange(%q, %q, %q) = %q, not a bounded range", tc.anchor, tc.before, cur, got)
			}
		})
	}
}
