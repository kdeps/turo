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
//	turo --preamble                   wrap output for system prompt injection
//	turo --max-depth 4                cap transitive edge depth
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
		preamble    bool
		showVersion bool
	)

	flag.StringVar(&level, "level", resolveDefaultLevel(), "compression level: lite, full, ultra")
	flag.IntVar(&maxDepth, "max-depth", 0, "max transitive edge depth (0=unlimited)")
	flag.BoolVar(&preamble, "preamble", false, "wrap output in a tagged block for system prompt injection")
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
	graph := parseToGraph(input, level, maxDepth)

	if preamble {
		graph = wrapPreamble(graph)
	}
	fmt.Print(graph)
}

// wrapPreamble wraps a graph in a tagged block that tells the LLM the content is
// a compressed structural graph, not prose. Injected verbatim into a system prompt.
func wrapPreamble(graph string) string {
	if strings.TrimSpace(graph) == "" {
		return ""
	}
	graph = strings.TrimRight(graph, "\n")
	return "<context-graph>\n" +
		"# Compressed context. Each line is a directed edge (A → B) or a node. " +
		"Articles, prepositions, and filler are stripped; content words and their " +
		"relationships are preserved. Code, paths, and identifiers are verbatim.\n" +
		graph + "\n" +
		"</context-graph>\n"
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
func parseToGraph(text string, level string, maxDepth int) string {
	g := extractStructure(text, level)
	if g != "" {
		return g
	}
	return extractTermGraph(text, level, maxDepth)
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
			fmt.Fprintf(&sb, "%s  → %s\n", indent, p)
		}
		if len(s.body) > 0 {
			bodyText := strings.Join(s.body, " ")
			bodyGraph := extractTermGraph(bodyText, level, 1)
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

type word struct {
	text  string
	class string
}

var wordEmoji = map[string]string{
	"fox": "🦊", "dog": "🐕", "cat": "🐈", "bird": "🐦", "fish": "🐟",
	"bear": "🐻", "wolf": "🐺", "snake": "🐍", "bug": "🐛",
	"tree": "🌳", "fire": "🔥", "water": "💧", "earth": "🌍", "sun": "☀️",
	"jump": "🦘", "jumps": "🦘", "run": "🏃", "runs": "🏃", "walk": "🚶",
	"move": "➡️", "go": "🚀", "goes": "🚀",
	"quick": "⚡", "fast": "⚡", "slow": "🐢", "lazy": "🦥",
	"big": "🐘", "small": "🔹", "new": "🆕", "old": "📜",
	"high": "📈", "low": "📉", "long": "📏", "short": "📐",
	"red": "🔴", "blue": "🔵", "green": "🟢", "yellow": "🟡", "brown": "🟤", "black": "⚫", "white": "⚪",
	"good": "✅", "bad": "❌", "hot": "🔥", "cold": "🧊",
	"easy": "👍", "hard": "💪", "simple": "✌️", "complex": "🧩",
	"clean": "🧹", "safe": "🛡", "broken": "💔", "best": "🏆",
	"computer": "💻", "server": "🖥", "phone": "📱", "car": "🚗",
	"data": "📊", "file": "📄", "folder": "📁", "code": "💻",
	"api": "🔌", "db": "🗄", "http": "🌐", "url": "🔗",
	"app": "📲", "web": "🌐", "cloud": "☁️", "network": "🌐",
	"test": "🧪", "build": "🏗", "deploy": "🚢", "release": "📦",
	"commit": "💾", "push": "⬆️", "pull": "⬇️", "merge": "🔀",
	"branch": "🌿", "fork": "🍴", "clone": "🐑", "patch": "🩹",
	"fix": "🔧", "add": "➕", "remove": "➖", "create": "✨",
	"delete": "🗑", "update": "🔄", "change": "🔀", "edit": "✏️",
	"find": "🔍", "search": "🔎", "read": "📖", "write": "✍️",
	"save": "💾", "load": "📥", "send": "📤", "fetch": "📥",
	"start": "▶️", "stop": "⏹", "pause": "⏸", "wait": "⏳",
	"check": "✅", "verify": "🔬", "validate": "👍", "confirm": "☑️",
	"handle": "✋", "support": "🏋", "accept": "🤝", "reject": "👎",
	"include": "📎", "contain": "📦", "keep": "📌", "drop": "💧",
	"call": "📞", "return": "↩️", "pass": "➡️", "skip": "⏭",
	"object": "📦", "reference": "🔗", "value": "💎", "type": "🏷",
	"name": "📛", "key": "🔑", "lock": "🔒", "list": "📋",
	"map": "🗺", "set": "📚", "array": "📊", "string": "📝",
	"number": "🔢", "bool": "🔘", "null": "⭕", "error": "⚠️",
	"warning": "⚠️", "info": "ℹ️", "help": "🆘", "question": "❓",
	"time": "🕐", "date": "📅", "year": "📆", "day": "🌅",
	"money": "💰", "price": "💲", "cost": "💸", "free": "🆓",
	"on": "🟢", "off": "🔴", "open": "📂", "close": "📁",
	"first": "🥇", "last": "🏁", "next": "⏩", "prev": "⏪",
	"same": "🟰", "different": "≠",
	"user": "👤", "admin": "👑", "team": "👥",
	"component": "🧩", "function": "ƒ", "class": "📦", "method": "🔧",
	"module": "📦", "package": "📦", "library": "📚",
	"interface": "🔌", "service": "⚙", "client": "💻",
	"database": "🗄", "cache": "💾", "queue": "📥", "log": "📋",
	"config": "⚙", "env": "🌍", "secret": "🤫", "token": "🎟",
	"request": "📨", "response": "📩", "event": "📢", "message": "💬",
	"render": "🎨", "react": "⚛️", "state": "📊", "prop": "📌",
	"hook": "🪝", "effect": "⚡", "memo": "🧠", "callback": "📞",
	"context": "🌐", "router": "🚦", "store": "🏪", "action": "🎬",
	"use": "🔨", "uses": "🔨", "using": "🔨", "make": "🏭", "makes": "🏭",
	"get": "📥", "gets": "📥", "see": "👁", "sees": "👁",
	"reason": "🤔", "cycle": "🔁", "creates": "✨",
	"re-render": "🔄", "rerender": "🔄", "re-rendering": "🔄",
	"usememo": "🧠", "rendering": "🎨", "shallow": "🪞",
	"comparison": "🆚", "inline": "📐", "trigger": "🔫",
	"recommend": "💡", "memoize": "🧠",
}

func wordIcon(w string) string {
	if e, ok := wordEmoji[w]; ok {
		return e
	}
	root := stem(w)
	if root != w {
		if e, ok := wordEmoji[root]; ok {
			return e
		}
	}
	return ""
}

func stem(w string) string {
	doubles := []struct{ suffix, replacement string }{
		{"nning", "n"}, {"pping", "p"}, {"tting", "t"}, {"ssing", "s"},
		{"gging", "g"}, {"mming", "m"}, {"lling", "l"}, {"rring", "r"},
		{"pped", "p"}, {"tted", "t"}, {"ssed", "s"}, {"gged", "g"},
		{"mmed", "m"}, {"nned", "n"}, {"lled", "l"}, {"rred", "r"},
	}
	for _, d := range doubles {
		if strings.HasSuffix(w, d.suffix) {
			return w[:len(w)-len(d.suffix)] + d.replacement
		}
	}
	for _, s := range []string{"ingly", "ment", "ments", "tion", "ness", "able", "ible", "less", "ful", "ish"} {
		if strings.HasSuffix(w, s) && len(w)-len(s) >= 2 {
			return w[:len(w)-len(s)]
		}
	}
	for _, s := range []string{"ing", "ed", "es", "s", "ly", "er", "est", "or"} {
		if strings.HasSuffix(w, s) && len(w)-len(s) >= 2 {
			return w[:len(w)-len(s)]
		}
	}
	return w
}

func extractTermGraph(text string, level string, maxDepth int) string {
	sentences := strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	})

	var all []word
	for _, sent := range sentences {
		for _, w := range strings.Fields(sent) {
			lower := strings.ToLower(strings.Trim(w, ",;:.!?\"'()[]{}\\`*~|<>—–-"))
			lower = strings.ReplaceAll(lower, "'", "")
			if len(lower) < 2 || stopWords[lower] || isAllPunct(lower) {
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
	}

	if len(edges) == 0 {
		return ""
	}
	edges = expandEdges(edges, maxDepth)
	if level == "ultra" {
		return formatUltra(edges)
	}
	return strings.Join(edges, "\n") + "\n"
}

func expandEdges(edges []string, maxDepth int) []string {
	if maxDepth <= 1 {
		return edges
	}
	// Build adjacency: node -> list of nodes it points to.
	next := make(map[string][]string)
	for _, e := range edges {
		parts := strings.SplitN(e, " → ", 2)
		if len(parts) != 2 {
			continue
		}
		a, b := parts[0], parts[1]
		next[a] = append(next[a], b)
	}

	var expanded []string
	for _, e := range edges {
		parts := strings.SplitN(e, " → ", 2)
		if len(parts) != 2 {
			continue
		}
		a, b := parts[0], parts[1]
		path := []string{a, b}
		// Follow chain: B -> C -> D up to maxDepth.
		cur := b
		for depth := 1; depth < maxDepth; depth++ {
			targets := next[cur]
			if len(targets) == 0 {
				break
			}
			// If multiple outgoing, take first; fork into separate edges.
			for i, t := range targets {
				if i == 0 {
					path = append(path, t)
					cur = t
				} else {
					// Fork: new edge from the branch point.
					branch := make([]string, len(path)-1)
					copy(branch, path[:len(path)-1])
					branch = append(branch, t)
					expanded = append(expanded, strings.Join(branch, " → "))
				}
			}
		}
		expanded = append(expanded, strings.Join(path, " → "))
	}
	return expanded
}

func formatUltra(edges []string) string {
	for i, e := range edges {
		parts := strings.SplitN(e, " → ", 2)
		if len(parts) != 2 {
			continue
		}
		a, b := parts[0], parts[1]
		if ea := wordIcon(a); ea != "" {
			a = ea
		}
		if eb := wordIcon(b); eb != "" {
			b = eb
		}
		edges[i] = a + " → " + b
	}
	return strings.Join(edges, "\n") + "\n"
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
