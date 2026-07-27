package main

import (
	"strings"
	"testing"
)

func hasAdjacentArrows(s string) bool {
	for i := 0; i+2 <= len(s); i++ {
		if s[i:i+2] != "->" {
			continue
		}
		j := i + 2
		for j < len(s) {
			switch s[j] {
			case ' ', '\t', '\n', '\r':
				j++
				continue
			}
			break
		}
		if j+2 <= len(s) && s[j:j+2] == "->" {
			return true
		}
	}
	return false
}

func TestCleanupCollapsesArrowRuns(t *testing.T) {
	cases := map[string]string{
		"a -> -> b":          "a -> b",
		"a -> -> -> b":       "a -> b",
		"a ->->-> b":         "a -> b",
		"a -> and -> b":      "a -> b",
		"a -> which -> b":    "a -> b",
		"a -> and thus -> b": "a -> b",
		"a -> b -> c":        "a -> b -> c",
		// one-sided arrows KEEP direction when content remains
		"-> -> x": "-> x",
		"x -> ->": "x ->",
		"-> x":    "-> x",
		"x ->":    "x ->",
		// pure arrows vanish
		"->":       "",
		"-> ->":    "",
		"-> -> ->": "",
	}
	for in, want := range cases {
		if got := cleanupArrows(in); got != want {
			t.Errorf("cleanupArrows(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNoAdjacentArrowArtifacts(t *testing.T) {
	cases := []string{
		"Alpha therefore thus hence Bravo",
		"Alpha and therefore and thus Bravo",
		"Alpha leads to Bravo which produces Charlie and therefore Delta",
		"step A which produces and therefore causes step B",
		"A becomes B becomes C becomes D",
	}
	for _, in := range cases {
		got := reduce(in, "full", 0, true, true, true, true, true, false)
		if hasAdjacentArrows(got) {
			t.Errorf("adjacent arrows for %q: %q", in, got)
		}
		if strings.Contains(got, "->->") {
			t.Errorf("glued arrows for %q: %q", in, got)
		}
	}
}

func TestArrowVisibleAfterReduce(t *testing.T) {
	// Verb-connectives with no separate subject must still show "->".
	cases := map[string]string{
		"defaults to zero":                 "->",
		"maps to the output":               "->",
		"compiles to bytecode":             "->",
		"evaluates to true":                "->",
		"desugars to a loop":               "->",
		"the call falls back to the cache": "->",
		"cache miss leads to a slow query which produces a timeout": "->",
		"Alpha causes Bravo and therefore Charlie":                  "->",
	}
	for in, want := range cases {
		got := reduce(in, "full", 0, true, true, true, true, true, false)
		if !strings.Contains(got, want) {
			t.Errorf("reduce(%q)=%q want contains %q", in, got, want)
		}
	}
}
