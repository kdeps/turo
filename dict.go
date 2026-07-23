// dict.go — word classification via embedded English dictionary.
//
// Source: English-Dictionary-Open-Source (csv/dictionary.csv)
// Format: word,wordtype,definition
// Word types: n. (noun), a. (adjective), v./v. t./v. i. (verb), adv. (adverb)
package main

import (
	"bufio"
	_ "embed"
	"strings"
	"sync"
)

//go:embed dictionary.csv
var dictCSV string

// wordClass maps a word to its primary part of speech.
type wordClass uint8

const (
	classUnknown wordClass = iota
	classNoun
	classVerb
	classAdj
	classAdv
	classPrep
)

//nolint:gochecknoglobals // loaded once at first use
var (
	dictOnce sync.Once
	dict     map[string]wordClass
)

func loadDict() map[string]wordClass {
	dictOnce.Do(func() {
		dict = make(map[string]wordClass)
		scanner := bufio.NewScanner(strings.NewReader(dictCSV))
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			word, class := parseDictLine(scanner.Text())
			if word != "" && class != classUnknown {
				if _, exists := dict[word]; !exists {
					dict[word] = class
				}
			}
		}
	})
	return dict
}

func parseDictLine(line string) (string, wordClass) {
	// Format: word,wordtype,definition
	// Skip the definition (third field) — it can contain commas.
	comma1 := strings.IndexByte(line, ',')
	if comma1 < 0 {
		return "", classUnknown
	}
	word := strings.ToLower(strings.TrimSpace(line[:comma1]))
	rest := line[comma1+1:]
	comma2 := strings.IndexByte(rest, ',')
	if comma2 < 0 {
		return "", classUnknown
	}
	wordType := strings.TrimSpace(rest[:comma2])

	switch {
	case strings.HasPrefix(wordType, "v.") ||
		strings.HasPrefix(wordType, "p. p.") ||
		strings.HasPrefix(wordType, "imp.") ||
		strings.HasPrefix(wordType, "p. pr."):
		return word, classVerb
	case wordType == "a." || wordType == "superl.":
		return word, classAdj
	case wordType == "adv." || wordType == "adv":
		return word, classAdv
	case wordType == "prep." || wordType == "conj.":
		return word, classPrep
	case wordType == "n." || strings.HasPrefix(wordType, "n. pl"):
		return word, classNoun
	default:
		return word, classUnknown
	}
}

// dictKnows reports whether w is a classified headword in the embedded
// dictionary. Used by lemma to prefer reductions that land on a real word.
func dictKnows(w string) bool {
	d := loadDict()
	if len(d) == 0 {
		return false
	}
	_, ok := d[strings.ToLower(w)]
	return ok
}

func dictClassify(w string) string {
	if conjunctions[w] {
		return "other"
	}
	d := loadDict()
	if len(d) == 0 {
		return heuristicClassify(w)
	}
	c, ok := d[strings.ToLower(w)]
	if !ok {
		return heuristicClassify(w)
	}
	switch c {
	case classVerb:
		return "verb"
	case classAdj:
		return "adj"
	case classAdv, classPrep:
		return "other"
	default:
		return "noun"
	}
}

var conjunctions = map[string]bool{
	"because": true, "although": true, "though": true, "while": true,
	"unless": true, "until": true, "since": true, "whereas": true,
	"whenever": true, "wherever": true, "however": true, "therefore": true,
	"moreover": true, "furthermore": true, "nevertheless": true,
}

func heuristicClassify(w string) string {
	if fallbackVerbs[w] {
		return "verb"
	}
	if fallbackAdjs[w] {
		return "adj"
	}
	if strings.HasSuffix(w, "y") || strings.HasSuffix(w, "ous") ||
		strings.HasSuffix(w, "ful") || strings.HasSuffix(w, "less") ||
		strings.HasSuffix(w, "ive") || strings.HasSuffix(w, "al") ||
		strings.HasSuffix(w, "ish") || strings.HasSuffix(w, "ble") {
		return "adj"
	}
	return "noun"
}

// Fallback lists used when dictionary is not available.
var fallbackVerbs = map[string]bool{
	"is": true, "are": true, "was": true, "were": true, "be": true,
	"have": true, "has": true, "had": true, "do": true, "does": true,
	"use": true, "uses": true, "used": true, "make": true, "makes": true,
	"run": true, "runs": true, "running": true, "call": true, "calls": true,
	"get": true, "gets": true, "set": true, "sets": true,
	"go": true, "goes": true, "take": true, "takes": true,
	"give": true, "gives": true, "find": true, "finds": true,
	"build": true, "builds": true, "create": true, "creates": true,
	"write": true, "writes": true, "read": true, "reads": true,
	"return": true, "returns": true, "pass": true, "passes": true,
	"need": true, "needs": true, "want": true, "wants": true,
	"add": true, "adds": true, "remove": true, "removes": true,
	"fix": true, "fixes": true, "change": true, "changes": true,
	"check": true, "checks": true, "load": true, "loads": true,
	"save": true, "saves": true, "send": true, "sends": true,
	"fetch": true, "fetches": true, "parse": true, "parses": true,
	"skip": true, "skips": true, "keep": true, "keeps": true,
	"drop": true, "drops": true, "jump": true, "jumps": true,
	"fall": true, "falls": true, "move": true, "moves": true,
	"include": true, "includes": true, "contain": true, "contains": true,
	"handle": true, "handles": true, "support": true, "supports": true,
}

var fallbackAdjs = map[string]bool{
	"quick": true, "brown": true, "lazy": true, "big": true, "small": true,
	"new": true, "old": true, "good": true, "bad": true, "high": true,
	"low": true, "long": true, "short": true, "fast": true, "slow": true,
	"easy": true, "hard": true, "simple": true, "complex": true,
	"main": true, "key": true, "core": true, "base": true, "full": true,
	"empty": true, "clean": true, "dirty": true, "raw": true, "safe": true,
}
