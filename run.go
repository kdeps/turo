package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// runTarget describes how to launch a CLI agent through the turo proxy: the
// command, the real upstream to forward to, the path suffix its base URL needs
// (OpenAI expects .../v1, Anthropic does not), and the environment variables
// that point the agent at a custom endpoint.
type runTarget struct {
	cmd      string
	upstream string
	suffix   string
	env      []string
}

//nolint:gochecknoglobals // static launch registry
var runTargets = map[string]runTarget{
	"claude":   {cmd: "claude", upstream: "https://api.anthropic.com", suffix: "", env: []string{"ANTHROPIC_BASE_URL"}},
	"codex":    {cmd: "codex", upstream: "https://api.openai.com", suffix: "/v1", env: []string{"OPENAI_BASE_URL", "OPENAI_API_BASE"}},
	"opencode": {cmd: "opencode", upstream: "https://api.openai.com", suffix: "/v1", env: []string{"OPENAI_BASE_URL", "OPENAI_API_BASE"}},
	"qwen":     {cmd: "qwen", upstream: "https://api.openai.com", suffix: "/v1", env: []string{"OPENAI_BASE_URL", "OPENAI_API_BASE"}},
	"aider":    {cmd: "aider", upstream: "https://api.openai.com", suffix: "/v1", env: []string{"OPENAI_API_BASE", "OPENAI_BASE_URL"}},
	"crush":    {cmd: "crush", upstream: "https://api.openai.com", suffix: "/v1", env: []string{"OPENAI_BASE_URL", "OPENAI_API_BASE"}},
	"goose":    {cmd: "goose", upstream: "https://api.openai.com", suffix: "/v1", env: []string{"OPENAI_BASE_URL", "OPENAI_API_BASE"}},
	"amp":      {cmd: "amp", upstream: "https://api.openai.com", suffix: "/v1", env: []string{"OPENAI_BASE_URL", "OPENAI_API_BASE"}},
}

func runTargetIDs() []string {
	ids := make([]string, 0, len(runTargets))
	for id := range runTargets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// runAgent launches an agent with every model request routed through an
// in-process turo proxy. The proxy stops when the agent exits. upstreamOverride,
// when non-empty, replaces the agent's default upstream (e.g. a local endpoint).
func runAgent(agent string, args []string, upstreamOverride string, pcfg proxyConfig) error {
	t, ok := runTargets[agent]
	if !ok {
		return fmt.Errorf("turo run: unknown agent %q; supported: %s", agent, strings.Join(runTargetIDs(), ", "))
	}
	if _, err := exec.LookPath(t.cmd); err != nil {
		return fmt.Errorf("turo run: %q is not on PATH", t.cmd)
	}
	if upstreamOverride != "" {
		t.upstream = upstreamOverride
	}
	pcfg.upstream = t.upstream

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("turo run: cannot start proxy: %w", err)
	}
	srv := &http.Server{Handler: proxyHandler(pcfg)} //nolint:gosec // local dev proxy
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	baseURL := "http://" + ln.Addr().String() + t.suffix
	if proxyShowsOutput(pcfg) {
		fmt.Fprintf(os.Stderr, "turo run: %s via proxy %s -> %s (reducing %s)\n",
			agent, ln.Addr().String(), t.upstream, rolesLabel(pcfg.all))
	}

	cmd := exec.Command(t.cmd, args...) //nolint:gosec // user-invoked agent
	cmd.Env = os.Environ()
	for _, name := range t.env {
		cmd.Env = append(cmd.Env, name+"="+baseURL)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func listRunTargets() {
	fmt.Println("turo run — launch an agent with every request reduced through turo:")
	for _, id := range runTargetIDs() {
		mark := " "
		if _, err := exec.LookPath(runTargets[id].cmd); err == nil {
			mark = "x"
		}
		fmt.Printf("  [%s] turo run %s\n", mark, id)
	}
	fmt.Println("\nFlags (before the agent name):")
	fmt.Println("  -level lite|full|ultra|wenyan   compression level")
	fmt.Println("  -filler/-synonyms/-gloss=false  disable a reduction stage")
	fmt.Println("  -arrows                         connective phrases -> \"->\"")
	fmt.Println("  -proxy-all=false                reduce only user + tool (default: every role)")
	fmt.Println("  -proxy-verbose                  echo each message's before -> after text")
	fmt.Println("  -proxy-quiet=false              show the token summary (default: hidden)")
	fmt.Println("  -upstream URL                   override the agent's upstream endpoint")
	fmt.Println("\nExample: turo run -level ultra -proxy-verbose codex")
}

// runExitCode extracts an agent's exit code from a cmd.Run error, defaulting to
// 1 for non-exit failures.
func runExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}
