package operators

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

// Every operator pattern compiles with Go's regexp, which is RE2: matching
// time is linear in the input, so a provider-controlled node name cannot make
// an operator-written pattern backtrack (design 28, "Node filtering"). RE2
// has no lookaround and no backreference inside a pattern; a stored pattern
// that uses them is not run natively (design-28.md:170).
//
// A leading "(?i)" makes a pattern case-insensitive, as upstream's buildRegex
// does by stripping it and adding the i flag. RE2 reads the same prefix as the
// same flag, so the pattern is compiled exactly as the operator wrote it.

// compilePattern compiles one operator pattern with its ECMAScript meaning
// (esPattern). A pattern RE2 refuses is recorded on the step as
// regex_incompatible, under the operator's own text, and nil is returned.
func (c *stepCompiler) compilePattern(pattern string) *regexp.Regexp {
	re, err := regexp.Compile(esPattern(pattern))
	if err != nil {
		c.diags = append(c.diags, Diagnostic{
			Step:    c.index,
			Code:    CodeRegexIncompatible,
			Pattern: pattern,
			Message: fmt.Sprintf("pattern %q does not compile under RE2 (%s); lookaround and backreferences inside a pattern are not supported", pattern, regexReason(pattern, err)),
		})
		return nil
	}
	return re
}

// The bundle compiles every operator pattern as an ECMAScript RegExp without
// the u flag, and ECMAScript reads some constructs differently from RE2
// (observed through engine.convert; TestRegexTranslationMatchesBundle in
// package main holds the two together). esPattern rewrites those constructs
// to their ECMAScript meaning before RE2 compiles the pattern:
//
//   - \s and \S. ECMAScript white space and line terminators are RE2's
//     [\t\n\f\r ] plus \v, U+00A0, U+1680, U+2000 to U+200A, U+2028, U+2029,
//     U+202F, U+205F, U+3000 and U+FEFF; a full-width space in a node name is
//     white space to the bundle.
//   - The dot. ECMAScript excludes \r, U+2028 and U+2029 besides \n.
//   - Outside a class, \A, \z, \p, \P and \a. ECMAScript reads each as an
//     identity escape, the letter itself, where RE2 reads anchors, Unicode
//     classes and the bell.
//
// The walk follows RE2's own reading of the pattern, so a pattern RE2
// accepts is still accepted: an escape is one unit, so an escaped backslash
// stays an escaped backslash; a \Q...\E span is copied as RE2 quotes it; and
// a character class keeps its leading ] and its [:name:] members. Inside a
// class \s and \S become range lists that begin and end with a range, so a
// neighbouring - stays the literal it was, and a \s that is itself a range
// endpoint is left as written for RE2 to refuse as before. A letter cannot
// make an atom invalid, except right after "(?", where an identity escape is
// left as written so it cannot complete a group syntax nobody wrote.
//
// Not rewritten, and recorded in the S1 dispatch PR, because rewriting them
// could turn a pattern RE2 accepts into one it refuses: \Q, \E and the
// identity escapes inside a class keep RE2's reading. Without the u flag
// ECMAScript's dot and classes read UTF-16 code units, so a dot matches half
// of a character outside the Basic Multilingual Plane where RE2 matches the
// whole character; the i flag folds case by ECMAScript's Canonicalize where
// RE2 uses Unicode simple folding; and inline flags other than a leading
// (?i), which the bundle refuses, compile here.

const (
	// esSpace is ECMAScript's white space and line terminators as a class
	// body: the range \t-\r first and U+2000-U+200A last.
	esSpace = `\t-\r \x{a0}\x{1680}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}\x{2000}-\x{200a}`
	// esNonSpace is its complement over every code point, as ranges only.
	esNonSpace = `\x00-\x08\x0e-\x1f\x21-\x9f\x{a1}-\x{167f}\x{1681}-\x{1fff}\x{200b}-\x{2027}\x{202a}-\x{202e}\x{2030}-\x{205e}\x{2060}-\x{2fff}\x{3001}-\x{fefe}\x{ff00}-\x{10ffff}`
	// esDot is ECMAScript's dot without the s flag.
	esDot = `[^\n\r\x{2028}\x{2029}]`
)

// esIdentity are the escapes ECMAScript reads as the letter itself and RE2
// accepts, outside a class, with another meaning.
var esIdentity = [128]bool{'A': true, 'z': true, 'p': true, 'P': true, 'a': true}

// esPattern is pattern with the constructs above given their ECMAScript
// meaning.
func esPattern(pattern string) string {
	var b strings.Builder
	b.Grow(len(pattern))
	for i := 0; i < len(pattern); {
		switch c := pattern[i]; {
		case c == '\\' && i+1 < len(pattern):
			n := pattern[i+1]
			switch {
			case n == 's':
				b.WriteString("[" + esSpace + "]")
				i += 2
			case n == 'S':
				b.WriteString("[^" + esSpace + "]")
				i += 2
			case n < 0x80 && esIdentity[n] && !strings.HasSuffix(b.String(), "(?"):
				b.WriteByte(n)
				i += 2
			case n == 'Q':
				end := len(pattern)
				if at := strings.Index(pattern[i+2:], `\E`); at >= 0 {
					end = i + 2 + at + 2
				}
				b.WriteString(pattern[i:end])
				i = end
			default:
				l := escapeLen(pattern, i)
				b.WriteString(pattern[i : i+l])
				i += l
			}
		case c == '[':
			i = esClass(&b, pattern, i)
		case c == '.':
			b.WriteString(esDot)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// esClass rewrites the character class that starts at pattern[i] into b and
// returns the index after it. It tracks RE2's range grammar: after a single
// character, a - that is not followed by ] is a range operator.
func esClass(b *strings.Builder, pattern string, i int) int {
	b.WriteByte('[')
	i++
	if i < len(pattern) && pattern[i] == '^' {
		b.WriteByte('^')
		i++
	}
	first := true
	// single: the last member was one character, which a - can extend into
	// a range. dash: that - was just written, so the next member ends a
	// range.
	single, dash := false, false
	endsMember := func(oneCharacter bool) {
		if dash || !oneCharacter {
			single, dash = false, false
			return
		}
		single = true
	}
	for i < len(pattern) {
		c := pattern[i]
		if c == ']' && !first {
			b.WriteByte(']')
			return i + 1
		}
		first = false
		switch {
		case c == '[' && strings.HasPrefix(pattern[i:], "[:") && strings.Contains(pattern[i+2:], ":]"):
			end := i + 2 + strings.Index(pattern[i+2:], ":]") + 2
			b.WriteString(pattern[i:end])
			i = end
			endsMember(false)
		case c == '\\' && i+1 < len(pattern):
			switch n := pattern[i+1]; {
			case (n == 's' || n == 'S') && !dash:
				if n == 's' {
					b.WriteString(esSpace)
				} else {
					b.WriteString(esNonSpace)
				}
				i += 2
				endsMember(false)
			case n == 's' || n == 'S' || n == 'd' || n == 'D' || n == 'w' || n == 'W':
				b.WriteString(pattern[i : i+2])
				i += 2
				endsMember(false)
			case (n == 'p' || n == 'P') && i+2 < len(pattern):
				// A Unicode class, \pL or \p{Name}, is one member to RE2.
				end := i + 3
				if pattern[i+2] == '{' {
					if at := strings.IndexByte(pattern[i+2:], '}'); at >= 0 {
						end = i + 2 + at + 1
					}
				}
				b.WriteString(pattern[i:end])
				i = end
				endsMember(false)
			default:
				l := escapeLen(pattern, i)
				b.WriteString(pattern[i : i+l])
				i += l
				endsMember(true)
			}
		case c == '-' && single && i+1 < len(pattern) && pattern[i+1] != ']':
			b.WriteByte('-')
			i++
			single, dash = false, true
		default:
			_, size := utf8.DecodeRuneInString(pattern[i:])
			b.WriteString(pattern[i : i+size])
			i += size
			endsMember(true)
		}
	}
	// An unterminated class: RE2 refuses it, as it did before.
	return i
}

// escapeLen is the length of the escape at pattern[i] as RE2 reads it: \x{...}
// and \xHH, up to three octal digits, a whole character after the
// backslash, and two bytes otherwise.
func escapeLen(pattern string, i int) int {
	rest := pattern[i+1:]
	switch n := rest[0]; {
	case n == 'x' && strings.HasPrefix(rest, "x{"):
		if end := strings.IndexByte(rest, '}'); end >= 0 {
			return end + 2
		}
	case n == 'x' && len(rest) >= 3 && isHexDigit(rest[1]) && isHexDigit(rest[2]):
		return 4
	case n >= '0' && n <= '7':
		l := 1
		for l < 3 && l < len(rest) && rest[l] >= '0' && rest[l] <= '7' {
			l++
		}
		return l + 1
	case n >= 0x80:
		_, size := utf8.DecodeRuneInString(rest)
		return size + 1
	}
	return 2
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// regexReason says why RE2 refused esPattern(pattern) with err, quoting only
// text the operator wrote. The error quotes the expression RE2 stopped at,
// which is the rewritten pattern's text: an unclosed group around a \s quotes
// the whole space class. When that quote is not part of pattern, the reason
// is RE2's error for pattern itself, which RE2 refuses too (the rewrite keeps
// every pattern RE2 accepts accepted), as long as that error quotes the
// operator's text; otherwise the reason is the error's code alone.
func regexReason(pattern string, err error) string {
	var refused *syntax.Error
	if !errors.As(err, &refused) {
		return err.Error()
	}
	if quotesFrom(pattern, refused) {
		return refused.Code.String() + ": `" + refused.Expr + "`"
	}
	var own *syntax.Error
	if _, err := syntax.Parse(pattern, syntax.Perl); errors.As(err, &own) && quotesFrom(pattern, own) {
		return own.Code.String() + ": `" + own.Expr + "`"
	}
	return refused.Code.String()
}

// quotesFrom reports whether an RE2 error quotes a part of pattern.
func quotesFrom(pattern string, err *syntax.Error) bool {
	return err.Expr != "" && strings.Contains(pattern, err.Expr)
}

// negativeLookahead matches the keep-everything-but idiom, `^(?!.*X).*$`,
// with X captured.
var negativeLookahead = regexp.MustCompile(`^\^\(\?!\.\*(.+)\)\.\*\$$`)

// RewriteNegativeLookahead recognises `^(?!.*(A|B)).*$` (and the single
// alternative form `^(?!.*A).*$`) used as a keep-mode Regex Filter and
// returns the drop-mode pattern `A|B`. A keep filter with that one pattern
// keeps exactly the names a drop filter on `A|B` keeps. ok=false for any
// other shape, and for an inner pattern RE2 would refuse as well. The
// editor's copy of this rule is ui/src/regexRewrite.ts.
func RewriteNegativeLookahead(pattern string) (drop string, ok bool) {
	m := negativeLookahead.FindStringSubmatch(strings.TrimSpace(pattern))
	if m == nil {
		return "", false
	}
	inner := m[1]
	for _, open := range []string{"(?:", "("} {
		if strings.HasPrefix(inner, open) && strings.HasSuffix(inner, ")") && balancedGroups(inner[len(open):len(inner)-1]) {
			inner = inner[len(open) : len(inner)-1]
			break
		}
	}
	if inner == "" || !balancedGroups(inner) {
		return "", false
	}
	if _, err := regexp.Compile(esPattern(inner)); err != nil {
		return "", false
	}
	return inner, true
}

// balancedGroups reports whether every unescaped parenthesis outside a
// character class closes in order.
func balancedGroups(s string) bool {
	depth := 0
	inClass := false
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; {
		case ch == '\\':
			i++
		case inClass:
			if ch == ']' {
				inClass = false
			}
		case ch == '[':
			inClass = true
			if i+1 < len(s) && s[i+1] == '^' {
				i++
			}
			if i+1 < len(s) && s[i+1] == ']' {
				i++
			}
		case ch == '(':
			depth++
		case ch == ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}
