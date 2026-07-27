// turo — stream editor that reduces text to its content words to cut tokens.
//
// Reads prose (CLAUDE.md, README, instructions, any text) from stdin or a file
// and outputs the meaning-bearing words — nouns, verbs, adjectives —
// deduplicated and in reading order, with all stopwords stripped. Never emits
// something larger than the input.
//
// Usage:
//
//	cat CLAUDE.md | turo              reduce text to content words
//	turo file.md                      same, from file
//	turo -proxy                       reverse proxy that reduces LLM requests
//	turo [flags] run <agent> [args]   launch an agent with requests reduced
//	                                  (turo flags before "run"; agent args after the name)
//	turo gain [--history] [--json]    report estimated tokens saved so far
//	turo discover [--json]            estimate tokens turo could save on your Claude Code history
//	turo --version                    print version
//
// Binary on PATH, detected by kdeps like RTK.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// version is the turo release, overridden at build time via -ldflags.
var version = "dev"

func main() {
	var (
		level       string
		passes      int
		synonyms    bool
		filler      bool
		gloss       bool
		defmatch    bool
		arrows      bool
		markdown    bool
		special     bool
		showVersion bool
	)

	flag.Usage = func() {
		_, _ = fmt.Fprint(flag.CommandLine.Output(), `turo — reduce text to fewer tokens.

Usage:
  turo [flags] [file]              reduce a file (or stdin) to content words
  turo -proxy [flags]              reverse proxy that reduces every LLM request
  turo [flags] run <agent> [args]  launch a coding agent with requests reduced
  turo run                         list run targets and their flags
  turo gain [--history]            report estimated tokens saved so far
  turo discover                    estimate tokens turo could save on your Claude Code history
  turo doctor                      health check: version, settings, paths, agent wiring
  turo -install-agents             register the turo skill with coding agents
  turo -list-agents                list supported coding agents

  turo flags go before "run"; agent args go after the agent name:
    turo -level ultra -proxy-verbose run claude --dangerously-skip-permissions

Flags:
`)
		flag.PrintDefaults()
	}

	flag.StringVar(&level, "level", resolveDefaultLevel(), "compression level: lite, full, ultra, wenyan")
	flag.IntVar(&passes, "passes", 0, "max reduction passes; 0 = run until the output stops changing")
	flag.BoolVar(&filler, "filler", envDefaultOn("TURO_FILLER"), "delete filler/pleasantry/hedge words first (on; disable with -filler=false or TURO_FILLER=off)")
	flag.BoolVar(&synonyms, "synonyms", envDefaultOn("TURO_SYNONYMS"), "replace words with fewer-token synonyms (on; disable with -synonyms=false or TURO_SYNONYMS=off)")
	flag.BoolVar(&gloss, "gloss", envDefaultOn("TURO_GLOSS"), "swap words for the shortest defining word in their dictionary definition (on; disable with -gloss=false or TURO_GLOSS=off)")
	flag.BoolVar(&defmatch, "defmatch", envDefaultOn("TURO_DEFMATCH"), "replace a definition-like phrase with the word it defines (a person who writes professionally -> author) (on; disable with -defmatch=false or TURO_DEFMATCH=off)")
	flag.BoolVar(&arrows, "arrows", envDefaultOn("TURO_ARROWS"), "replace causal/sequential/transformation connectives with -> (multi-word and single-word; re-run after every stage; disable with -arrows=false or TURO_ARROWS=off)")
	flag.BoolVar(&markdown, "markdown", envDefaultOn("TURO_MARKDOWN"), "keep markdown/HTML structure (headings, lists, tables, fences, tags) and reduce only the prose inside it (on; disable with -markdown=false or TURO_MARKDOWN=off)")
	flag.BoolVar(&special, "special", envDefaultOn("TURO_SPECIAL"), "preserve tokens that contain special characters (C++, $5, array[0], 50%, user@host, operators) (on; disable with -special=false or TURO_SPECIAL=off)")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	var installAll bool
	installAgentsFlag := flag.Bool("install-agents", false, "register the turo skill with detected coding agents, then exit")
	flag.BoolVar(&installAll, "all", false, "with -install-agents: register every supported agent, not just detected ones")
	listAgentsFlag := flag.Bool("list-agents", false, "list supported coding agents and whether each is detected, then exit")
	proxyFlag := flag.Bool("proxy", false, "run an OpenAI/Anthropic-compatible reverse proxy that reduces requests")
	listen := flag.String("listen", "127.0.0.1:8787", "with -proxy: address to listen on")
	upstream := flag.String("upstream", envOr("OPENAI_BASE_URL", "https://api.openai.com"), "with -proxy: real LLM base URL")
	proxyAll := flag.Bool("proxy-all", true, "with -proxy/run: reduce every role (default; -proxy-all=false for user + tool only)")
	proxyVerbose := flag.Bool("proxy-verbose", false, "with -proxy/run: print proxy activity (token summary + each message's before -> after text); off = silent")
	proxySafeMode := flag.Bool("proxy-safe-mode", envDefaultOn("TURO_SAFE_MODE"), "with -proxy/run: pass tool-call args and structured text (code, shell dumps, tables, JSON) through unreduced; prose tool results still reduce (on; disable with -proxy-safe-mode=false or TURO_SAFE_MODE=off)")
	flag.Parse()

	if showVersion {
		fmt.Println("turo", version)
		return
	}
	if *listAgentsFlag {
		listAgents()
		return
	}
	if *installAgentsFlag {
		installAgents(installAll)
		return
	}

	// `turo gain [--history] [--json]`: report estimated tokens saved across
	// recorded reductions.
	if flag.Arg(0) == "gain" {
		showGain(hasSubFlag("history"), hasSubFlag("json"))
		return
	}

	// `turo doctor`: health check — version, settings, paths, a reduction
	// self-test, and agent wiring. Runs before the level guard so it can report
	// an invalid level itself rather than exiting with the generic error.
	if flag.Arg(0) == "doctor" {
		showDoctor(proxyConfig{
			all: *proxyAll, level: level, filler: filler, synonyms: synonyms, gloss: gloss, defmatch: defmatch, arrows: arrows, markdown: markdown, special: special, safeMode: *proxySafeMode,
		})
		return
	}

	if !validLevel(level) {
		fmt.Fprintf(os.Stderr, "turo: invalid level %q — use lite, full, ultra, or wenyan\n", level)
		os.Exit(1)
	}

	// `turo discover [--json]`: scan Claude Code history and estimate the tokens
	// turo would have saved on sessions that ran without it.
	if flag.Arg(0) == "discover" {
		showDiscover(proxyConfig{
			all: *proxyAll, level: level, filler: filler, synonyms: synonyms, gloss: gloss, defmatch: defmatch, arrows: arrows, markdown: markdown, special: special, safeMode: *proxySafeMode,
		}, hasSubFlag("json"))
		return
	}

	// `turo run <agent> [args...]`: launch an agent with every request reduced
	// through an in-process proxy.
	if flag.Arg(0) == "run" {
		if flag.NArg() < 2 {
			listRunTargets()
			return
		}
		upstreamSet := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "upstream" {
				upstreamSet = true
			}
		})
		override := ""
		if upstreamSet {
			override = *upstream
		}
		err := runAgent(flag.Arg(1), flag.Args()[2:], override, proxyConfig{
			all: *proxyAll, level: level, filler: filler, synonyms: synonyms, gloss: gloss, defmatch: defmatch, arrows: arrows, markdown: markdown, special: special, safeMode: *proxySafeMode,
			verbose: *proxyVerbose,
		})
		// Print turo's own setup errors; an agent that exits non-zero already
		// reported to its stderr.
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(runExitCode(err))
	}

	if *proxyFlag {
		err := runProxy(proxyConfig{
			listen: *listen, upstream: strings.TrimSuffix(*upstream, "/v1"),
			all: *proxyAll, level: level, filler: filler, synonyms: synonyms, gloss: gloss, defmatch: defmatch, arrows: arrows, markdown: markdown, special: special, safeMode: *proxySafeMode,
			verbose: *proxyVerbose,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "turo proxy: %v\n", err)
			os.Exit(1)
		}
		return
	}

	input, err := readInput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "turo: %v\n", err)
		os.Exit(1)
	}

	out := reduce(input, level, passes, filler, synonyms, gloss, defmatch, arrows, markdown, special)
	recordGain("reduce", estimateTokens(input), estimateTokens(out))
	fmt.Print(out)
}

// maxConvergePasses caps the "run until fixpoint" mode so a pathological
// synonym cycle can never loop forever.
const maxConvergePasses = 100

// reduce runs the reduction pipeline over text. When markdown is on and the
// input looks like markup, it walks the document block by block so headings,
// lists, tables, fences, and HTML survive; otherwise the whole input is treated
// as flat prose.
func reduce(text, level string, passes int, filler, synonyms, gloss, defmatch, arrows, markdown, special bool) string {
	opts := reduceOpts{
		level: level, passes: passes, filler: filler, synonyms: synonyms,
		gloss: gloss, defmatch: defmatch, arrows: arrows, special: special,
	}
	if markdown && looksLikeMarkup(text) {
		return reduceMarkup(text, opts)
	}
	return reduceFlat(text, opts, protectedPatterns)
}

// reduceOpts carries the per-stage switches through the block walker so the
// markup path and the flat path stay in step.
type reduceOpts struct {
	level                                              string
	passes                                             int
	filler, synonyms, gloss, defmatch, arrows, special bool
}

// reduceFlat runs the three-stage pipeline (filler -> synonyms -> reduce)
// repeatedly, stopping as soon as a pass no longer changes the output. Later
// passes flatten structure left by earlier ones and dedupe across it, so large
// structured docs keep shrinking for a pass or two before converging. passes>0
// caps the number of iterations; passes<=0 runs to convergence (safety-capped).
// patterns is the literal-protection set; callers protecting extra shapes (HTML
// tags) pass an extended list.
func reduceFlat(text string, o reduceOpts, patterns []*regexp.Regexp) string {
	// wenyan: reduce at ultra, then swap surviving English words for their
	// 文言 character.
	base, wenyan := wenyanBaseLevel(o.level)
	level := base

	// Pull URLs, code paths and identifiers out before reducing — the
	// pipeline shreds anything with non-letter characters — leave numeric
	// camp; ride the swaps back into place afterwards.
	stripped, literals := protectWith(text, patterns)
	if o.special {
		// Shield $5, C++, array[0], 50%, operators, … so specials survive.
		stripped, literals = protectSpecialTokens(stripped, literals)
	}

	limit := o.passes
	if limit <= 0 {
		limit = maxConvergePasses
	}
	out := stripped
	// Headwords defmatch produces accumulate across later passes so gloss
	// cannot blur what was already resolved.
	headwords := map[string]bool{}

	// arrow re-runs after every mutating stage so connectives introduced or
	// left bare by filler/defmatch/gloss/synonym/reduce still become "->".
	// Longest-match + reverse-tail guards keep this idempotent and safe.
	arrow := func(s string) string {
		if !o.arrows {
			return s
		}
		// cleanup after every apply so re-runs cannot stack "-> -> ->"
		return cleanupArrows(applyArrows(s))
	}

	for i := 0; i < limit; i++ {
		step := out
		step = arrow(step)
		if o.filler {
			step = arrow(shrinkProse(step)) // delete filler/pleasantry/hedge words
		}
		if o.defmatch {
			// Definition-like phrase -> the word it defines. Run ahead of
			// gloss for the same reason as arrows — a match on the
			// definition body would rewrite a keyword ("disorder") and
			// lose the multi-word match ("state of disorder and lawlessness" ->
			// anarchy). Something act drop two stay and
			// draw the window.
			step = arrow(applyDefMatchInto(step, headwords))
		}
		if o.gloss {
			// Shortest defining-word from the
			// dictionary. Fail-soft: touch letting
			// picks land on a token-cheaper full README.
			// Headwords defmatch produced reach either way.
			step = arrow(swapWordsExcept(step, definitionGloss, headwords))
		}
		if o.synonyms {
			// Token-cheaper synonym pass just
			// — re-swap would walk a fresh headword back.
			step = arrow(swapWordsExcept(step, shorterSynonym, headwords))
		}
		step = parseToGraph(step, level, o.special) // content-word reduction
		step = arrow(step)                          // catch anything reduction left bare
		if step == out {
			break // fixpoint — later passes cannot help
		}
		out = step
	}

	if o.arrows {
		out = cleanupArrows(out) // collapse dangling/repeated arrows left by dedup
	}
	if wenyan {
		out = applyWenyan(out) // 文言 chars
	}

	return restoreLiterals(out, literals)
}

func wenyanBaseLevel(level string) (base string, wenyan bool) {
	if level == "wenyan" {
		return "ultra", true
	}
	return level, false
}

// envOr returns the environment variable value, or fallback when unset/empty.
func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

// hasSubFlag reports whether a subcommand arg like --json or --history is present
// among the args after the subcommand, accepting both -flag and --flag spellings
// so `turo gain --json` and `turo discover -json` both work regardless of order.
func hasSubFlag(name string) bool {
	for _, a := range flag.Args()[1:] {
		if a == "--"+name || a == "-"+name {
			return true
		}
	}
	return false
}

// envTrue reports whether an environment variable is set to a truthy value.
func envTrue(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// envDefaultOn returns the default for an on-by-default flag: true unless the
// named environment variable is set to a falsey value.
func envDefaultOn(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// envDefaultOff returns the default for an off-by-default flag: false unless the
// named environment variable is set to a truthy value.
func envDefaultOff(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// shortenSynonyms replaces each word with a token-cheaper synonym from the
// baked WordNet map. Lossy (WordNet polysemy can shift sense), so it is opt-in
// via --synonyms / TURO_SYNONYMS.
func shortenSynonyms(text string) string { return swapWords(text, shorterSynonym) }

// applyGloss replaces each word with the shortest defining word from its own
// dictionary definition. Very lossy — definitions are prose, not synonyms — so
// it is the lossiest stage; disable via -gloss=false / TURO_GLOSS=off.
func applyGloss(text string) string { return swapWords(text, definitionGloss) }

// arrowPhrases are causal/sequential/transformation connectives replaced
// with "->". Multi-word entries always save tokens; single-word entries
// (therefore, thus, becomes, yields, ...) normalize vocabulary even when
// the cl100k cost of "->" matches the word — the arrow then survives
// reduction and composes across stages.
//
// Direction constraint: every phrase must read left-to-right as
// cause/source -> effect/target. Reverse connectives ("due to", "because of",
// "owing to", "as a result of", "stems from") name the cause *after* the
// effect, so an arrow there points the wrong way and invert the sentence —
// they stay out of the table.
//
// Order does not matter here: buildArrowRegex sorts longest-first so the
// longest matching phrase always wins over a shorter one it contains.
//
//nolint:gochecknoglobals // static phrase table for the arrow regex
var arrowPhrases = []string{
	// causal
	"gives rise to",
	"give rise to",
	"gave rise to",
	"giving rise to",
	"brings about",
	"bring about",
	"brought about",
	"bringing about",
	"contributes to",
	"contribute to",
	"contributed to",
	"contributing to",
	"culminates in",
	"culminate in",
	"culminated in",
	"culminating in",
	"culminates with",
	"culminate with",
	"culminated with",
	"which results in",
	"which produces",
	"which yields",
	"which gives",
	"which causes",
	"which cause",
	"which caused",
	"with the result that",
	"which in turn",
	"and therefore",
	"and thus",
	"and hence",
	"which means that",
	"which means",
	"meaning that",
	"resulting in",
	"results in",
	"result in",
	"resulted in",
	"leading to",
	"leads to",
	"lead to",
	"led to",
	"paves the way for",
	"pave the way for",
	"paved the way for",
	"paving the way for",
	"sets the stage for",
	"set the stage for",
	"setting the stage for",
	"opens the door to",
	"open the door to",
	"opened the door to",
	"opening the door to",
	"clears the way for",
	"clear the way for",
	"cleared the way for",
	"makes way for",
	"make way for",
	"made way for",
	"lays the groundwork for",
	"lay the groundwork for",
	"laid the groundwork for",
	"gives birth to",
	"give birth to",
	"gave birth to",
	"giving birth to",
	"ushers in",
	"usher in",
	"ushered in",
	"ushering in",
	"brings on",
	"bring on",
	"brought on",
	"bringing on",
	"feeds into",
	"feed into",
	"fed into",
	"feeding into",
	"flows into",
	"flow into",
	"flowed into",
	"flowing into",
	"cascades into",
	"cascade into",
	"cascaded into",
	"propagates to",
	"propagate to",
	"propagated to",
	"propagating to",
	"carries over to",
	"carry over to",
	"carried over to",
	"ends in",
	"end in",
	"ended in",
	"ending in",
	"ends with",
	"end with",
	"ended with",
	"ending with",
	"winds up as",
	"wind up as",
	"wound up as",
	"winds up in",
	"wind up in",
	"wound up in",
	"concludes with",
	"conclude with",
	"concluded with",
	"concluding with",
	"concludes in",
	"conclude in",
	"concluded in",
	"finishes as",
	"finish as",
	"finished as",
	"gives way to",
	"give way to",
	"gives place to",
	"give place to",
	"gave place to",
	// purpose
	"in order to",
	"so as to",
	"so that",
	"such that",
	"for the purpose of",
	"with the aim of",
	"with the goal of",
	"with a view to",
	"in an effort to",
	"in an attempt to",
	"in order that",
	"with the effect that",
	"with the consequence that",
	"in such a way that",
	"thereby leading to",
	"thereby causing",
	"thereby producing",
	"thereby resulting in",
	"thus leading to",
	"thus causing",
	"thus resulting in",
	"therefore leading to",
	// sequential
	"followed by",
	"and then",
	"after which",
	"at which point",
	"and subsequently",
	"and eventually",
	"and ultimately",
	"and finally",
	"which subsequently",
	"which eventually",
	"which ultimately",
	"which finally",
	"following which",
	"subsequent to which",
	"is succeeded by",
	"is superseded by",
	"is replaced by",
	"is replaced with",
	// transform / target
	"translates to",
	"translate to",
	"translated to",
	"translates into",
	"translate into",
	"translated into",
	"transforms into",
	"transform into",
	"transformed into",
	"transforming into",
	"evolves into",
	"evolve into",
	"evolved into",
	"evolving into",
	"changes into",
	"change into",
	"changed into",
	"converts to",
	"convert to",
	"converted to",
	"turns into",
	"turn into",
	"turned into",
	"turning into",
	"morphs into",
	"morph into",
	"morphed into",
	"develops into",
	"develop into",
	"developed into",
	"grows into",
	"grow into",
	"grew into",
	"degenerates into",
	"degenerate into",
	"degenerated into",
	"compiles to",
	"compile to",
	"compiled to",
	"expands to",
	"expand to",
	"expanded to",
	"resolves to",
	"resolve to",
	"resolved to",
	"reduces to",
	"reduce to",
	"reduced to",
	"reduces down to",
	"reduce down to",
	"reduced down to",
	"collapses to",
	"collapse to",
	"collapsed to",
	"collapses into",
	"collapse into",
	"collapsed into",
	"simplifies to",
	"simplify to",
	"simplified to",
	"flattens to",
	"flatten to",
	"flattened to",
	"normalizes to",
	"normalize to",
	"normalized to",
	"coerces to",
	"coerce to",
	"coerced to",
	"casts to",
	"cast to",
	"evaluates to",
	"evaluate to",
	"evaluated to",
	"parses as",
	"parse as",
	"parsed as",
	"encodes as",
	"encode as",
	"encoded as",
	"encodes to",
	"encode to",
	"encoded to",
	"decodes to",
	"decode to",
	"decoded to",
	"desugars to",
	"desugar to",
	"desugared to",
	"rewrites to",
	"rewrite to",
	"rewritten to",
	"rewrites as",
	"rewrite as",
	"rewritten as",
	"renders as",
	"render as",
	"rendered as",
	"manifests as",
	"manifest as",
	"manifested as",
	"presents as",
	"present as",
	"presented as",
	"corresponds to",
	"correspond to",
	"corresponding to",
	"equates to",
	"equate to",
	"amounts to",
	"amount to",
	"boils down to",
	"bottoms out as",
	"bottom out as",
	"bottoms out at",
	"bottom out at",
	"defaults to",
	"default to",
	"aliases to",
	"alias to",
	"aliased to",
	"redirects to",
	"redirect to",
	"redirected to",
	"proxies to",
	"proxy to",
	"proxied to",
	"forwards to",
	"forward to",
	"forwarded to",
	"delegates to",
	"delegate to",
	"delegated to",
	"falls back to",
	"fall back to",
	"fell back to",
	"falling back to",
	"falls through to",
	"fall through to",
	"escalates to",
	"escalate to",
	"escalated to",
	"migrates to",
	"migrate to",
	"migrated to",
	"transitions to",
	"transition to",
	"transitioned to",
	"switches to",
	"switch to",
	"switched to",
	"upgrades to",
	"upgrade to",
	"upgraded to",
	"downgrades to",
	"downgrade to",
	"downgraded to",
	"maps to",
	"map to",
	"mapped to",
	"points to",
	"point to",
	"is rewritten as",
	"is rewritten to",
	"is expressed as",
	"is equivalent to",
	"is transformed into",
	"is converted into",
	"is converted to",
	"is reduced to",
	"is compiled into",
	"is compiled to",
	"is expanded into",
	"is expanded to",
	"is mapped to",
	"is projected onto",
	"is projected to",
	// single-word (vocab normalize to ->)
	"therefore",
	"thus",
	"hence",
	"consequently",
	"accordingly",
	"ergo",
	"thereafter",
	"subsequently",
	"eventually",
	"ultimately",
	"afterwards",
	"afterward",
	"whereupon",
	"thereby",
	"whereby",
	"thence",
	"becomes",
	"became",
	"becoming",
	"yields",
	"yielded",
	"yielding",
	"produces",
	"produced",
	"producing",
	"cause",
	"causes",
	"caused",
	"causing",
	"triggers",
	"triggered",
	"triggering",
	"prompts",
	"prompted",
	"prompting",
	"induces",
	"induced",
	"inducing",
	"implies",
	"implied",
	"implying",
	"entails",
	"entailed",
	"entailing",
	"necessitates",
	"necessitated",
	"necessitating",
	"precipitates",
	"precipitated",
	"precipitating",
	"spawns",
	"spawned",
	"spawning",
	"generates",
	"generated",
	"generating",
	"ensues",
	"ensued",
	"ensuing",
	"precedes",
	"preceded",
	"preceding",
	"enables",
	"enabled",
	"enabling",
	"allows",
	"allowed",
	"allowing",
	"permits",
	"permitted",
	"permitting",
	"forces",
	"forced",
	"forcing",
	"drives",
	"drove",
	"driven",
	"driving",
	"invites",
	"invited",
	"inviting",
	"encourages",
	"encouraged",
	"encouraging",
	"facilitates",
	"facilitated",
	"facilitating",
	"catalyzes",
	"catalyzed",
	"catalyzing",
	"catalyses",
	"catalysed",
	"catalysing",
	"warrants",
	"warranted",
	"warranting",
	"mandates",
	"mandated",
	"mandating",
	"requires",
	"required",
	"requiring",
}

// reArrow matches any arrow phrase, case-insensitively, with word boundaries.
// Intra-phrase spaces match any run of whitespace so wrapped/reflowed prose
// still matches.
//
//nolint:gochecknoglobals // compiled once from arrowPhrases
var reArrow = buildArrowRegex()

// buildArrowRegex compiles arrowPhrases into one alternation, longest phrase
// first. Go's regexp picks the leftmost alternative that matches at a position,
// not the longest, so "which results in" has to precede "results in" or the
// shorter branch would claim the tail and leave "which" stranded.
func buildArrowRegex() *regexp.Regexp {
	sorted := make([]string, len(arrowPhrases))
	copy(sorted, arrowPhrases)
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(sorted[i]) > len(sorted[j])
	})
	parts := make([]string, len(sorted))
	for i, p := range sorted {
		parts[i] = strings.ReplaceAll(regexp.QuoteMeta(p), " ", `\s+`)
	}
	return regexp.MustCompile(`(?i)\b(?:` + strings.Join(parts, "|") + `)\b`)
}

// reDanglingArrows collapses adjacent arrow runs (left when the term between
// them is dropped or when arrow re-runs stack "-> -> ->").
//
//nolint:gochecknoglobals // compiled once
var reDanglingArrows = regexp.MustCompile(`(?:->\s*){2,}`)

// reArrowDebris collapses arrows separated only by stopword/connective debris
// ("-> and ->", "-> which ->") left after partial reduction. Without this,
// triple-arrow artifacts survive until (or past) the final cleanup.
//
//nolint:gochecknoglobals // compiled once
var reArrowDebris = regexp.MustCompile(`(?i)->(?:\s+(?:and|or|but|which|that|then|also|plus|thus|hence|therefore|so|yet|still|really|just))+\s*->`)

// reverseArrowTail matches a preposition that turns a forward verb into a
// reverse-causal phrase ("caused by", "stems from", "driven by"). Single-word
// table entries must not fire in that context or the arrow would invert.
//
//nolint:gochecknoglobals // compiled once
var reverseArrowTail = regexp.MustCompile(`(?i)^\s+(by|from)\b`)

// applyArrows replaces connective phrases (multi-word and single-word) with
// "->". Opt-in via -arrows / TURO_ARROWS. The arrow survives the reduction
// pass because extractTermGraph treats "->" as a keeper token.
// Matches immediately followed by "by"/"from" are left alone so reverse
// connectives ("caused by", "driven by", "arises from") are not inverted.
func applyArrows(text string) string {
	if text == "" {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	last := 0
	for _, loc := range reArrow.FindAllStringIndex(text, -1) {
		start, end := loc[0], loc[1]
		if reverseArrowTail.MatchString(text[end:]) {
			continue
		}
		b.WriteString(text[last:start])
		b.WriteString("->")
		last = end
	}
	b.WriteString(text[last:])
	return b.String()
}

// reArrowGluedPunct peels "?" / "!" stuck to an arrow after in-place phrase
// rewrite ("defaults to?" -> "->?"). Spaced form is required so parseToGraph
// does not reject "-> ?" as larger than "->?" and pass the glued form through.
var reArrowGluedPunct = regexp.MustCompile(`->([?!]+)`)

// reWordGluedPunct peels "?" / "!" stuck to an alnum token ("a?", "0?") for
// the same reason — otherwise parseToGraph keeps the glued form as "smaller".
var reWordGluedPunct = regexp.MustCompile(`([A-Za-z0-9_]+)([?!]+)`)

// cleanupArrows removes dangling arrows: repeated runs and stopword-only gaps
// collapse to one "->", and a leading or trailing arrow (nothing on one side)
// is dropped. Idempotent — safe to call after every applyArrows.
func cleanupArrows(s string) string {
	if s == "" {
		return s
	}
	// "defaults to?" -> "->?"; "defaults to a?" -> "-> a?" — space the marks.
	s = reArrowGluedPunct.ReplaceAllString(s, "-> $1")
	s = reWordGluedPunct.ReplaceAllString(s, "$1 $2")
	// Collapse adjacent runs ("-> -> ->") and stopword-only gaps ("-> and ->").
	// Loop to fixpoint so mixed debris settles in one call.
	prev := ""
	for s != prev {
		prev = s
		s = reDanglingArrows.ReplaceAllString(s, "-> ")
		s = reArrowDebris.ReplaceAllString(s, "->")
		s = strings.Join(strings.Fields(s), " ")
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	// Drop pure-arrow junk with no content words on either side.
	hasContent := false
	for _, f := range fields {
		if f != "->" {
			hasContent = true
			break
		}
	}
	if !hasContent {
		return ""
	}
	// Collapse any remaining consecutive arrows in the token list.
	// Keep a single leading or trailing "->" when content remains — that is
	// how verb-connectives like "defaults to X" / "compiles to Y" surface
	// after the subject was the connective itself ("-> X").
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == "->" && len(out) > 0 && out[len(out)-1] == "->" {
			continue
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

// swapWords replaces each alphabetic word with its mapping when the replacement
// shares the word's dictionary part of speech. Non-letter runs (punctuation,
// code symbols, whitespace) pass through so text structure is preserved for the
// reduction stage that follows.
func swapWords(text string, m map[string]string) string {
	return swapWordsExcept(text, m, nil)
}

// swapWordsExcept is swapWords with a set of words (lowercased) it must leave
// verbatim, used to protect headwords an earlier stage just produced.
func swapWordsExcept(text string, m map[string]string, skip map[string]bool) string {
	var b, word strings.Builder
	flush := func() {
		if word.Len() == 0 {
			return
		}
		w := word.String()
		lw := strings.ToLower(w)
		if s, ok := m[lw]; ok && !skip[lw] && sameClass(lw, s) {
			b.WriteString(s)
		} else {
			b.WriteString(w)
		}
		word.Reset()
	}
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			word.WriteRune(r)
		} else {
			flush()
			b.WriteRune(r)
		}
	}
	flush()
	return b.String()
}

// sameClass reports whether two words share a known dictionary part of speech.
func sameClass(a, b string) bool {
	return dictKnows(a) && dictKnows(b) && dictClassify(a) == dictClassify(b)
}

// validLevel reports whether s is a recognized compression level, including the
// wenyan variants.
func validLevel(s string) bool {
	switch s {
	case "lite", "full", "ultra", "wenyan":
		return true
	}
	return false
}

func resolveDefaultLevel() string {
	if l := strings.ToLower(strings.TrimSpace(os.Getenv("TURO_LEVEL"))); l != "" {
		return l
	}
	return "ultra"
}

func readInput() (string, error) {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		// Pipe: read stdin
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	// File argument
	if flag.Arg(0) != "" {
		b, err := os.ReadFile(flag.Arg(0))
		return string(b), err
	}
	// No input
	return "", fmt.Errorf("no input — pipe text or provide a file")
}

// parseToGraph reduces text at the given compression level. It never returns
// something larger than the input: if the reduced form does not save tokens
// (estimated), the original text is passed through unchanged.
// special keeps single-letter non-stopword identifiers (x, y, i) so operator
// expressions like "x = y + 2" do not collapse when -special is on.
func parseToGraph(text string, level string, special bool) string {
	out := extractStructure(text, level, special)
	if out == "" {
		out = extractTermGraph(text, level, special)
	}
	if out == "" || !smaller(out, text) {
		return text
	}
	return out
}

// smaller reports whether a is a cheaper representation than b: fewer estimated
// tokens, or — on a token tie — fewer characters. The character tie-break lets
// reductions that shorten a word without changing its token estimate still win
// ("children" -> "child" is 8 -> 5 chars at the same 2-token estimate).
func smaller(a, b string) bool {
	ta, tb := estimateTokens(a), estimateTokens(b)
	if ta != tb {
		return ta < tb
	}
	return len(a) < len(b)
}

// estimateTokens approximates a BPE token count (cl100k-style) without a
// tokenizer. ASCII words cost ~1 token plus one per 5 extra chars; non-ASCII
// runs (emoji, rare unicode) cost roughly one token per rune. Used only to
// decide whether a reduction actually saves tokens.
func estimateTokens(s string) int {
	n := 0
	for _, f := range strings.Fields(s) {
		runes := []rune(f)
		ascii := true
		for _, r := range runes {
			if r > 127 {
				ascii = false
				break
			}
		}
		if ascii {
			n += 1 + len(f)/5
		} else {
			n += len(runes)
		}
	}
	if n == 0 {
		n = 1
	}
	return n
}

// --- structured: headings + file paths ---

type section struct {
	level int
	name  string
	paths []string
	body  []string // non-path body lines
}

func isAllPunct(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func isNoiseLine(line string) bool {
	// Table separators: |---|---|
	if strings.HasPrefix(line, "|---") || strings.HasPrefix(line, "| --") {
		return true
	}
	// Lines that are mostly non-alphanumeric.
	alpha := 0
	for _, r := range line {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			alpha++
		}
	}
	return len(line) > 3 && alpha < len(line)/3
}

func extractStructure(text string, level string, special bool) string {
	scanner := bufio.NewScanner(strings.NewReader(text))
	var sections []section
	var cur *section
	inCode := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		if h, ok := parseHeading(line); ok {
			if cur != nil {
				sections = append(sections, *cur)
			}
			cur = &section{level: h.level, name: h.name}
			continue
		}
		if cur != nil {
			if p := extractPath(line); p != "" {
				cur.paths = append(cur.paths, p)
			} else if line != "" && !strings.HasPrefix(line, "#") && !isNoiseLine(line) {
				clean := strings.NewReplacer("**", "", "__", "", "*", "", "_", "", "`", "").Replace(line)
				cur.body = append(cur.body, clean)
			}
		}
	}
	if cur != nil {
		sections = append(sections, *cur)
	}
	if len(sections) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, s := range sections {
		indent := strings.Repeat("  ", s.level-1)
		fmt.Fprintf(&sb, "%s%s\n", indent, s.name)
		for _, p := range s.paths {
			fmt.Fprintf(&sb, "%s  %s\n", indent, p)
		}
		if len(s.body) > 0 {
			bodyText := strings.Join(s.body, " ")
			bodyGraph := extractTermGraph(bodyText, level, special)
			for _, line := range strings.Split(strings.TrimSpace(bodyGraph), "\n") {
				if line != "" {
					fmt.Fprintf(&sb, "%s  %s\n", indent, line)
				}
			}
		}
	}
	return sb.String()
}

// --- fallback: term co-occurrence graph for free-form text ---

var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"might": true, "can": true, "shall": true, "to": true, "of": true,
	"in": true, "for": true, "on": true, "with": true, "at": true, "by": true,
	"from": true, "as": true, "into": true, "through": true, "during": true,
	"before": true, "after": true, "above": true, "below": true, "between": true,
	"and": true, "but": true, "or": true, "nor": true, "not": true,
	"this": true, "that": true, "these": true, "those": true, "it": true,
	"its": true, "they": true, "them": true, "their": true,
	"you": true, "me": true, "we": true, "us": true, "he": true, "she": true,
	"your": true, "youre": true, "youll": true, "youve": true, "youd": true,
	"im": true, "ive": true, "id": true, "ill": true,
	"hes": true, "shes": true, "heres": true, "theres": true,
	"isnt": true, "arent": true, "wasnt": true, "werent": true,
	"hasnt": true, "havent": true, "hadnt": true, "wont": true,
	"wouldnt": true, "couldnt": true, "shouldnt": true,
	"each": true, "every": true, "both": true, "few": true, "more": true,
	"most": true, "some": true, "such": true, "no": true,
	"too": true, "just": true, "about": true, "then": true,
	"likely": true, "really": true, "actually": true, "basically": true,
	"simply": true, "generally": true, "usually": true, "often": true,
	"always": true, "never": true, "quite": true, "rather": true,
	"our": true, "any": true, "what": true,
	"which": true, "who": true, "how": true, "when": true, "where": true,
}

var ultraStopWords = map[string]bool{
	"all": true, "over": true, "other": true, "very": true,
	"only": true, "just": true, "also": true, "then": true,
	"likely": true, "because": true, "really": true, "actually": true,
	"basically": true, "simply": true, "generally": true, "usually": true,
	"often": true, "always": true, "never": true, "quite": true, "rather": true,
}

// baseForms yields candidate base forms for an inflected word, best first.
// It only removes true inflectional suffixes (-ing, -ed, -s/-es/-ies); it does
// NOT strip derivational suffixes like -er/-or/-tion/-ment, which turn a base
// word into a different word ("render" is not "rend"+er, "server" is not a form
// of "serv"). Each candidate is validated against the dictionary by the caller.
func baseForms(w string) []string {
	var c []string
	switch {
	case strings.HasSuffix(w, "ing") && len(w) > 4:
		root := w[:len(w)-3]
		c = append(c, root+"e", root) // creating->create, using->use, testing->test
		if n := len(root); n >= 2 && root[n-1] == root[n-2] {
			c = append(c, root[:n-1]) // running->run
		}
	case strings.HasSuffix(w, "ed") && len(w) > 3:
		root := w[:len(w)-2]
		c = append(c, root+"e", root) // moved->move, used->use, tested->test
		if n := len(root); n >= 2 && root[n-1] == root[n-2] {
			c = append(c, root[:n-1]) // stopped->stop
		}
	case nonPluralS(w):
		// news, series, physics, analysis, virus — singular nouns that end in
		// s; de-pluralizing them yields the wrong word ("news" -> "new").
		// Checked before the plural cases so "series" is not read as an -ies form.
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		// consonant+ies -> y (companies->company); vowel+ies is just +s
		// (movies->movie), so offer the drop-s form too.
		c = append(c, w[:len(w)-3]+"y", w[:len(w)-1])
	case strings.HasSuffix(w, "ss"):
		// pass, class, process — not a plural; no candidate.
	case strings.HasSuffix(w, "es") && len(w) > 3:
		c = append(c, w[:len(w)-2], w[:len(w)-1]) // boxes->box, goes->go, sees->see
	case strings.HasSuffix(w, "s") && len(w) > 3:
		c = append(c, w[:len(w)-1]) // runs->run, servers->server
	}
	return c
}

// nonPluralNouns are singular nouns ending in s that must not be de-pluralized.
var nonPluralNouns = map[string]bool{
	"news": true, "series": true, "species": true, "means": true,
}

// nonPluralS reports whether a word ending in s is a singular noun, not a
// plural: an explicit exception, or a Latin/Greek singular ending (-us, -is,
// -ics like virus, analysis, physics).
func nonPluralS(w string) bool {
	return nonPluralNouns[w] ||
		strings.HasSuffix(w, "us") ||
		strings.HasSuffix(w, "is") ||
		strings.HasSuffix(w, "ics")
}

// irregularLemma maps irregular inflections to their base form. Suffix
// stemming cannot reach these ("went" -> "go", "children" -> "child"), so they
// are looked up directly. Only forms that carry content survive to this point;
// irregular auxiliaries (be/have/do) are already dropped as stop words.
var irregularLemma = map[string]string{
	// irregular verbs (past / past participle -> base)
	"went": "go", "gone": "go",
	"made": "make",
	"ran":  "run",
	"said": "say",
	"saw":  "see", "seen": "see",
	"took": "take", "taken": "take",
	"got": "get", "gotten": "get",
	"gave": "give", "given": "give",
	"found": "find",
	"wrote": "write", "written": "write",
	"built":   "build",
	"brought": "bring",
	"bought":  "buy",
	"caught":  "catch",
	"taught":  "teach",
	"thought": "think",
	"sought":  "seek",
	"came":    "come",
	"became":  "become",
	"began":   "begin", "begun": "begin",
	"broke": "break", "broken": "break",
	"chose": "choose", "chosen": "choose",
	"drove": "drive", "driven": "drive",
	"fell": "fall", "fallen": "fall",
	"felt": "feel",
	"held": "hold",
	"kept": "keep",
	"knew": "know", "known": "know",
	"led":        "lead",
	"left":       "leave",
	"lost":       "lose",
	"meant":      "mean",
	"met":        "meet",
	"paid":       "pay",
	"sent":       "send",
	"sold":       "sell",
	"spent":      "spend",
	"stood":      "stand",
	"told":       "tell",
	"understood": "understand",
	"won":        "win",
	"grew":       "grow", "grown": "grow",
	"threw": "throw", "thrown": "throw",
	"drew": "draw", "drawn": "draw",
	"ate": "eat", "eaten": "eat",
	"spoke": "speak", "spoken": "speak",
	"rose": "rise", "risen": "rise",
	"shown": "show",
	"flew":  "fly", "flown": "fly",
	"blew": "blow", "blown": "blow",
	"shook": "shake", "shaken": "shake",
	"forgot": "forget", "forgotten": "forget",
	"shot":   "shoot",
	"bound":  "bind",
	"ground": "grind",
	"wound":  "wind",
	"dealt":  "deal",
	"slept":  "sleep",
	"wept":   "weep",
	"crept":  "creep",
	"swept":  "sweep",
	"leapt":  "leap",
	"knelt":  "kneel",
	"dwelt":  "dwell",
	"froze":  "freeze", "frozen": "freeze",
	"tore": "tear", "torn": "tear",
	"wore": "wear", "worn": "wear",
	"bore": "bear", "borne": "bear",
	"swore": "swear", "sworn": "swear",
	"stole": "steal", "stolen": "steal",
	"wove": "weave", "woven": "weave",
	"rode": "ride", "ridden": "ride",
	"hid": "hide", "hidden": "hide",
	"bit": "bite", "bitten": "bite",
	"woke": "wake", "woken": "wake",
	"awoke": "awake",
	"arose": "arise", "arisen": "arise",
	"drove2": "drive", // see drove above
	"swam":   "swim", "swum": "swim",
	"drank": "drink", "drunk": "drink",
	"sank": "sink", "sunk": "sink",
	"rang": "ring", "rung": "ring",
	"sang": "sing", "sung": "sing",
	"sprang": "spring", "sprung": "spring",
	"swung":  "swing",
	"clung":  "cling",
	"stung":  "sting",
	"hung":   "hang",
	"dug":    "dig",
	"spun":   "spin",
	"lit":    "light",
	"fled":   "flee",
	"fed":    "feed",
	"bred":   "breed",
	"sped":   "speed",
	"slew":   "slay",
	"trod":   "tread",
	"shrank": "shrink", "shrunk": "shrink",
	"strove":  "strive",
	"forgave": "forgive", "forgiven": "forgive",
	"forsook": "forsake", "forsaken": "forsake",
	"mistook": "mistake", "mistaken": "mistake",
	"withdrew": "withdraw", "withdrawn": "withdraw",
	"overcame": "overcome",
	// irregular plurals (plural -> singular)
	"children": "child",
	"men":      "man", "women": "woman",
	"feet": "foot", "teeth": "tooth",
	"mice":   "mouse",
	"geese":  "goose",
	"oxen":   "ox",
	"dice":   "die",
	"people": "person",
	"leaves": "leaf", "lives": "life", "wives": "wife", "knives": "knife",
	"halves": "half", "shelves": "shelf", "wolves": "wolf", "calves": "calf",
	"indices": "index", "vertices": "vertex", "matrices": "matrix", "appendices": "appendix",
	// Latin/Greek plurals
	"criteria": "criterion", "phenomena": "phenomenon",
	"cacti": "cactus", "fungi": "fungus", "nuclei": "nucleus", "radii": "radius",
	"alumni": "alumnus", "bacteria": "bacterium", "curricula": "curriculum",
	"memoranda": "memorandum", "stimuli": "stimulus",
	"analyses": "analysis", "crises": "crisis", "theses": "thesis",
	"hypotheses": "hypothesis", "diagnoses": "diagnosis", "parentheses": "parenthesis",
	// irregular comparatives / superlatives (-> base adjective)
	"better": "good", "best": "good",
	"worse": "bad", "worst": "bad",
	"further": "far", "furthest": "far", "farther": "far", "farthest": "far",
}

// lemma reduces a word to its dictionary base form for deduplication so that
// different inflections of the same word collapse to one token. A candidate
// base form is accepted only when the dictionary knows it, so wrong or mangled
// reductions ("render" -> "rend", "pass" -> "pas", "serv") are never emitted.
// When no candidate is a known word, the original surface form is kept. Order:
//  1. irregular table ("went" -> "go", "children" -> "child")
//  2. the first inflectional base form the dictionary recognizes
//     ("creating" -> "create", "servers" -> "server", "sees" -> "see")
//  3. otherwise keep the word unchanged
//
// Used only in the most aggressive level.
func lemma(w string) string {
	if l, ok := irregularLemma[w]; ok {
		return l
	}
	for _, c := range baseForms(w) {
		if c != w && dictKnows(c) {
			return c
		}
	}
	return w
}

// keepClass reports whether a word of the given class survives at a level.
// lite keeps the most (adjectives, nouns, verbs, and leftover adverbs/preps);
// full drops the leftovers; ultra keeps only nouns and verbs.
func keepClass(level, class string) bool {
	switch level {
	case "lite":
		return class == "adj" || class == "noun" || class == "verb" || class == "other"
	case "ultra":
		return class == "noun" || class == "verb"
	default: // full
		return class == "adj" || class == "noun" || class == "verb"
	}
}

// isNumberToken reports whether s is an integer or simple decimal (optional
// leading +/−). Single digits count — "0", "2", "3" are content, not noise.
func isNumberToken(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		if len(s) == 1 {
			return false
		}
		i = 1
	}
	digits, dots := 0, 0
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			dots++
			if dots > 1 {
				return false
			}
		default:
			return false
		}
	}
	return digits > 0
}

// graphFields splits text for the term graph. Whitespace and clause
// punctuation separate tokens; "?" and "!" are kept as their own tokens so
// "defaults to?" becomes "-> ?" instead of a pure-arrow leftover that cleanup
// erases. Glued forms like "->?" (arrow rewrite + trailing punct) are split.
func graphFields(text string) []string {
	var fields []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		fields = append(fields, b.String())
		b.Reset()
	}
	for _, r := range text {
		switch r {
		case ' ', '\t', '\n', '\r', ',', ';', ':', '.':
			flush()
		case '?', '!':
			flush()
			fields = append(fields, string(r))
		default:
			b.WriteRune(r)
		}
	}
	flush()
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		// applyArrows replaces the phrase in place, so "defaults to?" becomes
		// "->?" with the mark stuck on the arrow — peel it off.
		if len(f) > 2 && strings.HasPrefix(f, "->") {
			out = append(out, "->")
			rest := f[2:]
			if rest != "" {
				out = append(out, rest)
			}
			continue
		}
		out = append(out, f)
	}
	return out
}

// extractTermGraph reduces free-form text to a space-joined stream of
// deduplicated content words in reading order. No arrows, no emoji, no
// repeated nodes — those all cost tokens. Stopwords are dropped; the surviving
// words carry the meaning. ultra additionally drops adjectives and dedupes by
// lemma so "runs", "running", and "ran" all collapse to one token.
//
// The token immediately after "->" is always kept (even a single letter or
// stopword): "defaults to a" / "defaults to 0" must surface as "-> a" / "-> 0",
// not collapse to empty after the min-length and stopword filters.
//
// special (from -special, default on): keep single-letter non-stopword
// identifiers so expressions like "x = y + 2" retain their variables once
// operators have been protected as literals.
func extractTermGraph(text string, level string, special bool) string {
	fields := graphFields(text)

	seen := make(map[string]bool)
	var out []string
	afterArrow := false
	for _, w := range fields {
		if w == "->" { // arrow connective (from applyArrows): keep verbatim
			// collapse stacked arrows from mid-pass rewrites ("-> -> ->")
			if len(out) > 0 && out[len(out)-1] == "->" {
				afterArrow = true
				continue
			}
			out = append(out, "->")
			afterArrow = true
			continue
		}
		// Bare ? / ! are content after an arrow rewrite ("defaults to?" -> "-> ?")
		// and meaningful elsewhere (questions/exclamations). Keep one of each.
		if w == "?" || w == "!" {
			if !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
			afterArrow = false
			continue
		}
		if hasSentinel(w) { // protected literal: keep verbatim, never dedup or lemmatize
			out = append(out, w)
			afterArrow = false
			continue
		}
		// Trim wrappers but keep a leading sign on bare numbers ("-1", "+2")
		// so the min-length filter does not see "1" and the sign is not lost.
		trimmed := strings.Trim(w, ",;:.!?\"'()[]{}\\`*~|<>—–")
		if isNumberToken(trimmed) {
			if !seen[trimmed] {
				seen[trimmed] = true
				out = append(out, trimmed)
			}
			afterArrow = false
			continue
		}
		lower := strings.ToLower(strings.Trim(w, ",;:.!?\"'()[]{}\\`*~|<>—–-"))
		lower = strings.ReplaceAll(lower, "'", "")
		rhs := afterArrow
		afterArrow = false
		if lower == "" || isAllPunct(lower) {
			continue
		}
		// Arrow RHS: keep even single-letter / stopword targets ("-> a").
		// Elsewhere: min length 2 drops short noise; with -special, min length
		// 1 keeps identifiers (x, y, i) while stopwords (a) still drop.
		if !rhs {
			minLen := 2
			if special {
				minLen = 1
			}
			if len(lower) < minLen || stopWords[lower] || (level == "ultra" && ultraStopWords[lower]) {
				continue
			}
			if !keepClass(level, classify(lower)) {
				continue
			}
		} else if len(lower) >= 2 && !stopWords[lower] && !(level == "ultra" && ultraStopWords[lower]) {
			// Normal multi-letter content after an arrow still goes through POS.
			if !keepClass(level, classify(lower)) {
				continue
			}
		}
		key := lower
		if !rhs && level == "ultra" {
			key = lemma(lower) // collapse inflections in the most aggressive mode
		} else if rhs && len(lower) >= 2 && level == "ultra" && !stopWords[lower] {
			key = lemma(lower)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, " ")
}

func classify(w string) string { return dictClassify(w) }

func parseHeading(line string) (section, bool) {
	if !strings.HasPrefix(line, "#") {
		return section{}, false
	}
	level := 0
	for _, c := range line {
		if c == '#' {
			level++
		} else {
			break
		}
	}
	name := strings.TrimSpace(line[level:])
	if name == "" || level > 4 {
		return section{}, false
	}
	return section{level: level, name: name}, true
}

func extractPath(line string) string {
	if i := strings.Index(line, "`"); i >= 0 {
		rest := line[i+1:]
		if j := strings.Index(rest, "`"); j > 0 {
			token := rest[:j]
			if strings.Contains(token, "/") || strings.Contains(token, ".") {
				return token
			}
		}
	}
	for _, word := range strings.Fields(line) {
		w := strings.Trim(word, ",;:()[]{}")
		if strings.Contains(w, "/") && !strings.Contains(w, "://") {
			return w
		}
	}
	return ""
}
