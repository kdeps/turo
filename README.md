<p align="center">
  <img src="docs/logo.png" alt="turo" width="480">
</p>

<p align="center">
  <strong>Point more. Token less.</strong>
</p>

```
the quick brown fox jumps all over the lazy dog
```

becomes:

```
quick → fox
brown → fox
fox → jumps
jumps → dog
lazy → dog
```

No articles. No prepositions. No adverbs. Just pointers between content words. Each line is a kartographer edge.

Turo is a skill/plugin for Claude Code, Codex, Gemini, Cursor, Windsurf, Cline, Copilot, and 30+ other agents. Install once. Every agent gets the same compact graph format — code, commands, and errors stay byte-for-byte exact. You save input tokens on every turn, forever.

## Install

| Method | Command |
|--------|---------|
| **npx** | `npx turo` |
| **Homebrew** | `brew install kdeps/tap/turo` |
| **Go** | `go install github.com/kdeps/turo@latest` |
| **Shell** | `curl -fsSL https://raw.githubusercontent.com/kdeps/turo/main/install.sh | sh` |
| **Manual** | Download from [releases](https://github.com/kdeps/turo/releases) |

## Usage

```bash
cat CLAUDE.md | turo              # text → graph
echo "fox jumps over dog" | turo  # pipe mode
turo --max-depth 3                # cap transitive edge depth
turo --preamble                   # wrap for system prompt injection
turo --version                    # print version
```

## Intensity levels

| Level | What it keeps | Reduction |
|-------|--------------|-----------|
| **lite** | All content words (adj, noun, verb, adv, prep) | ~40% |
| **full** (default) | Adj, noun, verb — kartographer edges | ~60% |
| **ultra** | Adj, noun, verb — single deduplicated chain | ~80% |

```bash
echo "fox jumps over lazy dog" | turo --level lite   # fox → jumps → over → lazy → dog
echo "fox jumps over lazy dog" | turo --level full   # fox→jumps, lazy→dog
echo "fox jumps over lazy dog" | turo --level ultra  # fox → jumps → lazy → dog
```

Set default via `TURO_LEVEL` env var.

## Integration

`npx turo` installs the binary **and** registers the turo skill + `/turo`
command with every coding agent it finds on your machine — Claude Code, Gemini
CLI, opencode, Codex, Cursor, Windsurf, Cline, Copilot, and 20+ more. Install
once; every agent gets the same reducer.

```bash
npx turo                 # binary + register with detected agents
npx turo --list          # show every supported agent and its status
npx turo --only claude   # register with one agent
npx turo --all           # register with every supported agent
npx turo --no-binary     # register agents only (binary already installed)
npx turo --uninstall     # remove binary + registered skills
```

Under the hood each agent gets one of:

- **Claude Code / opencode** — the skill and `/turo` command are copied into the agent's config dir
- **Gemini CLI** — `gemini extensions install`
- **everything else** — `npx skills add kdeps/turo --skill turo -a <profile>`

Once turo is on PATH, any agent can also pipe context through it directly:

```bash
cat CLAUDE.md | turo --preamble    # compact system prompt
cat error.log | turo               # graph from log output
```

Set `TURO_LEVEL=ultra` for maximum compression. `KDEPS_TURO=off` or `TURO_DISABLED=1` to disable.

## What it does NOT touch

- Code blocks and inline code — passed through unchanged
- URLs, file paths, version numbers — verbatim
- Technical terms (API names, CLI commands, error strings) — exact

## How it works

1. Embedded English dictionary (120k words, 14MB) classifies every word
2. Strips articles, prepositions, conjunctions, pronouns (~70 stop words)
3. Builds directed edges: adj→noun, noun→verb, verb→noun
4. Outputs kartographer graph

## Why

System prompts are 50-200k tokens. Most of those words carry grammar, not meaning. Turo points at what matters and drops the rest.

Point more. Token less.
