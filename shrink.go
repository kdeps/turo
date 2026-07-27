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

// reSpecialToken matches a non-space run. Used by protectSpecialTokens to scan
// candidates; only tokens that actually contain special characters are shielded.
var reSpecialToken = regexp.MustCompile(`\S+`)

// tokenHasSpecial reports whether tok should be protected under -special: after
// stripping common outer sentence punctuation, something non-alphanumeric remains
// (C++, $5.00, array[0], 50%, user@host, =, =>, foo_bar, 72°F, …). Pure words
// and trailing-comma/period noise stay unprotected so they can still reduce.
func tokenHasSpecial(tok string) bool {
	if tok == "" || hasSentinel(tok) {
		return false
	}
	core := strings.Trim(tok, ",.;:!?\"'`")
	if core == "" {
		return false
	}
	for _, r := range core {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return true
	}
	return false
}

// protectSpecialTokens shields whitespace-delimited tokens that contain special
// characters so the reduction pipeline cannot strip brackets, operators, sigils,
// or other non-letter marks. Appends to an existing literals table so prior
// URL/code sentinels stay valid. On by default via -special / TURO_SPECIAL.
func protectSpecialTokens(text string, literals []string) (string, []string) {
	if text == "" {
		return text, literals
	}
	index := map[string]int{}
	for i, lit := range literals {
		index[lit] = i
	}
	work := reSpecialToken.ReplaceAllStringFunc(text, func(m string) string {
		if !tokenHasSpecial(m) {
			return m
		}
		i, ok := index[m]
		if !ok {
			i = len(literals)
			index[m] = i
			literals = append(literals, m)
		}
		return sentinelFor(i)
	})
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

// Machine/shell/code shapes that prose reduction would scramble. Used by
// isStructured so proxy safe mode can pass tool results through when they look
// like command output or source, while still reducing prose tool replies.
var (
	reANSIEscape = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b\][^\x07]*\x07`)
	// Shell / REPL prompts (bash, zsh, fish, PowerShell, Python, Node, SQL, irb).
	reShellPrompt = regexp.MustCompile(`(?m)^(?:` +
		`\$ |# |% |>{1,2} |` +
		`bash-\d[\d.]*[$#] |zsh-\d[\d.]*[$#] |` +
		`\w+@[\w.-]+:[~\w./\-\\]*[#$%] |` +
		`(?:PS )?[A-Za-z]:\\[^\n]*> |` + // C:\path>
		`>>> |\.\.\. |In \[\d+\]: |Out\[\d+\]: |` + // python / ipython
		`node>|irb\(main\):\d+:\d+[>*] |pry\([^)]+\)> |` +
		`(?:mysql|postgres|sqlite|redis|mongo)>\s|` +
		`(?:sql|psql|sqlite3)=\# |\w+=# ` +
		`)`)
	// Stack traces across languages.
	reStackTrace = regexp.MustCompile(`(?m)^(?:` +
		`Traceback \(most recent call last\)|` +
		`panic: |goroutine \d+ \[|` +
		`\s+at [\w.$/<>]+\(|` + // Java / JS "at pkg.Class.method("
		`\s+at [\w./]+:\d+:\d+|` + // Node "at file:line:col"
		`^\s+File "[^"]+", line \d+|` +
		`Caused by: |Exception in thread |` +
		`thread '\w+' panicked at|` + // Rust
		`Unhandled Exception: |` + // .NET
		`#\d+\s+0x[0-9a-fA-F]+ in |` + // lldb/gdb
		`Fatal error: |PHP Fatal error:` +
		`)`)
	reDiffHunk = regexp.MustCompile(`(?m)^(?:--- |\+\+\+ |@@ -\d|diff --git |index [0-9a-f]{7,}\.\.|Binary files .* differ)`)
	reJSONHeavy = regexp.MustCompile(`^\s*[\[{]`)
	// path:line or path:line:col (compilers, linters, ripgrep).
	reFileLineCol = regexp.MustCompile(`(?m)^(?:` +
		`[\w./+\-@]+(?:\.\w+)?:\d+(?::\d+)?(?::\s|:|$)` + `|` +
		`[\w./+\-]+\(\d+(?:,\d+)?\):` + `|` + // MSVC path(line,col):
		`\./[\w./+\-]+:\d+` +
		`)`)
	reLogTimestamp = regexp.MustCompile(`(?m)^(?:` +
		`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}|` +
		`\w{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}|` +
		`\[\d{2}:\d{2}:\d{2}(?:\.\d+)?\]|` +
		`\d{2}:\d{2}:\d{2}(?:\.\d+)?\s+(?:INFO|WARN|ERROR|DEBUG|TRACE|FATAL)|` +
		`(?:INFO|WARN|ERROR|DEBUG|TRACE|FATAL)\s+(?:\[[^\]]+\]\s+)?[\w.]+` +
		`)`)
	reHexDump = regexp.MustCompile(`(?m)^(?:` +
		`[0-9a-fA-F]{4,8}(?::|\s{2,})[0-9a-fA-F]{2}(?:\s[0-9a-fA-F]{2}){7,}|` +
		`0x[0-9a-fA-F]{4,}\s+0x[0-9a-fA-F]+` +
		`)`)
	reBase64Block = regexp.MustCompile(`(?m)^[A-Za-z0-9+/]{40,}={0,2}$`)
	reCompilerDiag = regexp.MustCompile(`(?mi)^(?:` +
		`error(?:\[E\d+\])?:|warning(?:\[E\d+\])?:|note:|` +
		`FAIL\b|PASS\b|ok\s+\S+|--- FAIL:|--- PASS:|=== RUN\s+|=== CONT\s+|` +
		`\# [\w./\-]+|` + // go build package comment
		`\s*✗ |\s*✓ |\s*√ |\s*× |\s*● |` + // jest / vitest / mocha
		`\d+ (?:passing|failing|pending|skipped)|` +
		`Tests?:\s*\d+|Failures?:\s*\d+|` +
		`FAILED \(errors=|===================== .+ =====================` +
		`)`)
	rePathListLine = regexp.MustCompile(`(?m)^(?:` +
		`(?:[dlcbps\-])(?:[r\-][w\-][xsS\-]){3}[.+@]?\s|` + // ls -l including sticky
		`total \d+|` +
		`[0-7]{3,4}\s+\S|` +
		`[├└│─]+ |` + // tree
		`\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}\s+[\d.]+\w?\s+\S` + // ls --time-style
		`)`)
	reShebangOrCode = regexp.MustCompile(`(?m)^(?:` +
		`#!/(?:usr/)?bin/\S+|` +
		`package\s+\w+|func\s+\w+|def\s+\w+|class\s+\w+|async\s+def\s+|` +
		`import\s+[\w{\"']|from\s+\w[\w.]*\s+import|#include\s*[<"]|` +
		`(?:public|private|protected)\s+(?:static\s+)?(?:class|void|int|String)|` +
		`(?:const|let|var)\s+\w+\s*=|` +
		`fn\s+\w+|impl\s+\w+|use\s+[\w:]+::|` + // Rust
		`SELECT\s+.+\s+FROM\s+|INSERT\s+INTO\s+|UPDATE\s+\w+\s+SET\s+|DELETE\s+FROM\s+|` +
		`CREATE\s+(?:TABLE|INDEX|DATABASE)\s+|` +
		`---\s*$|^\.\.\.\s*$` + // YAML doc markers (also matched per-line)
		`)`)
	reCommandEcho = regexp.MustCompile(`(?m)^(?:\+ )?(?:` +
		`cd|ls|cat|head|tail|grep|rg|find|sed|awk|xargs|chmod|chown|rm|cp|mv|mkdir|touch|` +
		`git|gh|svn|hg|` +
		`go|npm|npx|yarn|pnpm|bun|deno|` +
		`cargo|rustc|pip|pip3|poetry|uv|conda|` +
		`python|python3|node|ruby|perl|php|java|javac|mvn|gradle|` +
		`make|cmake|ninja|bazel|` +
		`docker|docker-compose|podman|kubectl|helm|terraform|pulumi|ansible|` +
		`curl|wget|http|ssh|scp|rsync|` +
		`brew|apt|apt-get|yum|dnf|pacman|apk|` +
		`systemctl|journalctl|service|` +
		`ps|top|htop|df|du|free|uname|env|export|which|whereis` +
		`)\s+\S+`)
	// KEY=value env / dotenv dumps (several lines).
	reEnvAssign = regexp.MustCompile(`(?m)^(?:export\s+)?[A-Z][A-Z0-9_]{1,64}=`)
	// HTTP request/response wire dumps.
	reHTTPDump = regexp.MustCompile(`(?mi)^(?:` +
		`(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+/\S*\s+HTTP/|` +
		`HTTP/\d\.\d\s+\d{3}\b|` +
		`(?:Host|User-Agent|Content-Type|Authorization|Accept|Cookie|Location):\s*\S` +
		`)`)
	// HTML/XML tag density helpers.
	reXMLTag = regexp.MustCompile(`</?[A-Za-z][\w:.-]*(?:\s[^>]*)?>`)
	// YAML-ish mapping lines (indent + key: value).
	reYAMLLine = regexp.MustCompile(`(?m)^(?: {2,}|\t+)[\w.-]+:\s+\S`)
	// CSV / TSV: several commas or tabs per line across lines.
	reCSVLine = regexp.MustCompile(`(?m)^[^\n]*,[^\n]*,[^\n]*,`)
	reTSVLine = regexp.MustCompile(`(?m)^[^\n]*\t[^\n]*\t[^\n]*\t`)
	// Git porcelain / status / log.
	reGitPorcelain = regexp.MustCompile(`(?m)^(?:` +
		`(?:M|A|D|R|C|\?\?|!!)\s+\S|` +
		`commit [0-9a-f]{7,}\b|` +
		`Author: |Date:\s+|Merge: |` +
		`Your branch is (?:ahead|behind)|` +
		`On branch |Changes (?:not )?staged for commit|` +
		`Untracked files:|new file:\s+|deleted:\s+|modified:\s+` +
		`)`)
	// Docker / k8s / cloud resource lines.
	reContainerLine = regexp.MustCompile(`(?m)^(?:` +
		`[0-9a-f]{12}\s+\S+|` + // short container id
		`(?:sha256:)?[0-9a-f]{64}\b|` +
		`IMAGE\s+ID|CONTAINER\s+ID|REPOSITORY\s+TAG|` +
		`NAME\s+READY\s+STATUS\s+RESTARTS|` + // kubectl get pods header
		`(?:pod|svc|deploy|ingress)/[\w.-]+\s|` +
		`Created resource |Plan: \d+ to add|` + // terraform
		`Apply complete! |Resources: \d+ added` +
		`)`)
	// Process / system tables.
	reProcTable = regexp.MustCompile(`(?m)^(?:` +
		`PID\s+(?:TTY|USER|PPID)|` +
		`USER\s+PID\s+|` +
		`\d+\s+\d+\s+\d+\.\d+\s+\d+\.\d+\s+\d+\s+\d+\s+|` + // ps aux numeric cols
		`Filesystem\s+Size\s+Used|` +
		`Mem:\s+\d+|Swap:\s+\d+|` +
		`\d+ processes:|\d+ running,` +
		`)`)
	// Progress bars / spinners leftovers.
	reProgressBar = regexp.MustCompile(`(?m)^(?:` +
		`[█▉▊▋▌▍▎▏\-=#]{8,}|` +
		`\d{1,3}%\|[^\n]{5,}\|` + // tqdm
		`Downloading .*\d+%|` +
		`\[\s*\d+%\]` +
		`)`)
	// UUID-heavy or IP:port log noise.
	reUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reIPv4Port = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}:\d{2,5}\b`)
	// SQL result tables / explain.
	reSQLResult = regexp.MustCompile(`(?mi)^(?:` +
		`\s*\|\s*\w+\s*\|\s*\w+\s*\||` +
		`\+[-+]+\+|` + // +---+---+ mysql borders
		`\(\d+ rows?\)|` +
		`EXPLAIN\s|Seq Scan |Index Scan |` +
		`Query OK, \d+ rows?` +
		`)`)
	// Protobuf / thrift-ish / GraphQL operation noise.
	reSchemaIDL = regexp.MustCompile(`(?m)^(?:` +
		`syntax\s*=\s*"proto\d"|message\s+\w+\s*\{|service\s+\w+\s*\{|` +
		`type\s+\w+\s*\{|query\s+\w*\s*[\({]|mutation\s+\w*\s*[\({]|` +
		`enum\s+\w+\s*\{` +
		`)`)
	// Certificate / PEM blocks.
	rePEMBlock = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]+-----`)
	// TOML / INI section headers.
	reINISection = regexp.MustCompile(`(?m)^(?:\[[\w.-]+\]|[A-Za-z0-9_.-]+\s*=\s*(?:"[^"]*"|\S+))\s*$`)
)

// isSafeProtectedLine reports whether a single non-empty line must stay
// verbatim under proxy safe mode: machine dumps, tables, fences, stack/diff
// frames. List/heading lines are NOT protected here so their prose can still
// reduce via the normal (markdown-aware) pipeline when they form a run.
func isSafeProtectedLine(raw, trimmed string) bool {
	if fenceMarker(trimmed) != "" {
		return true
	}
	if isTableRow(trimmed) || reTableAlign.MatchString(trimmed) {
		return true
	}
	if isMachineLine(trimmed) {
		return true
	}
	if reStackTrace.MatchString(trimmed) || reDiffHunk.MatchString(trimmed) {
		return true
	}
	if reANSIEscape.MatchString(trimmed) {
		return true
	}
	// Indented code (not a nested list item).
	if isIndentedCode(raw) && !reListLine.MatchString(raw) {
		return true
	}
	return false
}

// reduceAroundProtected walks text under proxy safe mode: fenced code, tables,
// and machine/shell/log lines pass through verbatim, while contiguous prose
// runs still reduce. Mixed blobs (intro + dump + outro) therefore save tokens
// without scrambling the protected regions.
//
// red is invoked only on prose runs; pass is invoked on each protected span so
// gain accounting still counts those tokens on both sides.
func reduceAroundProtected(text string, role string, red func(role, s string) string, pass func(string)) string {
	if text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	var prose []string

	flushProse := func() {
		if len(prose) == 0 {
			return
		}
		joined := strings.Join(prose, "\n")
		out = append(out, red(role, joined))
		prose = prose[:0]
	}
	// emitPass appends a protected multi-line span and counts it as unreduced.
	emitPass := func(block []string) {
		if len(block) == 0 {
			return
		}
		flushProse()
		joined := strings.Join(block, "\n")
		pass(joined)
		out = append(out, joined)
	}

	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		trimmed := strings.TrimSpace(ln)

		if trimmed == "" {
			// Blank lines separate runs; keep them outside both red and pass
			// so token accounting is not double-counted on empty fields.
			flushProse()
			out = append(out, ln)
			continue
		}

		// Fenced code: verbatim through the closing fence (or EOF).
		if fence := fenceMarker(trimmed); fence != "" {
			block := []string{ln}
			for i++; i < len(lines); i++ {
				block = append(block, lines[i])
				if strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
					break
				}
			}
			emitPass(block)
			continue
		}

		// PEM / certificate blocks through the END line.
		if rePEMBlock.MatchString(trimmed) {
			block := []string{ln}
			for i++; i < len(lines); i++ {
				block = append(block, lines[i])
				if strings.Contains(lines[i], "-----END ") {
					break
				}
			}
			emitPass(block)
			continue
		}

		// Contiguous protected lines (tables, shell, stack frames, …).
		if isSafeProtectedLine(ln, trimmed) {
			block := []string{ln}
			for i+1 < len(lines) {
				next := lines[i+1]
				nt := strings.TrimSpace(next)
				if nt == "" {
					// Keep internal blank lines inside a protected dump so
					// shell transcripts with blank separators stay whole.
					// Stop if the blank is followed by non-protected prose.
					if i+2 < len(lines) {
						after := strings.TrimSpace(lines[i+2])
						if after != "" && !isSafeProtectedLine(lines[i+2], after) && fenceMarker(after) == "" {
							break
						}
					}
					block = append(block, next)
					i++
					continue
				}
				if fenceMarker(nt) != "" || rePEMBlock.MatchString(nt) {
					break // let the outer loop handle the next block type
				}
				if !isSafeProtectedLine(next, nt) {
					break
				}
				block = append(block, next)
				i++
			}
			emitPass(block)
			continue
		}

		prose = append(prose, ln)
	}
	flushProse()
	return strings.Join(out, "\n")
}

// isStructured reports whether text is dominated by layout or machine output
// that prose reduction would scramble: code fences, tables, lists, shell
// dumps, stack traces, JSON, diffs, build logs, and similar. Proxy safe mode
// uses this to choose reduceAroundProtected (prose still shrinks around
// protected regions) rather than a flat reduce that would mangle the dump.
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
	// Strong single-signal machine shapes (need not be multi-line majority).
	if isMachineOutput(s) {
		return true
	}
	nonBlank, structural := 0, 0
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		nonBlank++
		if isStructuralLine(ln, t) || isMachineLine(t) {
			structural++
		}
	}
	// Need a few lines, most of them structural/machine, before skipping.
	return nonBlank >= 3 && structural*5 >= nonBlank*3 // >= 60%
}

// isMachineOutput reports strong whole-blob signals of non-prose content.
func isMachineOutput(s string) bool {
	if reANSIEscape.MatchString(s) || rePEMBlock.MatchString(s) {
		return true
	}
	// Mostly JSON / JSONL.
	if reJSONHeavy.MatchString(s) && looksLikeJSON(s) {
		return true
	}
	// Stack traces, panics, diffs: any clear hit is enough.
	if reStackTrace.MatchString(s) || reDiffHunk.MatchString(s) {
		return true
	}
	if reHTTPDump.MatchString(s) && len(reHTTPDump.FindAllStringIndex(s, -1)) >= 2 {
		return true
	}
	if reSchemaIDL.MatchString(s) {
		return true
	}
	// Shell / REPL transcript.
	if n := len(reShellPrompt.FindAllStringIndex(s, -1)); n >= 1 && strings.Count(s, "\n") >= 1 {
		return true
	}
	if n := len(reCommandEcho.FindAllStringIndex(s, -1)); n >= 3 {
		return true
	}
	// Build/test/compiler dumps.
	if n := len(reCompilerDiag.FindAllStringIndex(s, -1)); n >= 3 {
		return true
	}
	if n := len(reFileLineCol.FindAllStringIndex(s, -1)); n >= 2 {
		return true
	}
	if n := len(reGitPorcelain.FindAllStringIndex(s, -1)); n >= 2 {
		return true
	}
	if n := len(reContainerLine.FindAllStringIndex(s, -1)); n >= 2 {
		return true
	}
	if n := len(reProcTable.FindAllStringIndex(s, -1)); n >= 1 && strings.Count(s, "\n") >= 2 {
		return true
	}
	if n := len(reSQLResult.FindAllStringIndex(s, -1)); n >= 2 {
		return true
	}
	if n := len(reEnvAssign.FindAllStringIndex(s, -1)); n >= 5 {
		return true
	}
	if n := len(reYAMLLine.FindAllStringIndex(s, -1)); n >= 5 {
		return true
	}
	if n := len(reINISection.FindAllStringIndex(s, -1)); n >= 5 {
		return true
	}
	if n := len(reCSVLine.FindAllStringIndex(s, -1)); n >= 3 {
		return true
	}
	if n := len(reTSVLine.FindAllStringIndex(s, -1)); n >= 3 {
		return true
	}
	if n := len(reBase64Block.FindAllStringIndex(s, -1)); n >= 2 {
		return true
	}
	if n := len(reProgressBar.FindAllStringIndex(s, -1)); n >= 2 {
		return true
	}
	// UUID / IP:port dense logs.
	if n := len(reUUID.FindAllStringIndex(s, -1)); n >= 4 {
		return true
	}
	if n := len(reIPv4Port.FindAllStringIndex(s, -1)); n >= 4 {
		return true
	}
	// HTML/XML: many tags relative to length.
	if tags := reXMLTag.FindAllStringIndex(s, -1); len(tags) >= 5 {
		tagChars := 0
		for _, loc := range tags {
			tagChars += loc[1] - loc[0]
		}
		if tagChars*3 >= len(s) { // tags ≥ ~1/3 of bytes
			return true
		}
	}
	// High non-letter density (hex dumps, binary-ish, minified).
	if denseNonLetters(s) {
		return true
	}
	if reHexDump.MatchString(s) || (rePathListLine.MatchString(s) && strings.Count(s, "\n") >= 2) {
		return true
	}
	// Source-like blobs spanning multiple lines.
	if n := len(reShebangOrCode.FindAllStringIndex(s, -1)); n >= 2 && strings.Count(s, "\n") >= 2 {
		return true
	}
	// Log lines with timestamps dominate.
	if n := len(reLogTimestamp.FindAllStringIndex(s, -1)); n >= 4 {
		return true
	}
	return false
}

// isMachineLine reports whether a single non-empty line looks like shell, log,
// path listing, or code rather than prose.
func isMachineLine(trimmed string) bool {
	switch {
	case reShellPrompt.MatchString(trimmed),
		reCommandEcho.MatchString(trimmed),
		reFileLineCol.MatchString(trimmed),
		reLogTimestamp.MatchString(trimmed),
		reCompilerDiag.MatchString(trimmed),
		rePathListLine.MatchString(trimmed),
		reShebangOrCode.MatchString(trimmed),
		reHexDump.MatchString(trimmed),
		reBase64Block.MatchString(trimmed),
		reEnvAssign.MatchString(trimmed),
		reHTTPDump.MatchString(trimmed),
		reYAMLLine.MatchString(trimmed),
		reGitPorcelain.MatchString(trimmed),
		reContainerLine.MatchString(trimmed),
		reProcTable.MatchString(trimmed),
		reProgressBar.MatchString(trimmed),
		reSQLResult.MatchString(trimmed),
		reSchemaIDL.MatchString(trimmed),
		rePEMBlock.MatchString(trimmed),
		reINISection.MatchString(trimmed),
		reCSVLine.MatchString(trimmed),
		reTSVLine.MatchString(trimmed):
		return true
	}
	// JSON line or key-ish assignment dumps.
	if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) &&
		(strings.Contains(trimmed, `"`) || strings.Contains(trimmed, ":")) {
		return true
	}
	// XML/HTML open/close on its own line.
	if reXMLTag.MatchString(trimmed) && len(reXMLTag.FindAllString(trimmed, -1)) >= 1 &&
		len(trimmed) < 200 && strings.Count(trimmed, " ") < 8 {
		return true
	}
	return false
}

// looksLikeJSON reports a blob that is predominantly JSON rather than prose
// that merely starts with "{" (e.g. "{see docs}").
func looksLikeJSON(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return false
	}
	// Balanced-ish braces/brackets and many quotes/colons.
	quotes, colons := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			quotes++
		case ':':
			colons++
		}
	}
	if quotes >= 4 && colons >= 2 {
		return true
	}
	// JSONL: several lines each starting with { or [
	lines, jsonLines := 0, 0
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		lines++
		if t[0] == '{' || t[0] == '[' {
			jsonLines++
		}
	}
	return lines >= 2 && jsonLines*2 >= lines
}

// denseNonLetters reports text where letters are a minority of non-space runes
// (minified JSON, hex, base64-ish, heavy punctuation code).
func denseNonLetters(s string) bool {
	letters, other, space := 0, 0, 0
	for _, r := range s {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			space++
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			letters++
		default:
			other++
		}
	}
	n := letters + other
	if n < 40 {
		return false
	}
	return other*2 >= n // ≥50% non-letters among non-space
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

// stripArticles drops a/an/the, except when the article is the sole (or
// punct-only) right-hand side of an arrow: "-> a" / "-> a ?" must keep "a"
// (from "defaults to a" / "defaults to a?"), while "-> a timeout" still drops
// it so the real noun remains.
func stripArticles(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for _, loc := range reArticles.FindAllStringIndex(s, -1) {
		start, end := loc[0], loc[1]
		b.WriteString(s[last:start])
		if keepArticleAfterArrow(s, start, end) {
			b.WriteString(s[start:end])
		}
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

func keepArticleAfterArrow(s string, start, end int) bool {
	// Must sit right after "->" or "-> ".
	afterArrow := false
	if start >= 3 && s[start-3:start] == "-> " {
		afterArrow = true
	} else if start >= 2 && s[start-2:start] == "->" {
		afterArrow = true
	}
	if !afterArrow {
		return false
	}
	rest := strings.TrimSpace(s[end:])
	if rest == "" {
		return true
	}
	// Keep when only ? / ! (or more of them) follow — no content word yet.
	for _, r := range rest {
		if r == '?' || r == '!' {
			continue
		}
		if r == ' ' || r == '\t' {
			continue
		}
		return false
	}
	return true
}

func compressProse(text string) string {
	s := text
	s = reLeaders.ReplaceAllString(s, "")
	s = rePleasantries.ReplaceAllString(s, "")
	s = reHedges.ReplaceAllString(s, "")
	s = reFillers.ReplaceAllString(s, "")
	s = stripArticles(s)
	s = reMultiSpace.ReplaceAllString(s, " ")
	s = reSpacePunct.ReplaceAllString(s, "$1")
	s = reTripleBlank.ReplaceAllString(s, "\n\n")
	s = reSentenceHead.ReplaceAllStringFunc(s, strings.ToUpper)
	return strings.TrimSpace(s)
}
