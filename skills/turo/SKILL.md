---
name: turo
description: >
  Stream editor that reduces prose to fewer tokens. Pipes text (CLAUDE.md,
  README, memory, instructions) through the turo binary and injects the reduced
  output as the system prompt. Deletes filler, collapses definition-like phrases
  to the word they define, swaps token-cheaper glosses and synonyms, strips
  stopwords, deduplicates, and lemmatizes — keeping meaning-bearing words. Use
  when user says "use turo", "compact context", "reduce tokens", or invokes
  /turo. Auto-triggers when token budget is tight.
---

turo is a stream editor: prose in, a compact token-reduced stream out. It keeps
the nouns, verbs, and adjectives that carry meaning — in reading order, no emoji
(they cost tokens). Multi-word causal connectives become a single `->` token.

## Trigger

`/turo` or `cat file.md | turo` or `turo file.md`

## Pipeline (all stages on by default, repeated until output stops shrinking)

Phrase-level stages run before word-level ones: a phrase matcher has to see the
whole phrase, and one word swap inside it loses the match.

1. **Arrows** — multi-word causal/sequential connectives (`leads to`,
   `results in`) -> `->`. `-arrows=false` to skip.
2. **Filler** — delete pleasantries, hedges, leaders, articles (`please`,
   `I think`, `of course`), protecting code/paths/URLs verbatim.
3. **Defmatch** — collapse a definition-like phrase into the word it defines
   (`the state of disorder and lawlessness` -> `anarchy`). Replaces only when
   every definition keyword is present and the headword is strictly cheaper;
   makes zero replacements on technical text. `-defmatch=false` to skip.
4. **Gloss** — swap each word for the shortest word in its dictionary
   definition (`approach` -> `come`). Lossiest; `-gloss=false` to skip.
5. **Synonyms** — swap each word for a fewer-token WordNet synonym
   (`utilize` -> `use`). Lossy; `-synonyms=false` to skip. Words defmatch just
   produced are held back from stages 4-5, which would otherwise walk the match
   back (`anarchy` -> `law`, inverting it).
6. **Reduce** — drop stopwords, keep content words by part of speech,
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
| `-filler=false` / `-gloss=false` / `-synonyms=false` | skip a lossy stage |
| `-defmatch=false` / `-arrows=false` | keep definition-like phrases / connectives |
| `-install-agents` / `-list-agents` | register the skill with detected agents |
| `gain` / `gain --history` | report estimated tokens saved across reductions |

`TURO_LEVEL`, `TURO_FILLER`, `TURO_SYNONYMS`, `TURO_GLOSS`, `TURO_DEFMATCH`,
`TURO_ARROWS` set the defaults.

## Boundaries

- Compresses prompt input only — does not modify agent behavior
- Never emits output larger than the input (passes the original through)
- Falls through silently if the turo binary is not on PATH
- `KDEPS_TURO=off` or `TURO_DISABLED=1` disables
