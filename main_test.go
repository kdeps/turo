package main

import (
	"strings"
	"testing"
)

func TestParseToGraph_ContentWords(t *testing.T) {
	got := parseToGraph("the quick brown fox jumps over the lazy dog", "full", 0)
	for _, want := range []string{"quick", "brown", "fox", "jumps", "lazy", "dog"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected content word %q in output, got:\n%s", want, got)
		}
	}
	// Stop words must not survive.
	for _, w := range strings.Fields(got) {
		if w == "the" || w == "over" {
			t.Fatalf("stop word %q leaked into output:\n%s", w, got)
		}
	}
	// No arrows or emoji — those cost tokens.
	if strings.ContainsAny(got, "→>") {
		t.Fatalf("output must not contain arrows:\n%s", got)
	}
}

func TestParseToGraph_ReducesTokens(t *testing.T) {
	text := "the quick brown fox jumps over the lazy dog"
	in := estimateTokens(text)
	for _, level := range []string{"lite", "full", "ultra"} {
		got := parseToGraph(text, level, 0)
		if out := estimateTokens(got); out >= in {
			t.Fatalf("level %s did not reduce tokens: in=%d out=%d\n%s", level, in, out, got)
		}
	}
}

func TestParseToGraph_LevelsDiffer(t *testing.T) {
	text := "the quick brown fox jumps over the lazy dog"
	lite := parseToGraph(text, "lite", 0)
	full := parseToGraph(text, "full", 0)
	ultra := parseToGraph(text, "ultra", 0)
	if lite == ultra || full == ultra {
		t.Fatalf("levels produced identical output:\nlite=%q\nfull=%q\nultra=%q", lite, full, ultra)
	}
	// ultra is the most aggressive — never more words than full.
	if len(strings.Fields(ultra)) > len(strings.Fields(full)) {
		t.Fatalf("ultra should keep no more words than full:\nfull=%q\nultra=%q", full, ultra)
	}
}

func TestParseToGraph_PassThroughWhenNotSmaller(t *testing.T) {
	// Already-terse, content-only input: reduction can't help, so the
	// original must be returned unchanged rather than something larger.
	text := "fox jump dog"
	if got := parseToGraph(text, "full", 0); got != text {
		t.Fatalf("expected pass-through of %q, got %q", text, got)
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

func TestLemma(t *testing.T) {
	cases := map[string]string{
		// irregular verbs
		"went": "go", "gone": "go", "saw": "see", "seen": "see", "ran": "run",
		// irregular plurals / comparatives
		"children": "child", "mice": "mouse", "men": "man",
		"better": "good", "worst": "bad",
		// regular inflections that reduce to a known base
		"goes": "go", "going": "go", "runs": "run", "running": "run",
		"sees": "see", "servers": "server", "processes": "process",
		// dropped-e restored: -ing / -ed base ends in "e"
		"creating": "create", "using": "use", "moved": "move",
		// doubled consonant collapsed
		"stopped": "stop",
		// -ies plural
		"companies": "company",
		// base words the naive stemmer used to corrupt must stay put:
		// -er is derivational, -ss is not a plural
		"render": "render", "pass": "pass", "process": "process",
		"server": "server", "user": "user",
		// already-base words are unchanged
		"go": "go", "fox": "fox",
	}
	for in, want := range cases {
		if got := lemma(in); got != want {
			t.Errorf("lemma(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseToGraph_UltraLemmaDedup(t *testing.T) {
	// Every inflection of go/fox/run collapses to one token each.
	got := parseToGraph("the fox goes and the fox went and foxes run while it ran", "ultra", 0)
	if got != "fox go run" {
		t.Fatalf("expected lemma-deduped %q, got %q", "fox go run", got)
	}
}

