package operators

import (
	"fmt"
	"regexp"
	"strings"
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

// compilePattern compiles one operator pattern. A pattern RE2 refuses is
// recorded on the step as regex_incompatible and nil is returned.
func (c *stepCompiler) compilePattern(pattern string) *regexp.Regexp {
	re, err := regexp.Compile(pattern)
	if err != nil {
		c.diags = append(c.diags, Diagnostic{
			Step:    c.index,
			Code:    CodeRegexIncompatible,
			Pattern: pattern,
			Message: fmt.Sprintf("pattern %q does not compile under RE2 (%s); lookaround and backreferences inside a pattern are not supported", pattern, regexReason(err)),
		})
		return nil
	}
	return re
}

// regexReason is the part of a regexp/syntax error after its fixed prefix.
func regexReason(err error) string {
	return strings.TrimPrefix(err.Error(), "error parsing regexp: ")
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
	if _, err := regexp.Compile(inner); err != nil {
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
