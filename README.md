# turo

**prose in, graph out**

Stream editor that converts text to compact kartographer graphs. Pipe in CLAUDE.md, README, memory, instructions — turo strips the filler and outputs arrow-chain relationships. 60-80% fewer tokens. Same substance.

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

## Install

| Method | Command |
|--------|---------|
| **npx** | `npx turo` |
| **Homebrew** | `brew install kdeps/tap/turo` |
| **Go** | `go install github.com/kdeps/turo@latest` |
| **Manual** | Download from [releases](https://github.com/kdeps/turo/releases) |

## Usage

```bash
cat CLAUDE.md | turo              # text → graph
echo "fox jumps over dog" | turo  # pipe mode
turo --scan .                     # directory tree from kartographer index
turo --preamble                   # wrap for system prompt injection
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

## Integration with kdeps

When turo is on PATH, kdeps routes ALL system prompt text through turo automatically. Memory, skills, instructions, tool guidance — everything becomes a compact graph. Tool outputs too.

```bash
TURO_LEVEL=ultra kdeps   # maximum compression
KDEPS_TURO=off kdeps     # disable, use normal text
```

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

System prompts are 50-200k tokens of prose. Most of those words don't carry information — they carry grammar. Turo extracts the structural relationships and drops the scaffolding.

One graph. No words. Same meaning.
