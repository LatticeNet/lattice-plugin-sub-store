// Package perfgen generates the synthetic VLESS Reality nodes the perf gate
// measures (S1 plan section 5.2). Node i is a function of i alone, so N nodes
// are the same N nodes on every run and the first N of any larger run, and a
// committed benchmark baseline keeps meaning the same input.
//
// The mix re-expresses the VLESS Reality lines of the conformance harness's
// acceptance fixture (lattice-substore-conformance tools/fixturegen), which
// public CI cannot import: TCP with the xtls-rprx-vision flow, gRPC and XHTTP,
// weighted 50, 6 and 6. Each node comes in two forms: the share link the
// fleet's subscriptions carry, written the way the harness's fleet corpus
// writes it, and the node-model object the harness's parse goldens record for
// such a link, keys sorted. The perf gate can then time the parse path and
// the node path on the same nodes. When this generator was written, the
// harness oracle at v0.1.0-alpha.1 (upstream 2.42.3) parsed the document of
// URIs(4096) to exactly Nodes(4096).
//
// Every value is synthetic: hosts and SNIs sit under example.com, example.net
// and example.org, addresses in 198.18.0.0/15, and UUIDs, Reality keys, short
// ids and names are derived from SHA-256 of the node index.
package perfgen

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
)

// Transports, in mix order.
const (
	TCPVision = "tcp"
	GRPC      = "grpc"
	XHTTP     = "xhttp"
)

// mixPeriod is the sum of the fixture's VLESS Reality weights: of every 62
// consecutive nodes, 50 are TCP with Vision, 6 gRPC and 6 XHTTP.
const mixPeriod = 62

var countries = []string{"HK", "JP", "SG", "US", "DE", "GB", "NL", "TW", "KR", "FR", "CA", "AU"}
var sniPrefixes = []string{"www", "img", "cdn", "api", "static"}
var tlds = []string{"com", "net", "org"}

// line is one generated node, the fields both forms share.
type line struct {
	transport   string
	uuid        string
	server      string
	port        int
	sni         string
	publicKey   string
	shortID     string
	name        string
	serviceName string // gRPC only
	path        string // XHTTP only
}

// URIs returns the share links of nodes 0 to n-1.
func URIs(n int) []string {
	out := make([]string, 0, max(n, 0))
	for i := range max(n, 0) {
		out = append(out, generate(i).uri())
	}
	return out
}

// Nodes returns nodes 0 to n-1 as node-model JSON objects with sorted keys,
// the shape the harness's parse goldens give the links URIs returns.
func Nodes(n int) []json.RawMessage {
	out := make([]json.RawMessage, 0, max(n, 0))
	for i := range max(n, 0) {
		b, err := json.Marshal(generate(i).node())
		if err != nil {
			panic(err) // the map holds only strings, numbers, booleans and maps
		}
		out = append(out, b)
	}
	return out
}

// draw returns 32 bytes that depend only on the node index and the field.
func draw(i int, field string) [32]byte {
	return sha256.Sum256(fmt.Appendf(nil, "perfgen/%d/%s", i, field))
}

// label is a lowercase alphanumeric word of len(b) characters that starts
// with a letter.
func label(b []byte) string {
	const letters, alnum = "abcdefghijklmnopqrstuvwxyz", "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, len(b))
	for k, c := range b {
		if k == 0 {
			out[k] = letters[int(c)%len(letters)]
		} else {
			out[k] = alnum[int(c)%len(alnum)]
		}
	}
	return string(out)
}

func generate(i int) line {
	l := line{transport: TCPVision}
	switch m := i % mixPeriod; {
	case m >= 56:
		l.transport = XHTTP
	case m >= 50:
		l.transport = GRPC
	}

	u := draw(i, "uuid")
	u[6] = u[6]&0x0f | 0x40 // version 4
	u[8] = u[8]&0x3f | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(u[:16])
	l.uuid = h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]

	s := draw(i, "server")
	if s[0]%2 == 0 {
		l.server = fmt.Sprintf("198.%d.%d.%d", 18+int(s[1]&1), s[2], 1+int(s[3])%254)
	} else {
		l.server = fmt.Sprintf("%s-%02d.%s.example.%s", label(s[4:7]), int(s[7])%40, label(s[8:14]), tlds[int(s[14])%len(tlds)])
	}

	p := draw(i, "port")
	l.port = 443
	if p[0] >= 128 {
		l.port = 20000 + int(binary.BigEndian.Uint16(p[1:3]))%40000
	}

	n := draw(i, "sni")
	l.sni = fmt.Sprintf("%s.%s.example.%s", sniPrefixes[int(n[0])%len(sniPrefixes)], label(n[1:7]), tlds[int(n[7])%len(tlds)])

	key := draw(i, "public-key")
	l.publicKey = base64.RawURLEncoding.EncodeToString(key[:])
	sid := draw(i, "short-id")
	l.shortID = hex.EncodeToString(sid[:4])

	nm := draw(i, "name")
	l.name = fmt.Sprintf("%s %s %04d", countries[int(nm[0])%len(countries)], label(nm[1:6]), i+1)

	t := draw(i, "transport")
	switch l.transport {
	case GRPC:
		l.serviceName = label(t[0:5]) + "-" + label(t[5:9])
	case XHTTP:
		l.path = "/" + label(t[0:4]) + "/" + label(t[4:10])
	}
	return l
}

// uri writes the link in the fleet corpus's parameter order.
func (l line) uri() string {
	q := "encryption=none"
	if l.transport == TCPVision {
		q += "&flow=xtls-rprx-vision"
	}
	q += "&security=reality&sni=" + l.sni + "&fp=chrome&pbk=" + l.publicKey + "&sid=" + l.shortID + "&spx=%2F&type=" + l.transport
	switch l.transport {
	case GRPC:
		q += "&serviceName=" + l.serviceName
	case XHTTP:
		q += "&path=" + url.QueryEscape(l.path) + "&mode=auto"
	}
	return fmt.Sprintf("vless://%s@%s:%d?%s#%s", l.uuid, l.server, l.port, q, url.PathEscape(l.name))
}

// node is the parse goldens' model of the link.
func (l line) node() map[string]any {
	n := map[string]any{
		"_h2":                false,
		"client-fingerprint": "chrome",
		"encryption":         "none",
		"name":               l.name,
		"network":            l.transport,
		"packet-encoding":    "xudp",
		"port":               l.port,
		"reality-opts": map[string]any{
			"_spider-x":  "/",
			"public-key": l.publicKey,
			"short-id":   l.shortID,
		},
		"server":           l.server,
		"skip-cert-verify": false,
		"sni":              l.sni,
		"tls":              true,
		"type":             "vless",
		"udp":              true,
		"uuid":             l.uuid,
	}
	switch l.transport {
	case TCPVision:
		n["flow"] = "xtls-rprx-vision"
	case GRPC:
		n["grpc-opts"] = map[string]any{"_grpc-type": "gun", "grpc-service-name": l.serviceName}
	case XHTTP:
		n["xhttp-opts"] = map[string]any{"mode": "auto", "path": l.path}
	}
	return n
}
