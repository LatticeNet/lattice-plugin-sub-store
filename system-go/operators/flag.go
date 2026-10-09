package operators

import (
	"regexp"
	"unicode"
	"unicode/utf8"
)

// pirateFlag is the flag of a name that carries no country.
const pirateFlag = "🏴‍☠️"

// flagEmoji matches what the Flag Operator removes from a name: any pair of
// regional indicator symbols, the pirate flag and the rainbow flag, each
// exactly as written with its variation selector.
var flagEmoji = regexp.MustCompile("[\U0001F1E6-\U0001F1FF]{2}|\U0001F3F4‍☠️|\U0001F3F3️‍\U0001F308")

// regionalPair matches a country flag already in a name.
var regionalPair = regexp.MustCompile("[\U0001F1E6-\U0001F1FF]{2}")

// removeFlags strips every flag from a name and trims the rest.
func removeFlags(name string) string {
	return trimES(flagEmoji.ReplaceAllLiteralString(name, ""))
}

// flagMatcher finds the flag a name carries in one pass per table over the
// name, rather than one search per row: the perf gate runs Flag over 4096
// nodes inside 100 ms with three other operators.
type flagMatcher struct {
	words map[rune][]keyword // words by their first folded rune
	codes map[string]int     // code to its flagCodes row
}

type keyword struct {
	runes []rune // folded
	row   int    // flagWords row
}

var flags = newFlagMatcher()

func newFlagMatcher() *flagMatcher {
	m := &flagMatcher{words: map[rune][]keyword{}, codes: map[string]int{}}
	for row, r := range flagWords {
		for _, w := range r.words {
			runes := []rune(w)
			for i, c := range runes {
				runes[i] = foldRune(c)
			}
			m.words[runes[0]] = append(m.words[runes[0]], keyword{runes: runes, row: row})
		}
	}
	for row, r := range flagCodes {
		for _, c := range r.codes {
			if _, seen := m.codes[c]; !seen {
				m.codes[c] = row
			}
		}
	}
	return m
}

// foldRune maps a rune to the form ECMAScript's case-insensitive matching
// (without the u flag) compares: its upper case, except that a character
// beyond ASCII never folds into ASCII.
func foldRune(r rune) rune {
	u := unicode.ToUpper(r)
	if r >= utf8.RuneSelf && u < utf8.RuneSelf {
		return r
	}
	return u
}

// find is the flag of the best word the name carries, else of the best
// code, else "".
func (m *flagMatcher) find(name string) string {
	best := len(flagWords)
	folded := make([]rune, 0, len(name))
	for _, r := range name {
		folded = append(folded, foldRune(r))
	}
	for i, r := range folded {
		for _, k := range m.words[r] {
			if k.row < best && hasRunePrefix(folded[i:], k.runes) {
				best = k.row
			}
		}
	}
	if best < len(flagWords) {
		return flagWords[best].flag
	}
	best = len(flagCodes)
	for i := 0; i < len(name); {
		if !isASCIILetter(name[i]) {
			i++
			continue
		}
		j := i
		for j < len(name) && isASCIILetter(name[j]) {
			j++
		}
		if row, ok := m.codes[name[i:j]]; ok && row < best && !carrierCN2(name, i, j) {
			best = row
		}
		i = j
	}
	if best < len(flagCodes) {
		return flagCodes[best].flag
	}
	return ""
}

// carrierCN2 reports the run name[i:j] as "CN" followed by a 2 that no
// letter follows: the CN2 carrier, which the bundle does not read as China
// ("CN2", "CN22", "CN2 01"; "CN2x" and "CN-2" are China).
func carrierCN2(name string, i, j int) bool {
	return name[i:j] == "CN" && j < len(name) && name[j] == '2' && (j+1 == len(name) || !isASCIILetter(name[j+1]))
}

func isASCIILetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

func hasRunePrefix(s, prefix []rune) bool {
	if len(prefix) > len(s) {
		return false
	}
	for i, r := range prefix {
		if s[i] != r {
			return false
		}
	}
	return true
}

// nameFlag is the flag a name stands for: the flag of the keyword it
// carries (flagtable.go), else the first country flag already in it, else
// the pirate flag. The Flag Operator applies its Taiwan choice to the
// result; the Region Filter compares it as it is.
func nameFlag(name string) string {
	if f := flags.find(name); f != "" {
		return f
	}
	if f := regionalPair.FindString(name); f != "" {
		return f
	}
	return pirateFlag
}

// FlagKeywords is every word and code the flag tables carry, words first in
// table order, for the test in package main that holds the tables to the
// bundle.
func FlagKeywords() []string {
	var out []string
	for _, r := range flagWords {
		out = append(out, r.words...)
	}
	for _, r := range flagCodes {
		out = append(out, r.codes...)
	}
	return out
}
