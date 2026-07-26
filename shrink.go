package main

import (
	"regexp"
	"strconv"
	"strings"
)

// Filler / pleasantry / hedge / leader / article patterns, ported from the
// caveman-shrink compressor. These delete words that carry grammar or tone but
// not meaning, while keeping the text as readable prose.
var (
	reFillers      = regexp.MustCompile(`(?i)\b(just|really|basically|actually|simply|quite|very|essentially|literally)\b`)
	rePleasantries = regexp.MustCompile(`(?i)\b(please|kindly|thank you|thanks|sure|certainly|of course|happy to|i'?d be happy)\b[,.]?\s*`)
	reHedges       = regexp.MustCompile(`(?i)\b(perhaps|maybe|might|could potentially|would like to|i think|in my opinion|it seems|it appears)\b\s*`)
	reLeaders      = regexp.MustCompile(`(?im)^(i'?ll|i will|i can|i'?d|you can|we will|we can|let me|let'?s)\s+`)
	reArticles     = regexp.MustCompile(`(?i)\b(a|an|the)\s+`)

	reMultiSpace   = regexp.MustCompile(`[ \t]{2,}`)
	reSpacePunct   = regexp.MustCompile(`\s+([,.;:!?])`)
	reTripleBlank  = regexp.MustCompile(`\n{3,}`)
	reSentenceHead = regexp.MustCompile(`(?:^|[.!?]\s+)([a-z])`)
	// Sentinels wrap their index in NUL bytes, which never occur in prose, so a
	// bare integer in the text (e.g. "keep 0 and 1") can never be mistaken for a
	// restore marker.
	reSentinel = regexp.MustCompile("\x00(\\d+)\x00")
)

// protectedPatterns are swapped out for numeric sentinels before filler
// deletion runs, then restored, so code, paths, URLs, and identifiers are never
// touched. Order matters: broadest (fenced code) first.
var protectedPatterns = []*regexp.Regexp{
	regexp.MustCompile("(?s)```.*?```"),                              // fenced code
	regexp.MustCompile("`[^`\n]+`"),                                  // inline code
	regexp.MustCompile(`(?i)\bhttps?://\S+`),                         // URLs
	regexp.MustCompile(`(?:[/~])?[\w.-]*[/\\][\w./\\-]+`),            // paths (leading / or ~ for absolute/home paths)
	regexp.MustCompile(`\b[A-Z][A-Za-z0-9]*(_[A-Z][A-Za-z0-9]*)+\b`), // CONST_CASE
	regexp.MustCompile(`\b\w+\.\w+(\.\w+)*(\(\))?`),                  // dotted.method / pkg.fn()
	regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*\s*\([^)]*\)`),         // function calls
	regexp.MustCompile(`\b\d+\.\d+\.\d+\b`),                          // version numbers
}

const maxRestorePasses = 8

// protectLiterals replaces every protected segment (code, inline code, URLs,
// paths, CONST_CASE, dotted.calls, version numbers) with a numeric sentinel and
// returns the sentinel-bearing text plus the literals those sentinels stand
// for, indexed by sentinel number. The reduction pipeline mangles anything with
// non-letter characters, so literals ride through as sentinels and
// restoreLiterals puts them back exactly where they were.
func protectLiterals(text string) (stripped string, literals []string) {
	return protectWith(text, protectedPatterns)
}

// protectWith is protectLiterals over an explicit pattern list, so callers that
// protect extra shapes (HTML tags, say) can extend the set without disturbing
// the global one. Repeat occurrences of the same literal share one sentinel
// number, so every occurrence is restored in its own place.
func protectWith(text string, patterns []*regexp.Regexp) (stripped string, literals []string) {
	index := map[string]int{}
	work := text
	for _, re := range patterns {
		work = re.ReplaceAllStringFunc(work, func(m string) string {
			i, ok := index[m]
			if !ok {
				i = len(literals)
				index[m] = i
				literals = append(literals, m)
			}
			return sentinelFor(i)
		})
	}
	return work, literals
}

// sentinelFor renders the placeholder for literal i. NUL bytes never occur in
// prose, so the marker can never collide with a bare integer in the text.
func sentinelFor(i int) string { return "\x00" + strconv.Itoa(i) + "\x00" }

// restoreLiterals puts protected literals back where their sentinels sit. A
// literal whose sentinel did not survive reduction is appended at the end, in
// original order, so nothing is silently dropped; a final sweep guarantees no
// NUL byte ever reaches stdout.
func restoreLiterals(out string, literals []string) string {
	if len(literals) == 0 {
		return stripNUL(out)
	}
	restored := make([]bool, len(literals))
	for pass := 0; pass < maxRestorePasses; pass++ {
		if !reSentinel.MatchString(out) {
			break
		}
		out = reSentinel.ReplaceAllStringFunc(out, func(m string) string {
			i, err := strconv.Atoi(strings.Trim(m, "\x00"))
			if err != nil || i < 0 || i >= len(literals) {
				return "" // unknown marker: drop it rather than leak a NUL
			}
			restored[i] = true
			return literals[i]
		})
	}
	out = stripNUL(out)

	var missing []string
	for i, lit := range literals {
		if !restored[i] {
			missing = append(missing, lit)
		}
	}
	if len(missing) == 0 {
		return out
	}
	tail := strings.Join(missing, " ")
	if strings.TrimSpace(out) == "" {
		return tail
	}
	return strings.TrimRight(out, "\n ") + " " + tail
}

// stripNUL removes any stray NUL byte left by a mangled sentinel.
func stripNUL(s string) string {
	if !strings.ContainsRune(s, 0) {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "")
}

// hasSentinel reports whether a token carries a protected literal, so the
// reduction stages can pass it through untouched.
func hasSentinel(s string) bool { return strings.ContainsRune(s, 0) }

// reFenceBlock matches a whole fenced code block, used by isStructured.
var reFenceBlock = regexp.MustCompile("(?s)```.*?```")

// isStructured reports whether text is dominated by code fences, tables, or
// list/heading markup — layout that prose reduction would scramble. The proxy
// allowlist uses it to pass such content through verbatim; ordinary prose (even
// with the odd bullet) still reduces.
func isStructured(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Fenced code covering half or more of the text -> structured.
	if fences := reFenceBlock.FindAllString(s, -1); fences != nil {
		n := 0
		for _, f := range fences {
			n += len(f)
		}
		if n*2 >= len(s) {
			return true
		}
	}
	nonBlank, structural := 0, 0
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		nonBlank++
		if isStructuralLine(ln, t) {
			structural++
		}
	}
	// Need a few lines, most of them structural, before skipping reduction.
	return nonBlank >= 3 && structural*5 >= nonBlank*3 // >= 60%
}

// isStructuralLine reports whether a single line is markdown table/list/heading
// markup or an indented code line. raw keeps leading whitespace; trimmed is
// non-empty.
func isStructuralLine(raw, trimmed string) bool {
	switch trimmed[0] {
	case '|', '#', '>': // table row, heading, blockquote
		return true
	case '-', '*', '+': // bullet: marker then space
		return len(trimmed) > 1 && trimmed[1] == ' '
	}
	if strings.HasPrefix(raw, "\t") || strings.HasPrefix(raw, "    ") { // indented code
		return true
	}
	i := 0 // numbered list: 1. / 12)
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	return i > 0 && i < len(trimmed) && (trimmed[i] == '.' || trimmed[i] == ')')
}

// shrinkProse deletes filler/pleasantry/hedge/leader/article words while
// protecting code, paths, URLs, and identifiers. It preserves readable prose;
// the reduction stage that follows does the heavier keyword extraction.
func shrinkProse(text string) string {
	if text == "" {
		return text
	}
	working, segs := protectWith(text, protectedPatterns)

	out := compressProse(working)

	// When segs is empty the only sentinels present came from an outer
	// protectLiterals; leave them for that caller to restore.
	if len(segs) == 0 {
		return out
	}
	for pass := 0; pass < maxRestorePasses; pass++ {
		if !reSentinel.MatchString(out) {
			break
		}
		out = reSentinel.ReplaceAllStringFunc(out, func(m string) string {
			i, err := strconv.Atoi(strings.Trim(m, "\x00"))
			if err != nil || i < 0 || i >= len(segs) {
				return m
			}
			return segs[i]
		})
	}
	return out
}

func compressProse(text string) string {
	s := text
	s = reLeaders.ReplaceAllString(s, "")
	s = rePleasantries.ReplaceAllString(s, "")
	s = reHedges.ReplaceAllString(s, "")
	s = reFillers.ReplaceAllString(s, "")
	s = reArticles.ReplaceAllString(s, "")
	s = reMultiSpace.ReplaceAllString(s, " ")
	s = reSpacePunct.ReplaceAllString(s, "$1")
	s = reTripleBlank.ReplaceAllString(s, "\n\n")
	s = reSentenceHead.ReplaceAllStringFunc(s, strings.ToUpper)
	return strings.TrimSpace(s)
}
