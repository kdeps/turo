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
