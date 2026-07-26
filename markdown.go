package main

import (
	"regexp"
	"strings"
)

// Markdown / HTML aware reduction. The flat pipeline treats a document as one
// run of prose, which scrambles anything whose meaning lives in its layout:
// heading markers vanish, table pipes are eaten by the term graph, list markers
// and indentation collapse. This walker keeps every structural marker and
// reduces only the prose payload inside it, so the reduced document still
// renders.
var (
	reHeadingLine = regexp.MustCompile(`^(\s*#{1,6}\s+)(.*)$`)
	reListLine    = regexp.MustCompile(`^(\s*(?:[-*+]|\d+[.)])\s+)(.*)$`)
	reQuoteLine   = regexp.MustCompile(`^(\s*>+\s*)(.*)$`)
	// Alignment row of a table: pipes, dashes, colons and spaces only.
	reTableAlign = regexp.MustCompile(`^\s*\|?[\s:|-]*-[\s:|-]*$`)
	// An HTML tag. Protected as a literal so its attributes survive.
	reHTMLTag = regexp.MustCompile(`<[^>\n]+>`)
	// Block-level containers whose contents are verbatim to the last line.
	reHTMLVerbatimOpen = regexp.MustCompile(`(?i)^\s*<(pre|code|script|style)\b`)
	reHTMLLine         = regexp.MustCompile(`^\s*<`)
)

// htmlProtectPatterns protects HTML tags ahead of the ordinary literal set, so
// a tag is taken whole instead of being half-claimed by the path or dotted-call
// pattern inside its attributes.
var htmlProtectPatterns = append([]*regexp.Regexp{reHTMLTag}, protectedPatterns...)

// looksLikeMarkup reports whether text carries markdown or HTML structure worth
// preserving: a fence, a heading, a table row, an HTML tag line, or two or more
// list/blockquote markers. One stray bullet or quoted line is not enough — that
// shape shows up in ordinary prose, which reduces better flat.
func looksLikeMarkup(text string) bool {
	markers := 0
	for _, ln := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		switch {
		case fenceMarker(trimmed) != "",
			reHeadingLine.MatchString(ln),
			isTableRow(trimmed),
			reHTMLLine.MatchString(ln) && reHTMLTag.MatchString(trimmed):
			return true
		}
		// Indented code also passes isStructuralLine; only list and blockquote
		// markers count toward the threshold.
		if reListLine.MatchString(ln) || reQuoteLine.MatchString(ln) {
			if markers++; markers >= 2 {
				return true
			}
		}
	}
	return false
}

// fenceMarker returns the fence delimiter a trimmed line opens with, or "".
func fenceMarker(trimmed string) string {
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return "```"
	case strings.HasPrefix(trimmed, "~~~"):
		return "~~~"
	}
	return ""
}

// isTableRow reports whether a trimmed line is a markdown table row.
func isTableRow(trimmed string) bool {
	return strings.HasPrefix(trimmed, "|") && strings.Count(trimmed, "|") >= 2
}

// isIndentedCode reports whether a line is an indented (four-space or tab) code
// line. Callers must rule out list items first — those are indented too.
func isIndentedCode(line string) bool {
	return strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ")
}

// reduceMarkup walks text block by block, emitting every structural marker
// unchanged and reducing only the prose inside it. Blocks come back in their
// original order, so the document still renders.
func reduceMarkup(text string, o reduceOpts) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	var para []string

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		out = append(out, reduceInline(strings.Join(para, "\n"), o, protectedPatterns))
		para = para[:0]
	}

	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		trimmed := strings.TrimSpace(ln)

		if trimmed == "" {
			flushPara()
			out = append(out, ln)
			continue
		}

		// Fenced code: verbatim through the closing fence (or to the end when
		// the fence is never closed).
		if fence := fenceMarker(trimmed); fence != "" {
			flushPara()
			out = append(out, ln)
			for i++; i < len(lines); i++ {
				out = append(out, lines[i])
				if strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
					break
				}
			}
			continue
		}

		// <pre>/<code>/<script>/<style>: verbatim through the closing tag.
		if m := reHTMLVerbatimOpen.FindStringSubmatch(ln); m != nil {
			flushPara()
			closer := "</" + strings.ToLower(m[1]) + ">"
			out = append(out, ln)
			if !strings.Contains(strings.ToLower(ln), closer) {
				for i++; i < len(lines); i++ {
					out = append(out, lines[i])
					if strings.Contains(strings.ToLower(lines[i]), closer) {
						break
					}
				}
			}
			continue
		}

		if m := reHeadingLine.FindStringSubmatch(ln); m != nil {
			flushPara()
			out = append(out, m[1]+reduceInline(m[2], o, protectedPatterns))
			continue
		}

		if isTableRow(trimmed) {
			flushPara()
			out = append(out, reduceTableRow(ln, o))
			continue
		}

		if m := reQuoteLine.FindStringSubmatch(ln); m != nil {
			flushPara()
			out = append(out, m[1]+reduceInline(m[2], o, protectedPatterns))
			continue
		}

		// Lists are checked before indented code: a four-space-indented bullet
		// is a nested item, not a code line.
		if m := reListLine.FindStringSubmatch(ln); m != nil {
			flushPara()
			out = append(out, m[1]+reduceInline(m[2], o, protectedPatterns))
			continue
		}

		if isIndentedCode(ln) {
			flushPara()
			out = append(out, ln)
			continue
		}

		if reHTMLLine.MatchString(ln) {
			flushPara()
			out = append(out, reduceInline(ln, o, htmlProtectPatterns))
			continue
		}

		para = append(para, ln)
	}
	flushPara()

	return strings.Join(out, "\n")
}

// reduceTableRow reduces each cell of a table row while keeping the row's
// pipes, indentation, and column count. The alignment row passes through
// untouched.
func reduceTableRow(line string, o reduceOpts) string {
	if reTableAlign.MatchString(line) {
		return line
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	body := strings.TrimRight(strings.TrimSpace(line), " ")

	lead, trail := "", ""
	if strings.HasPrefix(body, "|") {
		lead, body = "|", body[1:]
	}
	if strings.HasSuffix(body, "|") {
		trail, body = "|", body[:len(body)-1]
	}

	cells := strings.Split(body, "|")
	for i, cell := range cells {
		red := strings.TrimSpace(reduceInline(strings.TrimSpace(cell), o, protectedPatterns))
		if red == "" {
			continue // an emptied cell keeps its original text, never its column
		}
		cells[i] = " " + red + " "
	}
	return indent + lead + strings.Join(cells, "|") + trail
}

// reduceInline reduces one block's prose to a single line, falling back to the
// original whenever reduction would not save tokens. Keeping the fallback
// per-block is what makes the whole document never larger than its input.
func reduceInline(s string, o reduceOpts, patterns []*regexp.Regexp) string {
	if strings.TrimSpace(s) == "" {
		return s
	}
	red := strings.TrimSpace(strings.ReplaceAll(reduceFlat(s, o, patterns), "\n", " "))
	if red == "" || !smaller(red, s) {
		return s
	}
	return red
}
