---
name: turo
description: >
  Stream editor that reduces prose to fewer tokens. Pipes text (CLAUDE.md,
  README, memory, instructions) through the turo binary and injects the reduced
  output as the system prompt. Deletes filler, swaps token-cheaper synonyms and
  glosses, strips stopwords, deduplicates, and lemmatizes — keeping meaning-
  bearing words. Use when user says "use turo", "compact context", "reduce
  tokens", or invokes /turo. Auto-triggers when token budget is tight.
---

turo is a stream editor: prose in, a compact token-reduced stream out. It keeps
the nouns, verbs, and adjectives that carry meaning — in reading order, no
arrows, no emoji (both cost tokens).

## Trigger

`/turo` or `cat file.md | turo` or `turo file.md`

## Pipeline (all stages on by default, repeated until output stops shrinking)

1. **Filler** — delete pleasantries, hedges, leaders, articles (`please`,
   `I think`, `of course`), protecting code/paths/URLs verbatim.
2. **Synonyms** — swap each word for a fewer-token WordNet synonym
   (`utilize` -> `use`). Lossy; `-synonyms=false` to skip.
3. **Gloss** — swap each word for the shortest word in its dictionary
   definition (`approach` -> `come`). Lossiest; `-gloss=false` to skip.
4. **Reduce** — drop stopwords, keep content words by part of speech,
   deduplicate, and (ultra) collapse inflections by lemma.

Passes through the original unchanged if the reduced form is not smaller.

## Output format

Input:

```
the quick brown fox jumps over the lazy dog
```

Output (`--level full -synonyms=false -gloss=false`):

```
quick brown fox jumps lazy dog
```

~70% fewer input tokens on real docs.

## Levels

| Level | Keeps | Command |
|-------|-------|---------|
| lite  | adj, noun, verb, leftover adverbs/preps | `turo --level lite` |
| full  | adj, noun, verb | `turo --level full` |
| ultra | nouns + verbs, deduped by lemma (base form) — **default** | `turo --level ultra` |

## Flags

| Flag | Effect |
|------|--------|
| `--preamble` | wrap output in a tagged block for system-prompt injection |
| `-passes N` | cap reduction passes (0 = run to convergence, the default) |
| `-filler=false` / `-synonyms=false` / `-gloss=false` | skip a lossy stage |
| `-install-agents` / `-list-agents` | register the skill with detected agents |

`TURO_LEVEL`, `TURO_FILLER`, `TURO_SYNONYMS`, `TURO_GLOSS` set the defaults.

## Boundaries

- Compresses prompt input only — does not modify agent behavior
- Never emits output larger than the input (passes the original through)
- Falls through silently if the turo binary is not on PATH
- `KDEPS_TURO=off` or `TURO_DISABLED=1` disables
