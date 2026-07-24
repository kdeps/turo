package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// doctor is a health check: it prints turo's version, resolved settings, where
// it reads and writes, a live reduction self-test, and which coding agents are
// detected and wired up. Anything genuinely broken (invalid level, a pipeline
// that fails to reduce, an unwritable gain log) is counted as a problem so the
// command can exit non-zero for use in CI or an install script.

// doctorEnvVars are the environment overrides worth surfacing — set ones are
// echoed so a surprising default (a stray TURO_LEVEL, a redirected TURO_HOME)
// is obvious at a glance.
var doctorEnvVars = []string{
	"TURO_HOME", "TURO_LEVEL",
	"TURO_FILLER", "TURO_SYNONYMS", "TURO_GLOSS", "TURO_ARROWS",
	"CLAUDE_CONFIG_DIR", "OPENAI_BASE_URL", "XDG_CONFIG_HOME",
}

// doc accumulates the pass/fail tally while its methods print each line.
type doc struct{ problems int }

func (d *doc) ok(format string, a ...any)   { fmt.Printf("  ✓ "+format+"\n", a...) }
func (d *doc) info(format string, a ...any) { fmt.Printf("  · "+format+"\n", a...) }
func (d *doc) warn(format string, a ...any) { fmt.Printf("  ! "+format+"\n", a...) }
func (d *doc) fail(format string, a ...any) {
	d.problems++
	fmt.Printf("  ✗ "+format+"\n", a...)
}

func (d *doc) section(title string) { fmt.Printf("\n%s\n", title) }

// showDoctor runs every check under cfg (the flags the user actually passed) and
// exits non-zero if any hard problem was found.
func showDoctor(cfg proxyConfig) {
	d := &doc{}
	fmt.Println("turo doctor")

	d.section("turo")
	if version == "dev" {
		d.info("version dev (unreleased build)")
	} else {
		d.ok("version %s", version)
	}
	if exe, err := os.Executable(); err == nil {
		d.info("binary %s", exe)
	} else {
		d.warn("could not resolve binary path: %v", err)
	}
	if validLevel(cfg.level) {
		d.ok("default level %s (%s)", cfg.level, rolesLabel(cfg.all))
	} else {
		d.fail("invalid level %q — use lite, full, ultra, or wenyan", cfg.level)
	}

	d.section("environment")
	anyEnv := false
	for _, k := range doctorEnvVars {
		if v := os.Getenv(k); v != "" {
			d.info("%s=%s", k, v)
			anyEnv = true
		}
	}
	if !anyEnv {
		d.info("no turo env overrides set (using defaults)")
	}

	d.section("gain log")
	gp := gainPath()
	if err := os.MkdirAll(filepath.Dir(gp), 0o755); err != nil {
		d.fail("gain dir not creatable: %v", err)
	} else if f, err := os.OpenFile(gp, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err != nil {
		d.fail("gain log not writable: %s (%v)", shortDir(gp), err)
	} else {
		_ = f.Close()
		n := len(readGain())
		d.ok("writable: %s (%d events)", shortDir(gp), n)
	}

	d.section("claude history (turo discover source)")
	hd := claudeProjectsDir()
	logs := findSessionLogs(hd)
	if len(logs) > 0 {
		d.ok("%d session logs in %s", len(logs), shortDir(hd))
	} else {
		d.info("no history under %s (set CLAUDE_CONFIG_DIR if it lives elsewhere)", shortDir(hd))
	}

	d.section("pipeline self-test")
	const sample = "I would really appreciate it if you could please carefully review the changes"
	out := reduce(sample, cfg.level, 0, cfg.filler, cfg.synonyms, cfg.gloss, cfg.arrows)
	bt, at := estimateTokens(sample), estimateTokens(out)
	if at < bt {
		d.ok("%d -> %d tokens (%s smaller) at level %s", bt, at, pct(bt-at, bt), cfg.level)
	} else {
		d.fail("sample did not reduce (%d -> %d) — pipeline may be misconfigured", bt, at)
	}

	d.section("agents")
	detectedN := 0
	for _, a := range agentRegistry {
		if !detected(a.detect) {
			continue
		}
		detectedN++
		switch a.mech {
		case "native":
			if skillInstalled(a.id) {
				d.ok("%s — detected, skill installed", a.label)
			} else {
				d.warn("%s — detected, skill NOT installed (run turo -install-agents)", a.label)
			}
		default:
			d.info("%s — detected", a.label)
		}
	}
	if detectedN == 0 {
		d.warn("no supported agents detected")
	} else {
		d.info("%d of %d supported agents detected (turo -list-agents shows all)", detectedN, len(agentRegistry))
	}

	fmt.Println()
	if d.problems == 0 {
		fmt.Println("turo is healthy")
		return
	}
	fmt.Printf("%d problem(s) found\n", d.problems)
	os.Exit(1)
}

// skillInstalled reports whether the turo skill file is present in a native
// agent's config dir. Only meaningful for native-mechanism agents (Claude Code,
// opencode); skills/gemini installs leave no local marker turo can check.
func skillInstalled(id string) bool {
	d := configDir(id)
	if d == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(d, "skills", "turo", "SKILL.md"))
	return err == nil
}
