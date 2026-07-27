package main

import (
	"strings"
	"testing"
)

// mdReduce runs the full pipeline with markup awareness on, the shipped default.
func mdReduce(s string) string {
	return reduce(s, "full", 0, true, true, true, true, true, true, true)
}

const mdDoc = "# Getting Started\n" +
	"\n" +
	"This is a paragraph that basically explains how the thing actually works,\n" +
	"and it is quite verbose because I think we should say more words.\n" +
	"\n" +
	"## Install\n" +
	"\n" +
	"- Run `brew install kdeps/tap/turo` to install the binary\n" +
	"- Or you could potentially use `go install github.com/kdeps/turo@latest`\n" +
	"  - A nested item that explains something\n" +
	"\n" +
	"| Flag | Meaning |\n" +
	"|------|---------|\n" +
	"| `-level` | the compression level to use |\n" +
	"| `-passes` | how many passes to run over the input |\n" +
	"\n" +
	"```go\nfunc main() {\n    fmt.Println(\"hello\")\n}\n```\n" +
	"\n" +
	"> Note that this is a blockquote which explains something important.\n" +
	"\n" +
	"<div class=\"warning\">This paragraph is inside an HTML block element.</div>\n" +
	"\n" +
	"See https://github.com/kdeps/turo for more.\n"

func TestLooksLikeMarkup(t *testing.T) {
	markup := map[string]string{
		"heading":    "# Title\n\nsome prose here\n",
		"fence":      "text\n```\ncode()\n```\n",
		"table":      "| a | b |\n|---|---|\n| 1 | 2 |\n",
		"list":       "- one item\n- two item\n",
		"blockquote": "> quoted line one\n> quoted line two\n",
		"html":       "<div class=\"x\">hello there</div>\n",
	}
	for name, in := range markup {
		if !looksLikeMarkup(in) {
			t.Errorf("looksLikeMarkup(%s) = false, want true", name)
		}
	}

	prose := map[string]string{
		"plain":      "This is ordinary prose with no structure at all.\n",
		"one bullet": "Some prose.\n- a single stray bullet\nMore prose.\n",
	}
	for name, in := range prose {
		if looksLikeMarkup(in) {
			t.Errorf("looksLikeMarkup(%s) = true, want false", name)
		}
	}
}

func TestMarkupKeepsHeadingMarkers(t *testing.T) {
	got := mdReduce(mdDoc)
	for _, want := range []string{"# ", "## "} {
		if !strings.Contains(got, "\n"+want) && !strings.HasPrefix(got, want) {
			t.Errorf("reduced doc lost %q heading marker:\n%s", want, got)
		}
	}
	// The heading keeps its level, not just any "#" run.
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(ln, "#") && !strings.Contains(ln, " ") {
			t.Errorf("heading %q lost its text", ln)
		}
	}
}

func TestMarkupKeepsTableShape(t *testing.T) {
	got := mdReduce(mdDoc)
	var rows []string
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "|") {
			rows = append(rows, ln)
		}
	}
	if len(rows) != 4 {
		t.Fatalf("want 4 table rows, got %d:\n%s", len(rows), got)
	}
	if !strings.Contains(rows[1], "---") {
		t.Errorf("alignment row not preserved: %q", rows[1])
	}
	for _, r := range rows {
		if n := strings.Count(r, "|"); n != 3 {
			t.Errorf("row %q has %d pipes, want 3", r, n)
		}
	}
}

func TestMarkupKeepsFencedCodeByteIdentical(t *testing.T) {
	got := mdReduce(mdDoc)
	const fence = "```go\nfunc main() {\n    fmt.Println(\"hello\")\n}\n```"
	if !strings.Contains(got, fence) {
		t.Fatalf("fenced code not preserved verbatim:\n%s", got)
	}
	// And in position: still ahead of the blockquote that followed it.
	if strings.Index(got, fence) > strings.Index(got, ">") {
		t.Errorf("fenced code moved out of position:\n%s", got)
	}
}

func TestMarkupKeepsListMarkersAndNesting(t *testing.T) {
	got := mdReduce(mdDoc)
	top, nested := 0, 0
	for _, ln := range strings.Split(got, "\n") {
		switch {
		case strings.HasPrefix(ln, "- "):
			top++
		case strings.HasPrefix(ln, "  - "):
			nested++
		}
	}
	if top != 2 {
		t.Errorf("want 2 top-level bullets, got %d:\n%s", top, got)
	}
	if nested != 1 {
		t.Errorf("want 1 nested bullet, got %d:\n%s", nested, got)
	}
}

func TestMarkupRestoresLiteralsInPlace(t *testing.T) {
	got := mdReduce(mdDoc)
	// A URL stays on the line it came from rather than in a tail dump.
	for _, ln := range strings.Split(got, "\n") {
		if strings.Contains(ln, "https://github.com/kdeps/turo") {
			if !strings.Contains(strings.ToLower(ln), "see") {
				t.Errorf("URL moved off its own line: %q", ln)
			}
			return
		}
	}
	t.Fatalf("URL missing from output:\n%s", got)
}

func TestFlatRestoresLiteralsInPlace(t *testing.T) {
	in := "Read the config at /etc/turo/config.yaml before you restart the running server."
	got := reduce(in, "full", 0, true, true, true, true, true, false, true)
	const path = "/etc/turo/config.yaml"
	i := strings.Index(got, path)
	if i < 0 {
		t.Fatalf("path lost: %q", got)
	}
	// The path sits where it was written, with reduced prose on both sides —
	// not appended after everything else.
	if strings.TrimSpace(got[i+len(path):]) == "" {
		t.Errorf("path was tail-appended rather than restored in place: %q", got)
	}
	if strings.ContainsRune(got, 0) {
		t.Errorf("sentinel leaked into output: %q", got)
	}
}

func TestMarkupKeepsHTMLTags(t *testing.T) {
	got := mdReduce(mdDoc)
	if !strings.Contains(got, `<div class="warning">`) {
		t.Errorf("opening HTML tag not preserved:\n%s", got)
	}
	if !strings.Contains(got, "</div>") {
		t.Errorf("closing HTML tag not preserved:\n%s", got)
	}
}

func TestMarkupNoNULInOutput(t *testing.T) {
	for _, in := range []string{mdDoc, "# t\n\n`a.b()` and CONST_NAME and 1.2.3\n"} {
		if got := mdReduce(in); strings.ContainsRune(got, 0) {
			t.Errorf("NUL byte reached output for %q:\n%q", in, got)
		}
	}
}

func TestMarkupNeverLargerAndConverges(t *testing.T) {
	got := mdReduce(mdDoc)
	if estimateTokens(got) > estimateTokens(mdDoc) {
		t.Errorf("reduction grew the doc: %d -> %d tokens",
			estimateTokens(mdDoc), estimateTokens(got))
	}
	if again := mdReduce(got); estimateTokens(again) > estimateTokens(got) {
		t.Errorf("second pass grew the doc: %d -> %d tokens",
			estimateTokens(got), estimateTokens(again))
	}
}

func TestMarkupOffFallsBackToFlat(t *testing.T) {
	// special=false so table-rule tokens like |------| are not shielded as
	// specials; this asserts markdown structure itself is not preserved.
	flat := reduce(mdDoc, "full", 0, true, true, true, true, true, false, false)
	if strings.Contains(flat, "|------|") {
		t.Errorf("-markdown=false should not preserve table shape:\n%s", flat)
	}
}

func TestTableCellNeverCollapsesColumn(t *testing.T) {
	// "the" reduces to nothing; the cell must keep its text, not vanish.
	in := "| a | the | b |\n|---|---|---|\n| 1 | the | 2 |\n"
	got := reduceTableRow("| 1 | the | 2 |", reduceOpts{level: "full", filler: true, synonyms: true})
	if n := strings.Count(got, "|"); n != 4 {
		t.Errorf("row %q has %d pipes, want 4", got, n)
	}
	if !looksLikeMarkup(in) {
		t.Errorf("table not detected as markup")
	}
}
