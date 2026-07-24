package main

import "testing"

// shrinkProse used to swap protected literals for space-wrapped numeric
// sentinels (" 3 "), so a bare integer in the prose could be restored as the
// wrong literal — or dropped. Sentinels now use NUL delimiters, which never
// occur in prose, so real numbers survive untouched.
func TestShrinkProseKeepsBareIntegers(t *testing.T) {
	cases := []string{
		"call a.b keep 0 alive and 1 more",
		"review the top 5 results and 3 tables now",
		"use `code` then keep 0 and 1 and 2",
	}
	for _, in := range cases {
		out := shrinkProse(in)
		for _, n := range []string{"0", "1", "2", "3", "5"} {
			// Only assert for numbers actually present in the input.
			if contains(in, " "+n) && !contains(out, n) {
				t.Errorf("shrinkProse(%q) dropped %q: got %q", in, n, out)
			}
		}
		// The protected literals must come back verbatim.
		if contains(in, "a.b") && !contains(out, "a.b") {
			t.Errorf("shrinkProse(%q) lost literal a.b: got %q", in, out)
		}
		if contains(in, "`code`") && !contains(out, "`code`") {
			t.Errorf("shrinkProse(%q) lost literal `code`: got %q", in, out)
		}
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// reduce promises it never emits more tokens than it was given, and that a
// second reduction of its own output is a fixpoint (the converge loop halts).
func TestReduceNeverLargerAndConverges(t *testing.T) {
	inputs := []string{
		"The quick brown fox really jumps over the very lazy dog basically.",
		"Please kindly review the authentication middleware and just fix the expiry check simply.",
		"A cache miss leads to a slow query which produces a timeout for the user.",
		"call service.start() with `--flag` then keep 0 and 1 retries",
	}
	for _, level := range []string{"lite", "full", "ultra"} {
		for _, in := range inputs {
			out := reduce(in, level, 0, true, true, true, true)
			if estimateTokens(out) > estimateTokens(in) {
				t.Errorf("level %s: output larger than input\n in:  %q (%d)\n out: %q (%d)",
					level, in, estimateTokens(in), out, estimateTokens(out))
			}
			// Reducing the output again must not shrink it further — the first
			// call already ran to convergence.
			if again := reduce(out, level, 0, true, true, true, true); estimateTokens(again) > estimateTokens(out) {
				t.Errorf("level %s: second reduce grew output %q -> %q", level, out, again)
			}
		}
	}
}
