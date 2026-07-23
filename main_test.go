package main

import (
	"strings"
	"testing"
)

func TestParseToGraph_TermEdges(t *testing.T) {
	got := parseToGraph("the quick brown fox jumps over the lazy dog", "full", 0)
	for _, want := range []string{"quick → fox", "brown → fox", "fox → jumps", "lazy → dog"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected edge %q in output, got:\n%s", want, got)
		}
	}
	// Stop words must not appear as nodes.
	for _, drop := range []string{"the ", "over "} {
		if strings.Contains(got, drop) {
			t.Fatalf("stop word %q leaked into output:\n%s", drop, got)
		}
	}
}

func TestParseToGraph_LevelsDiffer(t *testing.T) {
	text := "the quick brown fox jumps over the lazy dog"
	lite := parseToGraph(text, "lite", 0)
	ultra := parseToGraph(text, "ultra", 0)
	if lite == ultra {
		t.Fatal("lite and ultra levels produced identical output")
	}
	// ultra emits a numbered chain header.
	if !strings.Contains(ultra, "→") {
		t.Fatalf("ultra output missing arrow chain, got:\n%s", ultra)
	}
}

func TestExtractStructure_HeadingsAndPaths(t *testing.T) {
	md := "# Title\n\nSome intro text here.\n\n## Section\n\nSee `pkg/agent/loop.go` for details.\n"
	got := extractStructure(md, "full")
	if !strings.Contains(got, "Title") || !strings.Contains(got, "Section") {
		t.Fatalf("expected headings preserved, got:\n%s", got)
	}
	if !strings.Contains(got, "pkg/agent/loop.go") {
		t.Fatalf("expected file path preserved verbatim, got:\n%s", got)
	}
}

func TestWrapPreamble(t *testing.T) {
	out := wrapPreamble("a → b\n")
	if !strings.HasPrefix(out, "<context-graph>") || !strings.HasSuffix(out, "</context-graph>\n") {
		t.Fatalf("preamble not wrapped in tags:\n%s", out)
	}
	if !strings.Contains(out, "a → b") {
		t.Fatalf("preamble dropped the graph body:\n%s", out)
	}
	if wrapPreamble("") != "" {
		t.Fatal("empty graph should wrap to empty string")
	}
	if wrapPreamble("   \n\n") != "" {
		t.Fatal("whitespace-only graph should wrap to empty string")
	}
}

func TestStem(t *testing.T) {
	cases := map[string]string{
		"running": "run",
		"jumps":   "jump",
		"created": "creat",
		"testing": "test",
	}
	for in, want := range cases {
		if got := stem(in); got != want {
			t.Errorf("stem(%q) = %q, want %q", in, got, want)
		}
	}
}

