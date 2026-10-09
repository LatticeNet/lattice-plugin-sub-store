package parse

import (
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise"
)

// The Surge line grammar of parser.md section 6 (parser rows 14 to 28), the
// Surge external parser (row 29), and the option engine the Loon (section 7)
// and Quantumult X (section 8) grammars share, since their options follow
// the Surge rules of 6.2.

// lineNode is what a line grammar builds: the node fields, and the values
// its post-processing reads that are not fields themselves (the WebSocket
// flag, obfs and Shadow-TLS settings, a transport).
type lineNode struct {
	f map[string]any
	x map[string]any
}

func newLineNode() *lineNode {
	return &lineNode{f: map[string]any{}, x: map[string]any{}}
}

// optKind is how an option value is read.
type optKind int

const (
	// optText runs to the next comma and keeps trailing white space.
	optText optKind = iota
	// optTextNoEq is a text without "=" or ","; an "=" after it rejects the
	// line (Quantumult X).
	optTextNoEq
	// optTextToKey runs to a comma followed by optional white space, a key
	// without "=" or ",", optional white space and "=", or to the end of the
	// line, so it may hold commas (the Quantumult X password).
	optTextToKey
	// optBool is exactly true or false, lower case.
	optBool
	// optDigits is decimal digits, read as a number.
	optDigits
	// optEnum is one of words, tried in order.
	optEnum
	// optHeaders is a header list with the separator sep (6.5).
	optHeaders
	// optQuotedOrText is a double- or single-quoted value, which may hold
	// commas, or else a value to the next comma (the Surge alpn).
	optQuotedOrText
	// optQuoted is a double-quoted value only; anything else is an unknown
	// option (the Loon alpn and server-ports).
	optQuoted
	// optQuotedDigits is digits, optionally in matching quotes.
	optQuotedDigits
	// optQuotedOrToKey is a double-quoted value, or else a value running to
	// the next ", key=" (the Loon server-dns).
	optQuotedOrToKey
)

// optSpec is one accepted option: how its value is read and what it sets.
// set may reject the line by returning errReject.
type optSpec struct {
	kind  optKind
	words []string
	sep   byte
	set   func(n *lineNode, v any) error
}

// readState is the outcome of reading an accepted option's value.
type readState int

const (
	readOK      readState = iota
	readNoMatch           // the value does not begin with an accepted token
	readReject            // a token matched and other characters follow it
)

// isGap is the white space allowed around "," and "=": space, tab and
// carriage return.
func isGap(c byte) bool { return c == ' ' || c == '\t' || c == '\r' }

func skipGap(s string, i int) int {
	for i < len(s) && isGap(s[i]) {
		i++
	}
	return i
}

// commaFrom is the index of the next comma at or after i, or len(s).
func commaFrom(s string, i int) int {
	if c := strings.IndexByte(s[i:], ','); c >= 0 {
		return i + c
	}
	return len(s)
}

// atOptionEnd reports that only white space separates i from the next comma
// or the end of the line.
func atOptionEnd(s string, i int) bool {
	i = skipGap(s, i)
	return i == len(s) || s[i] == ','
}

// tokenEnd finishes a token that ends at k: white space may come before the
// next comma or the end of the line, and anything else rejects the line.
func tokenEnd(s string, k int, v any) (int, any, readState) {
	if atOptionEnd(s, k) {
		return k, v, readOK
	}
	return k, nil, readReject
}

// keyAhead reports a comma at i followed by optional white space, a key
// without "=" or ",", optional white space and "=". The scan stops at the
// next comma, so testing every comma of a line stays linear.
func keyAhead(s string, i int) bool {
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case ',':
			return false
		case '=':
			return strings.Trim(s[i+1:j], " \t\r") != ""
		}
	}
	return false
}

// lineScan is a line under the option engine. It remembers, for each quote
// kind, a position from which no such quote ends an option, and the last
// token run a header-name test scanned, so a line made of header lists that
// never close or of separators is read in linear time. A plain search for a
// closing quote needs no memory: it always starts just after a quote, so it
// can only fail from the last quote of its kind.
type lineScan struct {
	s          string
	noQuoteEnd [2]int
	runStart   int
	runEnd     int
	runColon   bool
}

func newLineScan(s string) *lineScan {
	n := len(s) + 1
	return &lineScan{s: s, noQuoteEnd: [2]int{n, n}, runStart: -1, runEnd: -1}
}

func quoteKind(q byte) int {
	if q == '\'' {
		return 1
	}
	return 0
}

// closing returns the index of the first q at or after from, or -1.
func (ls *lineScan) closing(from int, q byte) int {
	if i := strings.IndexByte(ls.s[from:], q); i >= 0 {
		return from + i
	}
	return -1
}

// closingAtEnd returns the first q at or after from that optional white
// space and a comma or the end of the line follow, or -1.
func (ls *lineScan) closingAtEnd(from int, q byte) int {
	k := quoteKind(q)
	for i := from; i < ls.noQuoteEnd[k]; {
		j := ls.closing(i, q)
		if j < 0 {
			break
		}
		if atOptionEnd(ls.s, j+1) {
			return j
		}
		i = j + 1
	}
	if from < ls.noQuoteEnd[k] {
		ls.noQuoteEnd[k] = from
	}
	return -1
}

// quoted reads a value in quotes q at j: the text up to the next q, which
// must end the option.
func (ls *lineScan) quoted(j int, q byte) (int, any, readState, bool) {
	c := ls.closing(j+1, q)
	if c < 0 {
		return 0, nil, readNoMatch, false
	}
	k, v, st := tokenEnd(ls.s, c+1, ls.s[j+1:c])
	return k, v, st, true
}

// textToKey runs from j to a comma that a key and "=" follow, or to the end
// of the line.
func textToKey(s string, j int) (int, any, readState) {
	for i := j; i < len(s); i++ {
		if s[i] == ',' && keyAhead(s, i) {
			return i, s[j:i], readOK
		}
	}
	return len(s), s[j:], readOK
}

// readValue reads the value of an accepted option that starts at j.
func readValue(ls *lineScan, j int, spec optSpec) (int, any, readState) {
	s := ls.s
	rest := s[j:]
	isQuote := len(rest) > 0 && (rest[0] == '"' || rest[0] == '\'')
	switch spec.kind {
	case optText:
		end := commaFrom(s, j)
		return end, s[j:end], readOK
	case optTextNoEq:
		end := j
		for end < len(s) && s[end] != ',' && s[end] != '=' {
			end++
		}
		return tokenEnd(s, end, s[j:end])
	case optTextToKey:
		return textToKey(s, j)
	case optBool:
		switch {
		case strings.HasPrefix(rest, "true"):
			return tokenEnd(s, j+4, true)
		case strings.HasPrefix(rest, "false"):
			return tokenEnd(s, j+5, false)
		}
		return j, nil, readNoMatch
	case optDigits:
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k == j {
			return j, nil, readNoMatch
		}
		return tokenEnd(s, k, numberValue(s[j:k]))
	case optEnum:
		for _, w := range spec.words {
			if strings.HasPrefix(rest, w) {
				return tokenEnd(s, j+len(w), w)
			}
		}
		return j, nil, readNoMatch
	case optHeaders:
		headers, end, ok := headerList(ls, j, spec.sep)
		if !ok {
			return j, nil, readNoMatch
		}
		return tokenEnd(s, end, headers)
	case optQuotedOrText:
		if isQuote {
			if k, v, st, ok := ls.quoted(j, rest[0]); ok {
				return k, v, st
			}
		}
		end := commaFrom(s, j)
		return end, s[j:end], readOK
	case optQuoted:
		if isQuote && rest[0] == '"' {
			if k, v, st, ok := ls.quoted(j, '"'); ok {
				return k, v, st
			}
		}
		return j, nil, readNoMatch
	case optQuotedDigits:
		k, q := j, byte(0)
		if isQuote {
			q = rest[0]
			k++
		}
		d := k
		for d < len(s) && s[d] >= '0' && s[d] <= '9' {
			d++
		}
		if d == k {
			return j, nil, readNoMatch
		}
		v := numberValue(s[k:d])
		if q != 0 {
			if d >= len(s) || s[d] != q {
				return j, nil, readNoMatch
			}
			return tokenEnd(s, d+1, v)
		}
		return tokenEnd(s, d, v)
	case optQuotedOrToKey:
		if isQuote && rest[0] == '"' {
			if k, v, st, ok := ls.quoted(j, '"'); ok {
				return k, v, st
			}
		}
		return textToKey(s, j)
	}
	return j, nil, readNoMatch
}

// readOptions reads ", key=value" options from i to the end of the line
// under the rules of parser.md 6.2: an accepted key whose value begins with
// an accepted token sets its field; a token followed by other characters
// rejects the line; any other option is ignored provided neither its key
// nor its value holds "=" or ",", and rejects the line otherwise.
func readOptions(s string, i int, accept map[string]optSpec, n *lineNode) bool {
	ls := newLineScan(s)
	for {
		i = skipGap(s, i)
		if i == len(s) {
			return true
		}
		if s[i] != ',' {
			return false
		}
		i = skipGap(s, i+1)
		end := commaFrom(s, i)
		eq := strings.IndexByte(s[i:end], '=')
		if eq < 0 {
			return false
		}
		key := strings.TrimRight(s[i:i+eq], " \t\r")
		if spec, ok := accept[key]; ok {
			k, v, st := readValue(ls, skipGap(s, i+eq+1), spec)
			switch st {
			case readOK:
				if err := spec.set(n, v); err != nil {
					return false
				}
				i = k
				continue
			case readReject:
				return false
			}
		}
		// An unknown option, or an accepted one whose value matched nothing.
		if eq == 0 || strings.Count(s[i:end], "=") != 1 {
			return false
		}
		i = end
	}
}

// Value conversions of parser.md 6.2.

// stripQuotes removes one pair of surrounding double quotes, then one pair
// of surrounding single quotes ("strip").
func stripQuotes(s string) string {
	return unquote(unquote(s, '"'), '\'')
}

// unquote removes one pair of q around s, when q is the very first and the
// very last character.
func unquote(s string, q byte) string {
	if len(s) >= 2 && s[0] == q && s[len(s)-1] == q {
		return s[1 : len(s)-1]
	}
	return s
}

// trimStrip trims, then removes one pair of matching quotes of either kind.
func trimStrip(s string) string {
	s = TrimECMAScript(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// Option spec builders.

func setText(field string, conv func(string) string) optSpec {
	return optSpec{kind: optText, set: func(n *lineNode, v any) error {
		s := v.(string)
		if conv != nil {
			s = conv(s)
		}
		n.f[field] = s
		return nil
	}}
}

func setKind(kind optKind, field string) optSpec {
	return optSpec{kind: kind, set: func(n *lineNode, v any) error {
		n.f[field] = v
		return nil
	}}
}

func keepText(key string, conv func(string) string) optSpec {
	return optSpec{kind: optText, set: func(n *lineNode, v any) error {
		s := v.(string)
		if conv != nil {
			s = conv(s)
		}
		n.x[key] = s
		return nil
	}}
}

func keepKind(kind optKind, key string, words ...string) optSpec {
	return optSpec{kind: kind, words: words, set: func(n *lineNode, v any) error {
		n.x[key] = v
		return nil
	}}
}

func setEnum(field string, words ...string) optSpec {
	return optSpec{kind: optEnum, words: words, set: func(n *lineNode, v any) error {
		n.f[field] = v
		return nil
	}}
}

func trimText(s string) string { return TrimECMAScript(s) }

func dequote(s string) string { return unquote(s, '"') }

func mergeOpts(groups ...map[string]optSpec) map[string]optSpec {
	out := map[string]optSpec{}
	for _, g := range groups {
		for k, v := range g {
			out[k] = v
		}
	}
	return out
}

// headerList reads a header list that starts at j (parser.md 6.5) and
// returns the headers and the index after the list; ok is false for an
// empty list, which is an unknown option.
func headerList(ls *lineScan, j int, sep byte) (map[string]any, int, bool) {
	s := ls.s
	from, to, end := j, -1, -1
	if j < len(s) && (s[j] == '"' || s[j] == '\'') && !opensQuotedName(ls, j) {
		// The list runs to the matching closing quote that the end of the
		// line or a comma follows, and loses the outer pair.
		if c := ls.closingAtEnd(j+1, s[j]); c >= 0 {
			from, to, end = j+1, c, c+1
		}
	}
	if to < 0 {
		to = scanHeaderPairs(ls, j, len(s), sep, nil)
		end = to
	}
	if from == to {
		return nil, end, false
	}
	var pairs []string
	scanHeaderPairs(ls, from, to, sep, &pairs)
	headers := map[string]any{}
	for _, p := range pairs {
		if name, value, ok := splitHeaderPair(p); ok {
			headers[name] = value
		}
	}
	return headers, end, true
}

// opensQuotedName reports that the quote at j opens a quoted header name: a
// closing quote of the same kind, then a ":".
func opensQuotedName(ls *lineScan, j int) bool {
	c := ls.closing(j+1, ls.s[j])
	if c < 0 {
		return false
	}
	i := skipGap(ls.s, c+1)
	return i < len(ls.s) && ls.s[i] == ':'
}

// isTokenChar is a character of an RFC 9110 token.
func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

// headerNameAhead reports optional white space, a header name (a token or a
// quoted name), optional white space and ":" from i, before to. A token run
// may hold the separator ("|" is a token character), so the result for the
// last run is kept and a test that starts inside it reuses it.
func headerNameAhead(ls *lineScan, i, to int) bool {
	s := ls.s
	i = skipGap(s, i)
	if i >= to {
		return false
	}
	if s[i] == '"' || s[i] == '\'' {
		c := ls.closing(i+1, s[i])
		if c < 0 || c >= to {
			return false
		}
		k := skipGap(s, c+1)
		return k < to && s[k] == ':'
	}
	if !isTokenChar(s[i]) {
		return false
	}
	if i > ls.runStart && i < ls.runEnd {
		return ls.runColon
	}
	e := i
	for e < to && isTokenChar(s[e]) {
		e++
	}
	k := skipGap(s, e)
	ls.runStart, ls.runEnd, ls.runColon = i, e, k < to && s[k] == ':'
	return ls.runColon
}

// scanHeaderPairs walks a header list from from to to: a pair is a name (a
// quoted name included), a ":" and a value, where a value that starts with
// a quote runs to its closing quote; a separator splits pairs only when a
// header name and ":" follow it. Without pairs the walk stops at a comma
// outside a quoted name or value and returns its index; with pairs it
// records each pair.
func scanHeaderPairs(ls *lineScan, from, to int, sep byte, pairs *[]string) int {
	s := ls.s
	ls.runStart, ls.runEnd = -1, -1
	skipQuoted := func(i int) int {
		if i < to && (s[i] == '"' || s[i] == '\'') {
			if c := ls.closing(i+1, s[i]); c >= 0 && c < to {
				return c + 1
			}
		}
		return i
	}
	start, colon := from, false
	i := skipQuoted(skipGap(s, from))
	for i < to {
		c := s[i]
		switch {
		case c == ',' && pairs == nil:
			return i
		case c == ':' && !colon:
			colon = true
			i = skipQuoted(skipGap(s, i+1))
			continue
		case c == sep && headerNameAhead(ls, i+1, to):
			if pairs != nil {
				*pairs = append(*pairs, s[start:i])
			}
			start, colon = i+1, false
			i = skipQuoted(skipGap(s, i+1))
			continue
		}
		i++
	}
	if pairs != nil {
		*pairs = append(*pairs, s[start:to])
	}
	return to
}

// splitHeaderPair splits a pair at its first ":" outside a quoted name;
// name and value are trim-stripped, and a pair without ":" or with an empty
// name is dropped.
func splitHeaderPair(p string) (string, string, bool) {
	i := skipGap(p, 0)
	if i < len(p) && (p[i] == '"' || p[i] == '\'') {
		if k := strings.IndexByte(p[i+1:], p[i]); k >= 0 {
			i += k + 2
		}
	}
	c := strings.IndexByte(p[i:], ':')
	if c < 0 {
		return "", "", false
	}
	name := trimStrip(p[:i+c])
	if name == "" {
		return "", "", false
	}
	return name, trimStrip(p[i+c+1:]), true
}

// splitALPN is the alpn value rule: split on ",", trimmed, empties dropped.
func splitALPN(s string) []any {
	return splitTrimNonEmpty(s, ",")
}

// Surge options (parser.md 6.3).

var surgeCiphers = []string{
	"aes-128-cfb", "aes-128-ctr", "aes-128-gcm", "aes-192-cfb", "aes-192-ctr",
	"aes-192-gcm", "aes-256-cfb", "aes-256-ctr", "aes-256-gcm", "bf-cfb",
	"camellia-128-cfb", "camellia-192-cfb", "camellia-256-cfb", "cast5-cfb",
	"chacha20-ietf-poly1305", "chacha20-ietf", "chacha20-poly1305", "chacha20",
	"des-cfb", "idea-cfb", "none", "rc2-cfb", "rc4-md5", "rc4", "salsa20",
	"seed-cfb", "xchacha20-ietf-poly1305", "2022-blake3-aes-128-gcm",
	"2022-blake3-aes-256-gcm",
}

var surgeCommon = map[string]optSpec{
	"ip-version":            setText("ip-version", nil),
	"underlying-proxy":      setText("underlying-proxy", nil),
	"tos":                   setKind(optDigits, "tos"),
	"allow-other-interface": setKind(optBool, "allow-other-interface"),
	"interface":             setText("interface", nil),
	"test-url":              setText("test-url", nil),
	"test-udp":              setText("test-udp", nil),
	"test-timeout":          setKind(optDigits, "test-timeout"),
	"hybrid":                setKind(optBool, "hybrid"),
	"no-error-alert":        setText("no-error-alert", nil),
	"block-quic":            setText("block-quic", nil),
}

var surgeTLS = map[string]optSpec{
	"sni": {kind: optText, set: func(n *lineNode, v any) error {
		s := dequote(v.(string))
		if s == "off" {
			n.f["disable-sni"] = true
		} else {
			n.f["sni"] = s
		}
		return nil
	}},
	"server-cert-verify-name":        setText("name-cert-verify", trimStrip),
	"alpn":                           {kind: optQuotedOrText, set: setALPN},
	"server-cert-fingerprint-sha256": setText("tls-fingerprint", trimText),
	"skip-cert-verify":               setKind(optBool, "skip-cert-verify"),
	"client-cert":                    setText("keystore-client-cert", trimStrip),
}

func setALPN(n *lineNode, v any) error {
	if list := splitALPN(v.(string)); len(list) > 0 {
		n.f["alpn"] = list
	}
	return nil
}

var surgeShadowTLS = map[string]optSpec{
	"shadow-tls-version":  keepKind(optDigits, "shadow-tls-version"),
	"shadow-tls-sni":      keepText("shadow-tls-sni", nil),
	"shadow-tls-password": keepText("shadow-tls-password", stripQuotes),
}

var surgeTFO = map[string]optSpec{
	"fast-open": setKind(optBool, "tfo"),
	"tfo":       setKind(optBool, "tfo"),
}

var surgeUDP = map[string]optSpec{"udp-relay": setKind(optBool, "udp")}

var surgePassword = map[string]optSpec{"password": setText("password", stripQuotes)}

var surgeWebSocket = map[string]optSpec{
	"ws":         keepKind(optBool, "ws"),
	"ws-path":    keepText("ws-path", func(s string) string { return stripQuotes(TrimECMAScript(s)) }),
	"ws-headers": {kind: optHeaders, sep: '|', set: func(n *lineNode, v any) error { n.x["ws-headers"] = v; return nil }},
}

var surgeObfs = map[string]optSpec{
	"obfs":      keepKind(optEnum, "obfs", "http", "tls"),
	"obfs-host": keepText("obfs-host", dequote),
	"obfs-uri":  keepText("obfs-uri", nil),
}

var surgeHeaders = map[string]optSpec{
	"headers": {kind: optHeaders, sep: ';', set: func(n *lineNode, v any) error { n.f["headers"] = v; return nil }},
}

var surgeHopInterval = map[string]optSpec{"port-hopping-interval": setKind(optDigits, "hop-interval")}

var surgeStrippedUser = map[string]optSpec{"username": setText("username", stripQuotes)}

// surgeType is one alternative of the Surge grammar.
type surgeType struct {
	keyword     string
	nodeType    string
	tls         bool // the type sets tls: true
	noEndpoint  bool // wireguard and direct have no server or port
	credentials bool // the positional and keyword credential pairs
	portHopping bool // port-hopping is lifted out of the line first
	options     map[string]optSpec
}

// surgeTypes are the alternatives in the order the grammar tries them.
var surgeTypes = []surgeType{
	{keyword: "masque", nodeType: "masque-surge", tls: true, portHopping: true, options: mergeOpts(surgeCommon, surgeStrippedUser, surgePassword, surgeHopInterval, surgeTLS, surgeTFO, surgeUDP, map[string]optSpec{"ecn": setKind(optBool, "ecn")})},
	{keyword: "anytls", nodeType: "anytls", tls: true, options: mergeOpts(surgeCommon, surgePassword, surgeTLS, surgeTFO, map[string]optSpec{"reuse": setKind(optBool, "reuse")})},
	{keyword: "ss", nodeType: "ss", options: mergeOpts(surgeCommon, surgePassword, surgeObfs, surgeTFO, surgeUDP, surgeShadowTLS, map[string]optSpec{
		"encrypt-method": setEnum("cipher", surgeCiphers...),
		"alpn":           {kind: optQuotedOrText, set: setALPN},
		"udp-port":       setKind(optDigits, "udp-port"),
	})},
	{keyword: "vmess", nodeType: "vmess", options: mergeOpts(surgeCommon, surgeWebSocket, surgeTLS, surgeTFO, surgeUDP, surgeShadowTLS, map[string]optSpec{
		"username":       setText("uuid", nil),
		"vmess-aead":     setKind(optBool, "aead"),
		"encrypt-method": setText("cipher", surgeVMessCipher),
		"tls":            setKind(optBool, "tls"),
	})},
	{keyword: "trojan", nodeType: "trojan", options: mergeOpts(surgeCommon, surgePassword, surgeWebSocket, surgeTLS, surgeTFO, surgeUDP, surgeShadowTLS, map[string]optSpec{
		"tls": setKind(optBool, "tls"),
	})},
	{keyword: "h2-connect", nodeType: "h2-connect", tls: true, credentials: true, options: mergeOpts(surgeCommon, surgeHeaders, surgeTLS, surgeTFO, surgeShadowTLS, map[string]optSpec{
		"max-streams": setKind(optQuotedDigits, "max-streams"),
	})},
	{keyword: "https", nodeType: "http", tls: true, credentials: true, options: mergeOpts(surgeCommon, surgeHeaders, surgeTLS, surgeTFO, surgeShadowTLS)},
	{keyword: "http", nodeType: "http", credentials: true, options: mergeOpts(surgeCommon, surgeHeaders, surgeTFO, surgeShadowTLS)},
	{keyword: "snell", nodeType: "snell", options: mergeOpts(surgeCommon, surgeObfs, surgeTFO, surgeUDP, surgeShadowTLS, map[string]optSpec{
		"version": setKind(optDigits, "version"),
		"mode":    setText("mode", nil),
		"psk":     setText("psk", stripQuotes),
		"reuse":   setKind(optBool, "reuse"),
		"alpn":    {kind: optQuotedOrText, set: setALPN},
	})},
	{keyword: "socks5", nodeType: "socks5", credentials: true, options: mergeOpts(surgeCommon, surgeUDP, surgeTFO, surgeShadowTLS)},
	{keyword: "socks5-tls", nodeType: "socks5", tls: true, credentials: true, options: mergeOpts(surgeCommon, surgeUDP, surgeTFO, surgeShadowTLS, surgeTLS)},
	{keyword: "tuic", nodeType: "tuic", portHopping: true, options: mergeOpts(surgeCommon, surgeTLS, surgeTFO, surgeHopInterval, surgeShadowTLS, map[string]optSpec{
		"token": setText("token", nil),
		"ecn":   setKind(optBool, "ecn"),
	})},
	{keyword: "tuic-v5", nodeType: "tuic", portHopping: true, options: mergeOpts(surgeCommon, surgePassword, surgeTLS, surgeTFO, surgeHopInterval, surgeShadowTLS, map[string]optSpec{
		"uuid": setText("uuid", nil),
		"ecn":  setKind(optBool, "ecn"),
	})},
	{keyword: "wireguard", nodeType: "wireguard-surge", noEndpoint: true, options: mergeOpts(surgeCommon, surgeShadowTLS, map[string]optSpec{
		"section-name": setText("section-name", nil),
	})},
	{keyword: "hysteria2", nodeType: "hysteria2", portHopping: true, options: mergeOpts(surgeCommon, surgePassword, surgeTLS, surgeHopInterval, surgeShadowTLS, map[string]optSpec{
		"download-bandwidth":  setText("down", nil),
		"ecn":                 setKind(optBool, "ecn"),
		"salamander-password": obfsPassword("salamander", stripQuotes),
		"gecko-password":      obfsPassword("gecko", stripQuotes),
	})},
	{keyword: "ssh", nodeType: "ssh", credentials: true, options: mergeOpts(surgeCommon, surgeTFO, surgeShadowTLS, map[string]optSpec{
		"server-fingerprint": setText("server-fingerprint", dequote),
		"idle-timeout":       setKind(optDigits, "idle-timeout"),
		"private-key":        setText("keystore-private-key", trimStrip),
	})},
	{keyword: "trust-tunnel", nodeType: "trusttunnel", tls: true, options: mergeOpts(surgeCommon, surgeStrippedUser, surgePassword, surgeHeaders, surgeTLS, surgeTFO, map[string]optSpec{
		"max-streams": setKind(optQuotedDigits, "max-streams"),
		"reuse":       setKind(optBool, "reuse"),
		"h3": {kind: optBool, set: func(n *lineNode, v any) error {
			if v == true {
				n.f["network"] = "h3"
			}
			return nil
		}},
	})},
	{keyword: "direct", nodeType: "direct", noEndpoint: true, options: mergeOpts(surgeCommon, surgeUDP, surgeTFO)},
}

// obfsPassword is salamander-password and gecko-password: obfs-password and
// the obfs they name.
func obfsPassword(obfs string, conv func(string) string) optSpec {
	return optSpec{kind: optText, set: func(n *lineNode, v any) error {
		s := v.(string)
		if conv != nil {
			s = conv(s)
		}
		n.f["obfs-password"] = s
		n.f["obfs"] = obfs
		return nil
	}}
}

// surgeVMessCipher is the Surge VMess encrypt-method rule: aes-128-gcm
// stays, chacha20-ietf-poly1305 becomes chacha20-poly1305, anything else
// becomes auto (chacha20-poly1305 and none included, quirk).
func surgeVMessCipher(s string) string {
	switch strings.ToLower(TrimECMAScript(s)) {
	case "aes-128-gcm":
		return "aes-128-gcm"
	case "chacha20-ietf-poly1305":
		return "chacha20-poly1305"
	}
	return "auto"
}

// keywordAt reports keyword at i of s followed by optional white space and a
// comma or the end of the line, and returns the index after the keyword.
func keywordAt(s string, i int, keyword string, fold bool) (int, bool) {
	if len(s)-i < len(keyword) {
		return 0, false
	}
	w := s[i : i+len(keyword)]
	if w != keyword && !(fold && strings.EqualFold(w, keyword)) {
		return 0, false
	}
	return i + len(keyword), atOptionEnd(s, i+len(keyword))
}

// lineName is the name of a Surge or Loon line: the text before the first
// "=", trimmed, which may not hold a comma. eq is the index of that "=".
func lineName(s string) (name string, eq int, ok bool) {
	eq = strings.IndexByte(s, '=')
	if eq <= 0 || strings.IndexByte(s[:eq], ',') >= 0 {
		return "", 0, false
	}
	return TrimECMAScript(s[:eq]), eq, true
}

// serverPort reads ", server, port" at i: the server runs to the next comma,
// trimmed; the port is digits, absent when outside 0 to 65535 (Surge) and
// refused there when strictPort is set (Loon).
func serverPort(s string, i int, f map[string]any, strictPort bool) (int, bool) {
	i = skipGap(s, i)
	if i >= len(s) || s[i] != ',' {
		return 0, false
	}
	end := commaFrom(s, i+1)
	if end == len(s) {
		return 0, false
	}
	f["server"] = TrimECMAScript(s[i+1 : end])
	j := skipGap(s, end+1)
	k := j
	for k < len(s) && s[k] >= '0' && s[k] <= '9' {
		k++
	}
	if k == j || !atOptionEnd(s, k) {
		return 0, false
	}
	port := numberValue(s[j:k])
	switch {
	case port <= 65535:
		f["port"] = port
	case strictPort:
		return 0, false
	}
	return k, true
}

// liftPortHopping removes ", port-hopping = <list>" (the list optionally in
// matching quotes) from a tuic, tuic-v5, hysteria2 or masque line and
// returns the list with ";" replaced by ",".
func liftPortHopping(s string) (string, string, bool) {
	for i := strings.IndexByte(s, ','); i >= 0; {
		j := skipGap(s, i+1)
		if strings.HasPrefix(s[j:], "port-hopping") {
			k := skipGap(s, j+len("port-hopping"))
			if k < len(s) && s[k] == '=' {
				k = skipGap(s, k+1)
				q := byte(0)
				if k < len(s) && (s[k] == '"' || s[k] == '\'') {
					q = s[k]
					k++
				}
				end := portListEnd(s, k)
				if end > k {
					stop := end
					if q != 0 {
						if stop < len(s) && s[stop] == q {
							stop++
						} else {
							stop = -1
						}
					}
					if stop >= 0 && atOptionEnd(s, stop) {
						list := strings.ReplaceAll(s[k:end], ";", ",")
						return s[:i] + s[skipGap(s, stop):], list, true
					}
				}
			}
		}
		next := strings.IndexByte(s[i+1:], ',')
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return s, "", false
}

// portListEnd is the end of a list of numbers or a-b ranges joined by ","
// or ";" that starts at i, or i when there is none.
func portListEnd(s string, i int) int {
	end := i
	for {
		k := i
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k == i {
			return end
		}
		if k < len(s) && s[k] == '-' {
			r := k + 1
			for r < len(s) && s[r] >= '0' && s[r] <= '9' {
				r++
			}
			if r == k+1 {
				return k
			}
			k = r
		}
		end = k
		if k < len(s) && (s[k] == ',' || s[k] == ';') {
			i = k + 1
			continue
		}
		return end
	}
}

// surgeCredentials reads the optional positional pair ", user, password"
// and then the optional keyword pair ", username=..., password=..." in that
// order. A keyword pair that fails halfway keeps the user name it read
// (quirk); the position then stays before it.
func surgeCredentials(s string, i int, n *lineNode) int {
	if j := skipGap(s, i); j < len(s) && s[j] == ',' {
		u := skipGap(s, j+1)
		uEnd := commaFrom(s, u)
		if uEnd < len(s) && !strings.ContainsRune(s[u:uEnd], '=') && uEnd > u {
			p := skipGap(s, uEnd+1)
			pEnd := commaFrom(s, p)
			n.f["username"] = stripQuotes(TrimECMAScript(s[u:uEnd]))
			n.f["password"] = stripQuotes(s[p:pEnd])
			i = pEnd
		}
	}
	j := skipGap(s, i)
	if j >= len(s) || s[j] != ',' {
		return i
	}
	u, ok := keywordValue(s, skipGap(s, j+1), "username")
	if !ok {
		return i
	}
	uEnd := commaFrom(s, u)
	n.f["username"] = stripQuotes(s[u:uEnd])
	if uEnd == len(s) {
		return i
	}
	p, ok := keywordValue(s, skipGap(s, uEnd+1), "password")
	if !ok {
		return i
	}
	pEnd := commaFrom(s, p)
	n.f["password"] = stripQuotes(s[p:pEnd])
	return pEnd
}

// keywordValue matches key, optional white space, "=" and optional white
// space at i, and returns where the value starts.
func keywordValue(s string, i int, key string) (int, bool) {
	if !strings.HasPrefix(s[i:], key) {
		return 0, false
	}
	k := skipGap(s, i+len(key))
	if k >= len(s) || s[k] != '=' {
		return 0, false
	}
	return skipGap(s, k+1), true
}

// parseSurge is the Surge grammar shared by rows 14 to 28.
func parseSurge(line string, _ *lineState) (map[string]any, error) {
	name, eq, ok := lineName(line)
	if !ok {
		return nil, errReject
	}
	start := skipGap(line, eq+1)
	var t *surgeType
	for k := range surgeTypes {
		if _, ok := keywordAt(line, start, surgeTypes[k].keyword, false); ok {
			t = &surgeTypes[k]
			break
		}
	}
	if t == nil {
		return nil, errReject
	}
	n := newLineNode()
	if t.portHopping {
		var ports string
		if line, ports, ok = liftPortHopping(line); ok {
			n.f["ports"] = ports
		}
	}
	n.f["name"] = name
	n.f["type"] = t.nodeType
	if t.tls {
		n.f["tls"] = true
	}
	if t.keyword == "tuic-v5" {
		n.f["version"] = 5.0
	}
	i := start + len(t.keyword)
	if !t.noEndpoint {
		if i, ok = serverPort(line, i, n.f, false); !ok {
			return nil, errReject
		}
	}
	if t.credentials {
		i = surgeCredentials(line, i, n)
	}
	if !readOptions(line, i, t.options, n) {
		return nil, errReject
	}
	return surgePost(t.keyword, n)
}

// surgePost applies what each type sets, then WebSocket, then Shadow-TLS.
func surgePost(keyword string, n *lineNode) (map[string]any, error) {
	f, x := n.f, n.x
	switch keyword {
	case "ss":
		if mode, ok := x["obfs"]; ok {
			f["plugin"] = "obfs"
			f["plugin-opts"] = obfsOpts(mode, x)
		}
	case "snell":
		if mode, ok := x["obfs"]; ok {
			f["obfs-opts"] = obfsOpts(mode, x)
		}
		if m, ok := f["mode"].(string); ok {
			switch m = trimStrip(m); m {
			case "default", "unshaped", "unsafe-raw":
				f["mode"] = m
			default:
				delete(f, "mode")
			}
		}
	case "vmess":
		if _, ok := f["cipher"]; !ok {
			f["cipher"] = "auto"
		}
		if f["aead"] == true {
			f["alterId"] = 0.0
		} else {
			f["alterId"] = 1.0
		}
	}
	if _, ok := x["ws"]; ok {
		f["network"] = "ws"
		opts := map[string]any{}
		if p, ok := x["ws-path"]; ok {
			opts["path"] = p
		}
		if h, ok := x["ws-headers"].(map[string]any); ok {
			if host, ok := h["Host"].(string); ok {
				h["Host"] = dequote(host)
			}
			opts["headers"] = h
		}
		f["ws-opts"] = opts
	}
	if err := applyShadowTLS(n, 2); err != nil {
		return nil, err
	}
	return f, nil
}

// obfsOpts builds plugin-opts or obfs-opts from an obfs mode and the obfs
// host and path given with it.
func obfsOpts(mode any, x map[string]any) map[string]any {
	opts := map[string]any{"mode": mode}
	if h, ok := x["obfs-host"]; ok {
		opts["host"] = h
	}
	if p, ok := x["obfs-uri"]; ok {
		opts["path"] = p
	}
	return opts
}

// applyShadowTLS builds the shadow-tls plugin when a Shadow-TLS password was
// given: a version below 2 rejects the line; defaultVersion stands in for a
// missing version (0 leaves it absent, as Loon does). An alpn moves into the
// plugin options, and the plugin replaces any obfs plugin.
func applyShadowTLS(n *lineNode, defaultVersion float64) error {
	password, ok := n.x["shadow-tls-password"]
	if !ok {
		return nil
	}
	opts := map[string]any{"password": password}
	version, hasVersion := n.x["shadow-tls-version"].(float64)
	if !hasVersion && defaultVersion > 0 {
		version, hasVersion = defaultVersion, true
	}
	if hasVersion {
		if version < 2 {
			return errReject
		}
		opts["version"] = version
	}
	if h, ok := n.x["shadow-tls-sni"]; ok {
		opts["host"] = h
	}
	if a, ok := n.f["alpn"]; ok {
		opts["alpn"] = a
		delete(n.f, "alpn")
	}
	n.f["plugin"] = "shadow-tls"
	n.f["plugin-opts"] = opts
	return nil
}

// parseSurgeExternal is row 29: name = external, exec = ..., local-port =
// ..., args = ..., addresses = ...
func parseSurgeExternal(line string, _ *lineState) (map[string]any, error) {
	name, eq, ok := lineName(line)
	if !ok {
		return nil, errReject
	}
	start := skipGap(line, eq+1)
	i, ok := keywordAt(line, start, "external", false)
	if !ok {
		return nil, errReject
	}
	f := map[string]any{"name": name, "type": "external"}
	args, addresses := []any{}, []any{}
	for _, seg := range splitOutsideQuotes(line[i:]) {
		key, value, found := strings.Cut(seg, "=")
		if !found {
			continue
		}
		value = unquote(TrimECMAScript(value), '"')
		switch TrimECMAScript(key) {
		case "exec":
			if _, ok := f["exec"]; !ok {
				f["exec"] = value
			}
		case "local-port":
			if _, ok := f["local-port"]; !ok {
				f["local-port"] = value
			}
		case "args":
			args = append(args, value)
		case "addresses":
			a := strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
			if normalise.IsIPv4Literal(a) || normalise.IsIPv6Literal(a) {
				addresses = append(addresses, a)
			}
		}
	}
	f["args"] = args
	f["addresses"] = addresses
	return f, nil
}

// splitOutsideQuotes splits on commas that are not inside double quotes.
func splitOutsideQuotes(s string) []string {
	var out []string
	start, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}
