package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSession writes one JSONL history file with the given records.
func writeSession(t *testing.T, dir, name string, records []map[string]any) {
	t.Helper()
	var b strings.Builder
	for _, r := range records {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func msg(role string, content any) map[string]any {
	return map[string]any{"role": role, "content": content}
}

// A default scan reduces user and tool text but leaves assistant history alone.
func TestScanSessionRolesAndSavings(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "s.jsonl", []map[string]any{
		{"type": "mode", "mode": "normal"}, // non-message: ignored
		{"type": "user", "cwd": "/Users/joel/Projects/foo",
			"message": msg("user", "I would like to please kindly refactor the authentication middleware")},
		{"type": "assistant",
			"message": msg("assistant", []map[string]any{
				{"type": "text", "text": "I will certainly go ahead and refactor the authentication middleware for you"},
			})},
		{"type": "user",
			"message": msg("user", []map[string]any{
				{"type": "tool_result", "content": "the file basically just contains a very simple helper function"},
			})},
	})

	cfg := proxyConfig{level: "ultra", filler: true, synonyms: true, gloss: true, arrows: true}
	before, after, msgs, cwd := scanSession(filepath.Join(dir, "s.jsonl"), cfg)

	// user string + tool_result text = 2 reduced messages; assistant is skipped.
	if msgs != 2 {
		t.Fatalf("expected 2 reducible messages (assistant excluded), got %d", msgs)
	}
	if before <= 0 || after <= 0 {
		t.Fatalf("expected positive token totals, got before=%d after=%d", before, after)
	}
	if after >= before {
		t.Fatalf("expected a saving (after < before), got before=%d after=%d", before, after)
	}
	if cwd != "/Users/joel/Projects/foo" {
		t.Fatalf("cwd = %q, want the folder from the first record", cwd)
	}
}

// With all roles enabled, assistant text is reduced too.
func TestScanSessionAllRoles(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "s.jsonl", []map[string]any{
		{"type": "assistant",
			"message": msg("assistant", []map[string]any{
				{"type": "text", "text": "I will certainly refactor the middleware"},
			})},
	})

	def := scanMsgs(t, dir, proxyConfig{level: "ultra", filler: true})
	if def != 0 {
		t.Fatalf("assistant excluded by default, got %d messages", def)
	}
	all := scanMsgs(t, dir, proxyConfig{level: "ultra", filler: true, all: true})
	if all != 1 {
		t.Fatalf("assistant reduced with all=true, got %d messages", all)
	}
}

func scanMsgs(t *testing.T, dir string, cfg proxyConfig) int {
	t.Helper()
	_, _, m, _ := scanSession(filepath.Join(dir, "s.jsonl"), cfg)
	return m
}

// findSessionLogs walks nested project folders and picks up every .jsonl.
func TestFindSessionLogsNested(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "-Users-joel-Projects-foo")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, sub, "a.jsonl", []map[string]any{{"type": "mode"}})
	if err := os.WriteFile(filepath.Join(dir, "not-a-log.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := findSessionLogs(dir)
	if len(files) != 1 || !strings.HasSuffix(files[0], "a.jsonl") {
		t.Fatalf("expected exactly the nested a.jsonl, got %v", files)
	}
}

// claudeProjectsDir honors the test override, then CLAUDE_CONFIG_DIR.
func TestClaudeProjectsDir(t *testing.T) {
	t.Setenv("TURO_DISCOVER_DIR", "/tmp/explicit")
	if got := claudeProjectsDir(); got != "/tmp/explicit" {
		t.Fatalf("override ignored: %q", got)
	}

	t.Setenv("TURO_DISCOVER_DIR", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/cfg")
	if got := claudeProjectsDir(); got != filepath.Join("/tmp/cfg", "projects") {
		t.Fatalf("CLAUDE_CONFIG_DIR not used: %q", got)
	}
}

// Malformed and non-message lines are skipped without derailing the scan.
func TestScanSessionSkipsJunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	body := "not json at all\n" +
		`{"type":"user","message":{"role":"user","content":"delete the redundant helper functions right now"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, msgs, _ := scanSession(path, proxyConfig{level: "ultra", filler: true, synonyms: true, gloss: true})
	if msgs != 1 {
		t.Fatalf("expected 1 message around the junk line, got %d", msgs)
	}
}

// aggregateDiscover folds per-file results into project totals ordered by tokens
// saved, dropping sessions that reduced nothing and falling back to the parent
// folder name when a result carries no cwd.
func TestAggregateDiscover(t *testing.T) {
	results := []sessionResult{
		{before: 100, after: 40, msgs: 2, cwd: "/a"},                 // saved 60
		{before: 300, after: 150, msgs: 3, cwd: "/b"},                // saved 150 (busiest saver)
		{before: 0, after: 0, msgs: 0, cwd: "/c"},                    // no reducible text: dropped
		{before: 50, after: 20, msgs: 1, path: "/x/proj/sess.jsonl"}, // no cwd -> "proj"
	}

	sessions, order, stats := aggregateDiscover(results)
	if sessions != 3 {
		t.Fatalf("sessions=%d want 3 (empty one dropped)", sessions)
	}
	if len(order) != 3 || order[0] != "/b" {
		t.Fatalf("order wrong (biggest saver first): %v", order)
	}
	if stats["proj"] == nil || stats["proj"].before != 50 {
		t.Fatalf("cwd fallback to parent dir name failed: %+v", stats)
	}
}
