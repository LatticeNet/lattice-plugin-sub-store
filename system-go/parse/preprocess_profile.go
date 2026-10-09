package parse

import (
	"strings"
	"unicode/utf8"
)

// preprocessProfile is row 5 of parser.md section 2 (details in 2.5): a
// full Surge, Loon or Quantumult X profile is cut down to its proxy section.
// The test is a line starting with "[server_local]" or "[Proxy]" (exact
// case).
func preprocessProfile(text string) (string, bool, string, error) {
	if !lineStartsWith(text, "[server_local]") && !lineStartsWith(text, "[Proxy]") {
		return "", false, "", nil
	}
	return extractProxySection(text), true, "", nil
}

// lineStartsWith reports a line of s (after the start or a line terminator)
// starting with prefix.
func lineStartsWith(s, prefix string) bool {
	for start := 0; start >= 0; start = nextLineStart(s, start) {
		if strings.HasPrefix(s[start:], prefix) {
			return true
		}
	}
	return false
}

// extractProxySection looks for the earlier of two anchors: a line starting
// with "[server_local" in any case, which keeps the document unchanged (a
// Quantumult X profile is parsed in full), and the text "proxy]" in any case
// followed by at least one character and later by a header line ("[", one
// or more characters, "]" ending the line), which returns the text between
// "proxy]" and that header line. Without either anchor the document is kept
// unchanged. The first "proxy]" decides anchor 2: a header after any later
// occurrence also follows the first.
func extractProxySection(s string) string {
	p := indexFold(s, "proxy]")
	if p < 0 {
		return s
	}
	header := -1
	for start := nextLineStart(s, p); start >= 0; start = nextLineStart(s, start) {
		if start >= p+len("proxy]")+1 && headerLineAt(s, start) {
			header = start
			break
		}
	}
	if header < 0 {
		return s
	}
	for start := 0; start >= 0 && start < p; start = nextLineStart(s, start) {
		if hasPrefixFold(s[start:], "[server_local") {
			return s
		}
	}
	return s[p+len("proxy]") : header]
}

// headerLineAt reports a line at i that holds "[", at least one character
// and "]", with the "]" ending the line.
func headerLineAt(s string, i int) bool {
	if i >= len(s) || s[i] != '[' {
		return false
	}
	end := i + 1
	for end < len(s) {
		r, size := utf8.DecodeRuneInString(s[end:])
		if isESLineTerminator(r) {
			break
		}
		end += size
	}
	return end-i >= 3 && s[end-1] == ']'
}

// indexFold is strings.Index with ASCII case folding (needle is lower case).
func indexFold(s, needle string) int {
	for i := 0; i+len(needle) <= len(s); i++ {
		if hasPrefixFold(s[i:], needle) {
			return i
		}
	}
	return -1
}

// hasPrefixFold is strings.HasPrefix with ASCII case folding (prefix is
// lower case).
func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for k := 0; k < len(prefix); k++ {
		if lowerASCII(s[k]) != prefix[k] {
			return false
		}
	}
	return true
}
