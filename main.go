// turo — stream editor that converts text to compact graphs.
//
// Reads prose (CLAUDE.md, README, instructions, any text) from stdin or a file
// and outputs a structural graph — relationships, hierarchy, key terms — without
// the filler. Focus on big picture, not listing every file.
//
// Usage:
//
//	cat CLAUDE.md | turo              stream editor: text → graph
//	turo file.md                      same, from file
//	turo --scan [dir]                 directory graph from kartographer index
//	turo --preamble                   wrap output for system prompt injection
//	turo --preamble --max-depth 4     cap tree depth
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

func main() {
	var (
		scanDir      string
		showPreamble bool
		maxDepth     int
		level        string
	)

	flag.StringVar(&scanDir, "scan", "", "scan directory via kartographer index instead of text input")
	flag.BoolVar(&showPreamble, "preamble", false, "wrap output for system prompt injection")
	flag.IntVar(&maxDepth, "max-depth", 3, "max tree depth for scan mode")
	flag.StringVar(&level, "level", resolveDefaultLevel(), "compression level: lite, full, ultra")
	flag.Parse()

	if level != "lite" && level != "full" && level != "ultra" {
		fmt.Fprintf(os.Stderr, "turo: invalid level %q — use lite, full, or ultra\n", level)
		os.Exit(1)
	}

	var text string
	if scanDir != "" {
		tree, err := scanTree(scanDir, maxDepth)
		if err != nil {
			fmt.Fprintf(os.Stderr, "turo: %v\n", err)
			os.Exit(1)
		}
		text = tree
	} else {
		input, err := readInput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "turo: %v\n", err)
			os.Exit(1)
		}
		text = parseToGraph(input, level)
	}

	if showPreamble {
		text = formatPreamble(text)
	}
	fmt.Print(text)
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

// parseToGraph extracts structure from text at the given compression level.
func parseToGraph(text string, level string) string {
	g := extractStructure(text)
	if g != "" {
		return g
	}
	return extractTermGraph(text, level)
}

// --- structured: headings + file paths ---

type section struct {
	level int
	name  string
	paths []string
}

func extractStructure(text string) string {
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
			fmt.Fprintf(&sb, "%s  → %s\n", indent, p)
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
	"each": true, "every": true, "both": true, "few": true, "more": true,
	"most": true, "some": true, "such": true, "no": true,
	"only": true, "own": true, "same": true, "so": true, "than": true,
	"too": true, "just": true, "about": true, "then": true,
	"our": true, "any": true, "what": true,
	"which": true, "who": true, "how": true, "when": true, "where": true,
}

// Extra stop words filtered at ultra level only.
var ultraStopWords = map[string]bool{
	"all": true, "over": true, "other": true, "very": true,
	"only": true, "just": true, "also": true, "then": true,
}

type word struct {
	text  string
	class string
}

func extractTermGraph(text string, level string) string {
	sentences := strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	})

	var all []word
	for _, sent := range sentences {
		for _, w := range strings.Fields(sent) {
			lower := strings.ToLower(w)
			if len(lower) < 2 {
				continue
			}
			if stopWords[lower] {
				continue
			}
			if level == "ultra" && ultraStopWords[lower] {
				continue
			}
			all = append(all, word{text: lower, class: classify(lower)})
		}
	}

	if len(all) == 0 {
		return ""
	}

	var edges []string
	if level == "lite" {
		// Lite: sequential chain of all content words.
		var prev string
		for _, w := range all {
			if w.class == "adj" || w.class == "noun" || w.class == "verb" || w.class == "other" {
				if prev != "" {
					edges = append(edges, prev+" → "+w.text)
				}
				prev = w.text
			}
		}
	} else {
		// Full/Ultra: kartographer edges (adj→noun, noun→verb, verb→noun).
		var adjBuf []string
		var lastNoun, lastVerb string
		for _, w := range all {
			if w.class != "adj" && w.class != "noun" && w.class != "verb" {
				adjBuf = adjBuf[:0]
				continue
			}
			switch w.class {
			case "adj":
				adjBuf = append(adjBuf, w.text)
			case "noun":
				for _, a := range adjBuf {
					edges = append(edges, a+" → "+w.text)
				}
				adjBuf = adjBuf[:0]
				if lastVerb != "" {
					edges = append(edges, lastVerb+" → "+w.text)
					lastVerb = ""
				}
				lastNoun = w.text
			case "verb":
				if lastNoun != "" {
					edges = append(edges, lastNoun+" → "+w.text)
				}
				lastVerb = w.text
				adjBuf = adjBuf[:0]
			}
		}
		for _, a := range adjBuf {
			if lastNoun != "" {
				edges = append(edges, a+" → "+lastNoun)
			}
		}
		if level == "ultra" && len(edges) > 0 {
			// Deduplicate into single chain.
			seen := make(map[string]bool)
			var chain []string
			for _, e := range edges {
				for _, p := range strings.SplitN(e, " → ", 2) {
					if !seen[p] {
						seen[p] = true
						chain = append(chain, p)
					}
				}
			}
			if len(chain) > 0 {
				return strings.Join(chain, " → ") + "\n"
			}
			return ""
		}
	}

	if len(edges) > 0 {
		return strings.Join(edges, "\n") + "\n"
	}
	return ""
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
