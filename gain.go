package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// gainEvent is one recorded reduction: how many estimated tokens went in and
// how many came out, tagged by the command that produced it and the working
// folder it ran in.
type gainEvent struct {
	T      int64  `json:"t"`             // unix seconds
	Cmd    string `json:"cmd"`           // reduce | proxy | run
	Before int    `json:"before"`        // estimated input tokens
	After  int    `json:"after"`         // estimated output tokens
	Dir    string `json:"dir,omitempty"` // working folder the reduction ran in
}

// gainPath is the append-only JSONL log of reductions. One event per line keeps
// concurrent writers (proxy handling parallel requests) from corrupting earlier
// records the way a rewritten JSON array would.
//
// Location, most specific first:
//  1. $TURO_HOME/gain.jsonl               (explicit override, used by tests)
//  2. <os.UserConfigDir>/turo/gain.jsonl  (OS-standard: ~/Library/Application
//     Support/turo on macOS, ~/.config/turo on Linux, %AppData%\turo on Windows)
//  3. ~/.turo/gain.jsonl                  (fallback if the OS dir is unavailable)
func gainPath() string {
	if d := os.Getenv("TURO_HOME"); d != "" {
		return filepath.Join(expandPath(d), "gain.jsonl")
	}
	if d, err := os.UserConfigDir(); err == nil && d != "" {
		return filepath.Join(d, "turo", "gain.jsonl")
	}
	return filepath.Join(home(), ".turo", "gain.jsonl")
}

// recordGain appends one reduction event. It is best-effort: analytics must
// never break a reduction, so every error is swallowed. A no-op when the
// reduction saved nothing meaningful (before <= 0) or was not actually smaller.
func recordGain(cmd string, before, after int) {
	if before <= 0 {
		return
	}
	path := gainPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	dir, _ := os.Getwd()
	line, err := json.Marshal(gainEvent{T: time.Now().Unix(), Cmd: cmd, Before: before, After: after, Dir: dir})
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
}

// readGain loads every recorded event, skipping any malformed line so a single
// bad write never hides the rest of the history.
func readGain() []gainEvent {
	f, err := os.Open(gainPath())
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var events []gainEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e gainEvent
		if json.Unmarshal([]byte(line), &e) == nil {
			events = append(events, e)
		}
	}
	// A scan error (e.g. a line longer than the buffer) stops iteration; return
	// whatever parsed cleanly rather than nothing — analytics are best-effort.
	_ = sc.Err()
	return events
}

// folderSummary is one folder's savings, flattened for JSON output.
type folderSummary struct {
	Dir         string `json:"dir"`
	Reductions  int    `json:"reductions"`
	TokensIn    int    `json:"tokens_in"`
	TokensOut   int    `json:"tokens_out"`
	TokensSaved int    `json:"tokens_saved"`
	SavedPct    int    `json:"saved_pct"`
}

// gainSummary is the machine-readable form of `turo gain`, emitted by --json.
type gainSummary struct {
	Reductions  int             `json:"reductions"`
	TokensIn    int             `json:"tokens_in"`
	TokensOut   int             `json:"tokens_out"`
	TokensSaved int             `json:"tokens_saved"`
	SavedPct    int             `json:"saved_pct"`
	ByFolder    []folderSummary `json:"by_folder,omitempty"`
	History     []gainEvent     `json:"history,omitempty"` // newest first, only with --history
}

// buildGainSummary aggregates events into the struct both renderers share. With
// history, it attaches every event newest-first so a script can slice its own
// window; the text renderer caps what it prints separately.
func buildGainSummary(events []gainEvent, history bool) gainSummary {
	var before, after int
	for _, e := range events {
		before += e.Before
		after += e.After
	}
	s := gainSummary{
		Reductions:  len(events),
		TokensIn:    before,
		TokensOut:   after,
		TokensSaved: before - after,
		SavedPct:    pctInt(before-after, before),
	}
	order, stats := foldersBySaved(events)
	for _, d := range order {
		f := stats[d]
		s.ByFolder = append(s.ByFolder, folderSummary{
			Dir: shortDir(f.dir), Reductions: f.n,
			TokensIn: f.before, TokensOut: f.after,
			TokensSaved: f.before - f.after, SavedPct: pctInt(f.before-f.after, f.before),
		})
	}
	if history {
		for i := len(events) - 1; i >= 0; i-- {
			s.History = append(s.History, events[i])
		}
	}
	return s
}

// showGain prints aggregate token savings. With history, it also lists the most
// recent reductions newest-first. With asJSON, it emits the whole summary as
// JSON instead of the human report.
func showGain(history, asJSON bool) {
	events := readGain()
	if asJSON {
		printJSON(buildGainSummary(events, history))
		return
	}
	if len(events) == 0 {
		fmt.Println("turo gain: no reductions recorded yet")
		fmt.Println("run turo on some text (cat file | turo), or a proxy/agent session, then check back")
		return
	}

	var before, after int
	for _, e := range events {
		before += e.Before
		after += e.After
	}
	saved := before - after

	fmt.Printf("turo gain — %d reductions\n", len(events))
	fmt.Printf("  tokens in     %s\n", humanCount(before))
	fmt.Printf("  tokens out    %s\n", humanCount(after))
	fmt.Printf("  tokens saved  %s (%s)\n", humanCount(saved), pct(saved, before))

	showByFolder(events)

	if !history {
		return
	}

	fmt.Println("\nhistory (newest first):")
	const maxRows = 20
	shown := 0
	for i := len(events) - 1; i >= 0 && shown < maxRows; i-- {
		e := events[i]
		when := time.Unix(e.T, 0).Format("2006-01-02 15:04")
		s := e.Before - e.After
		fmt.Printf("  %s  %-6s %7s -> %-7s  saved %s (%s)  %s\n",
			when, e.Cmd, humanCount(e.Before), humanCount(e.After), humanCount(s), pct(s, e.Before), shortDir(e.Dir))
		shown++
	}
	if len(events) > maxRows {
		fmt.Printf("  ... %d older\n", len(events)-maxRows)
	}
}

// folderStat accumulates savings for one working folder.
type folderStat struct {
	dir           string
	n             int
	before, after int
}

// foldersBySaved groups events by working folder and returns the folder keys
// ordered by tokens saved (descending), plus the per-folder stats. Shared by the
// text breakdown and the JSON summary so both see the same grouping and order.
func foldersBySaved(events []gainEvent) ([]string, map[string]*folderStat) {
	order := []string{}
	stats := map[string]*folderStat{}
	for _, e := range events {
		d := e.Dir
		if d == "" {
			d = "(unknown)"
		}
		s, ok := stats[d]
		if !ok {
			s = &folderStat{dir: d}
			stats[d] = s
			order = append(order, d)
		}
		s.n++
		s.before += e.Before
		s.after += e.After
	}
	sortFoldersBySaved(order, stats)
	return order, stats
}

// showByFolder prints a per-folder savings breakdown, busiest folder first, so
// you can see which projects turo is saving the most tokens in. Folders are
// only shown when reductions came from more than one.
func showByFolder(events []gainEvent) {
	order, stats := foldersBySaved(events)
	if len(order) < 2 {
		return
	}
	fmt.Println("\nby folder:")
	for _, d := range order {
		s := stats[d]
		saved := s.before - s.after
		fmt.Printf("  %-40s %4d reductions  saved %s (%s)\n",
			shortDir(s.dir), s.n, humanCount(saved), pct(saved, s.before))
	}
}

func savedOf(s *folderStat) int { return s.before - s.after }

// sortFoldersBySaved orders folder keys by tokens saved, descending, in place.
// A simple selection sort — the folder list is small (one entry per project).
func sortFoldersBySaved(order []string, stats map[string]*folderStat) {
	for i := 0; i < len(order); i++ {
		max := i
		for j := i + 1; j < len(order); j++ {
			if savedOf(stats[order[j]]) > savedOf(stats[order[max]]) {
				max = j
			}
		}
		order[i], order[max] = order[max], order[i]
	}
}

// shortDir replaces the home-directory prefix with ~ so folders print compactly.
func shortDir(dir string) string {
	if dir == "" {
		return ""
	}
	if h := home(); h != "" && strings.HasPrefix(dir, h) {
		return "~" + strings.TrimPrefix(dir, h)
	}
	return dir
}

// pct renders n/total as a percentage string, guarding against divide-by-zero.
func pct(n, total int) string {
	return fmt.Sprintf("%d%%", pctInt(n, total))
}

// pctInt is pct as a bare integer, for JSON output.
func pctInt(n, total int) int {
	if total <= 0 {
		return 0
	}
	return n * 100 / total
}

// printJSON marshals v as indented JSON to stdout. Best-effort like the rest of
// analytics: a marshal error prints nothing rather than breaking the caller.
func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return
	}
	fmt.Println(string(b))
}

// humanCount abbreviates a count with a magnitude suffix — 1234 -> "1.23k",
// 13524093 -> "13.52m", 1660000000000 -> "1.66t" — so the big token totals
// read at a glance. Values under 1000 print as plain integers. Up to two
// decimals, trailing zeros trimmed (1200 -> "1.2k", 100000000 -> "100m").
func humanCount(n int) string {
	abs := n
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 1e12:
		return trimDecimals(float64(n)/1e12) + "t"
	case abs >= 1e9:
		return trimDecimals(float64(n)/1e9) + "b"
	case abs >= 1e6:
		return trimDecimals(float64(n)/1e6) + "m"
	case abs >= 1e3:
		return trimDecimals(float64(n)/1e3) + "k"
	default:
		return strconv.Itoa(n)
	}
}

// trimDecimals formats f with up to two decimals, dropping trailing zeros and a
// bare trailing dot (1.20 -> "1.2", 100.00 -> "100").
func trimDecimals(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}
