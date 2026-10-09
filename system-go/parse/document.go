// Package parse turns raw subscription text into nodes, as parser.md
// specifies: the whole-document preprocessors (section 2), the split into
// lines and the choice of a line parser (section 3), the line parsers
// (sections 4 to 8), and then the normaliser on every node
// (system-go/normalise).
//
// The parsers follow the specification, quirks included, because the parse
// conformance number counts deep equality with the oracle. Lattice's bounds
// come on top: a raw document above MaxDocumentBytes is refused before
// preprocessing, a preprocessor may not expand a document past
// MaxExpandedBytes, and a line above nodemodel.MaxLineBytes is skipped.
//
// Nothing in this package imports package main, the SDK or the script engine.
package parse

import (
	"errors"
	"strconv"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise"
)

// MaxDocumentBytes is the raw document bound of normaliser.md section 5 (4
// MiB, MaxSubscriptionRawBytes after S0): a larger document is refused
// before preprocessing.
const MaxDocumentBytes = 4 << 20

// MaxExpandedBytes bounds what a preprocessor may produce from one document.
// Upstream has no bound; a Clash document's aliases and an SSD document's
// inherited fields can make the output far larger than the input, so a
// preprocessor whose output would pass this bound refuses the document.
const MaxExpandedBytes = 4 * MaxDocumentBytes

var (
	// ErrDocumentTooLarge refuses a raw document above MaxDocumentBytes.
	ErrDocumentTooLarge = errors.New("parse: document exceeds the raw document bound")
	// ErrExpansionTooLarge refuses a document whose preprocessed text would
	// exceed MaxExpandedBytes.
	ErrExpansionTooLarge = errors.New("parse: preprocessed document exceeds the expansion bound")
)

// Options are the switches of one parse. AllowExternal is the H3 switch of
// normaliser.md section 4: true only for a local record whose operator set
// the explicit external opt-in.
type Options struct{ AllowExternal bool }

// Warning is something the parse logged. Line is the 1-based position of the
// line in the preprocessed text, or 0 for the whole document; a warning never
// carries the line's text.
type Warning struct {
	Line    int
	Message string
}

// Kind names the preprocessor whose output was split into lines.
type Kind string

const (
	KindPlain          Kind = "plain" // no preprocessor matched
	KindHTML           Kind = "html"
	KindClash          Kind = "clash"
	KindBase64         Kind = "base64"
	KindSSD            Kind = "ssd"
	KindProfile        Kind = "profile"
	KindBase64Fallback Kind = "base64-fallback"
)

// Document turns raw subscription text into nodes: preprocessors in
// parser.md section 2 order, line split, parser selection (section 3), one
// line parser, then normalise.Node on each node and normalise.Document at
// the end. opts.AllowExternal is the H3 switch (true only for a local record
// with the explicit opt-in). Document never returns an error for an ordinary
// document: a line no parser accepts is skipped with a warning; an N33
// whole-document failure (normalise.ErrCertificateNotPEM) is the one error
// besides the Lattice document bounds (ErrDocumentTooLarge,
// ErrExpansionTooLarge).
func Document(text string, opts Options) (nodes []*nodemodel.Node, warnings []Warning, err error) {
	return document(text, opts, nil)
}

// Line parses one line with the parser selection of section 3. Used by tests
// and by the collection path when a member is a URI list. ok is false when no
// parser accepts the line or the normaliser drops the node; err is set only
// for an N33 failure.
func Line(line string, opts Options) (*nodemodel.Node, bool, error) {
	line = TrimECMAScript(wellFormed(line))
	if line == "" || len(line) > nodemodel.MaxLineBytes {
		return nil, false, nil
	}
	st := newLineState(nil)
	fields, _, ok := st.parse(line)
	if !ok {
		return nil, false, nil
	}
	n := &nodemodel.Node{Fields: fields}
	drop, _, err := normalise.Node(n, opts.AllowExternal)
	if err != nil {
		return nil, false, err
	}
	if drop {
		return nil, false, nil
	}
	out := normalise.Document([]*nodemodel.Node{n})
	if len(out) == 0 {
		return nil, false, nil
	}
	return out[0], true, nil
}

// Preprocess exposes section 2 alone, for tests and for context.raw in S3.
// A document above MaxDocumentBytes, or one a preprocessor refuses under
// MaxExpandedBytes, gives the empty text.
func Preprocess(text string) (string, Kind) {
	if len(text) > MaxDocumentBytes {
		return "", KindPlain
	}
	out, kind, _, err := preprocess(wellFormed(text))
	if err != nil {
		return "", kind
	}
	return out, kind
}

// document is Document with a hook the corpus test uses to learn which
// parsers a line needed that are not implemented yet.
func document(text string, opts Options, onUnimplemented func(label string)) ([]*nodemodel.Node, []Warning, error) {
	if len(text) > MaxDocumentBytes {
		return nil, nil, ErrDocumentTooLarge
	}
	return parseText(text, opts, onUnimplemented)
}

// parseText is the whole pipeline without the raw document bound, which
// Document applies first. The linear-time test drives it past the bound.
func parseText(text string, opts Options, onUnimplemented func(label string)) ([]*nodemodel.Node, []Warning, error) {
	pre, _, warnings, err := preprocess(wellFormed(text))
	if err != nil {
		return nil, warnings, err
	}
	st := newLineState(onUnimplemented)
	var nodes []*nodemodel.Node
	var lines []int // the line each node came from
	for start, lineNo := 0, 1; start <= len(pre); lineNo++ {
		end := strings.IndexByte(pre[start:], '\n')
		if end < 0 {
			end = len(pre)
		} else {
			end += start
		}
		line := TrimECMAScript(pre[start:end])
		start = end + 1
		if line == "" {
			continue
		}
		if len(line) > nodemodel.MaxLineBytes {
			warnings = append(warnings, Warning{Line: lineNo, Message: "line exceeds " + strconv.Itoa(nodemodel.MaxLineBytes) + " bytes; skipped"})
			continue
		}
		fields, parser, ok := st.parse(line)
		if !ok {
			warnings = append(warnings, Warning{Line: lineNo, Message: "no parser accepts the line; skipped"})
			continue
		}
		n := &nodemodel.Node{Fields: fields}
		drop, reason, err := normalise.Node(n, opts.AllowExternal)
		if err != nil {
			return nil, warnings, err
		}
		if drop {
			warnings = append(warnings, Warning{Line: lineNo, Message: parsers[parser].label + " node dropped: " + reason})
			continue
		}
		nodes = append(nodes, n)
		lines = append(lines, lineNo)
	}
	out, notes := normalise.DocumentNotes(nodes)
	for _, note := range notes {
		warnings = append(warnings, Warning{Line: lines[note.Index], Message: note.Rule + ": " + note.Message})
	}
	return out, warnings, nil
}

// lineState is the per-document parser state: the parser that produced the
// most recent node, and the object value the Clash object test decoded for
// the current line (so the parser does not decode it twice).
type lineState struct {
	last            int
	objectLine      string
	objectValue     any
	onUnimplemented func(label string)
}

func newLineState(onUnimplemented func(string)) *lineState {
	return &lineState{last: -1, onUnimplemented: onUnimplemented}
}

// parse runs parser selection on one trimmed line: the parser that produced
// the most recent node first, then the ordered search. A parser accepts a
// line when its test passes and its parse step returns no error.
func (st *lineState) parse(line string) (map[string]any, int, bool) {
	if st.last >= 0 {
		if f, ok := st.try(st.last, line); ok {
			return f, st.last, true
		}
	}
	for i := range parsers {
		if f, ok := st.try(i, line); ok {
			st.last = i
			return f, i, true
		}
	}
	return nil, -1, false
}

func (st *lineState) try(i int, line string) (map[string]any, bool) {
	p := &parsers[i]
	if !p.test(line, st) {
		return nil, false
	}
	if p.parse == nil {
		if st.onUnimplemented != nil {
			st.onUnimplemented(p.label)
		}
		return nil, false
	}
	f, err := p.parse(line, st)
	if err != nil || f == nil {
		return nil, false
	}
	return f, true
}

// lineParser is one row of the parser table of parser.md 3.1. label names
// the grammar for warnings and for the corpus test's pending list; parse is
// nil for a grammar that has not landed yet, which makes the row accept
// nothing.
type lineParser struct {
	label string
	test  func(line string, st *lineState) bool
	parse func(line string, st *lineState) (map[string]any, error)
}

// scheme builds a URI row: the test is a prefix match and parse receives the
// text after the matched prefix.
func scheme(label string, parse func(rest string, st *lineState) (map[string]any, error), prefixes ...string) lineParser {
	p := lineParser{label: label, test: prefixTest(prefixes...)}
	if parse != nil {
		p.parse = func(line string, st *lineState) (map[string]any, error) {
			for _, pre := range prefixes {
				if strings.HasPrefix(line, pre) {
					return parse(line[len(pre):], st)
				}
			}
			return nil, errReject
		}
	}
	return p
}

// prefixTest is the test of a URI row: the line starts with one of the
// prefixes.
func prefixTest(prefixes ...string) func(string, *lineState) bool {
	return func(line string, _ *lineState) bool {
		for _, pre := range prefixes {
			if strings.HasPrefix(line, pre) {
				return true
			}
		}
		return false
	}
}

// typed builds a Surge, Loon or Quantumult X row from a test on the line.
func typed(label string, test func(line string) bool) lineParser {
	return lineParser{label: label, test: func(line string, _ *lineState) bool { return test(line) }}
}

// parsers is the table of parser.md 3.1, in order. The Surge rows share one
// grammar, as do the Loon rows 30 to 38 and the Quantumult X rows; rows 29
// and 39 have their own parsers.
var parsers = []lineParser{
	/* 1 */ {label: "socks5-http", test: prefixTest(proxySchemePrefixes...), parse: parseProxyLine},
	/* 2 */ scheme("socks", parseSocks, "socks://"),
	/* 3 */ scheme("ss", parseSS, "ss://"),
	/* 4 */ scheme("ssr", parseSSR, "ssr://"),
	/* 5 */ scheme("vmess", parseVMess, "vmess://"),
	/* 6 */ scheme("vless", parseVLESS, "vless://"),
	/* 7 */ scheme("tuic", parseTUIC, "tuic://"),
	/* 8 */ scheme("wireguard", parseWireGuard, "wireguard://", "wg://"),
	/* 9 */ scheme("hysteria", parseHysteria, "hysteria://", "hy://"),
	/* 10 */ scheme("hysteria2", parseHysteria2, "hysteria2://", "hy2://"),
	/* 11 */ scheme("trojan", parseTrojan, "trojan://"),
	/* 12 */ scheme("anytls", parseAnyTLS, "anytls://"),
	/* 13 */ {label: "clash", test: clashObjectTest},
	/* 14 */ typed("surge", surgeDirectTest),
	/* 15 */ typed("surge", typeWordPrefix("anytls")),
	/* 16 */ typed("surge", typeWordPrefix("trust-tunnel")),
	/* 17 */ typed("surge", typeWordPrefix("masque")),
	/* 18 */ typed("surge", typeWordPrefix("h2-connect")),
	/* 19 */ typed("surge", typeWordPrefix("ssh")),
	/* 20 */ typed("surge", typeWordPrefix("ss")),
	/* 21 */ typed("surge", func(l string) bool { return typeWordPrefix("vmess")(l) && strings.Contains(l, "username") }),
	/* 22 */ typed("surge", typeWordPrefix("trojan")),
	/* 23 */ typed("surge", func(l string) bool { return typeWordPrefix("http")(l) && !hasLoonOnlyOption(l) }),
	/* 24 */ typed("surge", typeWordPrefix("snell")),
	/* 25 */ typed("surge", typeWordPrefix("tuic")),
	/* 26 */ typed("surge", typeWordPrefix("wireguard")),
	/* 27 */ typed("surge", typeWordPrefix("hysteria2")),
	/* 28 */ typed("surge", func(l string) bool { return typeWordPrefix("socks5")(l) && !hasLoonOnlyOption(l) }),
	/* 29 */ typed("surge-external", typeWordPrefix("external")),
	/* 30 */ typed("loon", func(l string) bool { return strings.ToLower(typeWord(l)) == "shadowsocks" }),
	/* 31 */ typed("loon", func(l string) bool { return strings.ToLower(typeWord(l)) == "shadowsocksr" }),
	/* 32 */ typed("loon", func(l string) bool { return typeWordPrefixFold("vmess")(l) && !strings.Contains(l, "username") }),
	/* 33 */ typed("loon", typeWordPrefixFold("vless")),
	/* 34 */ typed("loon", typeWordPrefixFold("hysteria2")),
	/* 35 */ typed("loon", typeWordPrefixFold("trojan")),
	/* 36 */ typed("loon", typeWordPrefixFold("anytls")),
	/* 37 */ typed("loon", typeWordPrefixFold("http")),
	/* 38 */ typed("loon", typeWordPrefixFold("socks5")),
	/* 39 */ typed("loon-wireguard", typeWordPrefixFold("wireguard")),
	/* 40 */ typed("qx", func(l string) bool { return qxFirstField(l, "shadowsocks") && !strings.Contains(l, "ssr-protocol") }),
	/* 41 */ typed("qx", func(l string) bool { return qxFirstField(l, "shadowsocks") && strings.Contains(l, "ssr-protocol") }),
	/* 42 */ typed("qx", func(l string) bool { return qxFirstField(l, "vmess") }),
	/* 43 */ typed("qx", func(l string) bool { return qxFirstField(l, "vless") }),
	/* 44 */ typed("qx", func(l string) bool { return qxFirstField(l, "anytls") }),
	/* 45 */ typed("qx", func(l string) bool { return qxFirstField(l, "trojan") }),
	/* 46 */ typed("qx", func(l string) bool { return qxFirstField(l, "http") }),
	/* 47 */ typed("qx", func(l string) bool { return qxFirstField(l, "socks5") }),
}

// firstField is the text before the first comma of the line.
func firstField(line string) string {
	if i := strings.IndexByte(line, ','); i >= 0 {
		return line[:i]
	}
	return line
}

// typeWord is the text after the first "=" of the first field, trimmed; ""
// when the first field has no "=".
func typeWord(line string) string {
	_, after, ok := strings.Cut(firstField(line), "=")
	if !ok {
		return ""
	}
	return TrimECMAScript(after)
}

func typeWordPrefix(prefix string) func(string) bool {
	return func(line string) bool {
		_, after, ok := strings.Cut(firstField(line), "=")
		return ok && strings.HasPrefix(TrimECMAScript(after), prefix)
	}
}

func typeWordPrefixFold(prefix string) func(string) bool {
	return func(line string) bool {
		_, after, ok := strings.Cut(firstField(line), "=")
		w := TrimECMAScript(after)
		return ok && len(w) >= len(prefix) && strings.EqualFold(w[:len(prefix)], prefix)
	}
}

// surgeDirectTest is row 14: the first field holds "=", optional white
// space and "direct" (case-sensitive).
func surgeDirectTest(line string) bool {
	f := firstField(line)
	for i := strings.IndexByte(f, '='); i >= 0; {
		if strings.HasPrefix(strings.TrimLeftFunc(f[i+1:], isESWhiteSpace), "direct") {
			return true
		}
		next := strings.IndexByte(f[i+1:], '=')
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return false
}

// qxFirstField is the Quantumult X test: the first field, trimmed, starts
// with word, optional white space and "=".
func qxFirstField(line, word string) bool {
	f := TrimECMAScript(firstField(line))
	if !strings.HasPrefix(f, word) {
		return false
	}
	return strings.HasPrefix(strings.TrimLeftFunc(f[len(word):], isESWhiteSpace), "=")
}

// loonOnlyOptions are the keys of parser.md 3.2.
var loonOnlyOptions = []string{"fast-open", "over-tls", "tls-name", "ip-mode", "tls-cert-sha256", "tls-pubkey-sha256", "server-dns"}

// hasLoonOnlyOption reports a Loon-only key, in any letter case, at the
// start of the line or after a comma and optional white space, followed by
// optional white space and "=".
func hasLoonOnlyOption(line string) bool {
	at := func(s string) bool {
		s = strings.TrimLeftFunc(s, isESWhiteSpace)
		for _, k := range loonOnlyOptions {
			if len(s) >= len(k) && strings.EqualFold(s[:len(k)], k) &&
				strings.HasPrefix(strings.TrimLeftFunc(s[len(k):], isESWhiteSpace), "=") {
				return true
			}
		}
		return false
	}
	if at(line) {
		return true
	}
	for i := strings.IndexByte(line, ','); i >= 0; {
		if at(line[i+1:]) {
			return true
		}
		next := strings.IndexByte(line[i+1:], ',')
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return false
}

// clashObjectTest is row 13: the line parses as JSON5, or failing that as
// YAML, into an object with a truthy type. The decoded value is kept for the
// parse step.
func clashObjectTest(line string, st *lineState) bool {
	v, err := decodeJSON5(line)
	if err != nil {
		if v, err = decodeYAML(line); err != nil {
			return false
		}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	t, ok := m["type"]
	if !truthy(t, ok) {
		return false
	}
	st.objectLine, st.objectValue = line, m
	return true
}

// preprocessor is one row of the table of parser.md section 2. apply returns
// ok=false when its test fails or its transformation fails with an error,
// and the next row is tried; ok=true ends the search, even when the row
// returns the raw text with a warning. err refuses the document (a Lattice
// bound).
type preprocessor struct {
	kind  Kind
	apply func(text string) (out string, ok bool, warning string, err error)
}

var preprocessors = []preprocessor{
	{KindHTML, preprocessHTML},
	{KindClash, preprocessClash},
	{KindBase64, preprocessBase64Known},
	{KindSSD, preprocessSSD},
	{KindProfile, preprocessProfile},
	{KindBase64Fallback, preprocessBase64Fallback},
}

// preprocess runs the preprocessors in order; the first that matches
// produces the text split into lines. Without a match the raw document is
// used unchanged.
func preprocess(text string) (string, Kind, []Warning, error) {
	for _, p := range preprocessors {
		out, ok, warning, err := p.apply(text)
		if err != nil {
			return "", p.kind, nil, err
		}
		if !ok {
			continue
		}
		var warnings []Warning
		if warning != "" {
			warnings = append(warnings, Warning{Message: warning})
		}
		return out, p.kind, warnings, nil
	}
	return text, KindPlain, nil, nil
}
