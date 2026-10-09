package nodemodel

import (
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestNodeAccessors(t *testing.T) {
	n := &Node{Fields: map[string]any{
		"name": "JP 01", "type": "vless", "server": "a.example", "port": float64(443),
		"tls": true, "reality-opts": map[string]any{"public-key": "pk"},
	}}
	if n.Name() != "JP 01" || n.Type() != "vless" || n.Server() != "a.example" {
		t.Errorf("name, type, server = %q, %q, %q", n.Name(), n.Type(), n.Server())
	}
	if p, ok := n.Port(); !ok || p != 443 {
		t.Errorf("Port = %d, %v", p, ok)
	}
	for _, v := range []any{math.NaN(), 443.5, "443", nil} {
		n.Fields["port"] = v
		if _, ok := n.Port(); ok {
			t.Errorf("Port read %v as a port", v)
		}
	}
	n.Fields["port"] = int64(8443)
	if p, ok := n.Port(); !ok || p != 8443 {
		t.Errorf("Port of an int64 = %d, %v", p, ok)
	}
	if v, ok := n.Bool("tls"); !ok || !v {
		t.Errorf("Bool(tls) = %v, %v", v, ok)
	}
	if _, ok := n.Bool("name"); ok {
		t.Error("Bool read a text")
	}
	if _, ok := n.String("tls"); ok {
		t.Error("String read a boolean")
	}
	if v, ok := n.Get("reality-opts", "public-key"); !ok || v != "pk" {
		t.Errorf("Get nested = %v, %v", v, ok)
	}
	if _, ok := n.Get("name", "x"); ok {
		t.Error("Get went through a text")
	}
	if _, ok := n.Get(); ok {
		t.Error("Get with no path read something")
	}

	n.SetName("renamed")
	n.Set("h.example", "ws-opts", "headers", "Host")
	n.Set("/p", "name", "replaced") // a non-object on the path is replaced
	if v, _ := n.Get("ws-opts", "headers", "Host"); v != "h.example" {
		t.Errorf("Set created %v", n.Fields["ws-opts"])
	}
	if v, _ := n.Get("name", "replaced"); v != "/p" {
		t.Errorf("Set through a text gave %v", n.Fields["name"])
	}
	n.Delete("ws-opts", "headers", "Host")
	n.Delete("tls", "x") // through a boolean: nothing happens
	if _, ok := n.Get("ws-opts", "headers", "Host"); ok {
		t.Error("Delete left the key")
	}
	if _, ok := n.Get("tls"); !ok {
		t.Error("Delete through a boolean removed it")
	}

	empty := &Node{}
	empty.SetName("x")
	if empty.Name() != "x" {
		t.Error("SetName on a node without fields")
	}
	var nilNode *Node
	if _, ok := nilNode.Get("name"); ok || nilNode.Clone() != nil {
		t.Error("nil node")
	}
}

func TestNodeCloneIsDeep(t *testing.T) {
	n := &Node{
		Fields:  map[string]any{"ws-opts": map[string]any{"headers": map[string]any{"Host": "h"}}, "alpn": []any{"h2"}},
		Script:  map[string]any{"_tag": []any{"a"}},
		Lattice: &LatticeFields{LineUUID: "u"},
	}
	c := n.Clone()
	if !reflect.DeepEqual(c, n) {
		t.Fatalf("clone differs: %+v", c)
	}
	c.Set("other", "ws-opts", "headers", "Host")
	c.Fields["alpn"].([]any)[0] = "h3"
	c.Script["_tag"].([]any)[0] = "b"
	c.Lattice.LineUUID = "v"
	if v, _ := n.Get("ws-opts", "headers", "Host"); v != "h" {
		t.Error("clone shares nested objects")
	}
	if n.Fields["alpn"].([]any)[0] != "h2" || n.Script["_tag"].([]any)[0] != "a" || n.Lattice.LineUUID != "u" {
		t.Error("clone shares lists, the script map or the fleet block")
	}
}

func TestStripFromInputRemovesUnderscoreKeysAtEveryDepth(t *testing.T) {
	n := &Node{Fields: map[string]any{
		"name": "n", "_exec": "x", "_loon_tls_profile": "chrome147",
		"reality-opts": map[string]any{"public-key": "pk", "_spider-x": "/s"},
		"peers":        []any{map[string]any{"_x": 1, "server": "p"}, []any{map[string]any{"_y": 2}}},
	}}
	r := StripFromInput(n)
	if r.UnderscoreKeys != 5 || r.ExecShaped {
		t.Errorf("report %+v, want 5 keys and not exec-shaped", r)
	}
	want := map[string]any{
		"name": "n", "reality-opts": map[string]any{"public-key": "pk"},
		"peers": []any{map[string]any{"server": "p"}, []any{map[string]any{}}},
	}
	if !reflect.DeepEqual(n.Fields, want) {
		t.Errorf("after H1: %v", n.Fields)
	}
}

// boundsBaseline is a realistic node that every bound accepts.
func boundsBaseline() *Node {
	return &Node{Fields: map[string]any{
		"name": "JP 01", "type": "vless", "server": "a.example", "port": float64(443),
		"uuid": "5840f636-1f16-4327-8520-2c6d190b4fb9", "network": "ws", "tls": true, "udp": true,
		"sni": "s.example", "alpn": []any{"h2", "http/1.1"}, "client-fingerprint": "chrome",
		"reality-opts": map[string]any{"public-key": "pk", "short-id": "ab"},
		"ws-opts":      map[string]any{"path": "/p", "headers": map[string]any{"Host": "h.example"}},
		"xhttp-opts":   map[string]any{"download-settings": map[string]any{"server": "d.example"}},
		"h2-opts":      map[string]any{"host": []any{"h.example"}},
		"plugin-opts":  map[string]any{"mode": "websocket"},
		"peers":        []any{map[string]any{"server": "p.example"}},
		"addresses":    []any{"192.0.2.1", "192.0.2.2"},
		"ech-opts":     map[string]any{"config": "AAAA"},
		"_extra":       "x",
	}}
}

// TestFieldBoundsRefuse holds each bound of normaliser.md section 5 at its
// limit and one past it, and checks the error names the rule and the path,
// never the value.
func TestFieldBoundsRefuse(t *testing.T) {
	if err := Bounds(boundsBaseline()); err != nil {
		t.Fatalf("baseline refused: %v", err)
	}
	text := func(n int) string { return strings.Repeat("a", n) }
	list := func(n int, v any) []any {
		l := make([]any, n)
		for i := range l {
			l[i] = v
		}
		return l
	}
	headers := func(n int) map[string]any {
		h := map[string]any{}
		for i := 0; i < n; i++ {
			h["X-"+strconv.Itoa(i)] = "v"
		}
		return h
	}
	commaList := func(n int) string { return strings.TrimSuffix(strings.Repeat("1,", n), ",") }
	nested := func(depth int) map[string]any {
		// depth counts the node object itself as level one.
		v := map[string]any{}
		for i := 2; i < depth; i++ {
			v = map[string]any{"n": v}
		}
		return v
	}

	cases := []struct {
		name       string
		at, past   func(f map[string]any)
		rule, path string
	}{
		{"name", func(f map[string]any) { f["name"] = text(1024) }, func(f map[string]any) { f["name"] = text(1025) }, "name", "name"},
		{"server", func(f map[string]any) { f["server"] = text(255) }, func(f map[string]any) { f["server"] = text(256) }, "server", "server"},
		{"download server",
			func(f map[string]any) {
				f["xhttp-opts"].(map[string]any)["download-settings"].(map[string]any)["server"] = text(255)
			},
			func(f map[string]any) {
				f["xhttp-opts"].(map[string]any)["download-settings"].(map[string]any)["server"] = text(256)
			},
			"server", "xhttp-opts.download-settings.server"},
		{"peer server",
			func(f map[string]any) { f["peers"] = []any{map[string]any{"server": text(255)}} },
			func(f map[string]any) { f["peers"] = []any{map[string]any{"server": text(256)}} },
			"server", "peers[0].server"},
		{"addresses element",
			func(f map[string]any) { f["addresses"] = []any{"192.0.2.1", text(255)} },
			func(f map[string]any) { f["addresses"] = []any{"192.0.2.1", text(256)} },
			"server", "addresses[1]"},
		{"enumeration", func(f map[string]any) { f["type"] = text(64) }, func(f map[string]any) { f["type"] = text(65) }, "enumeration", "type"},
		{"nested enumeration",
			func(f map[string]any) { f["plugin-opts"] = map[string]any{"mode": text(64)} },
			func(f map[string]any) { f["plugin-opts"] = map[string]any{"mode": text(65)} },
			"enumeration", "plugin-opts.mode"},
		{"credential", func(f map[string]any) { f["uuid"] = text(1024) }, func(f map[string]any) { f["uuid"] = text(1025) }, "credential", "uuid"},
		{"reality short id",
			func(f map[string]any) { f["reality-opts"] = map[string]any{"short-id": text(1024)} },
			func(f map[string]any) { f["reality-opts"] = map[string]any{"short-id": text(1025)} },
			"credential", "reality-opts.short-id"},
		{"sni", func(f map[string]any) { f["sni"] = text(255) }, func(f map[string]any) { f["sni"] = text(256) }, "wire_name", "sni"},
		{"host list element",
			func(f map[string]any) { f["h2-opts"] = map[string]any{"host": []any{text(255)}} },
			func(f map[string]any) { f["h2-opts"] = map[string]any{"host": []any{text(256)}} },
			"wire_name", "h2-opts.host[0]"},
		{"Host header",
			func(f map[string]any) { f["ws-opts"] = map[string]any{"headers": map[string]any{"Host": text(255)}} },
			func(f map[string]any) { f["ws-opts"] = map[string]any{"headers": map[string]any{"Host": text(256)}} },
			"wire_name", "ws-opts.headers.Host"},
		{"path",
			func(f map[string]any) { f["ws-opts"] = map[string]any{"path": text(4096)} },
			func(f map[string]any) { f["ws-opts"] = map[string]any{"path": text(4097)} },
			"text", "ws-opts.path"},
		{"header entries",
			func(f map[string]any) { f["ws-opts"] = map[string]any{"headers": headers(32)} },
			func(f map[string]any) { f["ws-opts"] = map[string]any{"headers": headers(33)} },
			"headers", "ws-opts.headers"},
		{"header name",
			func(f map[string]any) { f["ws-opts"] = map[string]any{"headers": map[string]any{text(128): "v"}} },
			func(f map[string]any) { f["ws-opts"] = map[string]any{"headers": map[string]any{text(129): "v"}} },
			"headers", "ws-opts.headers." + text(129)},
		{"header value",
			func(f map[string]any) { f["ws-opts"] = map[string]any{"headers": map[string]any{"X-A": text(4096)}} },
			func(f map[string]any) { f["ws-opts"] = map[string]any{"headers": map[string]any{"X-A": text(4097)}} },
			"headers", "ws-opts.headers.X-A"},
		{"alpn entries", func(f map[string]any) { f["alpn"] = list(16, "h2") }, func(f map[string]any) { f["alpn"] = list(17, "h2") }, "alpn", "alpn"},
		{"alpn entry", func(f map[string]any) { f["alpn"] = []any{text(255)} }, func(f map[string]any) { f["alpn"] = []any{text(256)} }, "alpn", "alpn[0]"},
		{"alpn text",
			func(f map[string]any) { f["plugin-opts"] = map[string]any{"alpn": commaList(16)} },
			func(f map[string]any) { f["plugin-opts"] = map[string]any{"alpn": commaList(17)} },
			"alpn", "plugin-opts.alpn"},
		{"list", func(f map[string]any) { f["allowed-ips"] = list(64, "0.0.0.0/0") }, func(f map[string]any) { f["allowed-ips"] = list(65, "0.0.0.0/0") }, "list", "allowed-ips"},
		{"ports entries", func(f map[string]any) { f["ports"] = commaList(64) }, func(f map[string]any) { f["ports"] = commaList(65) }, "list", "ports"},
		{"peers",
			func(f map[string]any) { f["peers"] = list(16, map[string]any{}) },
			func(f map[string]any) { f["peers"] = list(17, map[string]any{}) },
			"peers", "peers"},
		{"certificate", func(f map[string]any) { f["ca-str"] = text(32 << 10) }, func(f map[string]any) { f["ca-str"] = text(32<<10 + 1) }, "certificate", "ca-str"},
		{"keystore", func(f map[string]any) { f["keystore-private-key"] = text(32 << 10) }, func(f map[string]any) { f["keystore-private-key"] = text(32<<10 + 1) }, "certificate", "keystore-private-key"},
		{"ech config",
			func(f map[string]any) { f["ech-opts"] = map[string]any{"config": text(8 << 10)} },
			func(f map[string]any) { f["ech-opts"] = map[string]any{"config": text(8<<10 + 1)} },
			"ech", "ech-opts.config"},
		// Two bytes of quotes: the compact JSON of the text is the measure.
		{"structured annotation", func(f map[string]any) { f["_extra"] = text(16<<10 - 2) }, func(f map[string]any) { f["_extra"] = text(16<<10 - 1) }, "structured", "_extra"},
		{"structured contents are data",
			func(f map[string]any) { f["_finalmask"] = map[string]any{"type": text(4000), "server": text(4000)} },
			func(f map[string]any) { f["_finalmask"] = map[string]any{"type": text(9000), "server": text(9000)} },
			"structured", "_finalmask"},
		{"any other text", func(f map[string]any) { f["mieru-note"] = text(4096) }, func(f map[string]any) { f["mieru-note"] = text(4097) }, "text", "mieru-note"},
		{"unknown keys",
			func(f map[string]any) {
				for i := 0; i < 64; i++ {
					f["x-"+strconv.Itoa(i)] = true
				}
			},
			func(f map[string]any) {
				for i := 0; i < 65; i++ {
					f["x-"+strconv.Itoa(i)] = true
				}
			},
			"unknown_keys", ""},
		{"whole node",
			func(f map[string]any) { f["ca-str"] = text(30 << 10); f["ca_str"] = text(30 << 10) },
			func(f map[string]any) { f["ca-str"] = text(32 << 10); f["tls-pubkey-sha256"] = text(32 << 10) },
			"node", ""},
		{"safe integer", func(f map[string]any) { f["mtu"] = float64(1 << 53) }, func(f map[string]any) { f["mtu"] = float64(1<<53 + 2) }, "number", "mtu"},
		{"infinity", func(f map[string]any) { f["port"] = math.NaN() }, func(f map[string]any) { f["port"] = math.Inf(1) }, "number", "port"},
		{"int64", func(f map[string]any) { f["mtu"] = int64(-1 << 53) }, func(f map[string]any) { f["mtu"] = int64(-1<<53 - 1) }, "number", "mtu"},
		{"nesting",
			func(f map[string]any) { f["smux"] = nested(16) },
			func(f map[string]any) { f["smux"] = nested(17) },
			"nesting", "smux" + strings.Repeat(".n", 15)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			at := boundsBaseline()
			c.at(at.Fields)
			if err := Bounds(at); err != nil {
				t.Fatalf("at the limit: %v", err)
			}
			past := boundsBaseline()
			c.past(past.Fields)
			err := Bounds(past)
			var be *BoundError
			if !errors.As(err, &be) {
				t.Fatalf("past the limit: %v, want a BoundError", err)
			}
			if be.Rule != c.rule || be.Path != c.path {
				t.Errorf("past the limit: rule %q path %q, want %q %q", be.Rule, be.Path, c.rule, c.path)
			}
			if strings.Contains(be.Error(), text(200)) {
				t.Errorf("error carries the value: %.80s", be.Error())
			}
		})
	}
}
