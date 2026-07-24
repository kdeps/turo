package main

import (
	"os"
	"testing"
)

func TestGainRecordAndAggregate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TURO_HOME", dir)

	// Nothing recorded yet.
	if got := readGain(); len(got) != 0 {
		t.Fatalf("expected empty history, got %d events", len(got))
	}

	recordGain("reduce", 100, 40)
	recordGain("proxy", 50, 30)
	recordGain("reduce", 0, 0) // before<=0: must be skipped, not recorded

	events := readGain()
	if len(events) != 2 {
		t.Fatalf("expected 2 events (the zero-before one skipped), got %d", len(events))
	}

	var before, after int
	for _, e := range events {
		before += e.Before
		after += e.After
	}
	if before != 150 || after != 70 {
		t.Fatalf("aggregate wrong: before=%d after=%d, want 150/70", before, after)
	}

	// Each event records the working folder it ran in.
	for _, e := range events {
		if e.Dir == "" {
			t.Errorf("event %+v missing Dir", e)
		}
	}
}

func TestPct(t *testing.T) {
	cases := []struct {
		n, total int
		want     string
	}{
		{60, 100, "60%"},
		{1, 3, "33%"},
		{5, 0, "0%"}, // divide-by-zero guard
		{0, 100, "0%"},
	}
	for _, c := range cases {
		if got := pct(c.n, c.total); got != c.want {
			t.Errorf("pct(%d,%d)=%q want %q", c.n, c.total, got, c.want)
		}
	}
}

func TestHumanCount(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{478, "478"},
		{999, "999"},
		{1000, "1k"},
		{1200, "1.2k"},
		{1234, "1.23k"},
		{53011, "53.01k"},
		{13524093, "13.52m"},
		{100000000, "100m"},
		{1200000000, "1.2b"},
		{1660000000000, "1.66t"},
	}
	for _, c := range cases {
		if got := humanCount(c.n); got != c.want {
			t.Errorf("humanCount(%d)=%q want %q", c.n, got, c.want)
		}
	}
}

func TestPctInt(t *testing.T) {
	cases := []struct {
		n, total, want int
	}{
		{60, 100, 60},
		{1, 3, 33},
		{5, 0, 0}, // divide-by-zero guard
	}
	for _, c := range cases {
		if got := pctInt(c.n, c.total); got != c.want {
			t.Errorf("pctInt(%d,%d)=%d want %d", c.n, c.total, got, c.want)
		}
	}
}

// buildGainSummary aggregates totals, orders folders by tokens saved (busiest
// project first), and — with history — lists events newest-first.
func TestBuildGainSummary(t *testing.T) {
	events := []gainEvent{
		{T: 1, Cmd: "reduce", Before: 100, After: 40, Dir: "/a"}, // saved 60
		{T: 2, Cmd: "proxy", Before: 200, After: 150, Dir: "/b"}, // saved 50
		{T: 3, Cmd: "reduce", Before: 100, After: 60, Dir: "/a"}, // saved 40 -> /a total 100
	}

	s := buildGainSummary(events, false)
	if s.Reductions != 3 || s.TokensIn != 400 || s.TokensOut != 250 || s.TokensSaved != 150 {
		t.Fatalf("totals wrong: %+v", s)
	}
	if s.SavedPct != 37 { // 150/400
		t.Errorf("SavedPct=%d want 37", s.SavedPct)
	}
	if len(s.ByFolder) != 2 || s.ByFolder[0].Dir != "/a" || s.ByFolder[0].TokensSaved != 100 {
		t.Fatalf("by-folder order/values wrong: %+v", s.ByFolder)
	}
	if s.History != nil {
		t.Errorf("history should be nil without the flag, got %v", s.History)
	}

	h := buildGainSummary(events, true)
	if len(h.History) != 3 || h.History[0].T != 3 || h.History[2].T != 1 {
		t.Fatalf("history should be newest-first: %+v", h.History)
	}
}

// A malformed line in the log must be skipped without hiding valid records.
func TestReadGainSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TURO_HOME", dir)

	recordGain("reduce", 10, 4)
	// Append garbage directly.
	f, err := os.OpenFile(gainPath(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("not json at all\n")
	_ = f.Close()
	recordGain("reduce", 20, 8)

	if got := readGain(); len(got) != 2 {
		t.Fatalf("expected 2 valid events around 1 corrupt line, got %d", len(got))
	}
}
