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
//	turo run <agent>                  launch an agent with requests reduced
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
		arrows      bool
		showVersion bool
	)

	flag.Usage = func() {
		_, _ = fmt.Fprint(flag.CommandLine.Output(), `turo — reduce text to fewer tokens.

Usage:
  turo [flags] [file]         reduce a file (or stdin) to content words
  turo -proxy [flags]         reverse proxy that reduces every LLM request
  turo run <agent> [flags]    launch a coding agent with requests reduced
  turo run                    list run targets and their flags
  turo gain [--history]       report estimated tokens saved so far
  turo -install-agents        register the turo skill with coding agents
  turo -list-agents           list supported coding agents

Flags:
`)
		flag.PrintDefaults()
	}

	flag.StringVar(&level, "level", resolveDefaultLevel(), "compression level: lite, full, ultra, wenyan")
	flag.IntVar(&passes, "passes", 0, "max reduction passes; 0 = run until the output stops changing")
	flag.BoolVar(&filler, "filler", envDefaultOn("TURO_FILLER"), "delete filler/pleasantry/hedge words first (on; disable with -filler=false or TURO_FILLER=off)")
	flag.BoolVar(&synonyms, "synonyms", envDefaultOn("TURO_SYNONYMS"), "replace words with fewer-token synonyms (on; disable with -synonyms=false or TURO_SYNONYMS=off)")
	flag.BoolVar(&gloss, "gloss", envDefaultOn("TURO_GLOSS"), "swap words for the shortest defining word in their dictionary definition (on; disable with -gloss=false or TURO_GLOSS=off)")
	flag.BoolVar(&arrows, "arrows", envDefaultOn("TURO_ARROWS"), "replace multi-word causal/sequential connectives (leads to, results in, gives rise to) with -> (on; disable with -arrows=false or TURO_ARROWS=off)")
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

	// `turo gain [--history]`: report estimated tokens saved across recorded
	// reductions.
	if flag.Arg(0) == "gain" {
		hist := flag.Arg(1) == "--history" || flag.Arg(1) == "-history"
		showGain(hist)
		return
	}

	if !validLevel(level) {
		fmt.Fprintf(os.Stderr, "turo: invalid level %q — use lite, full, ultra, or wenyan\n", level)
		os.Exit(1)
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
			all: *proxyAll, level: level, filler: filler, synonyms: synonyms, gloss: gloss, arrows: arrows,
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
			all: *proxyAll, level: level, filler: filler, synonyms: synonyms, gloss: gloss, arrows: arrows,
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

	out := reduce(input, level, passes, filler, synonyms, gloss, arrows)
	recordGain("reduce", estimateTokens(input), estimateTokens(out))
	fmt.Print(out)
}

// maxConvergePasses caps the "run until fixpoint" mode so a pathological
// synonym cycle can never loop forever.
const maxConvergePasses = 100

// reduce runs the three-stage pipeline (filler -> synonyms -> reduce)
// repeatedly, stopping as soon as a pass no longer changes the output. Later
// passes flatten structure left by earlier ones and dedupe across it, so large
// structured docs keep shrinking for a pass or two before converging. passes>0
// caps the number of iterations; passes<=0 runs to convergence (safety-capped).
func reduce(text, level string, passes int, filler, synonyms, gloss, arrows bool) string {
	// wenyan: reduce at ultra, then swap surviving English words for their
	// 文言 character.
	base, wenyan := wenyanBaseLevel(level)
	level = base

	// Pull URLs, code, paths, and identifiers out before reducing — the
	// pipeline shreds anything with non-letter characters — then re-append them
	// verbatim so they survive intact.
	stripped, literals := protectLiterals(text)

	limit := passes
	if limit <= 0 {
		limit = maxConvergePasses
	}
	out := stripped
	for i := 0; i < limit; i++ {
		step := out
		if arrows {
			step = applyArrows(step) // stage 0: connective phrase -> "->" (before word swaps mangle it)
		}
		if filler {
			step = shrinkProse(step) // stage 1: delete filler/pleasantry/hedge words
		}
		if synonyms {
			step = shortenSynonyms(step) // stage 2: token-cheaper synonym swap
		}
		if gloss {
			step = applyGloss(step) // stage 3: shortest defining-word swap (opt-in)
		}
		step = parseToGraph(step, level) // stage 4: reduce to content words
		if step == out {
			break // fixpoint — further passes cannot help
		}
		out = step
	}

	if arrows {
		out = cleanupArrows(out) // collapse dangling/repeated arrows left by dedup
	}
	if wenyan {
		out = applyWenyan(out) // swap reduced English words for 文言 chars
	}

	if len(literals) == 0 {
		return out
	}
	lits := strings.Join(literals, " ")
	if strings.TrimSpace(out) == "" {
		return lits
	}
	return strings.TrimRight(out, "\n ") + " " + lits
}

// wenyanBaseLevel maps a wenyan level to its base reduction level and reports
// whether the 文言 swap should run. Non-wenyan levels pass through unchanged.
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

// shortenSynonyms replaces each word with a token-cheaper synonym from the
// baked WordNet map. Lossy (WordNet polysemy can shift sense), so it is opt-in
// via --synonyms / TURO_SYNONYMS.
func shortenSynonyms(text string) string { return swapWords(text, shorterSynonym) }

// applyGloss replaces each word with the shortest defining word from its own
// dictionary definition. Very lossy — definitions are prose, not synonyms — so
// it is opt-in via -gloss / TURO_GLOSS and off by default.
func applyGloss(text string) string { return swapWords(text, definitionGloss) }

// arrowPhrases are multi-word causal/sequential/transformation connectives that
// a single "->" token expresses. Every entry is two or more words (>=2 tokens),
// so the swap always saves at least one token — single-token connectives
// (then, thus, becomes) are excluded because "->" costs the same. Ordered
// longest-first so "gives rise to" wins over any shorter overlapping phrase.
//
//nolint:gochecknoglobals // static phrase table for the arrow regex
var arrowPhrases = []string{
	"gives rise to", "give rise to", "gave rise to", "giving rise to",
	"which results in", "which produces", "which yields", "which gives",
	"in order to", "so as to",
	"resulting in", "results in", "result in", "resulted in",
	"leading to", "leads to", "lead to", "led to",
	"translates to", "translate to", "translated to",
	"converts to", "convert to", "converted to",
	"turns into", "turn into", "turned into", "turning into",
	"gives way to", "give way to",
	"maps to", "map to", "mapped to",
	"so that",
}

// reArrow matches any arrow phrase, case-insensitively, with word boundaries.
// Intra-phrase spaces match any run of whitespace so wrapped/reflowed prose
// still matches.
//
//nolint:gochecknoglobals // compiled once from arrowPhrases
var reArrow = buildArrowRegex()

func buildArrowRegex() *regexp.Regexp {
	parts := make([]string, len(arrowPhrases))
	for i, p := range arrowPhrases {
		parts[i] = strings.ReplaceAll(regexp.QuoteMeta(p), " ", `\s+`)
	}
	return regexp.MustCompile(`(?i)\b(?:` + strings.Join(parts, "|") + `)\b`)
}

// reDanglingArrows collapses two or more consecutive arrows (left when the term
// between them is dropped or deduped) into one.
//
//nolint:gochecknoglobals // compiled once
var reDanglingArrows = regexp.MustCompile(`(?:->\s*){2,}`)

// applyArrows replaces multi-word connective phrases with "->". Opt-in via
// -arrows / TURO_ARROWS. The arrow survives the reduction pass because
// extractTermGraph passes "->" tokens through verbatim.
func applyArrows(text string) string { return reArrow.ReplaceAllString(text, " -> ") }

// cleanupArrows removes dangling arrows: repeated runs collapse to one, and a
// leading or trailing arrow (nothing on one side) is dropped.
func cleanupArrows(s string) string {
	s = reDanglingArrows.ReplaceAllString(s, "-> ")
	s = strings.TrimSpace(s)
	s = strings.TrimSpace(strings.TrimPrefix(s, "->"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "->"))
	return strings.TrimSpace(s)
}

// swapWords replaces each alphabetic word with its mapping when the replacement
// shares the word's dictionary part of speech. Non-letter runs (punctuation,
// code symbols, whitespace) pass through so text structure is preserved for the
// reduction stage that follows.
func swapWords(text string, m map[string]string) string {
	var b, word strings.Builder
	flush := func() {
		if word.Len() == 0 {
			return
		}
		w := word.String()
		lw := strings.ToLower(w)
		if s, ok := m[lw]; ok && sameClass(lw, s) {
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
func parseToGraph(text string, level string) string {
	out := extractStructure(text, level)
	if out == "" {
		out = extractTermGraph(text, level)
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

func extractStructure(text string, level string) string {
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
			bodyGraph := extractTermGraph(bodyText, level)
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

// extractTermGraph reduces free-form text to a space-joined stream of
// deduplicated content words in reading order. No arrows, no emoji, no
// repeated nodes — those all cost tokens. Stopwords are dropped; the surviving
// words carry the meaning. ultra additionally drops adjectives and dedupes by
// lemma so "runs", "running", and "ran" all collapse to one token.
func extractTermGraph(text string, level string) string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n' || r == '\t' ||
			r == ' ' || r == ',' || r == ';' || r == ':'
	})

	seen := make(map[string]bool)
	var out []string
	for _, w := range fields {
		if w == "->" { // arrow connective (from applyArrows): keep verbatim, never dedup
			out = append(out, "->")
			continue
		}
		lower := strings.ToLower(strings.Trim(w, ",;:.!?\"'()[]{}\\`*~|<>—–-"))
		lower = strings.ReplaceAll(lower, "'", "")
		if len(lower) < 2 || stopWords[lower] || isAllPunct(lower) {
			continue
		}
		if level == "ultra" && ultraStopWords[lower] {
			continue
		}
		if !keepClass(level, classify(lower)) {
			continue
		}
		key := lower
		if level == "ultra" {
			key = lemma(lower) // collapse inflections in the most aggressive mode
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
