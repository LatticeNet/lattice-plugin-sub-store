package nodemodel

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The bounds of normaliser.md section 5. Upstream has none; Lattice bounds
// every field so one hostile node cannot inflate a plan, a snapshot or a
// script payload. The numbers sit far above anything a real subscription
// carries, so no conformance input reaches them. The specification says the
// S1 model lane may change them only by changing its table, so a change here
// starts there.
const (
	// MaxLineBytes is the bound on one line after preprocessing; a longer
	// line is skipped as unparseable (the parser applies it).
	MaxLineBytes = 64 << 10
	// MaxNestingDepth is the deepest object or list nesting inside one node,
	// counting the node object itself as level one.
	MaxNestingDepth = 16
	// MaxNodeBytes bounds the compact JSON of a whole node.
	MaxNodeBytes = 64 << 10

	maxNameBytes        = 1 << 10
	maxServerBytes      = 255
	maxEnumBytes        = 64
	maxCredentialBytes  = 1 << 10
	maxWireNameBytes    = 255
	maxTextBytes        = 4 << 10 // paths, service names and any other text
	maxHeaderEntries    = 32
	maxHeaderNameBytes  = 128
	maxHeaderValueBytes = 4 << 10
	maxALPNEntries      = 16
	maxALPNBytes        = 255
	maxListEntries      = 64
	maxPeers            = 16
	maxCertificateBytes = 32 << 10
	maxECHBytes         = 8 << 10
	maxStructuredBytes  = 16 << 10
	maxUnknownKeys      = 64
	maxSafeInteger      = 1 << 53
)

// BoundError is the first bound a node violates. It names the rule and the
// field path, never the value, so it can be logged and shown.
type BoundError struct {
	Rule  string // name, server, enumeration, credential, wire_name, text, headers, alpn, list, peers, certificate, ech, structured, unknown_keys, number, nesting, node
	Path  string // for example ws-opts.headers.Host[0]; "" for the whole node
	Limit int64
}

func (e *BoundError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("nodemodel: %s bound exceeded (limit %d)", e.Rule, e.Limit)
	}
	return fmt.Sprintf("nodemodel: %s bound exceeded at %s (limit %d)", e.Rule, e.Path, e.Limit)
}

// textRules classify a text by the key that holds it, in one lookup. Keys not
// listed here fall to the "any other text" bound, except the few that depend
// on their parent or a prefix (textRule).
var textRules = func() map[string]textBound {
	m := map[string]textBound{}
	add := func(b textBound, keys ...string) {
		for _, k := range keys {
			m[k] = b
		}
	}
	add(textBound{"server", maxServerBytes}, "server", "addresses")
	add(textBound{"enumeration", maxEnumBytes}, "type", "network", "cipher", "flow", "encryption", "protocol",
		"obfs", "mode", "congestion-controller", "udp-relay-mode", "ip-version", "client-fingerprint",
		"packet-encoding", "plugin")
	add(textBound{"credential", maxCredentialBytes}, "password", "uuid", "username", "psk", "token", "auth-str",
		"private-key", "public-key", "pre-shared-key", "preshared-key", "obfs-password", "short-id")
	add(textBound{"wire_name", maxWireNameBytes}, "sni", "servername", "name-cert-verify", "query-server-name", "host")
	add(textBound{"certificate", maxCertificateBytes}, "ca-str", "ca_str", "tls-fingerprint", "tls-pubkey-sha256")
	add(textBound{"ech", maxECHBytes}, "_echConfigList", "_dns")
	return m
}()

type textBound struct {
	rule  string
	limit int
}

// listKeys hold lists of at most maxListEntries entries.
var listKeys = setOf("allowed-ips", "dns", "server-dns", "args", "ports", "_vcn", "host", "path", "reserved")

// structuredKeys are annotations that carry a whole copied JSON value. They
// are measured as compact JSON; their contents are data, so the per-key rules
// do not apply inside them.
var structuredKeys = setOf("_extra", "_extra_unsupported", "_finalmask")

// namedFields are the model's named top-level fields. Every other top-level
// key, except underscore annotations (closed by the normaliser's registry),
// belongs to the pass-through extra map and counts toward maxUnknownKeys. The
// list is the union of the top-level keys the parse goldens carry, the fields
// design 28's model section names and the keys the normaliser reads or
// writes.
var namedFields = setOf(
	"addresses", "aead", "aead-method", "allow-other-interface", "allowed-ips", "alpn", "alterId",
	"amnezia-wg-option", "args", "auth-key", "auth-str", "benchmark-timeout", "benchmark-url",
	"block-quic", "ca", "ca-str", "ca_str", "cipher", "client-fingerprint", "congestion-control",
	"congestion-controller", "dialer-proxy", "disable-reuse", "disable-sni", "dns", "down", "ech-opts",
	"ecn", "encryption", "exec", "fingerprint", "flow", "fp", "grpc-opts", "h2-opts", "headerType",
	"headers", "hop-interval", "hop-interval-max", "host-key", "hostname", "http-opts",
	"httpupgrade-opts", "hybrid", "idle-session-check-interval", "idle-session-timeout", "idle-timeout",
	"interface", "interface-name", "ip", "ip-cidr", "ip-version", "ipv6", "ipv6-cidr", "kcp-opts",
	"keepalive", "key", "keystore-private-key", "local-address", "local-port", "max-stream-count",
	"max-streams", "min-idle-session", "mptcp", "mtu", "multiplexing", "name", "name-cert-verify",
	"network", "network-id", "network-name", "network-secret", "no-error-alert", "obfs", "obfs-opts",
	"obfs-param", "obfs-password", "obfs_password", "packet-addr", "packet-encoding", "padding",
	"padding-max", "padding-min", "password", "peers", "plugin", "plugin-opts", "port", "port-range",
	"ports", "pre-shared-key", "preshared-key", "presharedkey", "private-key", "proto", "protocol",
	"protocol-param", "psk", "public-key", "query-server-name", "quic-opts", "reality-opts",
	"reduce-rtt", "remote-dns-resolve", "reserved", "reuse", "section-name", "seed", "server",
	"server-cert-fingerprint", "server-dns", "server-fingerprint", "server-name", "servername",
	"shadow-tls-opts", "skip-cert-verify", "smux", "sni", "table-type", "test-timeout", "test-url",
	"tfo", "tls", "tls-alpn", "tls-fingerprint", "tls-no-session-reuse", "tls-no-session-ticket",
	"tls-pubkey-sha256", "token", "tos", "transport", "type", "udp", "udp-over-tcp",
	"udp-over-tcp-version", "udp-port", "udp-relay-mode", "underlying-proxy", "up", "username", "uuid",
	"version", "ws-headers", "ws-opts", "ws-path", "xhttp-opts", "xudp",
)

func setOf(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// Bounds applies normaliser.md section 5 and reports the first violation, in
// a fixed order: the per-field walk over keys in sorted order, then the
// unknown-key count, then the whole-node size. Not-a-number is allowed: it
// is the value the model carries for a port the normaliser could not read
// (normaliser.md section 7) and it is written as null; the infinities are
// refused.
//
// Bounds runs on every parsed node, so the common case avoids sorting and
// encoding: the walk first visits keys in map order, and only when it finds a
// violation does it walk again in sorted order to name the first one; the
// node size is counted without building the encoding.
func Bounds(n *Node) error {
	if n == nil {
		return nil
	}
	w := boundsWalker{}
	if !w.object(n.Fields, "", 1) {
		sorted := boundsWalker{sorted: true}
		sorted.object(n.Fields, "", 1)
		return sorted.err
	}
	if w.unknown > maxUnknownKeys {
		return &BoundError{Rule: "unknown_keys", Limit: maxUnknownKeys}
	}
	size, err := jsonSize(n.Fields, 0)
	if err != nil {
		return err
	}
	if size > MaxNodeBytes {
		return &BoundError{Rule: "node", Limit: MaxNodeBytes}
	}
	return nil
}

// pathSeg is one step of a field path; index is -1 for an object key.
type pathSeg struct {
	key   string
	index int
}

type boundsWalker struct {
	sorted  bool // visit keys in sorted order, to name the first violation
	path    []pathSeg
	err     *BoundError
	unknown int // top-level keys outside namedFields and the annotations
}

// keys returns m's keys, sorted when the walker reports a violation.
func (w *boundsWalker) keys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	if w.sorted {
		sort.Strings(keys)
	}
	return keys
}

// fail records a violation. The first, unsorted walk only needs to know that
// one exists, so it records no path; the sorted walk that follows names it.
func (w *boundsWalker) fail(rule string, limit int64) bool {
	if !w.sorted {
		w.err = &BoundError{Rule: rule, Limit: limit}
		return false
	}
	var b strings.Builder
	for i, s := range w.path {
		if s.index >= 0 {
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(s.index))
			b.WriteByte(']')
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.key)
	}
	w.err = &BoundError{Rule: rule, Path: b.String(), Limit: limit}
	return false
}

func (w *boundsWalker) push(key string, index int) {
	if w.sorted {
		w.path = append(w.path, pathSeg{key, index})
	}
}

func (w *boundsWalker) pop() {
	if w.sorted {
		w.path = w.path[:len(w.path)-1]
	}
}

// object walks an object at the given nesting level. parent is the key that
// holds it ("" for the node itself).
func (w *boundsWalker) object(m map[string]any, parent string, level int) bool {
	if level > MaxNestingDepth {
		return w.fail("nesting", MaxNestingDepth)
	}
	if !w.sorted {
		for k, v := range m {
			w.push(k, -1)
			if !w.field(parent, k, v, level) {
				return false
			}
			w.pop()
		}
		return true
	}
	for _, k := range w.keys(m) {
		w.push(k, -1)
		if !w.field(parent, k, m[k], level) {
			return false
		}
		w.pop()
	}
	return true
}

// field checks one keyed value inside an object at level.
func (w *boundsWalker) field(parent, key string, v any, level int) bool {
	if level == 1 && !namedFields[key] && !strings.HasPrefix(key, "_") && !strings.HasPrefix(key, "keystore-") {
		w.unknown++
	}
	switch {
	case structuredKeys[key] && parent == "":
		return w.structured(v, level)
	case key == "headers":
		if h, ok := v.(map[string]any); ok {
			return w.headers(h, level+1)
		}
	case key == "peers":
		if l, ok := v.([]any); ok && len(l) > maxPeers {
			return w.fail("peers", maxPeers)
		}
	case key == "alpn":
		return w.alpn(v, level)
	case key == "download-settings" && parent == "xhttp-opts":
		if !w.compactSize(v, "structured", maxStructuredBytes) {
			return false
		}
	}
	return w.value(parent, key, v, level)
}

// value checks a value held by key: text by the key's rule, numbers,
// lists by the list rules, objects recursively.
func (w *boundsWalker) value(parent, key string, v any, level int) bool {
	switch x := v.(type) {
	case string:
		rule, limit := textRule(parent, key)
		if len(x) > limit {
			return w.fail(rule, int64(limit))
		}
		if key == "ports" && strings.Count(x, ",")+1 > maxListEntries {
			return w.fail("list", maxListEntries)
		}
	case float64:
		if math.IsInf(x, 0) || (x == math.Trunc(x) && math.Abs(x) > maxSafeInteger) {
			return w.fail("number", maxSafeInteger)
		}
	case int64:
		if x > maxSafeInteger || x < -maxSafeInteger {
			return w.fail("number", maxSafeInteger)
		}
	case int:
		if int64(x) > maxSafeInteger || int64(x) < -maxSafeInteger {
			return w.fail("number", maxSafeInteger)
		}
	case []any:
		if level+1 > MaxNestingDepth {
			return w.fail("nesting", MaxNestingDepth)
		}
		if listKeys[key] && len(x) > maxListEntries {
			return w.fail("list", maxListEntries)
		}
		for i, e := range x {
			w.push("", i)
			if !w.value(parent, key, e, level+1) {
				return false
			}
			w.pop()
		}
	case map[string]any:
		return w.object(x, key, level+1)
	}
	return true
}

// textRule is the bound for a text held by key inside parent.
func textRule(parent, key string) (string, int) {
	if b, ok := textRules[key]; ok {
		return b.rule, b.limit
	}
	switch {
	case key == "name" && parent == "":
		return "name", maxNameBytes
	case key == "config" && parent == "ech-opts":
		return "ech", maxECHBytes
	case strings.HasPrefix(key, "keystore-"):
		return "certificate", maxCertificateBytes
	}
	return "text", maxTextBytes
}

// headers checks a headers object: entry count, name length, and each value
// (a Host value is a name on the wire; any other value is header text).
func (w *boundsWalker) headers(h map[string]any, level int) bool {
	if level > MaxNestingDepth {
		return w.fail("nesting", MaxNestingDepth)
	}
	if len(h) > maxHeaderEntries {
		return w.fail("headers", maxHeaderEntries)
	}
	for _, name := range w.keys(h) {
		w.push(name, -1)
		if len(name) > maxHeaderNameBytes {
			return w.fail("headers", maxHeaderNameBytes)
		}
		rule, limit := "headers", maxHeaderValueBytes
		if strings.EqualFold(name, "host") {
			rule, limit = "wire_name", maxWireNameBytes
		}
		if !w.headerValue(h[name], rule, limit, level) {
			return false
		}
		w.pop()
	}
	return true
}

func (w *boundsWalker) headerValue(v any, rule string, limit, level int) bool {
	switch x := v.(type) {
	case string:
		if len(x) > limit {
			return w.fail(rule, int64(limit))
		}
	case []any:
		if level+1 > MaxNestingDepth {
			return w.fail("nesting", MaxNestingDepth)
		}
		if len(x) > maxListEntries {
			return w.fail("list", maxListEntries)
		}
		for i, e := range x {
			w.push("", i)
			if !w.headerValue(e, rule, limit, level+1) {
				return false
			}
			w.pop()
		}
	default:
		return w.value("headers", "", v, level)
	}
	return true
}

// alpn checks an ALPN value: a list or a comma-separated text of at most
// maxALPNEntries entries of at most maxALPNBytes each.
func (w *boundsWalker) alpn(v any, level int) bool {
	switch x := v.(type) {
	case string:
		parts := strings.Split(x, ",")
		if len(parts) > maxALPNEntries {
			return w.fail("alpn", maxALPNEntries)
		}
		for _, p := range parts {
			if len(p) > maxALPNBytes {
				return w.fail("alpn", maxALPNBytes)
			}
		}
		return true
	case []any:
		if level+1 > MaxNestingDepth {
			return w.fail("nesting", MaxNestingDepth)
		}
		if len(x) > maxALPNEntries {
			return w.fail("alpn", maxALPNEntries)
		}
		for i, e := range x {
			w.push("", i)
			if s, ok := e.(string); ok && len(s) > maxALPNBytes {
				return w.fail("alpn", maxALPNBytes)
			}
			if !w.value("", "alpn", e, level+1) {
				return false
			}
			w.pop()
		}
		return true
	}
	return w.value("", "alpn", v, level)
}

// structured checks a copied JSON annotation: its compact size, its nesting
// and its numbers, but not the per-key rules.
func (w *boundsWalker) structured(v any, level int) bool {
	if !w.compactSize(v, "structured", maxStructuredBytes) {
		return false
	}
	return w.opaque(v, level)
}

func (w *boundsWalker) opaque(v any, level int) bool {
	switch x := v.(type) {
	case float64, int64, int:
		return w.value("", "", x, level)
	case []any:
		if level+1 > MaxNestingDepth {
			return w.fail("nesting", MaxNestingDepth)
		}
		for i, e := range x {
			w.push("", i)
			if !w.opaque(e, level+1) {
				return false
			}
			w.pop()
		}
	case map[string]any:
		if level+1 > MaxNestingDepth {
			return w.fail("nesting", MaxNestingDepth)
		}
		for _, k := range w.keys(x) {
			w.push(k, -1)
			if !w.opaque(x[k], level+1) {
				return false
			}
			w.pop()
		}
	}
	return true
}

func (w *boundsWalker) compactSize(v any, rule string, limit int) bool {
	size, err := jsonSize(v, 0)
	if err != nil || size > limit {
		return w.fail(rule, int64(limit))
	}
	return true
}

// jsonSize is len(appendValue(nil, v, 0)) without building the bytes or
// sorting keys, since key order does not change the length.
func jsonSize(v any, depth int) (int, error) {
	if depth > maxWriteDepth {
		return 0, errors.New("nodemodel: value nested too deeply to write")
	}
	switch x := v.(type) {
	case nil:
		return 4, nil
	case bool:
		if x {
			return 4, nil
		}
		return 5, nil
	case string:
		return jsonStringSize(x), nil
	case float64:
		var buf [32]byte
		return len(AppendNumber(buf[:0], x)), nil
	case int64:
		var buf [24]byte
		return len(strconv.AppendInt(buf[:0], x, 10)), nil
	case int:
		var buf [24]byte
		return len(strconv.AppendInt(buf[:0], int64(x), 10)), nil
	case []any:
		size := 2 + max(len(x)-1, 0)
		for _, e := range x {
			n, err := jsonSize(e, depth+1)
			if err != nil {
				return 0, err
			}
			size += n
		}
		return size, nil
	case map[string]any:
		size := 2 + max(len(x)-1, 0)
		for k, e := range x {
			n, err := jsonSize(e, depth+1)
			if err != nil {
				return 0, err
			}
			size += jsonStringSize(k) + 1 + n
		}
		return size, nil
	}
	return 0, fmt.Errorf("nodemodel: value of type %T is not part of the model", v)
}

// jsonStringSize is len(AppendJSONString(nil, s)).
func jsonStringSize(s string) int {
	size := 2
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c >= utf8.RuneSelf:
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n == 1 {
				size += 3 // U+FFFD
			} else {
				size += n
			}
			i += n
			continue
		case c == '"' || c == '\\' || c == '\b' || c == '\f' || c == '\n' || c == '\r' || c == '\t':
			size += 2
		case c < 0x20:
			size += 6
		default:
			size++
		}
		i++
	}
	return size
}
