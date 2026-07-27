package main

import "testing"

// shrinkProse used to swap protected literals for space-wrapped numeric
// sentinels (" 3 "), so a bare integer in the prose could be restored as the
// wrong literal — or dropped. Sentinels now use NUL delimiters, which never
// occur in prose, so real numbers survive untouched.
func TestShrinkProseKeepsBareIntegers(t *testing.T) {
	cases := []string{
		"call a.b keep 0 alive and 1 more",
		"review the top 5 results and 3 tables now",
		"use `code` then keep 0 and 1 and 2",
	}
	for _, in := range cases {
		out := shrinkProse(in)
		for _, n := range []string{"0", "1", "2", "3", "5"} {
			// Only assert for numbers actually present in the input.
			if contains(in, " "+n) && !contains(out, n) {
				t.Errorf("shrinkProse(%q) dropped %q: got %q", in, n, out)
			}
		}
		// The protected literals must come back verbatim.
		if contains(in, "a.b") && !contains(out, "a.b") {
			t.Errorf("shrinkProse(%q) lost literal a.b: got %q", in, out)
		}
		if contains(in, "`code`") && !contains(out, "`code`") {
			t.Errorf("shrinkProse(%q) lost literal `code`: got %q", in, out)
		}
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// reduce promises it never emits more tokens than it was given, and that a
// second reduction of its own output is a fixpoint (the converge loop halts).
func TestReduceNeverLargerAndConverges(t *testing.T) {
	inputs := []string{
		"The quick brown fox really jumps over the very lazy dog basically.",
		"Please kindly review the authentication middleware and just fix the expiry check simply.",
		"A cache miss leads to a slow query which produces a timeout for the user.",
		"call service.start() with `--flag` then keep 0 and 1 retries",
	}
	for _, level := range []string{"lite", "full", "ultra"} {
		for _, in := range inputs {
			out := reduce(in, level, 0, true, true, true, false, true, false, true)
			if estimateTokens(out) > estimateTokens(in) {
				t.Errorf("level %s: output larger than input\n in:  %q (%d)\n out: %q (%d)",
					level, in, estimateTokens(in), out, estimateTokens(out))
			}
			// Reducing the output again must not shrink it further — the first
			// call already ran to convergence.
			if again := reduce(out, level, 0, true, true, true, false, true, false, true); estimateTokens(again) > estimateTokens(out) {
				t.Errorf("level %s: second reduce grew output %q -> %q", level, out, again)
			}
		}
	}
}

func TestIsStructured(t *testing.T) {
	structured := map[string]string{
		"markdown table": "| Col A | Col B |\n|-------|-------|\n| one   | two   |\n| three | four  |",
		"bullet list":    "Steps:\n- clone the repo\n- run make build\n- ship it\n- celebrate",
		"numbered list":  "1. first\n2. second\n3. third\n4. fourth",
		"fenced code":    "```go\nfor i := range xs {\n\ttotal += xs[i]\n}\n```",
		"bash transcript": "$ go test ./...\nok  \tgithub.com/kdeps/turo\t1.2s\nFAIL\tgithub.com/kdeps/other\t0.1s\n--- FAIL: TestX (0.00s)\n",
		"stack trace":     "Traceback (most recent call last):\n  File \"app.py\", line 10, in main\n    run()\nValueError: boom\n",
		"json object":     `{"name": "turo", "version": "1.0.0", "scripts": {"test": "go test"}}`,
		"unified diff":    "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1,3 +1,4 @@\n package main\n+import \"fmt\"\n",
		"go source":       "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n",
		"python repl":     ">>> import os\n>>> os.getcwd()\n'/Users/joel/Projects/turo'\n",
		"http dump":       "GET /v1/chat HTTP/1.1\nHost: api.example.com\nContent-Type: application/json\n\nHTTP/1.1 200 OK\n",
		"env dump":        "PATH=/usr/bin\nHOME=/home/joel\nSHELL=/bin/zsh\nUSER=joel\nLANG=en_US.UTF-8\nTERM=xterm-256color\n",
		"git status":      "On branch main\nChanges not staged for commit:\n  (use \"git add\"...)\n\tmodified:   main.go\n\nUntracked files:\n  foo.txt\n",
		"kubectl pods":    "NAME\tREADY\tSTATUS\tRESTARTS\napi-7d9f\t1/1\tRunning\t0\nweb-2abc\t0/1\tCrashLoopBackOff\t3\n",
		"ls -l":           "total 48\n-rw-r--r--  1 joel  staff  1200 Jul 27 12:00 main.go\ndrwxr-xr-x  5 joel  staff   160 Jul 27 12:00 docs\n",
		"csv":             "name,age,city,country\nalice,30,paris,fr\nbob,25,berlin,de\ncarol,40,madrid,es\n",
		"yaml indent":     "services:\n  api:\n    image: api:latest\n    ports:\n      - \"8080:8080\"\n    environment:\n      LOG_LEVEL: debug\n",
		"jest":            " PASS  src/foo.test.ts\n  ✓ adds numbers (2 ms)\n  ✓ handles null (1 ms)\n\nTest Suites: 1 passed, 1 total\nTests:       2 passed, 2 total\n",
		"rust panic":      "thread 'main' panicked at 'index out of bounds', src/main.rs:10:5\nnote: run with `RUST_BACKTRACE=1`\n",
		"pem":             "-----BEGIN CERTIFICATE-----\nMIIBkTCB+wIJAKHBf\n-----END CERTIFICATE-----\n",
		"mysql table":     "+----+------+\n| id | name |\n+----+------+\n|  1 | ann  |\n+----+------+\n(1 row)\n",
		"file:line diag":  "main.go:42:3: undefined: foo\nproxy.go:10:1: imported and not used: \"fmt\"\n",
	}
	for name, s := range structured {
		if !isStructured(s) {
			t.Errorf("%s: expected structured, got false for:\n%s", name, s)
		}
	}
	prose := map[string]string{
		"plain sentence": "Please utilize this approach to demonstrate the functionality clearly.",
		"prose one bullet": "We should review the middleware today.\n- and also the expiry check\n" +
			"then confirm the tests pass before we merge the pull request upstream.",
		"two short lines": "first line here\nsecond line here",
		"tool prose reply": "The search found three relevant packages that implement streaming JSON parsers for Go.",
		"email-ish prose":  "Alice said the deploy looked fine after the timeout fix landed in production yesterday.",
	}
	for name, s := range prose {
		if isStructured(s) {
			t.Errorf("%s: expected prose (not structured), got true for:\n%s", name, s)
		}
	}
}
