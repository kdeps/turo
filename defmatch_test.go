package main

import (
	"strings"
	"testing"
)

// useDefFixture swaps the generated dictionary for a small hand-built one so
// these tests describe the matcher, not whatever WordNet happened to emit.
func useDefFixture(t *testing.T) {
	t.Helper()

	words := []string{"author", "landlord", "daughter", "internationalization"}
	sigs := [][]string{
		{"professionally", "writes"},
		{"landowner", "leases"},
		{"female", "offspring"},
		{"code", "global"},
	}
	index := map[string][]uint32{}
	for id, sig := range sigs {
		for _, k := range sig {
			index[k] = append(index[k], uint32(id))
		}
	}
	stop := map[string]bool{"a": true, "who": true, "the": true, "on": true}

	oldWords, oldSigs, oldIndex, oldStop := defWords, defSigs, defIndex, defStopWords
	defWords, defSigs, defIndex, defStopWords = words, sigs, index, stop
	t.Cleanup(func() {
		defWords, defSigs, defIndex, defStopWords = oldWords, oldSigs, oldIndex, oldStop
	})
}

func TestApplyDefMatch(t *testing.T) {
	useDefFixture(t)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "definition phrase collapses to its headword",
			in:   "a person who writes professionally",
			want: "author",
		},
		{
			name: "leading capital carries to the replacement",
			in:   "A person who writes professionally",
			want: "Author",
		},
		{
			name: "no candidate leaves the text alone",
			in:   "the cat sat on the mat",
			want: "the cat sat on the mat",
		},
		{
			name: "partial signature overlap is not a match",
			in:   "a person who writes",
			want: "a person who writes",
		},
		{
			name: "headword no cheaper than the span is skipped",
			in:   "global code",
			want: "global code",
		},
		{
			name: "carrier noun may ride along with the signature",
			in:   "a person who writes professionally",
			want: "author",
		},
		{
			name: "a meaningful surplus word is never swallowed",
			in:   "female offspring landowner",
			want: "daughter landowner",
		},
		{
			name: "adjacent phrases both collapse without overlapping",
			in:   "female offspring landowner leases",
			want: "daughter landlord",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyDefMatch(tc.in); got != tc.want {
				t.Errorf("applyDefMatch(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Sentinels stand in for content the reducer has already protected, so a window
// straddling one must be left whole rather than matched through.
func TestApplyDefMatchSkipsSentinelWindows(t *testing.T) {
	useDefFixture(t)

	in := "female \x0012\x00 offspring"
	got := applyDefMatch(in)
	if got != in {
		t.Errorf("applyDefMatch(%q) = %q, want it unchanged", in, got)
	}
	if !strings.Contains(got, "\x0012\x00") {
		t.Errorf("sentinel lost: %q", got)
	}
}

// The pipeline must run defmatch before the word-level swaps and then hold its
// headwords back from them; either half missing and the match is lost or undone.
func TestReducePreservesDefMatchHeadwords(t *testing.T) {
	const in = "The state of disorder and lawlessness spread through the region."

	got := reduce(in, "lite", 1, true, true, true, true, true)
	if !strings.Contains(strings.ToLower(got), "anarchy") {
		t.Errorf("reduce(%q) = %q, want it to contain the headword \"anarchy\"", in, got)
	}
	if strings.Contains(strings.ToLower(got), "lawlessness") {
		t.Errorf("reduce(%q) = %q, want the definition phrase collapsed", in, got)
	}
}

func TestApplyDefMatchIntoRecordsHeadwords(t *testing.T) {
	useDefFixture(t)

	produced := map[string]bool{}
	applyDefMatchInto("a person who writes professionally", produced)
	if !produced["author"] {
		t.Errorf("produced = %v, want it to record \"author\"", produced)
	}

	produced = map[string]bool{}
	applyDefMatchInto("the cat sat on the mat", produced)
	if len(produced) != 0 {
		t.Errorf("produced = %v, want nothing recorded when no phrase matches", produced)
	}
}

func TestApplyDefMatchEmptyDictionary(t *testing.T) {
	oldWords := defWords
	defWords = nil
	t.Cleanup(func() { defWords = oldWords })

	const in = "a person who writes professionally"
	if got := applyDefMatch(in); got != in {
		t.Errorf("applyDefMatch(%q) = %q, want it unchanged", in, got)
	}
}

// Map iteration order must not leak into output: same input, same result.
func TestApplyDefMatchDeterministic(t *testing.T) {
	useDefFixture(t)

	const in = "a person who writes professionally and a female offspring"
	want := applyDefMatch(in)
	for i := 0; i < 200; i++ {
		if got := applyDefMatch(in); got != want {
			t.Fatalf("run %d: applyDefMatch = %q, want %q", i, got, want)
		}
	}
}
