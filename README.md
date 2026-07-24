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

Or **69 characters** with `--level wenyan`, which additionally swaps each
word for a single Classical Chinese character (for CJK-tokenizer models — see
[wenyan](#wenyan-cjk-tokenizer-models-only)):

```
閱引請作察變檔驗碼引退存為要查者增試 untested 破診後證文更映交訊覺安隙 unsanitized 戶入 hardcoded 須標併
```

766 -> 69 characters (35 Han chars). Token counts for that output:

| tokenizer | tokens |
|-----------|--------|
| Qwen / DeepSeek / GLM (~1 per Han char) | **~42** |
| OpenAI cl100k (2-3 per Han char) | 67 |

So wenyan **wins on CJK-tokenizer models** (~42 vs plain `ultra`'s ~71) but
loses on OpenAI (67 vs 41) — use it only with CJK models. See below.

No articles. No prepositions. No adverbs. No repeated words. Only the content
words that carry meaning, deduplicated, in reading order. Every prompt, every
turn — the savings compound. If a reduction is not smaller than the input, turo
passes the original through unchanged.

Install turo once and any coding agent that can shell out to a binary — Claude Code, Codex, Gemini, Cursor, Windsurf, Cline, Copilot, and 20+ more — pipes its context through the same reducer. Code, paths, and identifiers pass through untouched.

## Install

| Method | Command |
|--------|---------|
| **Homebrew** | `brew install kdeps/tap/turo` |
| **Go** | `go install github.com/kdeps/turo@latest` |
| **Shell** | `curl -fsSL https://raw.githubusercontent.com/kdeps/turo/main/install.sh \| sh` |
| **Manual** | Download from [releases](https://github.com/kdeps/turo/releases) |

## Usage

```bash
cat CLAUDE.md | turo              # text -> deduped content words
echo "fox jumps over dog" | turo  # pipe mode
turo -passes 1                    # single pass (default runs to convergence)
turo -filler=false                # skip filler deletion
turo -synonyms=false              # skip the synonym pass (keep words verbatim)
turo -gloss=false                 # skip the defining-word swap (less lossy)
turo -arrows=false                # keep connective phrases verbatim (skip the -> swap)
turo gain                         # estimated tokens saved so far
turo gain --history               # per-reduction history, newest first
turo gain --json                  # same totals as JSON for scripts/dashboards
turo discover                     # tokens turo could save on your Claude Code history
turo discover --json              # discover totals as JSON
turo doctor                       # health check: version, settings, paths, agent wiring
turo --version                    # print version
```

`-gloss` (on by default) replaces each word with the shortest
same-part-of-speech word from its own dictionary definition (`approach` ->
`come`). Definitions are prose, not synonyms, so it is the lossiest stage —
disable it with `-gloss=false` / `TURO_GLOSS=off` when you need words closer to
the original.

`-arrows` (on by default) replaces multi-word causal/sequential connectives
(`leads to`, `results in`, `gives rise to`, `which produces`) with a single `->`
token. Only multi-word phrases qualify, so the swap always saves at least one
token; single-token connectives (`then`, `becomes`, `thus`) are left alone
because `->` costs the same. Disable with `-arrows=false` / `TURO_ARROWS=off`.

```text
A cache miss leads to a slow query which produces a timeout
                     |  arrows (on by default)
Cache miss -> slow query -> timeout
```

## Savings — `turo gain`

Every reduction (pipe mode, the proxy, and `turo run`) appends an estimated
before/after token count to a JSONL log in your OS config dir
(`~/Library/Application Support/turo/` on macOS, `~/.config/turo/` on Linux;
override with `TURO_HOME`). `turo gain` totals it up; `turo gain --history` lists
recent reductions newest-first.

```text
turo gain — 42 reductions
  tokens in     8140
  tokens out    2610
  tokens saved  5530 (67%)

by folder:
  ~/Projects/turo        31 reductions  saved 4100 (69%)
  ~/Projects/api          11 reductions  saved 1430 (61%)
```

Each event also records the working folder it ran in, so `turo gain` breaks the
savings down per project (busiest first) and `turo gain --history` shows the
folder for each reduction.

Add `--json` to either command for the same numbers in machine form — totals,
raw (un-abbreviated) token counts, integer percentages, and the per-folder
breakdown — so you can pipe savings into a dashboard, a CI check, or `jq`:

```console
$ turo gain --json | jq '.tokens_saved, .saved_pct'
5530
67
```

Counts are estimates from the built-in `cl100k`-style approximation, not a real
tokenizer — treat them as a trend, not a bill.

## Missed savings — `turo discover`

`turo gain` totals reductions that happened. `turo discover` shows the ones that
didn't: it scans your existing Claude Code history and estimates how many tokens
turo would have saved had those sessions run through the proxy.

```text
turo discover — scanned 403 sessions in ~/.claude/projects
  messages       53011 reducible (all roles)
  tokens in      13524093
  would be out   6246528
  would save     7277565 (53%)

by project:
  ~/Projects/api          29490 msgs  saved 3242664 (49%)
  ~/Projects/web          19943 msgs  saved 3142015 (57%)

these sessions ran without turo — capture the savings next time with:
  turo run claude
```

It reads the per-project session logs under `~/.claude/projects` (set
`CLAUDE_CONFIG_DIR` if Claude Code stores them elsewhere), applying the same role
gating and compression flags (`-level`, `-filler`, `-gloss`, `-arrows`,
`-proxy-all`) as the proxy — so the estimate reflects what `turo run claude`
would actually do. Nothing is sent anywhere; the scan is local and read-only.

## Health check — `turo doctor`

`turo doctor` runs a local health check and exits non-zero if anything is
broken — useful in CI, install scripts, or after an upgrade to confirm turo is
wired up correctly.

```text
turo doctor

turo
  · version dev (unreleased build)
  · binary /usr/local/bin/turo
  ✓ default level ultra (all roles)

environment
  · no turo env overrides set (using defaults)

gain log
  ✓ writable: ~/Library/Application Support/turo/gain.jsonl (135 events)

claude history (turo discover source)
  ✓ 414 session logs in ~/.claude/projects

pipeline self-test
  ✓ 22 -> 5 tokens (77% smaller) at level ultra

agents
  ✓ Claude Code — detected, skill installed
  ✓ opencode — detected, skill installed
  · Gemini CLI — detected
  · Cursor — detected
  · Qwen Code — detected
  · 5 of 18 supported agents detected (turo -list-agents shows all)

turo is healthy
```

What it checks:

| Section | What it verifies |
|---------|-----------------|
| **turo** | Version string, binary path, default level validity |
| **environment** | Lists any `TURO_*` env overrides that are set |
| **gain log** | Gain directory creatable, log file writable, event count |
| **claude history** | Session logs found under `~/.claude/projects` |
| **pipeline self-test** | Runs `reduce()` on a sample sentence, confirms token count decreased |
| **agents** | Detects installed coding agents, checks skill installation for native agents |

Pass `-level <name>` to test a specific level; an invalid level is reported as a
problem (✗) rather than a hard exit, so the full report is still visible.

```bash
turo doctor               # healthy -> exit 0
turo -level bogus doctor  # invalid level -> exit 1
```

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

**Arrows** (on by default, runs before reduction) rewrites multi-word connective
phrases to `->`, which the reducer keeps verbatim between the surviving content
words. Disable with `-arrows=false` / `TURO_ARROWS=off`.

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
| **full** | Adjectives, nouns, verbs | ~70% |
| **ultra** (default) | Nouns and verbs only, deduplicated by lemma (base form) | ~70%+ |
| **wenyan** | ultra, then swap surviving words for a single 文言 (Classical Chinese) character | CJK models only |

```bash
echo "the quick brown fox jumps over the lazy dog" | turo --level lite   # quick brown fox jumps over lazy dog
echo "the quick brown fox jumps over the lazy dog" | turo --level full   # quick brown fox jumps lazy dog
echo "the quick brown fox jumps over the lazy dog" | turo --level ultra  # fox jump dog
echo "the wise king studies the old book" | turo --level wenyan    # 智王學舊書
```

### wenyan (CJK-tokenizer models only)

`wenyan` reduces at ultra, then swaps each surviving English content word
for one Classical Chinese character (`water` -> `水`, `king` -> `王`, `verify` ->
`驗`) from a ~380-entry hand-curated lexicon. One char per concept, no spaces
(Classical Chinese has none).

Two examples, measured:

| input | ultra | wenyan | chars | cl100k | CJK-model (~1/char) |
|-------|-------|--------------|-------|--------|----------------------|
| `The wise king uses water and fire...` (80 ch) | `Wise king use water fire person see mountain old tree` | `智王用水火人見山舊樹` | **10** | 15 | **~10** |
| the PR-review paragraph (766 ch) | 283 ch / 41 tok | `閱引請作察變檔驗碼引退存為要查者增試 untested 破診後證文更映交訊覺安隙 unsanitized 戶入 hardcoded 須標併` | **69** | 67 | **~42** |

It collapses to the fewest **characters** (766 -> 69 on the paragraph). A CJK
character is 2-3 tokens on OpenAI's cl100k, so `wenyan` is *larger* there
(67 > 41). **It only wins on CJK-optimized tokenizers** (Qwen, DeepSeek, GLM),
where a common character is ~1 token — then those 69 chars are ~42 tokens vs
plain ultra's 71. Don't use it with OpenAI models.

turo's own token estimator counts CJK as 1 rune = 1 token (matching those
models), so it treats `wenyan` as a reduction and never rejects it. Words
outside the lexicon stay English (`untested`, `unsanitized`, `hardcoded`
above); code/paths/URLs are preserved verbatim. Extend `wenyanMap` for more
coverage.

In **ultra**, inflections of the same word collapse to one token by their
dictionary base form: `goes`, `went`, `going` -> `go`; `children` -> `child`;
`servers` -> `server`. A reduction is only applied when it lands on a real
dictionary word, so no mangled non-words are ever emitted.

Set default via `TURO_LEVEL` env var.

## Proxy — reduce every request for any agent

To compress **all** input for an agent that turo can't reach from the inside
(Claude Code, Codex, ...), route the agent's requests through turo.

### `turo run` — launch an agent with everything reduced (turnkey)

```bash
turo run claude          # every claude request reduced, base URL wired for you
turo run codex           # OPENAI_BASE_URL wired instead
turo run                 # list supported agents
```

`turo run` starts an in-process proxy on a free port, points the agent's
base-URL env var at it (`ANTHROPIC_BASE_URL` for claude, `OPENAI_BASE_URL` for
OpenAI-compatible agents), execs the agent, and stops the proxy when it exits.
One command, no exports, no `/turo` inside the agent. Supported:
`claude`, `codex`, `opencode`, `qwen`, `aider`, `crush`, `goose`, `amp`.

### `turo -proxy` — the proxy on its own

```bash
turo -proxy -upstream https://api.openai.com   # silent by default, listens on 127.0.0.1:8787
turo -proxy -proxy-verbose                      # print activity: token summary + before -> after text
turo -proxy -proxy-all=false                    # reduce only user + tool, not every role
export OPENAI_BASE_URL=http://127.0.0.1:8787/v1
```

Every `/chat/completions` (and Anthropic `/messages`) request has its message
content reduced before it reaches the real endpoint; the response streams back
untouched. By default **every role** is reduced (`-proxy-all` is on); pass
`-proxy-all=false` to reduce only `user` and `tool` content and leave system and
assistant history verbatim. Auth headers pass through; non-chat paths are
forwarded unchanged.

The proxy is **silent by default**. Pass `-proxy-verbose` to print its activity:
the estimated `before -> after` token count per request plus each message's text
before and after (truncated for the terminal). The flag also applies to
`turo run <agent>`.

kdeps does not need this: in agent mode it already pipes the preamble, input,
tool results, and history through turo before every call.

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
cat CLAUDE.md | turo               # compact system prompt
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
