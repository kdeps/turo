package main

import (
	"strings"
	"testing"
)

func TestParseToGraph_ContentWords(t *testing.T) {
	got := parseToGraph("the quick brown fox jumps over the lazy dog", "full")
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
		got := parseToGraph(text, level)
		if out := estimateTokens(got); out >= in {
			t.Fatalf("level %s did not reduce tokens: in=%d out=%d\n%s", level, in, out, got)
		}
	}
}

func TestParseToGraph_LevelsDiffer(t *testing.T) {
	text := "the quick brown fox jumps over the lazy dog"
	lite := parseToGraph(text, "lite")
	full := parseToGraph(text, "full")
	ultra := parseToGraph(text, "ultra")
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
	if got := parseToGraph(text, "full"); got != text {
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
	one := estimateTokens(reduce(txt, "full", 1, true, true, false, false, false, false))
	four := estimateTokens(reduce(txt, "full", 4, true, true, false, false, false, false))
	if four > one {
		t.Fatalf("multi-pass larger than single: 1=%d 4=%d", one, four)
	}

	// passes <= 0 runs to convergence; the result must be a fixpoint.
	conv := reduce(txt, "ultra", 0, true, true, false, false, false, false)
	if again := reduce(conv, "ultra", 0, true, true, false, false, false, false); again != conv {
		t.Fatalf("convergence not stable:\n%q\n%q", conv, again)
	}

	// Convergence is at least as aggressive as a single pass.
	if estimateTokens(conv) > estimateTokens(reduce(txt, "ultra", 1, true, true, false, false, false, false)) {
		t.Fatal("converged output larger than a single pass")
	}
}

func TestApplyArrows(t *testing.T) {
	// Multi-word connective becomes a single "->" token.
	if got := applyArrows("cache miss leads to a slow query"); !strings.Contains(got, "->") {
		t.Fatalf("expected arrow in %q", got)
	}
	// Longest phrase wins: "gives rise to" not partially matched.
	got := applyArrows("the change gives rise to errors")
	if strings.Count(got, "->") != 1 {
		t.Fatalf("expected exactly one arrow in %q", got)
	}
}

func TestReduceArrowsOptIn(t *testing.T) {
	in := "A cache miss leads to a slow query which produces a timeout"
	// Off by default: no arrow.
	off := reduce(in, "full", 0, true, false, false, false, false, false)
	if strings.Contains(off, "->") {
		t.Fatalf("arrows must be off by default: %q", off)
	}
	// On: arrow survives the reduction and sits between content words.
	on := reduce(in, "full", 0, true, false, false, false, true, false)
	if !strings.Contains(on, "->") {
		t.Fatalf("expected arrow in reduced output: %q", on)
	}
	// No dangling/leading/trailing arrow.
	if strings.HasPrefix(on, "->") || strings.HasSuffix(strings.TrimSpace(on), "->") ||
		strings.Contains(on, "-> ->") {
		t.Fatalf("dangling arrow in %q", on)
	}
}

func TestArrowPhrasesAreForward(t *testing.T) {
	// Phrases may be single-word (vocab normalize) or multi-word (token save).
	for _, p := range arrowPhrases {
		if strings.TrimSpace(p) == "" {
			t.Errorf("arrow phrase %q is empty", p)
		}
	}
	// Reverse-causal stems name the cause *after* the effect; an arrow there
	// would invert the sentence. Keep them out of the table.
	for _, b := range []string{
		"due to", "because of", "owing to", "as a result of",
		"stems from", "caused by", "arises from", "comes from",
		"originates from", "attributable to", "on account of",
	} {
		in := "the outage " + b + " a bad deploy"
		got := applyArrows(in)
		if got != in {
			t.Errorf("backward %q: applyArrows(%q) = %q, want unchanged", b, in, got)
		}
	}
}

func TestArrowLongestPhraseWins(t *testing.T) {
	// Each long phrase contains a shorter table entry. Go's regexp takes the
	// leftmost alternative, so without the longest-first sort the short branch
	// would match and strand the leading word.
	cases := map[string]string{
		"the retry which results in a stall":  "which",
		"the retry which means that it hangs": "which",
		"a flag which causes a rebuild":       "which",
	}
	for in, stranded := range cases {
		got := applyArrows(in)
		if strings.Count(got, "->") != 1 {
			t.Errorf("applyArrows(%q) = %q, want exactly one arrow", in, got)
		}
		if strings.Contains(got, stranded+" ->") {
			t.Errorf("applyArrows(%q) = %q, stranded %q before the arrow", in, got, stranded)
		}
	}
}

func TestArrowNewPhrasesMatch(t *testing.T) {
	// Spot-check families added beyond the original causal/transform core.
	cases := []string{
		"a refactor paves the way for simpler code",
		"the feature sets the stage for migration",
		"the patch opens the door to a cleaner API",
		"cache misses feed into slow queries",
		"errors cascade into a full outage",
		"the change ushers in a new policy",
		"the bug gives birth to a race",
		"retries end in a timeout",
		"the job winds up as a no-op",
		"run for the purpose of validation",
		"try in an effort to recover",
		"fail, thereby resulting in a retry",
		"fail thus leading to a fallback",
		"step A and subsequently step B",
		"step A which eventually becomes B",
		"phase one is succeeded by phase two",
		"legacy is superseded by the rewrite",
		"the call falls back to the cache",
		"the proxy forwards to upstream",
		"the handler delegates to the worker",
		"the macro desugars to a loop",
		"the AST rewrites to SSA",
		"the expression evaluates to true",
		"the value coerces to a string",
		"the type casts to int",
		"the path aliases to /usr/bin",
		"the route redirects to /home",
		"the schema migrates to v2",
		"the service transitions to draining",
		"the error escalates to a panic",
		"the list flattens to a set",
		"the form normalizes to NFC",
		"the query simplifies to a scan",
		"the graph collapses to a path",
		"the IR is rewritten as bytecode",
		"the type is equivalent to a union",
		"the pipeline bottoms out as a no-op",
		"the type reduces down to any",
		"the feature is replaced by a flag",
		"the enum is mapped to an integer",
		"the value is converted into a string",
		"the token is projected onto a vector",
		"with the aim of reducing cost",
		"with the goal of shipping sooner",
		"in such a way that it fits",
		"following which the job exits",
		"the process grows into a service",
		"the module morphs into a library",
		"the binary propagates to replicas",
		"the layout carries over to mobile",
	}
	for _, in := range cases {
		got := applyArrows(in)
		if !strings.Contains(got, "->") {
			t.Errorf("applyArrows(%q) = %q, want arrow", in, got)
		}
	}
}

func TestArrowSingleWordVocab(t *testing.T) {
	// Single-word connectives normalize to "->" so later reduction sees one form.
	cases := map[string]string{
		"cache miss therefore a retry":   "therefore",
		"cache miss thus a retry":        "thus",
		"cache miss hence a retry":       "hence",
		"errors consequently a rollback": "consequently",
		"deploy accordingly a restart":   "accordingly",
		"step A subsequently step B":     "subsequently",
		"step A eventually step B":       "eventually",
		"step A ultimately step B":       "ultimately",
		"A becomes B":                    "becomes",
		"A yields B":                     "yields",
		"A produces B":                   "produces",
		"A causes B":                     "causes",
		"A triggers B":                   "triggers",
		"A implies B":                    "implies",
		"A entails B":                    "entails",
		"A spawns B":                     "spawns",
		"A generates B":                  "generates",
		"A necessitates B":               "necessitates",
		"A precipitates B":               "precipitates",
		"A enables B":                    "enables",
		"A forces B":                     "forces",
		"A drives B":                     "drives",
		"A facilitates B":                "facilitates",
		"A precedes B":                   "precedes",
		"failure ensues downtime":        "ensues",
		"A whereupon B":                  "whereupon",
		"A thereby B":                    "thereby",
	}
	for in, word := range cases {
		got := applyArrows(in)
		if !strings.Contains(got, "->") {
			t.Errorf("applyArrows(%q) = %q, want arrow for %q", in, got, word)
		}
		if strings.Contains(strings.ToLower(got), word) {
			t.Errorf("applyArrows(%q) = %q, still contains %q", in, got, word)
		}
	}
	// High-frequency function words stay literal (would over-match prose).
	for _, in := range []string{
		"so far so good",
		"if x then y else z",
		"as as as",
	} {
		got := applyArrows(in)
		// "then" is intentionally not in the table
		if in == "if x then y else z" && strings.Contains(got, "->") {
			t.Errorf("applyArrows(%q) = %q, must not rewrite bare then", in, got)
		}
	}
}

func TestArrowSeesPostStageChanges(t *testing.T) {
	// Arrows re-run after every stage so connectives left or revealed mid-pass
	// still become "->". Use multi-letter nouns so reduction does not drop them.
	in := "Alpha causes Bravo and therefore Charlie"
	got := reduce(in, "full", 1, true, false, false, false, true, false)
	if strings.Count(got, "->") < 1 {
		t.Fatalf("expected arrows in reduced output, got %q", got)
	}

	// Filler removes "really"; arrow pass after filler still sees "leads to".
	in = "Alpha really leads to Bravo"
	got = reduce(in, "full", 1, true, false, false, false, true, false)
	if !strings.Contains(got, "->") {
		t.Fatalf("arrows should see post-filler text, got %q", got)
	}

	// Reverse-causal still protected after filler.
	filled := shrinkProse("failure caused by timeout")
	got = applyArrows(filled)
	if got != filled {
		t.Fatalf("reverse causal inverted after filler: applyArrows(%q)=%q", filled, got)
	}

	// Idempotent: arrowing twice matches arrowing once.
	once := applyArrows("Alpha causes Bravo therefore Charlie")
	twice := applyArrows(once)
	if once != twice {
		t.Fatalf("arrows not idempotent: once=%q twice=%q", once, twice)
	}
}

func TestCleanupArrows(t *testing.T) {
	cases := map[string]string{
		"a -> -> b":    "a -> b",
		"a -> -> -> b": "a -> b",
		"-> -> x":      "-> x",
		"x -> ->":      "x ->",
		"->":           "",
		"a -> b -> c":  "a -> b -> c",
	}
	for in, want := range cases {
		got := cleanupArrows(in)
		if got != want {
			t.Errorf("cleanupArrows(%q) = %q, want %q", in, got, want)
		}
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
		"ultra":  {"ultra", false},
		"full":   {"full", false},
	}
	for in, want := range cases {
		b, w := wenyanBaseLevel(in)
		if b != want.base || w != want.wenyan {
			t.Errorf("wenyanBaseLevel(%q) = (%q,%v), want (%q,%v)", in, b, w, want.base, want.wenyan)
		}
	}
}

func TestReduceWenyanSwapsAndKeepsCode(t *testing.T) {
	got := reduce("The wise king studies pkg/x/y.go", "wenyan", 0, true, false, false, false, false, false)
	if !strings.Contains(got, "智") || !strings.Contains(got, "王") {
		t.Fatalf("expected wenyan chars in %q", got)
	}
	if !strings.Contains(got, "pkg/x/y.go") {
		t.Fatalf("path must be preserved verbatim in %q", got)
	}
}

func TestReducePreservesLiterals(t *testing.T) {
	in := "See https://example.com/a/b?q=1 and pkg/agent/loop.go, then run `make build` at version 1.2.3."
	got := reduce(in, "ultra", 0, true, true, true, false, false, false)
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

func TestReducePreservesFileNamesAndPaths(t *testing.T) {
	// Ultra level with every transform on is the most aggressive path; file
	// names, absolute/home paths, fenced + inline code, and CONST_CASE
	// identifiers must all survive verbatim.
	fence := "```go\nfor i := range xs { total += weights[i] }\n```"
	in := "First open main.go and README.md, then read /Users/joel/Projects/turo/shrink.go " +
		"and ~/.claude/CLAUDE.md while CLAUDE_CONFIG_DIR is set. Run `git status` before the block:\n" +
		fence + "\nThat is the whole boring plan you should carefully follow."
	got := reduce(in, "ultra", 0, true, true, true, false, true, false)
	for _, lit := range []string{
		"main.go", "README.md",
		"/Users/joel/Projects/turo/shrink.go", "~/.claude/CLAUDE.md",
		"CLAUDE_CONFIG_DIR", "`git status`", fence,
	} {
		if !strings.Contains(got, lit) {
			t.Fatalf("literal %q not preserved verbatim in:\n%s", lit, got)
		}
	}
	if estimateTokens(got) > estimateTokens(in) {
		t.Fatalf("output larger than input: %d > %d", estimateTokens(got), estimateTokens(in))
	}
}

func TestParseToGraph_UltraLemmaDedup(t *testing.T) {
	// Every inflection of go/fox/run collapses to one token each.
	got := parseToGraph("the fox goes and the fox went and foxes run while it ran", "ultra")
	if got != "fox go run" {
		t.Fatalf("expected lemma-deduped %q, got %q", "fox go run", got)
	}
}
