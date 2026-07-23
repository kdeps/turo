package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Embedded skill + slash command, copied verbatim into agents that read
// Markdown skills/commands from disk (Claude Code, opencode). Kept in sync with
// the files under skills/ and commands/.
//
//go:embed skills/turo/SKILL.md
var embeddedSkill string

//go:embed commands/turo.md
var embeddedCommand string

// agent describes one supported coding agent and how the turo skill reaches it.
//
//	native   copy the skill + /turo command into the agent's config dir
//	gemini   `gemini extensions install`
//	skills   `npx -y skills add kdeps/turo --skill turo -a <profile>`
type agent struct {
	id, label, mech, profile, detect string
}

var agentRegistry = []agent{
	{id: "claude", label: "Claude Code", mech: "native", detect: "command:claude"},
	{id: "opencode", label: "opencode", mech: "native", detect: "command:opencode"},
	{id: "gemini", label: "Gemini CLI", mech: "gemini", detect: "command:gemini"},

	{id: "codex", label: "Codex CLI", mech: "skills", profile: "codex", detect: "command:codex"},
	{id: "cursor", label: "Cursor", mech: "skills", profile: "cursor", detect: "command:cursor||macapp:Cursor"},
	{id: "windsurf", label: "Windsurf", mech: "skills", profile: "windsurf", detect: "command:windsurf||macapp:Windsurf"},
	{id: "cline", label: "Cline", mech: "skills", profile: "cline", detect: "vscode-ext:cline"},
	{id: "continue", label: "Continue", mech: "skills", profile: "continue", detect: "vscode-ext:continue"},
	{id: "kilo", label: "Kilo Code", mech: "skills", profile: "kilo", detect: "vscode-ext:kilocode"},
	{id: "roo", label: "Roo Code", mech: "skills", profile: "roo", detect: "vscode-ext:roo"},
	{id: "copilot", label: "GitHub Copilot", mech: "skills", profile: "github-copilot", detect: "vscode-ext:github.copilot"},
	{id: "aider", label: "Aider", mech: "skills", profile: "aider-desk", detect: "command:aider"},
	{id: "amp", label: "Sourcegraph Amp", mech: "skills", profile: "amp", detect: "command:amp"},
	{id: "crush", label: "Crush", mech: "skills", profile: "crush", detect: "command:crush"},
	{id: "goose", label: "Block Goose", mech: "skills", profile: "goose", detect: "command:goose"},
	{id: "qwen", label: "Qwen Code", mech: "skills", profile: "qwen-code", detect: "command:qwen"},
	{id: "warp", label: "Warp", mech: "skills", profile: "warp", detect: "command:warp"},
	{id: "trae", label: "Trae", mech: "skills", profile: "trae", detect: "command:trae"},
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// detected reports whether an agent's detect spec matches this machine.
func detected(spec string) bool {
	for _, clause := range strings.Split(spec, "||") {
		kind, val, _ := strings.Cut(strings.TrimSpace(clause), ":")
		val = strings.ReplaceAll(val, "$HOME", home())
		switch kind {
		case "command":
			if _, err := exec.LookPath(val); err == nil {
				return true
			}
		case "dir":
			if fi, err := os.Stat(val); err == nil && fi.IsDir() {
				return true
			}
		case "macapp":
			if runtime.GOOS == "darwin" {
				for _, p := range []string{"/Applications/" + val + ".app", filepath.Join(home(), "Applications", val+".app")} {
					if _, err := os.Stat(p); err == nil {
						return true
					}
				}
			}
		case "vscode-ext":
			if vscodeExtInstalled(val) {
				return true
			}
		}
	}
	return false
}

func vscodeExtInstalled(needle string) bool {
	roots := []string{".vscode/extensions", ".vscode-server/extensions", ".cursor/extensions", ".windsurf/extensions"}
	for _, r := range roots {
		entries, err := os.ReadDir(filepath.Join(home(), r))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.Contains(strings.ToLower(e.Name()), strings.ToLower(needle)) {
				return true
			}
		}
	}
	return false
}

func configDir(id string) string {
	switch id {
	case "claude":
		if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
			return d
		}
		return filepath.Join(home(), ".claude")
	case "opencode":
		if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
			return filepath.Join(d, "opencode")
		}
		return filepath.Join(home(), ".config", "opencode")
	}
	return ""
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// installAgents registers the turo skill with every detected agent (or every
// agent when all is true). Returns the number registered.
func installAgents(all bool) int {
	n := 0
	for _, a := range agentRegistry {
		if !all && !detected(a.detect) {
			continue
		}
		fmt.Printf("-> %s\n", a.label)
		var err error
		switch a.mech {
		case "native":
			dir := configDir(a.id)
			if e := writeFile(filepath.Join(dir, "skills", "turo", "SKILL.md"), embeddedSkill); e != nil {
				err = e
			} else {
				err = writeFile(filepath.Join(dir, "commands", "turo.md"), embeddedCommand)
			}
			if err == nil {
				fmt.Printf("   installed skill + /turo command into %s\n", dir)
			}
		case "gemini":
			err = runCmd("gemini", "extensions", "install", "https://github.com/kdeps/turo")
		case "skills":
			err = runCmd("npx", "-y", "skills", "add", "kdeps/turo", "--skill", "turo", "-a", a.profile, "--yes")
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "   failed: %v\n", err)
			continue
		}
		n++
	}
	if n == 0 && !all {
		fmt.Println("no supported agents detected; pass -install-agents=all to register every one")
	}
	return n
}

// agentInstallTimeout bounds each external registration command so a stuck
// npx/gemini download cannot hang the installer.
const agentInstallTimeout = 90 * time.Second

func runCmd(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), agentInstallTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func listAgents() {
	fmt.Println("turo — supported agents")
	for _, a := range agentRegistry {
		mark := "[ ]"
		if detected(a.detect) {
			mark = "[x]"
		}
		fmt.Printf("  %s %-9s %s\n", mark, a.id, a.label)
	}
}
