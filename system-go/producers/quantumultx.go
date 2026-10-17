package producers

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// The Quantumult X producer (specs/producers/quantumultx.md): one line per
// node, "<kind>=<server>:<port>" and ",key=value" parameters ending in
// ",tag=<name>" and the two Reality parameters, lines joined by "\n" with no
// trailing newline. Values are written verbatim, the name included, as the
// oracle writes them, except that a line holding a line-breaking character is
// rejected (quantumultx.md, "Line safety"). Parameters follow each kind's
// reference order, so the output also matches the golden bytes.

type quantumultXProducer struct{}

func (quantumultXProducer) ID() string { return "quantumultx" }

// EmptyDocument is the empty string (quantumultx.md, "Empty document").
func (quantumultXProducer) EmptyDocument(Options) []byte { return []byte{} }

// errQXUnsupported marks a node the Quantumult X producer rejects.
var errQXUnsupported = errors.New("no Quantumult X form")

func (quantumultXProducer) Produce(dst *bytes.Buffer, nodes []*nodemodel.Node, target string, opts Options) (Result, error) {
	ps, dropped := prepare(nodes, target, "quantumultx", opts)
	res := Result{Dropped: dropped}
	for _, p := range ps {
		line, err := qxLine(p.node.Fields)
		if err != nil {
			res.Dropped = append(res.Dropped, Dropped{Index: p.index, Type: typeOf(p.node.Fields), Reason: ReasonUnsupported})
			continue
		}
		if res.Entries > 0 {
			dst.WriteByte('\n')
		}
		dst.WriteString(line)
		res.Entries++
	}
	sortDropped(res.Dropped)
	return res, nil
}

// qxSSCiphers are the Shadowsocks ciphers Quantumult X accepts.
var qxSSCiphers = map[string]bool{
	"none": true, "rc4-md5": true, "rc4-md5-6": true, "aes-128-cfb": true, "aes-192-cfb": true,
	"aes-256-cfb": true, "aes-128-ctr": true, "aes-192-ctr": true, "aes-256-ctr": true, "bf-cfb": true,
	"cast5-cfb": true, "des-cfb": true, "rc2-cfb": true, "salsa20": true, "chacha20": true,
	"chacha20-ietf": true, "aes-128-gcm": true, "aes-192-gcm": true, "aes-256-gcm": true,
	"chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true,
	"2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true,
}

// qxObfsHTTPTokens are the obfs tokens the Quantumult X parser records in
// _qx_obfs_http.
var qxObfsHTTPTokens = map[string]bool{"http": true, "vmess-http": true, "vemss-http": true, "shadowsocks-http": true}

// qxParams builds one line's parameters.
type qxParams struct {
	b strings.Builder
}

func (q *qxParams) add(key, value string) {
	q.b.WriteByte(',')
	q.b.WriteString(key)
	q.b.WriteByte('=')
	q.b.WriteString(value)
}

func (q *qxParams) verbatim(key string, f map[string]any, src string) {
	if v, ok := present(f, src); ok {
		q.add(key, text(v))
	}
}

// qxLine writes one node's line, or returns errQXUnsupported for a node the
// producer rejects (quantumultx.md, "Unsupported rule"), which includes a
// line that would hold a line-breaking character ("Line safety").
func qxLine(f map[string]any) (string, error) {
	typ, _ := f["type"].(string)
	if wsHTTPUpgrade(f) {
		return "", errQXUnsupported
	}
	var q qxParams
	head := func(kind string) {
		q.b.WriteString(kind + "=" + textOf(f, "server") + ":" + textOf(f, "port"))
	}
	switch typ {
	case "ss":
		cipher := "none"
		if v, ok := present(f, "cipher"); ok {
			cipher = text(v)
		}
		if !qxSSCiphers[cipher] {
			return "", errQXUnsupported
		}
		head("shadowsocks")
		q.add("method", cipher)
		q.add("password", textOf(f, "password"))
		overTLS := ssOverTLS(f)
		po, _ := obj(f, "plugin-opts")
		switch plugin, _ := present(f, "plugin"); {
		case overTLS:
			q.add("obfs", "over-tls")
			if v, ok := present(f, "sni"); ok {
				q.add("obfs-host", text(v))
			} else {
				q.verbatim("obfs-host", f, "servername")
			}
		case plugin == "obfs":
			mode := textOf(po, "mode")
			if mode == "http" {
				mode = qxObfsHTTP(f)
			}
			q.add("obfs", mode)
			q.verbatim("obfs-host", po, "host")
			q.verbatim("obfs-uri", po, "path")
		case plugin == "v2ray-plugin":
			if v2rayPluginMode(f) != "websocket" {
				return "", errQXUnsupported
			}
			q.add("obfs", wsObfs(po))
			q.verbatim("obfs-host", po, "host")
			q.verbatim("obfs-uri", po, "path")
		case plugin != nil:
			return "", errQXUnsupported
		}
		if set(f, "tls") {
			if err := q.tlsGroup(f, !overTLS); err != nil {
				return "", err
			}
		}
		q.verbatim("fast-open", f, "tfo")
		q.verbatim("udp-relay", f, "udp")
		switch {
		case set(f, "_ssr_python_uot"):
			q.add("udp-over-tcp", "true")
		case set(f, "udp-over-tcp"):
			v, ok := present(f, "udp-over-tcp-version")
			switch {
			case !ok || numberIs(v, 1):
				q.add("udp-over-tcp", "sp.v1")
			case numberIs(v, 2):
				q.add("udp-over-tcp", "sp.v2")
			}
		}
	case "ssr":
		head("shadowsocks")
		q.add("method", textOf(f, "cipher"))
		q.add("password", textOf(f, "password"))
		q.add("ssr-protocol", textOf(f, "protocol"))
		q.verbatim("ssr-protocol-param", f, "protocol-param")
		q.verbatim("obfs", f, "obfs")
		q.verbatim("obfs-host", f, "obfs-param")
		q.verbatim("fast-open", f, "tfo")
		q.verbatim("udp-relay", f, "udp")
	case "trojan":
		head("trojan")
		q.add("password", textOf(f, "password"))
		switch net, _ := present(f, "network"); net {
		case "ws":
			q.add("obfs", wsObfs(f))
			q.obfsPathHost(f)
		case nil, "tcp":
			if set(f, "tls") {
				q.add("over-tls", "true")
			}
		default:
			return "", errQXUnsupported
		}
		if set(f, "tls") {
			if err := q.tlsGroup(f, true); err != nil {
				return "", err
			}
		}
		q.verbatim("fast-open", f, "tfo")
		q.verbatim("udp-relay", f, "udp")
	case "vmess", "vless":
		if typ == "vless" {
			if v, ok := present(f, "encryption"); ok && truthy(v) && v != "none" {
				return "", errQXUnsupported
			}
			head("vless")
			q.add("method", "none")
		} else {
			head("vmess")
			q.add("method", qxVMessMethod(f))
		}
		q.add("password", textOf(f, "uuid"))
		switch net, _ := present(f, "network"); net {
		case "ws":
			q.add("obfs", wsObfs(f))
			q.obfsPathHost(f)
		case "http":
			q.add("obfs", qxObfsHTTP(f))
			q.obfsPathHost(f)
		case nil, "tcp":
			if set(f, "tls") {
				q.add("obfs", "over-tls")
			}
			q.obfsPathHost(f)
		default:
			return "", errQXUnsupported
		}
		if set(f, "tls") {
			if err := q.tlsGroup(f, true); err != nil {
				return "", err
			}
		}
		if typ == "vmess" {
			if v, ok := present(f, "aead"); ok {
				q.add("aead", text(v))
			} else {
				q.add("aead", boolText(isZero(f["alterId"])))
			}
		} else {
			q.verbatim("vless-flow", f, "flow")
		}
		q.verbatim("fast-open", f, "tfo")
		q.verbatim("udp-relay", f, "udp")
	case "anytls":
		if v, ok := present(f, "network"); ok && strings.ToLower(trimES(text(v))) != "tcp" {
			return "", errQXUnsupported
		}
		head("anytls")
		q.add("password", textOf(f, "password"))
		q.add("over-tls", "true")
		if err := q.tlsGroup(f, true); err != nil {
			return "", err
		}
		q.verbatim("fast-open", f, "tfo")
		q.verbatim("udp-relay", f, "udp")
	case "http", "socks5":
		head(typ)
		q.verbatim("username", f, "username")
		q.verbatim("password", f, "password")
		q.verbatim("over-tls", f, "tls")
		if set(f, "tls") {
			if err := q.tlsGroup(f, true); err != nil {
				return "", err
			}
		}
		q.verbatim("fast-open", f, "tfo")
		q.verbatim("udp-relay", f, "udp")
	default:
		return "", errQXUnsupported
	}
	q.verbatim("server_check_url", f, "test-url")
	q.add("tag", textOf(f, "name"))
	if ro, ok := obj(f, "reality-opts"); ok {
		if v := ro["public-key"]; truthy(v) {
			q.add("reality-base64-pubkey", text(v))
		}
		if v := ro["short-id"]; truthy(v) {
			q.add("reality-hex-shortid", text(v))
		}
	}
	// A flow other than xtls-rprx-vision is checked after the line is built,
	// so it rejects any kind.
	if v := f["flow"]; truthy(v) && v != "xtls-rprx-vision" {
		return "", errQXUnsupported
	}
	// One check over the finished line covers every field it writes
	// (quantumultx.md, "Line safety").
	line := q.b.String()
	if !lineSafe(line) {
		return "", errQXUnsupported
	}
	return line, nil
}

// tlsGroup writes the TLS group: tls-pubkey-sha256, tls-alpn,
// tls-no-session-ticket, tls-no-session-reuse, tls-cert-sha256,
// tls-verification and, when host, tls-host. An ALPN protocol longer than 255
// bytes rejects the node.
func (q *qxParams) tlsGroup(f map[string]any, host bool) error {
	q.verbatim("tls-pubkey-sha256", f, "tls-pubkey-sha256")
	if v, ok := present(f, "tls-alpn"); ok {
		q.add("tls-alpn", text(v))
	} else if v, ok := present(f, "alpn"); ok {
		a, err := qxALPN(v)
		if err != nil {
			return err
		}
		if a != "" {
			q.add("tls-alpn", a)
		}
	}
	q.verbatim("tls-no-session-ticket", f, "tls-no-session-ticket")
	q.verbatim("tls-no-session-reuse", f, "tls-no-session-reuse")
	q.verbatim("tls-cert-sha256", f, "tls-fingerprint")
	if v, ok := present(f, "name-cert-verify"); ok {
		q.add("tls-verification", text(v))
	} else if v, ok := present(f, "skip-cert-verify"); ok {
		q.add("tls-verification", boolText(!truthy(v)))
	}
	if host {
		q.verbatim("tls-host", f, "sni")
	}
	return nil
}

// qxALPN encodes an alpn list, or comma-separated text, as Quantumult X's
// tls-alpn: each trimmed, non-empty protocol as its UTF-8 byte length in two
// lower-case hex digits and its bytes in lower-case hex, concatenated. "" is
// no protocol; a protocol longer than 255 bytes is an error.
func qxALPN(v any) (string, error) {
	var items []string
	if l, ok := v.([]any); ok {
		for _, e := range l {
			if e != nil {
				items = append(items, text(e))
			}
		}
	} else {
		items = strings.Split(text(v), ",")
	}
	var b strings.Builder
	for _, it := range items {
		s := trimES(it)
		if s == "" {
			continue
		}
		if len(s) > 255 {
			return "", errQXUnsupported
		}
		if len(s) < 16 {
			b.WriteByte('0')
		}
		b.WriteString(strconv.FormatInt(int64(len(s)), 16))
		b.WriteString(hex.EncodeToString([]byte(s)))
	}
	return b.String(), nil
}

// obfsPathHost writes obfs-uri from <network>-opts.path and obfs-host from
// <network>-opts.headers.Host, the first item of a list, each when present.
// The path is written as stored, an ed parameter included.
func (q *qxParams) obfsPathHost(f map[string]any) {
	o, _ := obj(f, textOf(f, "network")+"-opts")
	if v, ok := present(o, "path"); ok {
		if v, ok := first(v); ok && v != nil {
			q.add("obfs-uri", text(v))
		}
	}
	h, _ := obj(o, "headers")
	if v, ok := present(h, "Host"); ok {
		if v, ok := first(v); ok && v != nil {
			q.add("obfs-host", text(v))
		}
	}
}

// qxObfsHTTP is the obfs token for HTTP obfuscation: the parser's
// _qx_obfs_http when it is one of the four tokens, else http.
func qxObfsHTTP(f map[string]any) string {
	if s, ok := f["_qx_obfs_http"].(string); ok && qxObfsHTTPTokens[s] {
		return s
	}
	return "http"
}

// wsObfs is wss when m's tls is set, else ws.
func wsObfs(m map[string]any) string {
	if set(m, "tls") {
		return "wss"
	}
	return "ws"
}

// qxVMessMethod is VMess method: none stays none and everything else becomes
// chacha20-poly1305.
func qxVMessMethod(f map[string]any) string {
	c := ""
	if v, ok := f["cipher"]; ok {
		c = strings.ToLower(trimES(text(v)))
	}
	if c == "none" {
		return "none"
	}
	return "chacha20-poly1305"
}
