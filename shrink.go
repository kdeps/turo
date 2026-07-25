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

// protectLiterals removes every protected segment (code, inline code, URLs,
// paths, CONST_CASE, dotted.calls, version numbers) from the text and returns
// the stripped prose plus the collected literals in first-seen order. The
// reduction pipeline mangles anything with non-letter characters, so these are
// pulled out, the prose is reduced, and the literals are re-appended verbatim.
func protectLiterals(text string) (stripped string, literals []string) {
	seen := map[string]bool{}
	work := text
	for _, re := range protectedPatterns {
		work = re.ReplaceAllStringFunc(work, func(m string) string {
			if !seen[m] {
				seen[m] = true
				literals = append(literals, m)
			}
			return " "
		})
	}
	return work, literals
}

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
	var segs []string
	working := text
	for _, re := range protectedPatterns {
		working = re.ReplaceAllStringFunc(working, func(m string) string {
			i := len(segs)
			segs = append(segs, m)
			return "\x00" + strconv.Itoa(i) + "\x00"
		})
	}

	out := compressProse(working)

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
