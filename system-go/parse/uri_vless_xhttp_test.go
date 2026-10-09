package parse

import (
	"reflect"
	"testing"
)

func xhttpLine(extra string) string {
	return "vless://" + uuid4 + "@h:443?security=reality&pbk=K&type=xhttp&path=%2Fx&mode=auto&extra=" + encodeURIComponent(extra)
}

func TestXHTTPExtraModelsAndKeeps(t *testing.T) {
	f := parserFields(t, xhttpLine(`{
		"noGRPCHeader": true, "xPaddingObfsMode": "yes", "xPaddingBytes": 100,
		"sessionIDPlacement": " ", "sessionPlacement": "path", "sessionIDTable": "",
		"scMaxEachPostBytes": "5-5", "scMinPostsIntervalMs": "0", "uplinkChunkSize": "0",
		"headers": {"X-A": "1", "Host": "skip.example.com", "X-N": 2},
		"xmux": {"maxConnections": 16, "hKeepAlivePeriod": "-5", "cMaxReuseTimes": "2-1"},
		"downloadSettings": "not an object"
	}`))
	opts, _ := f["xhttp-opts"].(map[string]any)
	wantOpts := map[string]any{
		"path": "/x", "mode": "auto", "no-grpc-header": true, "x-padding-bytes": "100",
		"session-placement": "path", "session-table": "", "sc-max-each-post-bytes": 5.0, "uplink-chunk-size": 0.0,
		"headers":        map[string]any{"X-A": "1", "Host": "skip.example.com"},
		"reuse-settings": map[string]any{"max-connections": "16", "h-keep-alive-period": -5.0},
	}
	if !reflect.DeepEqual(opts, wantOpts) {
		t.Errorf("xhttp-opts = %#v\nwant %#v", opts, wantOpts)
	}
	wantKept := map[string]any{
		"xPaddingObfsMode":     "yes",
		"sessionIDPlacement":   " ",
		"scMinPostsIntervalMs": "0",
		"headers":              map[string]any{"X-N": 2.0},
		"xmux":                 map[string]any{"cMaxReuseTimes": "2-1"},
		"downloadSettings":     "not an object",
	}
	if got := f["_extra_unsupported"]; !reflect.DeepEqual(got, wantKept) {
		t.Errorf("_extra_unsupported = %#v\nwant %#v", got, wantKept)
	}
	if _, ok := f["_extra"]; ok {
		t.Error("a valid extra was also kept as _extra")
	}
}

func TestXHTTPDownloadSettings(t *testing.T) {
	f := parserFields(t, xhttpLine(`{"downloadSettings": {
		"address": "d.example.com", "port": "8443", "network": "SplitHTTP", "security": "tls",
		"tlsSettings": {"serverName": "t.example.com", "fingerprint": "chrome", "alpn": ["h2", ""],
			"allowInsecure": true, "echConfigList": "cfg", "echForceQuery": "half", "echSockopt": {"a": 1}, "x": 1},
		"realitySettings": {"serverName": "r.example.com", "publicKey": " ", "spiderX": "/s"},
		"xhttpSettings": {"path": "", "host": "x.example.com", "xPaddingBytes": "1-2", "extra": {"seqKey": "k", "bad": {}}},
		"sockopt": {}
	}}`))
	opts, _ := f["xhttp-opts"].(map[string]any)
	wantDS := map[string]any{
		"server": "d.example.com", "port": 8443.0, "network": "xhttp", "tls": true,
		"servername": "r.example.com", "client-fingerprint": "chrome", "skip-cert-verify": true,
		"ech-opts": map[string]any{"enable": true, "config": "cfg", "_force-query": "half", "_sockopt": map[string]any{"a": 1.0}},
		"host":     "x.example.com", "x-padding-bytes": "1-2", "seq-key": "k",
	}
	if got := opts["download-settings"]; !reflect.DeepEqual(got, wantDS) {
		t.Errorf("download-settings = %#v\nwant %#v", got, wantDS)
	}
	// Kept: the bad ALPN list, the unknown TLS key, the blank Reality key
	// and spiderX, the blank xhttp path; empty objects are dropped.
	wantKept := map[string]any{"downloadSettings": map[string]any{
		"tlsSettings":     map[string]any{"alpn": []any{"h2", ""}, "x": 1.0},
		"realitySettings": map[string]any{"publicKey": " ", "spiderX": "/s"},
		"xhttpSettings":   map[string]any{"path": ""},
	}}
	if got := f["_extra_unsupported"]; !reflect.DeepEqual(got, wantKept) {
		t.Errorf("_extra_unsupported = %#v\nwant %#v", got, wantKept)
	}
	// security reality always sets reality-opts, empty when nothing fills it.
	f = parserFields(t, xhttpLine(`{"downloadSettings": {"security": "REALITY"}}`))
	opts, _ = f["xhttp-opts"].(map[string]any)
	if got := opts["download-settings"]; !reflect.DeepEqual(got, map[string]any{"tls": true, "reality-opts": map[string]any{}}) {
		t.Errorf("reality download-settings = %#v", got)
	}
	// An ECH force-query without a mappable config list is kept.
	f = parserFields(t, xhttpLine(`{"downloadSettings": {"tlsSettings": {"echForceQuery": "full"}}}`))
	if got := f["_extra_unsupported"]; !reflect.DeepEqual(got, map[string]any{"downloadSettings": map[string]any{"tlsSettings": map[string]any{"echForceQuery": "full"}}}) {
		t.Errorf("unmappable ECH kept as %#v", got)
	}
}

func TestXHTTPBadExtraIsKeptAsText(t *testing.T) {
	f := parserFields(t, xhttpLine(`{bad`))
	expectFields(t, "bad extra", f, map[string]any{"_extra": "{bad", "_extra_unsupported": nil})
}

func TestRangeAndIntegerValues(t *testing.T) {
	for _, c := range []struct {
		v    any
		kind rangeKind
		want any
		ok   bool
	}{
		{"100-1000", rangeStrictPositiveText, "100-1000", true},
		{100.0, rangeStrictPositiveText, "100", true},
		{" +7 - 7 ", rangeStrictPositiveValue, 7.0, true},
		{"0", rangeNonNegative, 0.0, true},
		{"0", rangePositive, nil, false},
		{"0-1", rangePositive, "0-1", true},
		{"0-0", rangePositive, nil, false},
		{"2-1", rangeNonNegative, nil, false},
		{"1-2-3", rangeNonNegative, nil, false},
		{-5.0, rangeNonNegative, nil, false},
		{1.5, rangeNonNegative, nil, false},
		{true, rangeNonNegative, nil, false},
		{"9007199254740992", rangeNonNegative, nil, false},
	} {
		got, ok := rangeValue(c.v, c.kind)
		if ok != c.ok || (ok && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("rangeValue(%#v, %+v) = %#v, %v; want %#v, %v", c.v, c.kind, got, ok, c.want, c.ok)
		}
	}
	for _, c := range []struct {
		v           any
		nonNegative bool
		want        float64
		ok          bool
	}{
		{443.0, true, 443, true},
		{" 443 ", true, 443, true},
		{"-1", true, 0, false},
		{"-1", false, -1, true},
		{-1.0, true, 0, false},
		{1.5, false, 0, false},
		{"1e3", false, 0, false},
	} {
		got, ok := integerValue(c.v, c.nonNegative)
		if ok != c.ok || got != c.want {
			t.Errorf("integerValue(%#v, %v) = %v, %v; want %v, %v", c.v, c.nonNegative, got, ok, c.want, c.ok)
		}
	}
}
