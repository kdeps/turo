---
name: turo
description: >
  Stream editor that reduces prose to its content words to cut input tokens.
  Pipes text (CLAUDE.md, README, memory, instructions) through the turo binary
  and injects the reduced output as the system prompt. Strips stopwords,
  deduplicates, and keeps only meaning-bearing words. Use when user says "use
  turo", "compact context", "reduce tokens", or invokes /turo. Auto-triggers
  when token budget is tight.
---

turo is a stream editor: prose in, a compact stream of deduplicated content
words out. It strips articles, prepositions, conjunctions, pronouns, and
repeated words, keeping only the nouns, verbs, and adjectives that carry
meaning — in reading order, no arrows, no emoji (both cost tokens).

## Trigger

`/turo` or `cat file.md | turo` or `turo file.md`

## What it does

1. Reads input text (CLAUDE.md, memory, instructions, tool guidance, etc.)
2. Classifies words using an embedded English dictionary (120k words)
3. Keeps the content words for the level, deduplicated, in reading order
4. Passes the original through unchanged if the reduced form is not smaller

## Output format

Input:

```
the quick brown fox jumps over the lazy dog
```

Output (full):

```
quick brown fox jumps lazy dog
```

Zero filler words, no repeats. ~70% fewer input tokens on real docs.

## Levels

| Level | Keeps | Command |
|-------|-------|---------|
| lite  | adj, noun, verb, leftover adverbs/preps | `turo --level lite` |
| full  | adj, noun, verb (default) | `turo --level full` |
| ultra | nouns + verbs, deduped by stem | `turo --level ultra` |

## Modes

| Mode | Command | Use |
|------|---------|-----|
| Stream | `cat file \| turo` | Reduce text to content words |
| Preamble | `turo --preamble` | Wrap output for system prompt injection |

## Boundaries

- Only compresses system prompt input — does not modify agent behavior
- Never emits output larger than the input (passes original through)
- Falls through silently if the turo binary is not on PATH
- `KDEPS_TURO=off` or `TURO_DISABLED=1` disables
