package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseToGraph_TermEdges(t *testing.T) {
	got := parseToGraph("the quick brown fox jumps over the lazy dog", "full", 0)
	for _, want := range []string{"quick → fox", "brown → fox", "fox → jumps", "lazy → dog"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected edge %q in output, got:\n%s", want, got)
		}
	}
	// Stop words must not appear as nodes.
	for _, drop := range []string{"the ", "over "} {
		if strings.Contains(got, drop) {
			t.Fatalf("stop word %q leaked into output:\n%s", drop, got)
		}
	}
}

func TestParseToGraph_LevelsDiffer(t *testing.T) {
	text := "the quick brown fox jumps over the lazy dog"
	lite := parseToGraph(text, "lite", 0)
	ultra := parseToGraph(text, "ultra", 0)
	if lite == ultra {
		t.Fatal("lite and ultra levels produced identical output")
	}
	// ultra emits a numbered chain header.
	if !strings.Contains(ultra, "→") {
		t.Fatalf("ultra output missing arrow chain, got:\n%s", ultra)
	}
}

func TestExtractStructure_HeadingsAndPaths(t *testing.T) {
	md := "# Title\n\nSome intro text here.\n\n## Section\n\nSee `pkg/agent/loop.go` for details.\n"
	got := extractStructure(md, "full")
	if !strings.Contains(got, "Title") || !strings.Contains(got, "Section") {
		t.Fatalf("expected headings preserved, got:\n%s", got)
	}
	if !strings.Contains(got, "pkg/agent/loop.go") {
		t.Fatalf("expected file path preserved verbatim, got:\n%s", got)
	}
}

func TestWrapPreamble(t *testing.T) {
	out := wrapPreamble("a → b\n")
	if !strings.HasPrefix(out, "<context-graph>") || !strings.HasSuffix(out, "</context-graph>\n") {
		t.Fatalf("preamble not wrapped in tags:\n%s", out)
	}
	if !strings.Contains(out, "a → b") {
		t.Fatalf("preamble dropped the graph body:\n%s", out)
	}
	if wrapPreamble("") != "" {
		t.Fatal("empty graph should wrap to empty string")
	}
	if wrapPreamble("   \n\n") != "" {
		t.Fatal("whitespace-only graph should wrap to empty string")
	}
}

func TestStem(t *testing.T) {
	cases := map[string]string{
		"running": "run",
		"jumps":   "jump",
		"created": "creat",
		"testing": "test",
	}
	for in, want := range cases {
		if got := stem(in); got != want {
			t.Errorf("stem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScanDir(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.go"), "package main")
	mustMkdir(t, filepath.Join(root, "pkg"))
	mustWrite(t, filepath.Join(root, "pkg", "util.go"), "package pkg")
	mustMkdir(t, filepath.Join(root, ".git")) // ignored
	mustWrite(t, filepath.Join(root, ".git", "config"), "x")
	mustMkdir(t, filepath.Join(root, "node_modules")) // ignored
	mustWrite(t, filepath.Join(root, "node_modules", "dep.js"), "x")

	got, err := scanDir(root, 0)
	if err != nil {
		t.Fatalf("scanDir: %v", err)
	}
	if !strings.Contains(got, "pkg/") || !strings.Contains(got, "util.go") || !strings.Contains(got, "main.go") {
		t.Fatalf("expected tree entries, got:\n%s", got)
	}
	if strings.Contains(got, ".git") || strings.Contains(got, "node_modules") || strings.Contains(got, "dep.js") {
		t.Fatalf("ignored dirs leaked into output:\n%s", got)
	}
}

func TestScanDir_MaxDepth(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "a"))
	mustMkdir(t, filepath.Join(root, "a", "b"))
	mustWrite(t, filepath.Join(root, "a", "b", "deep.go"), "x")

	got, err := scanDir(root, 1)
	if err != nil {
		t.Fatalf("scanDir: %v", err)
	}
	if !strings.Contains(got, "a/") {
		t.Fatalf("expected top-level dir at depth 1, got:\n%s", got)
	}
	if strings.Contains(got, "deep.go") {
		t.Fatalf("max-depth 1 should not descend to deep.go:\n%s", got)
	}
}

func TestScanDir_NotADirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	mustWrite(t, f, "x")
	if _, err := scanDir(f, 0); err == nil {
		t.Fatal("expected error scanning a non-directory")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
