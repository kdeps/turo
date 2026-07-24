package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// discover scans Claude Code's session history and estimates how many tokens
// turo would have saved had those requests gone through the proxy. Unlike `gain`
// — which totals reductions that actually happened — `discover` reports the
// missed opportunity: savings still on the table in sessions that ran without
// turo.

// claudeProjectsDir returns the directory holding Claude Code's per-project
// session logs. Order, most specific first:
//  1. $TURO_DISCOVER_DIR             (explicit override, used by tests)
//  2. $CLAUDE_CONFIG_DIR/projects    (Claude Code's configured home)
//  3. ~/.claude/projects             (default install location)
func claudeProjectsDir() string {
	if d := os.Getenv("TURO_DISCOVER_DIR"); d != "" {
		return expandPath(d)
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(expandPath(d), "projects")
	}
	return filepath.Join(home(), ".claude", "projects")
}

// piece is one reducible chunk of a message, tagged with the role turo's proxy
// would see so the same role gating (shouldReduce) applies here as on the wire.
type piece struct {
	role string
	text string
}

// histLine is the subset of a Claude Code history record turo cares about: the
// record type, the folder it ran in, and the message content.
type histLine struct {
	Type    string `json:"type"` // user | assistant | mode | attachment | ...
	Cwd     string `json:"cwd"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"` // string, or an array of blocks
	} `json:"message"`
}

// histBlock is one content block inside a message. Anthropic content is either a
// bare string or an array of these; tool_result blocks nest their own content.
type histBlock struct {
	Type    string          `json:"type"` // text | tool_use | tool_result | image | ...
	Text    string          `json:"text"`
	Content json.RawMessage `json:"content"` // tool_result payload: string or blocks
}

// messagePieces extracts the reducible text from a message's content, tagging
// each piece with the role the proxy would reduce it under. Plain text takes the
// message role; tool_result text counts as "tool" (matching shouldReduce), so a
// default scan reduces user prose and tool output but leaves assistant history
// alone. tool_use input and non-text blocks (images) are skipped — they are not
// prose turo compresses.
func messagePieces(role string, raw json.RawMessage) []piece {
	if len(raw) == 0 {
		return nil
	}
	if s, ok := decodeString(raw); ok {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return []piece{{role, s}}
	}
	var blocks []histBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []piece
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				out = append(out, piece{role, b.Text})
			}
		case "tool_result":
			out = append(out, toolResultPieces(b.Content)...)
		}
	}
	return out
}

// toolResultPieces pulls text out of a tool_result's nested content, which may
// itself be a string or an array of text blocks.
func toolResultPieces(raw json.RawMessage) []piece {
	if len(raw) == 0 {
		return nil
	}
	if s, ok := decodeString(raw); ok {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return []piece{{"tool", s}}
	}
	var blocks []histBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []piece
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			out = append(out, piece{"tool", b.Text})
		}
	}
	return out
}

// decodeString reports whether raw is a JSON string and returns its value.
func decodeString(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	return "", false
}

// findSessionLogs returns every *.jsonl file under dir. Non-conversation logs
// contribute no reducible messages and fall out of the totals naturally, so no
// filtering beyond the extension is needed.
func findSessionLogs(dir string) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip it, keep scanning the rest
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// scanSession reduces every eligible message in one history file and returns the
// estimated before/after token totals, the count of reduced messages, and the
// working folder the session ran in (from the first record that names one).
func scanSession(path string, cfg proxyConfig) (before, after, msgs int, cwd string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, ""
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	// History lines embed whole tool outputs and file contents, so a line can be
	// far larger than the default 64 KiB. Allow up to 32 MiB before giving up.
	sc.Buffer(make([]byte, 0, 64*1024), 32<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var hl histLine
		if json.Unmarshal([]byte(line), &hl) != nil {
			continue
		}
		if cwd == "" && hl.Cwd != "" {
			cwd = hl.Cwd
		}
		if hl.Type != "user" && hl.Type != "assistant" {
			continue
		}
		role := hl.Message.Role
		if role == "" {
			role = hl.Type
		}
		for _, p := range messagePieces(role, hl.Message.Content) {
			if !shouldReduce(p.role, cfg.all) {
				continue
			}
			red := reduce(p.text, cfg.level, 0, cfg.filler, cfg.synonyms, cfg.gloss, cfg.arrows)
			before += estimateTokens(p.text)
			after += estimateTokens(red)
			msgs++
		}
	}
	_ = sc.Err() // a too-long line stops this file early; totals so far still count
	return before, after, msgs, cwd
}

// sessionResult is one file's contribution to the discover totals.
type sessionResult struct {
	before, after, msgs int
	cwd, path           string
}

// projectSummary is one project's missed savings, flattened for JSON output.
type projectSummary struct {
	Dir        string `json:"dir"`
	Messages   int    `json:"messages"`
	TokensIn   int    `json:"tokens_in"`
	WouldBeOut int    `json:"would_be_out"`
	WouldSave  int    `json:"would_save"`
	SavedPct   int    `json:"saved_pct"`
}

// discoverSummary is the machine-readable form of `turo discover`, emitted by
// --json.
type discoverSummary struct {
	Sessions   int              `json:"sessions"`
	Messages   int              `json:"messages"`
	Roles      string           `json:"roles"`
	TokensIn   int              `json:"tokens_in"`
	WouldBeOut int              `json:"would_be_out"`
	WouldSave  int              `json:"would_save"`
	SavedPct   int              `json:"saved_pct"`
	ByProject  []projectSummary `json:"by_project,omitempty"`
}

// aggregateDiscover folds per-file scan results into project totals, ordered by
// tokens saved (descending). Pure over its input so it can be tested without
// touching the filesystem. Files that reduced nothing (msgs == 0) drop out.
func aggregateDiscover(results []sessionResult) (sessions int, order []string, stats map[string]*folderStat) {
	stats = map[string]*folderStat{}
	for _, r := range results {
		if r.msgs == 0 {
			continue
		}
		sessions++
		cwd := r.cwd
		if cwd == "" {
			cwd = filepath.Base(filepath.Dir(r.path))
		}
		st, ok := stats[cwd]
		if !ok {
			st = &folderStat{dir: cwd}
			stats[cwd] = st
			order = append(order, cwd)
		}
		st.n += r.msgs
		st.before += r.before
		st.after += r.after
	}
	sortFoldersBySaved(order, stats)
	return sessions, order, stats
}

// scanAll reduces every session file, spreading the work across CPU cores —
// reduction is CPU-bound and each file is independent, so a whole history of
// hundreds of sessions finishes in a fraction of the single-threaded time.
// Results keep file order so the per-project breakdown is deterministic. When
// stderr is a terminal, a live counter shows progress on the long scans.
func scanAll(files []string, cfg proxyConfig) []sessionResult {
	results := make([]sessionResult, len(files))
	workers := max(1, runtime.NumCPU())
	progress := isTerminalStderr()
	var done atomic.Int64

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				b, a, m, cwd := scanSession(files[i], cfg)
				results[i] = sessionResult{before: b, after: a, msgs: m, cwd: cwd, path: files[i]}
				if progress {
					n := done.Add(1)
					fmt.Fprintf(os.Stderr, "\rturo discover: scanning %d/%d sessions...", n, len(files))
				}
			}
		})
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if progress {
		fmt.Fprint(os.Stderr, "\r\033[K") // erase the progress line before the report
	}
	return results
}

// isTerminalStderr reports whether stderr is an interactive terminal, so the
// progress counter is only drawn when a human is watching (not when piped).
func isTerminalStderr() bool {
	st, err := os.Stderr.Stat()
	return err == nil && (st.Mode()&os.ModeCharDevice) != 0
}

// showDiscover scans all Claude Code history and prints the estimated tokens
// turo could have saved, broken down per project. With asJSON, it emits the
// whole summary as JSON instead of the human report.
func showDiscover(cfg proxyConfig, asJSON bool) {
	dir := claudeProjectsDir()
	files := findSessionLogs(dir)
	if len(files) == 0 {
		if asJSON {
			printJSON(discoverSummary{Roles: rolesLabel(cfg.all)})
			return
		}
		fmt.Printf("turo discover: no Claude Code history under %s\n", shortDir(dir))
		fmt.Println("set CLAUDE_CONFIG_DIR if it lives elsewhere, or run some claude sessions first")
		return
	}

	sessions, order, stats := aggregateDiscover(scanAll(files, cfg))

	var before, after, msgs int
	for _, d := range order {
		s := stats[d]
		before += s.before
		after += s.after
		msgs += s.n
	}

	if asJSON {
		sum := discoverSummary{
			Sessions: sessions, Messages: msgs, Roles: rolesLabel(cfg.all),
			TokensIn: before, WouldBeOut: after, WouldSave: before - after,
			SavedPct: pctInt(before-after, before),
		}
		for _, d := range order {
			s := stats[d]
			sum.ByProject = append(sum.ByProject, projectSummary{
				Dir: shortDir(s.dir), Messages: s.n,
				TokensIn: s.before, WouldBeOut: s.after,
				WouldSave: s.before - s.after, SavedPct: pctInt(s.before-s.after, s.before),
			})
		}
		printJSON(sum)
		return
	}

	if msgs == 0 {
		fmt.Printf("turo discover: scanned %d sessions in %s, found no reducible text\n",
			len(files), shortDir(dir))
		return
	}

	saved := before - after
	fmt.Printf("turo discover — scanned %d sessions in %s\n", sessions, shortDir(dir))
	fmt.Printf("  messages       %s reducible (%s)\n", humanCount(msgs), rolesLabel(cfg.all))
	fmt.Printf("  tokens in      %s\n", humanCount(before))
	fmt.Printf("  would be out   %s\n", humanCount(after))
	fmt.Printf("  would save     %s (%s)\n", humanCount(saved), pct(saved, before))

	if len(order) > 1 {
		fmt.Println("\nby project:")
		for _, d := range order {
			s := stats[d]
			s2 := s.before - s.after
			fmt.Printf("  %-40s %6s msgs  saved %s (%s)\n",
				shortDir(s.dir), humanCount(s.n), humanCount(s2), pct(s2, s.before))
		}
	}

	fmt.Println("\nthese sessions ran without turo — capture the savings next time with:")
	fmt.Println("  turo run claude")
}
