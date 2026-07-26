#!/usr/bin/env python3
# Generates defs_data.go: the phrase -> headword index behind turo's -defmatch
# stage. This is the reverse of gengloss.py: instead of replacing a word with a
# word from its definition, it replaces a phrase that *looks like* a definition
# with the headword it defines ("a person who writes professionally" ->
# "author").
#
# The gloss source is WordNet, not dictionary.csv. Webster 1913 leads with
# historical senses ("game" is defined as "lame", "currency" as "fluency"), and
# no frequency gate can repair a signature that describes an archaic reading.
# WordNet's glosses are modern and its senses are ordered by observed use, so
# the dominant sense is the one that gets indexed.
#
# For each WordNet lemma:
#   - the headword must be a single alphabetic word, >= MIN_HEADWORD_LEN
#     letters, not a stopword, and common (Zipf >= MIN_HEADWORD_ZIPF). The
#     frequency floor is the quality guard: swapping a phrase for "abaciscus"
#     is worse than leaving it alone.
#   - the gloss of its dominant sense is reduced to a keyword signature:
#     lowercase alpha words of >= MIN_KEYWORD_LEN letters, stopwords dropped,
#     deduped, each common enough (Zipf >= MIN_KEYWORD_ZIPF) to survive the
#     reducer's earlier stages. Only signatures of SIG_MIN..SIG_MAX keywords
#     are kept: one keyword matches everything, five never match.
#   - the signature must cost >= MIN_TOKEN_SAVING more cl100k tokens than the
#     headword, else the swap is not worth making.
#
# Headword ids are emitted in frequency order (most common first), so the
# runtime tie-break "lowest id wins" prefers the more common word.
#
# Requires: pip install nltk wordfreq tiktoken
#           python -c "import nltk; nltk.download('wordnet'); nltk.download('omw-1.4')"
# Run:      python3 tools/gendefs.py

import re
import sys

try:
    import tiktoken
    from nltk.corpus import wordnet as wn
    from wordfreq import zipf_frequency
except ImportError as e:
    sys.exit(f"missing dependency: {e}\n"
             "install: pip install nltk wordfreq tiktoken\n"
             "then:    python -c \"import nltk; nltk.download('wordnet'); nltk.download('omw-1.4')\"")

enc = tiktoken.get_encoding("cl100k_base")
tok = lambda s: len(enc.encode(s))
alpha = re.compile(r"[a-z]+")
word_re = re.compile(r"^[a-z]+$")
paren = re.compile(r"\([^)]*\)")

MIN_HEADWORD_LEN = 3
MIN_HEADWORD_ZIPF = 3.5   # the replacement is all that's left; keep it common
MIN_KEYWORD_LEN = 3
MIN_KEYWORD_ZIPF = 2.5    # keywords are matched against, not written out
SIG_MIN, SIG_MAX = 2, 4   # usable signature size
MIN_TOKEN_SAVING = 2      # signature must cost this many more tokens than word

# Function words carry no matching signal; the runtime drops the same set from
# each window before looking up candidates.
STOPWORDS = frozenset("""
a an the and or but nor for yet so as if than then that this these those there
here it its it's he she they them his her their our your my me we us you i
is am are was were be been being become becomes became
have has had having do does did doing done
will would shall should can could may might must ought
of in on at to from by with without within into onto upon over under above
below between among through during before after since until while about across
against along around behind beside besides beyond down off out near per
not no any some each every all both few more most other another such own same
one two who whom whose which what when where why how
also very much many said says say thus hence therefore
used using use way like unto etc esp cf viz
""".split())

# Negation is invisible to the matcher: "not" and friends are stopwords, so the
# gloss of "unlocked" ("not firmly fastened or secured") would reduce to a
# signature meaning its own opposite. Glosses carrying any of these are dropped.
NEGATIONS = frozenset("""
not no never none nothing without lacking lack lacks nor cannot non
""".split())


def dominant_gloss(word):
    """Gloss of the sense of `word` that WordNet sees used most often."""
    best = None  # (count, -index) -> gloss
    for i, syn in enumerate(wn.synsets(word)):
        count = max((l.count() for l in syn.lemmas() if l.name().lower() == word),
                    default=0)
        key = (count, -i)
        if best is None or key > best[0]:
            best = (key, syn.definition())
    return best[1] if best else None


def signature(gloss, word, zipf):
    """Keyword signature of a gloss, or None if it is unusable."""
    # Drop parentheticals ("(of a person)", "(for pay)") and examples after the
    # first semicolon: both are asides, not part of the definition proper.
    text = paren.sub(" ", gloss.split(";", 1)[0]).lower()
    words = alpha.findall(text)
    if any(k in NEGATIONS for k in words):
        return None
    sig, have = [], set()
    for k in words:
        if len(k) < MIN_KEYWORD_LEN or k == word or k in have or k in STOPWORDS:
            continue
        if zipf(k) < MIN_KEYWORD_ZIPF:
            continue
        have.add(k)
        sig.append(k)
        if len(sig) > SIG_MAX:
            return None
    if not SIG_MIN <= len(sig) <= SIG_MAX:
        return None
    if tok(" ".join(sig)) - tok(word) < MIN_TOKEN_SAVING:
        return None
    return sorted(sig)


def main():
    zipf_cache = {}

    def zipf(w):
        z = zipf_cache.get(w)
        if z is None:
            z = zipf_cache[w] = zipf_frequency(w, "en")
        return z

    entries = []  # (-zipf, word, sig)
    for name in wn.all_lemma_names():
        # Proper nouns are excluded: their dominant sense is a coin flip
        # ("Alexandria" is a town in Louisiana here, not the Egyptian city),
        # and a confidently wrong place name reads worse than no match.
        if name[:1].isupper():
            continue
        w = name.lower()
        if not word_re.match(w) or len(w) < MIN_HEADWORD_LEN or w in STOPWORDS:
            continue
        zw = zipf(w)
        if zw < MIN_HEADWORD_ZIPF:
            continue
        gloss = dominant_gloss(w)
        if not gloss:
            continue
        sig = signature(gloss, w, zipf)
        if sig:
            entries.append((-zw, w, sig))

    entries.sort()  # most common headword first => lowest id

    index = {}
    for i, (_z, _w, sig) in enumerate(entries):
        for k in sig:
            index.setdefault(k, []).append(i)

    with open("defs_data.go", "w") as out:
        out.write("// Code generated by tools/gendefs.py; DO NOT EDIT.\n")
        out.write("// Keyword signatures of WordNet's dominant-sense glosses,\n")
        out.write("// frequency-filtered (wordfreq) and measured with the\n")
        out.write("// cl100k_base tokenizer.\n\n")
        out.write("package main\n\n")
        out.write("// defWords holds the headwords, most common first, so the\n")
        out.write("// lowest matching id is the most common candidate.\n")
        out.write("var defWords = []string{\n")
        for _z, w, _s in entries:
            out.write('\t"%s",\n' % w)
        out.write("}\n\n")
        out.write("// defSigs holds each headword's sorted gloss keywords.\n")
        out.write("var defSigs = [][]string{\n")
        for _z, _w, sig in entries:
            out.write("\t{%s},\n" % ", ".join('"%s"' % k for k in sig))
        out.write("}\n\n")
        out.write("// defIndex maps a gloss keyword to the headword ids whose\n")
        out.write("// signature contains it, ascending.\n")
        out.write("var defIndex = map[string][]uint32{\n")
        for k in sorted(index):
            out.write('\t"%s": {%s},\n'
                      % (k, ", ".join(str(i) for i in index[k])))
        out.write("}\n\n")
        out.write("// defStopWords is the exact set this generator dropped when\n")
        out.write("// building signatures. The matcher drops it from each window\n")
        out.write("// too: a keyword the two sides disagree about could never be\n")
        out.write("// matched, so both read it from here.\n")
        out.write("var defStopWords = map[string]bool{\n")
        for k in sorted(STOPWORDS):
            out.write('\t"%s": true,\n' % k)
        out.write("}\n")

    postings = sum(len(v) for v in index.values())
    print("wrote defs_data.go with %d headwords, %d keywords, %d postings"
          % (len(entries), len(index), postings))


if __name__ == "__main__":
    main()
