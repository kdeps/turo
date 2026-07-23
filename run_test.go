package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunExitCode(t *testing.T) {
	if runExitCode(nil) != 0 {
		t.Fatal("nil error should be exit 0")
	}
	if runExitCode(errors.New("boom")) != 1 {
		t.Fatal("non-exit error should be 1")
	}
}

func TestRunAgent_Errors(t *testing.T) {
	if err := runAgent("bogus", nil, "", proxyConfig{}); err == nil ||
		!strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("expected unknown-agent error, got %v", err)
	}
	// A known target whose command is absent must report a clear PATH error.
	runTargets["_missing_"] = runTarget{cmd: "definitely-not-a-real-binary-xyz", upstream: "http://x"}
	defer delete(runTargets, "_missing_")
	if err := runAgent("_missing_", nil, "", proxyConfig{}); err == nil ||
		!strings.Contains(err.Error(), "not on PATH") {
		t.Fatalf("expected not-on-PATH error, got %v", err)
	}
}

func TestRunAgent_WiresProxyAndEnv(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	var received string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = string(b)
		_, _ = w.Write([]byte("{}"))
	}))
	defer upstream.Close()

	// A fake agent that posts to its configured base URL using Go's net/http
	// via a tiny sh + the turo binary is overkill; use a sh script with curl,
	// skipping when curl is unavailable.
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fakeagent.sh")
	body := `{"messages":[{"role":"user","content":"Please utilize this approach to demonstrate the functionality."}]}`
	content := "#!/bin/sh\ncurl -s -X POST \"$OPENAI_BASE_URL/chat/completions\" -d '" + body + "' >/dev/null\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}

	runTargets["_fake_"] = runTarget{cmd: script, upstream: upstream.URL, suffix: "/v1", env: []string{"OPENAI_BASE_URL"}}
	defer delete(runTargets, "_fake_")

	if err := runAgent("_fake_", nil, "", proxyConfig{level: "full", filler: true}); err != nil {
		t.Fatalf("runAgent: %v", err)
	}
	if received == "" {
		t.Fatal("upstream received nothing — proxy/env not wired")
	}
	if strings.Contains(received, "Please") || strings.Contains(received, "utilize") {
		t.Fatalf("content should have been reduced through the proxy, got %q", received)
	}
}
