---
name: turo
description: >
  Stream editor that converts prose to compact kartographer graphs. Pipes text
  (CLAUDE.md, README, memory, instructions) through turo binary and injects the
  graph output as the system prompt. Cuts input tokens by replacing verbose text
  with arrow-chain graphs. Use when user says "graph mode", "use turo", "compact
  context", or invokes /turo. Auto-triggers when token budget is tight.
---

turo is a stream editor: text in, graph out. It reads prose and outputs a
kartographer-style directed graph — adjectives point to nouns, nouns to verbs,
verbs to objects. All filler words stripped. Only structural relationships remain.

## Trigger

`/turo` or `cat file.md | turo` or `turo file.md`

## What it does

1. Reads input text (CLAUDE.md, memory, instructions, tool guidance, etc.)
2. Classifies words using embedded English dictionary (120k words)
3. Outputs directed graph edges: `adj → noun`, `noun → verb`, `verb → object`
4. Strips articles, prepositions, conjunctions, adverbs — structure only

## Output format

```
quick → fox
brown → fox
fox → jumps
jumps → dog
lazy → dog
```

Each line is a kartographer edge. Zero filler words. ~60-80% fewer tokens than
equivalent prose.

## Modes

| Mode | Command | Use |
|------|---------|-----|
| Stream | `cat file \| turo` | Convert text to graph |
| Preamble | `turo --preamble` | Wrap graph for system prompt injection |

## Boundaries

- Only compresses system prompt input — does not modify agent behavior
- Falls through silently if turo binary not on PATH
- `KDEPS_TURO=off` or `TURO_DISABLED=1` disables
