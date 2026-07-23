<p align="center">
  <img src="docs/logo.png" alt="turo" width="480">
</p>

<p align="center">
  <strong>Point more. Token less.</strong>
</p>

A real instruction block — **138 tokens** (cl100k):

```
When you are reviewing a pull request, please make sure that you carefully
examine each of the changed files and verify that the new code does not
introduce any regressions in the existing behavior. It is really important that
you check whether the author has added appropriate tests for the new
functionality, because untested code is very likely to break in subtle ways that
are difficult to debug later. You should also confirm that the documentation has
been updated to reflect the changes, and that the commit messages clearly
explain what was changed and why. If you notice any potential security
vulnerabilities, such as unsanitized user input or hardcoded credentials, you
must flag them immediately and request changes before the pull request can be
merged.
```

becomes **54 tokens — 61% fewer** (`turo`, meaning intact):

```
Reviewing pull request make examine changed files verify new code introduce
regressions existing behavior important check author added appropriate tests
functionality untested break subtle ways difficult debug later also confirm
documentation updated reflect changes commit messages explain notice potential
security vulnerabilities unsanitized user input hardcoded credentials must flag
merged
```

or **41 tokens — 70% fewer** at `--level ultra` (deduped by lemma):

```
Review pull request make examine change file verify code introduce regression
exist behavior important check author add test untested break debug later
confirm documentation updated reflect commit message notice security
vulnerability unsanitized user input hardcoded must flag merge
```

No articles. No prepositions. No adverbs. No repeated words. Only the content
words that carry meaning, deduplicated, in reading order. Every prompt, every
turn — the savings compound. If a reduction is not smaller than the input, turo
passes the original through unchanged.

Install turo once and any coding agent that can shell out to a binary — Claude Code, Codex, Gemini, Cursor, Windsurf, Cline, Copilot, and 20+ more — pipes its context through the same reducer. Code, paths, and identifiers pass through untouched.

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
cat CLAUDE.md | turo              # text -> deduped content words
echo "fox jumps over dog" | turo  # pipe mode
turo --preamble                   # wrap for system prompt injection
turo -passes 1                    # single pass (default runs to convergence)
turo -filler=false                # skip filler deletion
turo -synonyms=false              # skip the synonym pass (keep words verbatim)
turo -gloss=false                 # skip the defining-word swap (less lossy)
turo --version                    # print version
```

`-gloss` (on by default) replaces each word with the shortest
same-part-of-speech word from its own dictionary definition (`approach` ->
`come`). Definitions are prose, not synonyms, so it is the lossiest stage —
disable it with `-gloss=false` / `TURO_GLOSS=off` when you need words closer to
the original.

## Pipeline

Every run is four stages, each on by default:

```text
text -> [1] delete filler -> [2] swap cheaper synonyms -> [3] swap defining words -> [4] reduce to content words
```

1. **Filler deletion** removes pleasantries, hedges, and leaders that survive
   word-level stopword lists (`please`, `I think`, `of course`, `let me`),
   while protecting code, paths, URLs, and identifiers verbatim. Disable with
   `-filler=false` / `TURO_FILLER=off`.
2. **Synonym swap** replaces words with a fewer-token synonym (see below).
   Disable with `-synonyms=false` / `TURO_SYNONYMS=off`.
3. **Gloss swap** replaces words with the shortest defining word from their
   dictionary definition — the lossiest stage. Disable with `-gloss=false` /
   `TURO_GLOSS=off`.
4. **Reduction** drops the remaining stopwords, keeps content words by part of
   speech, deduplicates, and (ultra) collapses inflections by lemma.

The whole pipeline repeats until the output stops changing (`-passes 0`, the
default; a positive `-passes N` caps the count). The first pass keeps document
structure (headings, per-section bodies); later passes flatten that and dedupe
across it, so large structured docs keep shrinking before converging — this
README goes 522 (1 pass) -> 376 tokens (converged, ~29 passes). Set `-passes 1`
to keep it single-shot.

turo never emits output larger than the input: if a stage does not save tokens,
the text passes through unchanged.

### Synonym substitution (on by default, lossy)

turo runs a first pass that replaces each word with a fewer-token synonym before
reducing (`utilize` -> `use`, `demonstrate` -> `show`). The table is built from
**WordNet synsets** (real synonyms) and frequency-filtered so swaps land on
common words, then gated to same-part-of-speech words and validated to cost
strictly fewer tokens. turo still passes the original through if the result is
not smaller.

Disable it with `-synonyms=false` or `TURO_SYNONYMS=off` when you need words
verbatim. The gain is usually small — modern tokenizers
already encode most words as a single token. WordNet polysemy also leaves some
noise (`leverage` -> `purchase`), so keep it opt-in for prose, not code. The
table is generated by `tools/gensyn.py` (WordNet + wordfreq + the cl100k
tokenizer), so token counts are measured for cl100k, not the target model's
tokenizer.

## Intensity levels

| Level | What it keeps | Reduction |
|-------|--------------|-----------|
| **lite** | Adjectives, nouns, verbs, and leftover adverbs/prepositions | ~65% |
| **full** (default) | Adjectives, nouns, verbs | ~70% |
| **ultra** | Nouns and verbs only, deduplicated by lemma (base form) | ~70%+ |

```bash
echo "the quick brown fox jumps over the lazy dog" | turo --level lite   # quick brown fox jumps over lazy dog
echo "the quick brown fox jumps over the lazy dog" | turo --level full   # quick brown fox jumps lazy dog
echo "the quick brown fox jumps over the lazy dog" | turo --level ultra  # fox jump dog
```

In **ultra**, inflections of the same word collapse to one token by their
dictionary base form: `goes`, `went`, `going` -> `go`; `children` -> `child`;
`servers` -> `server`. A reduction is only applied when it lands on a real
dictionary word, so no mangled non-words are ever emitted.

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
cat error.log | turo               # reduce log output
```

Set `TURO_LEVEL=ultra` for maximum compression. `KDEPS_TURO=off` or `TURO_DISABLED=1` to disable.

## What it does NOT touch

- Code blocks and inline code — passed through unchanged
- URLs, file paths, version numbers — verbatim
- Technical terms (API names, CLI commands, error strings) — exact

## How it works

1. Embedded English dictionary (120k words, 14MB) classifies every word
2. Strips articles, prepositions, conjunctions, pronouns (~70 stop words)
3. Keeps the content words for the level (nouns, verbs, adjectives)
4. Deduplicates and emits them in reading order — then keeps the result only if it is actually smaller than the input

## turo vs caveman

[caveman](https://github.com/JuliusBrussee/caveman) is a sibling idea with the
opposite dial. caveman deletes filler with regex (articles, pleasantries,
hedges) and **keeps readable prose**. turo runs that same filler pass, then
keeps going — POS-classifying, deduplicating, lemmatizing, and swapping words
for shorter synonyms and glosses — trading readability for a much smaller token
count.

Same input, measured with the cl100k tokenizer:

| | output | tokens |
|--|--------|--------|
| input | `Please, I think you should really just utilize this approach to demonstrate the functionality of the component.` | 19 |
| caveman | `You should utilize this approach to demonstrate functionality of component.` | 11 |
| turo full | `Use come show functionality component` | 5 |
| turo ultra | `Use come show component` | 4 |

|  | caveman | turo |
|--|---------|------|
| method | regex filler removal | dict POS + dedup + lemma + synonyms + gloss |
| output | readable prose | keyword stream |
| dictionary / WordNet | no | yes |
| synonym / gloss swaps | no | yes (on by default) |
| best when | you still need to read it | you only feed it to an LLM |

Use caveman when a human reads the result; use turo when only a model does.

## Why

System prompts are 50-200k tokens. Most of those words carry grammar, not meaning. Turo points at what matters and drops the rest.

Point more. Token less.
