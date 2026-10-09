package operators

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRewriteNegativeLookahead(t *testing.T) {
	for in, want := range map[string]string{
		"^(?!.*(HK|TW)).*$":       "HK|TW",
		"^(?!.*HK).*$":            "HK",
		"^(?!.*(?:HK|TW)).*$":     "HK|TW",
		" ^(?!.*(a|b)).*$ ":       "a|b",
		"^(?!.*(网址|流量|到期)).*$":    "网址|流量|到期",
		"^(?!.*(a)|(b)).*$":       "(a)|(b)",
		"^(?!.*[(]).*$":           "[(]",
		"^(?!.*(a(?=b))).*$":      "",
		"^(?!.*).*$":              "",
		"^(?!.*(HK|TW)).+$":       "",
		"(?!.*(HK|TW)).*$":        "",
		"^(?!.*(HK|TW))$":         "",
		"^(?=.*(HK|TW)).*$":       "",
		"^(?!.*(HK|TW)).*$|extra": "",
	} {
		got, ok := RewriteNegativeLookahead(in)
		if got != want || ok != (want != "") {
			t.Errorf("RewriteNegativeLookahead(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// Every operator pattern runs on RE2, so a provider-controlled name cannot
// make an operator-written pattern backtrack: patterns that are catastrophic
// on a backtracking engine (the bundle's warm runtime loses its whole budget
// to the first one, TestWarmEngineBoundsCatastrophicRegexOnScriptlessPath)
// finish over a 10 MB name in time proportional to its length.
func TestRegexFilterLinearTimeOn10MBInput(t *testing.T) {
	const small, large = 1 << 20, 10 << 20
	// Each pattern fails on the trailing "!", so every one reads the whole
	// name; on a backtracking engine each is exponential in its length.
	patterns := []string{`^(a+)+$`, `(a|aa)+$`, `^(a|a?)+b`, `^(.*a){6}$`, `^(\w+\s?)*$`}
	if raceDetector {
		// The instrumented engine takes about 25 s for the first pattern's
		// 11 MiB alone; the others are measured in every build without it.
		patterns = patterns[:1]
	}
	timeOne := func(pattern string, size int) time.Duration {
		step := `{"type":"Regex Filter","args":{"regex":[` + strconvQuote(pattern) + `],"keep":true}}`
		plan := compileOne(t, step)
		ns := named(t, strings.Repeat("a", size-1)+"!")
		start := time.Now()
		kept := plan.Run(ns, nil)
		elapsed := time.Since(start)
		if len(kept) != 0 {
			t.Fatalf("%s matched %d bytes ending in !", pattern, size)
		}
		return elapsed
	}
	for _, p := range patterns {
		tSmall, tLarge := timeOne(p, small), timeOne(p, large)
		ratio := float64(tLarge) / float64(max(tSmall, time.Microsecond))
		t.Logf("%-12s 1 MiB %8s  10 MiB %8s  ratio %.1f", p, tSmall.Round(time.Microsecond), tLarge.Round(time.Microsecond), ratio)
		// Ten times the input in linear time is about ten times the work;
		// 25 leaves room for a noisy machine and a race build, and is far
		// below what any super-linear growth would show.
		if ratio > 25 {
			t.Errorf("%s: 10 MiB took %.1f times as long as 1 MiB (%s against %s)", p, ratio, tLarge, tSmall)
		}
		if tLarge > time.Minute {
			t.Errorf("%s: 10 MiB took %s", p, tLarge)
		}
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
