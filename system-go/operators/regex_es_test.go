package operators

import (
	"encoding/json"
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// esWhiteSpace is every code point ECMAScript's \s matches: WhiteSpace and
// LineTerminator (ECMA-262, "White Space" and "Line Terminators"), the
// space separators of Unicode category Zs included.
var esWhiteSpace = []rune{
	'\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680,
	0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a,
	0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff,
}

// Characters ECMAScript does not count as white space although some regex
// engines or older Unicode versions do.
var esNotWhiteSpace = []rune{'x', '-', 0x85, 0x180e, 0x200b, 0x200c, 0x2060, 0x3001}

// Characters the cases below name, spelled as code points so the source
// carries no invisible character.
var (
	nbsp      = string(rune(0xa0))
	ogham     = string(rune(0x1680))
	fourEm    = string(rune(0x2005))
	narrow    = string(rune(0x202f))
	fullWidth = string(rune(0x3000))
	bom       = string(rune(0xfeff))
	maxRune   = string(rune(0x10ffff))
)

func mustES(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(esPattern(pattern))
	if err != nil {
		t.Fatalf("esPattern(%q) = %q does not compile: %v", pattern, esPattern(pattern), err)
	}
	return re
}

type esCase struct {
	pattern string
	match   []string
	miss    []string
}

func checkES(t *testing.T, cases []esCase) {
	t.Helper()
	for _, c := range cases {
		re := mustES(t, c.pattern)
		for _, s := range c.match {
			if !re.MatchString(s) {
				t.Errorf("%s does not match %+q (compiled %q)", c.pattern, s, esPattern(c.pattern))
			}
		}
		for _, s := range c.miss {
			if re.MatchString(s) {
				t.Errorf("%s matches %+q (compiled %q)", c.pattern, s, esPattern(c.pattern))
			}
		}
	}
}

func TestESPatternWhitespaceIsECMAScripts(t *testing.T) {
	for _, pattern := range []string{`^a\sb$`, `^a[\s]b$`, `^a[^\S]b$`, `^a[y\s]b$`} {
		re := mustES(t, pattern)
		for _, r := range esWhiteSpace {
			if name := "a" + string(r) + "b"; !re.MatchString(name) {
				t.Errorf("%s does not match %+q", pattern, name)
			}
		}
		for _, r := range esNotWhiteSpace {
			if name := "a" + string(r) + "b"; re.MatchString(name) {
				t.Errorf("%s matches %+q", pattern, name)
			}
		}
	}
	for _, pattern := range []string{`^a\Sb$`, `^a[\S]b$`, `^a[^\s]b$`} {
		re := mustES(t, pattern)
		for _, r := range esWhiteSpace {
			if name := "a" + string(r) + "b"; re.MatchString(name) {
				t.Errorf("%s matches %+q", pattern, name)
			}
		}
		for _, r := range append(esNotWhiteSpace, 0x10ffff, 0x1f1ed, 0xa1, 0x167f, 0x1fff, 0xff00) {
			if name := "a" + string(r) + "b"; !re.MatchString(name) {
				t.Errorf("%s does not match %+q", pattern, name)
			}
		}
	}
}

func TestESPatternDotExcludesECMAScriptLineTerminators(t *testing.T) {
	re := mustES(t, `^a.b$`)
	for _, r := range []rune{'\n', '\r', 0x2028, 0x2029} {
		if name := "a" + string(r) + "b"; re.MatchString(name) {
			t.Errorf(". matches %+q", name)
		}
	}
	for _, r := range []rune{' ', '\t', '\v', 0xa0, 0x3000, 0x85, '.', 'x'} {
		if name := "a" + string(r) + "b"; !re.MatchString(name) {
			t.Errorf(". does not match %+q", name)
		}
	}
	// Inside a class a dot is the character itself, as in both dialects.
	checkES(t, []esCase{{`^a[.]b$`, []string{"a.b"}, []string{"axb"}}})
}

func TestESPatternKeepsEscapedBackslash(t *testing.T) {
	checkES(t, []esCase{
		{`^\\s$`, []string{`\s`}, []string{" ", "s", fullWidth}},
		{`^\\S$`, []string{`\S`}, []string{"x"}},
		{`^\\.$`, []string{`\.`, `\x`}, []string{"\\\n", "."}},
		{`^\\\s$`, []string{`\ `, `\` + nbsp}, []string{`\s`}},
		{`^\\\\s$`, []string{`\\s`}, []string{`\ `}},
		{`^\.$`, []string{"."}, []string{"x"}},
		{`^[\\s]$`, []string{`\`, "s"}, []string{" "}},
		{`^\\pL$`, []string{`\pL`}, []string{"pL", "x"}},
	})
}

func TestESPatternReadsIdentityEscapesAsTheLetter(t *testing.T) {
	checkES(t, []esCase{
		{`^a\pLb$`, []string{"apLb"}, []string{"axb"}},
		{`^a\p{L}b$`, []string{"ap{L}b"}, []string{"axb"}},
		{`^a\P{L}b$`, []string{"aP{L}b"}, []string{"a1b"}},
		{`^a\zb$`, []string{"azb"}, []string{"ab"}},
		{`^\Aab$`, []string{"Aab"}, []string{"ab"}},
		{`^a\ab$`, []string{"aab"}, []string{"a\ab"}},
		{`^a\p+$`, []string{"appp"}, []string{"a"}},
	})
}

// Where a rewrite could turn a pattern RE2 accepts into one it refuses, RE2's
// reading is kept: a \Q...\E span is quoted text, and a Unicode class inside
// a character class is one member, so a - after it stays literal.
func TestESPatternKeepsRE2ReadingWhereARewriteCouldRefuse(t *testing.T) {
	checkES(t, []esCase{
		{`^a\Q\s.[\Eb$`, []string{`a\s.[b`}, []string{"a .[b", "aQ"}},
		{`^\Q.\s`, []string{`.\s`}, []string{"x "}},
		{`^[\pZ-A]$`, []string{"-", "A", " "}, []string{"B"}},
		{`^[\p{Greek}-\s]$`, []string{"-", "λ", " ", nbsp}, []string{"x"}},
	})
}

// The class grammar follows RE2's reading, so a rewrite never lands inside a
// [:name:] member or outside the class it belongs to, and a - beside a
// rewritten \s keeps the meaning it had.
func TestESPatternFollowsRE2ClassGrammar(t *testing.T) {
	checkES(t, []esCase{
		{`^[]\s]$`, []string{"]", fullWidth}, []string{"x"}},
		{`^[^]\s]$`, []string{"x"}, []string{"]", nbsp}},
		{`^[[:alpha:].]$`, []string{"a", "."}, []string{"\n", "1"}},
		{`^[[:alpha:]\s]$`, []string{"a", fullWidth}, []string{"1"}},
		{`^[\s-z]$`, []string{"-", "z", fourEm}, []string{"y"}},
		{`^[-\s]$`, []string{"-", nbsp}, []string{"x"}},
		{`^[a\-\s]$`, []string{"a", "-", bom}, []string{"b"}},
		{`^[a-z-\s]$`, []string{"m", "-", narrow}, []string{"A"}},
		{`^[\S\s]$`, []string{"x", " ", maxRune}, nil},
		{`^[^\s\S]$`, nil, []string{"x", " "}},
		{`^[\x{41}-Z\s]$`, []string{"A", "Z", ogham}, []string{"a"}},
		{`^[\d\s]$`, []string{"7", fullWidth}, []string{"x"}},
		{`(?i)^a\sB$`, []string{"A" + fullWidth + "b"}, []string{"AxB"}},
		{`^a\s+b$`, []string{"a " + fullWidth + nbsp + "b"}, []string{"ab"}},
		{`^.{2}$`, []string{"ab"}, []string{"a\r"}},
		{`^\x2E\x{2e}$`, []string{".."}, []string{"ab"}},
		{`^\101$`, []string{"A"}, []string{"a"}},
	})
}

// A pattern RE2 refuses stays refused when the rewrite would only give it a
// meaning the operator never wrote: a \s as a range endpoint, and an escape
// that would complete "(?P<".
func TestESPatternKeepsRefusedPatternsRefused(t *testing.T) {
	for _, pattern := range []string{`[\x00-\s]`, `[a-\S]`, `(?\P<n>x)`, `(?!.*\s)`, `a\`, `[a`, `\1`} {
		if _, err := regexp.Compile(pattern); err == nil {
			t.Fatalf("%q compiles under RE2; the case is not a refusal", pattern)
		}
		if _, err := regexp.Compile(esPattern(pattern)); err == nil {
			t.Errorf("esPattern(%q) = %q compiles", pattern, esPattern(pattern))
		}
	}
}

// No pattern RE2 accepts is turned into one it refuses. The patterns are
// drawn, deterministically, from the tokens the rewrite treats specially.
func TestESPatternNeverRefusesAValidPattern(t *testing.T) {
	tokens := []string{
		`\s`, `\S`, `.`, `[`, `]`, `^`, `-`, `a`, `z`, `\\`, `\p`, `\P`, `\Q`, `\E`, `\A`, `\z`, `\a`,
		`{`, `}`, `L`, `(`, `)`, `?`, `:`, `*`, `+`, `|`, `\d`, `[:alpha:]`, `\x41`, `\x{3000}`, `\-`,
		`\]`, `\[`, fullWidth, `0`, `7`, `\0`, `(?i)`, `(?:`, `$`,
	}
	rng := rand.New(rand.NewSource(28))
	valid := 0
	for i := 0; i < 200000; i++ {
		var b strings.Builder
		for n := 1 + rng.Intn(8); n > 0; n-- {
			b.WriteString(tokens[rng.Intn(len(tokens))])
		}
		pattern := b.String()
		if _, err := regexp.Compile(pattern); err != nil {
			continue
		}
		valid++
		if _, err := regexp.Compile(esPattern(pattern)); err != nil {
			t.Fatalf("esPattern(%q) = %q no longer compiles: %v", pattern, esPattern(pattern), err)
		}
	}
	if valid < 10000 {
		t.Fatalf("only %d drawn patterns compiled; the draw does not exercise the rewrite", valid)
	}
}

// The step keeps the operator's own text in its diagnostic, never the
// rewritten pattern.
func TestIncompatibleDiagnosticNamesTheOperatorsPattern(t *testing.T) {
	const pattern = `^(?=.*\s)HK.$`
	step := `{"type":"Regex Filter","args":{"regex":[` + strconvQuote(pattern) + `],"keep":true}}`
	plan, err := Compile("r", []json.RawMessage{json.RawMessage(step)})
	if err != nil {
		t.Fatal(err)
	}
	diags := plan.Incompatible()
	if len(diags) != 1 || diags[0].Pattern != pattern {
		t.Fatalf("diagnostics = %+v, want one naming %q", diags, pattern)
	}
}
