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
		// singular nouns ending in s must not be de-pluralized
		"news": "news", "analysis": "analysis", "virus": "virus",
		"physics": "physics", "series": "series",
		// added irregular verbs
		"flew": "fly", "hung": "hang", "dug": "dig", "spun": "spin",
		"rang": "ring", "sang": "sing", "froze": "freeze", "shot": "shoot",
		"bound": "bind", "dealt": "deal", "slept": "sleep", "hid": "hide",
		"shook": "shake", "forgot": "forget", "fled": "flee",
		// added irregular plurals
		"geese": "goose", "criteria": "criterion", "analyses": "analysis",
		"crises": "crisis", "cacti": "cactus", "wolves": "wolf",
		// -ies where the base ends in a consonant + y
		"cities": "city",
		// already-base words are unchanged
		"go": "go", "fox": "fox",
	}
	for in, want := range cases {
		if got := lemma(in); got != want {
			t.Errorf("lemma(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShortenSynonyms(t *testing.T) {
	// A swap fires only when the map has a token-cheaper synonym of the same
	// part of speech; punctuation and structure pass through.
	got := shortenSynonyms("The abdomen is here.")
	if !strings.Contains(got, "belly") {
		t.Fatalf("expected noun->noun swap abdomen->belly, got %q", got)
	}
	if !strings.HasSuffix(got, ".") {
		t.Fatalf("expected trailing punctuation preserved, got %q", got)
	}
	// A word with no mapping is left untouched.
	if out := shortenSynonyms("kubernetes"); out != "kubernetes" {
		t.Fatalf("unmapped word should be unchanged, got %q", out)
	}
}

func TestApplyGloss(t *testing.T) {
	// A same-POS defining word replaces the original; unmapped/mismatched
	// words are left alone.
	if got := applyGloss("demonstrate"); got != "show" {
		t.Fatalf("expected demonstrate->show, got %q", got)
	}
	if out := applyGloss("kubernetes"); out != "kubernetes" {
		t.Fatalf("unmapped word should be unchanged, got %q", out)
	}
}

func TestEnvDefaultOn(t *testing.T) {
	t.Setenv("TURO_TEST_DEF", "")
	if !envDefaultOn("TURO_TEST_DEF") {
		t.Fatal("should default on when unset")
	}
	for _, off := range []string{"off", "0", "false", "no"} {
		t.Setenv("TURO_TEST_DEF", off)
		if envDefaultOn("TURO_TEST_DEF") {
			t.Fatalf("%q should be falsey", off)
		}
	}
}

func TestShrinkProse(t *testing.T) {
	// Filler, pleasantry, hedge, and article words are deleted; the meaning
	// words survive.
	got := shrinkProse("Please, I think you should just use the tool.")
	for _, drop := range []string{"Please", "I think", "just", "the "} {
		if strings.Contains(got, drop) {
			t.Fatalf("filler %q survived: %q", drop, got)
		}
	}
	for _, keep := range []string{"use", "tool"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("content word %q dropped: %q", keep, got)
		}
	}
	// Code and paths are protected verbatim.
	code := "Please run `make build` and edit pkg/agent/loop.go now."
	out := shrinkProse(code)
	if !strings.Contains(out, "`make build`") || !strings.Contains(out, "pkg/agent/loop.go") {
		t.Fatalf("protected segment altered: %q", out)
	}
}

func TestEnvTrue(t *testing.T) {
	t.Setenv("TURO_TEST_FLAG", "1")
	if !envTrue("TURO_TEST_FLAG") {
		t.Fatal("expected envTrue for \"1\"")
	}
	t.Setenv("TURO_TEST_FLAG", "off")
	if envTrue("TURO_TEST_FLAG") {
		t.Fatal("expected envTrue false for \"off\"")
	}
}

func TestReduceMultiPass(t *testing.T) {
	// Structured text repeats words across sections; a second pass flattens
	// and dedupes, so more passes never yield a larger result.
	txt := "# Server\nthe server handles the request quickly\n# Client\nthe client sends the request to the server\n"
	one := estimateTokens(reduce(txt, "full", 0, 1, true, true, false))
	four := estimateTokens(reduce(txt, "full", 0, 4, true, true, false))
	if four > one {
		t.Fatalf("multi-pass larger than single: 1=%d 4=%d", one, four)
	}

	// passes <= 0 runs to convergence; the result must be a fixpoint.
	conv := reduce(txt, "ultra", 0, 0, true, true, false)
	if again := reduce(conv, "ultra", 0, 0, true, true, false); again != conv {
		t.Fatalf("convergence not stable:\n%q\n%q", conv, again)
	}

	// Convergence is at least as aggressive as a single pass.
	if estimateTokens(conv) > estimateTokens(reduce(txt, "ultra", 0, 1, true, true, false)) {
		t.Fatal("converged output larger than a single pass")
	}
}

func TestApplyWenyan(t *testing.T) {
	got := applyWenyan("wise king water fire mountain kubernetes")
	for _, c := range []string{"智", "王", "水", "火", "山"} {
		if !strings.Contains(got, c) {
			t.Fatalf("expected %s in %q", c, got)
		}
	}
	if !strings.Contains(got, "kubernetes") {
		t.Fatalf("unmapped word should stay English: %q", got)
	}
}

func TestWenyanBaseLevel(t *testing.T) {
	cases := map[string]struct {
		base   string
		wenyan bool
	}{
		"wenyan": {"ultra", true},
		"ultra":        {"ultra", false},
		"full":         {"full", false},
	}
	for in, want := range cases {
		b, w := wenyanBaseLevel(in)
		if b != want.base || w != want.wenyan {
			t.Errorf("wenyanBaseLevel(%q) = (%q,%v), want (%q,%v)", in, b, w, want.base, want.wenyan)
		}
	}
}

func TestReduceWenyanSwapsAndKeepsCode(t *testing.T) {
	got := reduce("The wise king studies pkg/x/y.go", "wenyan", 0, 0, true, false, false)
	if !strings.Contains(got, "智") || !strings.Contains(got, "王") {
		t.Fatalf("expected wenyan chars in %q", got)
	}
	if !strings.Contains(got, "pkg/x/y.go") {
		t.Fatalf("path must be preserved verbatim in %q", got)
	}
}

func TestReducePreservesLiterals(t *testing.T) {
	in := "See https://example.com/a/b?q=1 and pkg/agent/loop.go, then run `make build` at version 1.2.3."
	got := reduce(in, "ultra", 0, 0, true, true, true)
	for _, lit := range []string{
		"https://example.com/a/b?q=1", "pkg/agent/loop.go", "`make build`", "1.2.3",
	} {
		if !strings.Contains(got, lit) {
			t.Fatalf("literal %q not preserved verbatim in:\n%s", lit, got)
		}
	}
	// never larger than the input
	if estimateTokens(got) > estimateTokens(in) {
		t.Fatalf("output larger than input: %d > %d", estimateTokens(got), estimateTokens(in))
	}
}

func TestParseToGraph_UltraLemmaDedup(t *testing.T) {
	// Every inflection of go/fox/run collapses to one token each.
	got := parseToGraph("the fox goes and the fox went and foxes run while it ran", "ultra", 0)
	if got != "fox go run" {
		t.Fatalf("expected lemma-deduped %q, got %q", "fox go run", got)
	}
}
