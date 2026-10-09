package parse

import (
	"encoding/base64"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

// linearShapes are inputs built to make a quadratic step show: long runs of
// the delimiters each preprocessor and parser scans for, lines just under
// the line bound, and documents that reach the YAML and JSON5 readers. Each
// builder returns a text of about n bytes.
var linearShapes = []struct {
	name  string
	build func(n int) string
}{
	{"uri-list", func(n int) string {
		line := "vless://" + uuid4 + "@a.example.com:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=b.example.com&fp=chrome&pbk=dWg3dwQdO7WGR7_Rk6WrlyOis-kG9MVvy11W2Aq-HfE&sid=84d363f6&spx=%2F&type=tcp#n\n"
		return repeatTo(line, n)
	}},
	{"vless-colons", func(n int) string {
		line := "vless://u@" + strings.Repeat(":a", 30<<10) + "\n"
		return repeatTo(line, n)
	}},
	{"vless-percent", func(n int) string {
		line := "vless://" + uuid4 + "@h:443?type=ws&path=" + strings.Repeat("%41", 20<<10) + "#n\n"
		return repeatTo(line, n)
	}},
	{"vless-ampersands", func(n int) string {
		line := "vless://" + uuid4 + "@h:443?" + strings.Repeat("a=1&", 15<<10) + "#n\n"
		return repeatTo(line, n)
	}},
	{"vless-early-data", func(n int) string {
		line := "vless://" + uuid4 + "@h:443?type=ws&path=%2Fp%3F" + strings.Repeat("ed%3D1%26", 6<<10) + "#n\n"
		return repeatTo(line, n)
	}},
	{"vmess-json", func(n int) string {
		payload := `{"add":"h","port":"1","id":"` + uuid4 + `","net":"ws","path":"/p?` + strings.Repeat("x=1&", 4<<10) + `ed=2048"}`
		return repeatTo("vmess://"+base64.StdEncoding.EncodeToString([]byte(payload))+"\n", n)
	}},
	{"base64-known", func(n int) string {
		lines := repeatTo("vless://"+uuid4+"@a.example.com:443?security=tls&type=tcp#n\n", n*3/4)
		return base64.StdEncoding.EncodeToString([]byte(lines))
	}},
	{"base64-no-protocol", func(n int) string {
		return base64.StdEncoding.EncodeToString([]byte(repeatTo("word "+strings.Repeat(" ", 200)+"\n", n*3/4)))
	}},
	{"clash-yaml", func(n int) string {
		head := "proxies:\n"
		entry := "  - {name: n, type: vless, server: a.example.com, port: 443, uuid: " + uuid4 + ", short-id: 0088}\n"
		return head + repeatTo(entry, n-len(head))
	}},
	{"profile-without-header", func(n int) string {
		return "[Proxy]\n" + repeatTo("a = ss, h, 1, proxy]\n", n)
	}},
	{"object-lines", func(n int) string {
		return repeatTo("{a: 1, b: [1, 2, 3], c: 'x'}\na: b: c\n- [x, {y: z}]\n", n)
	}},
	{"one-long-line", func(n int) string {
		return strings.Repeat("v", n)
	}},
	{"white-space", func(n int) string {
		return repeatTo(" \t\n\u3000\r\n", n)
	}},
}

func repeatTo(unit string, n int) string {
	if n <= len(unit) {
		return unit
	}
	return strings.Repeat(unit, n/len(unit))
}

// timeParse returns the least CPU time of runs parses of text, through the
// whole pipeline without the raw document bound.
func timeParse(t *testing.T, text string, runs int) time.Duration {
	t.Helper()
	best := time.Duration(1<<63 - 1)
	for i := 0; i < runs; i++ {
		runtime.GC()
		start := cpuTime()
		if _, _, err := parseText(text, Options{}, nil); err != nil && !errors.Is(err, ErrExpansionTooLarge) {
			t.Fatalf("parse failed: %v", err)
		}
		if d := cpuTime() - start; d < best {
			best = d
		}
	}
	return best
}

// TestParseLinearTimeOn10MBInput parses each shape at 2 MB and at 10 MB, past
// the raw document bound Document applies first, and requires the larger
// parse to cost no more than twelve times the CPU time of the smaller one:
// five times is linear, twenty-five quadratic. Document itself refuses the
// 10 MB text before preprocessing.
func TestParseLinearTimeOn10MBInput(t *testing.T) {
	const small, large = 2 << 20, 10 << 20
	const maxRatio = 12.0
	floor := 20 * time.Millisecond // below this, clock granularity dominates the ratio

	for _, shape := range linearShapes {
		t.Run(shape.name, func(t *testing.T) {
			s, l := shape.build(small), shape.build(large)
			if len(l) < 9<<20 || len(l) > 11<<20 {
				t.Fatalf("large input is %d bytes, want about 10 MB", len(l))
			}
			start := time.Now()
			if _, _, err := Document(l, Options{}); !errors.Is(err, ErrDocumentTooLarge) {
				t.Fatalf("Document on %d bytes gave %v, want ErrDocumentTooLarge", len(l), err)
			}
			if d := time.Since(start); d > 50*time.Millisecond {
				t.Errorf("refusing the large document took %v", d)
			}
			// CI runs this test only under the race detector, which slows
			// each parse about tenfold; one timing per size keeps it within
			// minutes, and the ratio bound leaves room for one-shot noise.
			smallRuns, largeRuns := 3, 2
			if raceEnabled {
				smallRuns, largeRuns = 1, 1
			}
			ts := timeParse(t, s, smallRuns)
			tl := timeParse(t, l, largeRuns)
			base := ts
			if base < floor {
				base = floor
			}
			ratio := float64(tl) / float64(base)
			t.Logf("%s: %d bytes %v, %d bytes %v, ratio %.1f", shape.name, len(s), ts, len(l), tl, ratio)
			if ratio > maxRatio {
				t.Errorf("10 MB took %.1f times as long as 2 MB (%v vs %v), want at most %.0f", ratio, tl, ts, maxRatio)
			}
			if limit := 30 * time.Second; !raceEnabled && tl > limit {
				t.Errorf("10 MB took %v of CPU time, above %v", tl, limit)
			}
		})
	}
}
