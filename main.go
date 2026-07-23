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
//	turo --preamble                   wrap output for system prompt injection
//	turo --version                    print version
//
// Binary on PATH, detected by kdeps like RTK.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// version is the turo release, overridden at build time via -ldflags.
var version = "dev"

func main() {
	var (
		level       string
		maxDepth    int
		passes      int
		preamble    bool
		synonyms    bool
		filler      bool
		gloss       bool
		showVersion bool
	)

	flag.StringVar(&level, "level", resolveDefaultLevel(), "compression level: lite, full, ultra")
	flag.IntVar(&maxDepth, "max-depth", 0, "max transitive edge depth (0=unlimited)")
	flag.IntVar(&passes, "passes", 0, "max reduction passes; 0 = run until the output stops changing")
	flag.BoolVar(&preamble, "preamble", false, "wrap output in a tagged block for system prompt injection")
	flag.BoolVar(&filler, "filler", envDefaultOn("TURO_FILLER"), "delete filler/pleasantry/hedge words first (on; disable with -filler=false or TURO_FILLER=off)")
	flag.BoolVar(&synonyms, "synonyms", envDefaultOn("TURO_SYNONYMS"), "replace words with fewer-token synonyms (on; disable with -synonyms=false or TURO_SYNONYMS=off)")
	flag.BoolVar(&gloss, "gloss", envTrue("TURO_GLOSS"), "swap words for the shortest defining word in their dictionary definition (very lossy; off)")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println("turo", version)
		return
	}

	if level != "lite" && level != "full" && level != "ultra" {
		fmt.Fprintf(os.Stderr, "turo: invalid level %q — use lite, full, or ultra\n", level)
		os.Exit(1)
	}
	input, err := readInput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "turo: %v\n", err)
		os.Exit(1)
	}

	graph := reduce(input, level, maxDepth, passes, filler, synonyms, gloss)

	if preamble {
		graph = wrapPreamble(graph)
	}
	fmt.Print(graph)
}

// maxConvergePasses caps the "run until fixpoint" mode so a pathological
// synonym cycle can never loop forever.
const maxConvergePasses = 100

// reduce runs the three-stage pipeline (filler -> synonyms -> reduce)
// repeatedly, stopping as soon as a pass no longer changes the output. Later
// passes flatten structure left by earlier ones and dedupe across it, so large
// structured docs keep shrinking for a pass or two before converging. passes>0
// caps the number of iterations; passes<=0 runs to convergence (safety-capped).
func reduce(text, level string, maxDepth, passes int, filler, synonyms, gloss bool) string {
	limit := passes
	if limit <= 0 {
		limit = maxConvergePasses
	}
	out := text
	for i := 0; i < limit; i++ {
		step := out
		if filler {
			step = shrinkProse(step) // stage 1: delete filler/pleasantry/hedge words
		}
		if synonyms {
			step = shortenSynonyms(step) // stage 2: token-cheaper synonym swap
		}
		if gloss {
			step = applyGloss(step) // stage 3: shortest defining-word swap (opt-in)
		}
		step = parseToGraph(step, level, maxDepth) // stage 4: reduce to content words
		if step == out {
			break // fixpoint — further passes cannot help
		}
		out = step
	}
	return out
}

// wrapPreamble wraps reduced text in a tagged block that tells the LLM the
// content is compressed, not prose. Injected verbatim into a system prompt.
func wrapPreamble(graph string) string {
	if strings.TrimSpace(graph) == "" {
		return ""
	}
	graph = strings.TrimRight(graph, "\n")
	return "<context-graph>\n" +
		"# Compressed context: content words only. Articles, prepositions, filler, " +
		"and repeated words are stripped; nouns, verbs, and adjectives are kept. " +
		"Code, paths, and identifiers are verbatim.\n" +
		graph + "\n" +
		"</context-graph>\n"
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

func resolveDefaultLevel() string {
	if l := strings.ToLower(strings.TrimSpace(os.Getenv("TURO_LEVEL"))); l != "" {
		return l
	}
	return "full"
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
func parseToGraph(text string, level string, _ int) string {
	out := extractStructure(text, level)
	if out == "" {
		out = extractTermGraph(text, level)
	}
	if out == "" || estimateTokens(out) >= estimateTokens(text) {
		return text
	}
	return out
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
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		c = append(c, w[:len(w)-3]+"y") // companies->company
	case strings.HasSuffix(w, "ss"):
		// pass, class, process — not a plural; no candidate.
	case strings.HasSuffix(w, "es") && len(w) > 3:
		c = append(c, w[:len(w)-2], w[:len(w)-1]) // boxes->box, goes->go, sees->see
	case strings.HasSuffix(w, "s") && len(w) > 3:
		c = append(c, w[:len(w)-1]) // runs->run, servers->server
	}
	return c
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
	// irregular plurals (plural -> singular)
	"children": "child",
	"men":      "man", "women": "woman",
	"feet": "foot", "teeth": "tooth",
	"mice":   "mouse",
	"people": "person",
	"leaves": "leaf", "lives": "life", "wives": "wife", "knives": "knife",
	"indices": "index", "vertices": "vertex", "matrices": "matrix",
	// irregular comparatives / superlatives (-> base adjective)
	"better": "good", "best": "good",
	"worse": "bad", "worst": "bad",
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
