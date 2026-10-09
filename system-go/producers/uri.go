package producers

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/normalise"
)

// The URI producer (specs/producers/uri.md): one share link per node, joined
// by "\n" with no trailing newline. Its goldens are byte-exact, so every rule
// below reproduces upstream's bytes, the lossy and malformed ones included.

type uriProducer struct{}

func (uriProducer) ID() string { return "uri" }

// EmptyDocument is the empty string under every option (uri.md, "Empty
// document").
func (uriProducer) EmptyDocument(Options) []byte { return []byte{} }

func (uriProducer) Produce(dst *bytes.Buffer, nodes []*nodemodel.Node, target string, opts Options) (Result, error) {
	ps, dropped := prepare(nodes, target, "uri", opts)
	res := Result{Dropped: dropped}
	for _, p := range ps {
		line, err := uriLine(p)
		if err != nil {
			res.Dropped = append(res.Dropped, droppedBy(p, err))
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

// errNoForm marks a node the URI target has no form for. The URI document
// leaves it out like a failing node; the V2Ray document gives it an empty
// line where a failing node leaves nothing (v2ray.md, "Output shape").
var errNoForm = errors.New("no URI form")

func droppedBy(p prepared, err error) Dropped {
	reason := ReasonFailed
	if errors.Is(err, errNoForm) {
		reason = ReasonUnsupported
	}
	return Dropped{Index: p.index, Type: typeOf(p.node.Fields), Reason: reason}
}

func sortDropped(d []Dropped) {
	slices.SortStableFunc(d, func(a, b Dropped) int { return a.Index - b.Index })
}

// uriLine writes one node's link. It returns errNoForm for a type without a
// URI form and for a TUIC v4 node, and another error when producing the node
// fails.
func uriLine(p prepared) (string, error) {
	f := p.node.Fields
	uriPrepare(f)
	switch f["type"] {
	case "socks5":
		return socksLine(f), nil
	case "ss":
		return ssLine(f)
	case "ssr":
		return ssrLine(f), nil
	case "vmess":
		return vmessLine(f)
	case "vless":
		return vlessLine(f)
	case "trojan":
		return trojanLine(f)
	case "hysteria2":
		return hysteria2Line(f), nil
	case "hysteria":
		return hysteriaLine(f, p.added), nil
	case "tuic":
		return tuicLine(f, p.added)
	case "anytls":
		return anytlsLine(f, p.added)
	case "wireguard":
		return wireguardLine(f, p.added), nil
	}
	return "", errNoForm
}

// uriPrepare is the producer's own preparation (uri.md, "Preparation inside
// the producer"). f is the steps' top-level copy, so removing keys here never
// reaches the caller's node.
func uriPrepare(f map[string]any) {
	for _, k := range []string{"subName", "collectionName", "id", "resolved", "no-resolve"} {
		delete(f, k)
	}
	for k, v := range f {
		if v == nil {
			delete(f, k)
		}
	}
	switch f["type"] {
	case "tuic", "hysteria", "hysteria2", "juicity", "trusttunnel":
		delete(f, "tls")
	}
	if f["type"] != "vmess" {
		bracketIPv6(f)
	}
}

func bracketIPv6(f map[string]any) {
	if s, ok := f["server"].(string); ok && normalise.IsIPv6Literal(s) {
		f["server"] = "[" + s + "]"
	}
}

// authority is server:port as the links write them, raw.
func authority(f map[string]any) string {
	return textOf(f, "server") + ":" + textOf(f, "port")
}

// fragment is "#" and the component-encoded name.
func fragment(f map[string]any) string {
	return "#" + encodeComponent(textOf(f, "name"))
}

// query collects key=value pieces in order.
type query []string

func (q *query) add(key, value string) { *q = append(*q, key+"="+value) }

// enc adds key with the component-encoded text form of v.
func (q *query) enc(key string, v any) { q.add(key, encodeComponent(text(v))) }

// ifSet adds key with m[src] component-encoded when m[src] is set.
func (q *query) ifSet(key string, m map[string]any, src string) {
	if set(m, src) {
		q.enc(key, m[src])
	}
}

func (q query) String() string { return strings.Join(q, "&") }

// transportOpts is <network>-opts.
func transportOpts(f map[string]any) map[string]any {
	o, _ := obj(f, textOf(f, "network")+"-opts")
	return o
}

// transportPath is the first path of the transport options as text, "" when
// there is none.
func transportPath(o map[string]any) string {
	if v, ok := o["path"]; ok {
		if p, ok := first(v); ok && p != nil {
			return text(p)
		}
	}
	return ""
}

func httpUpgrade(o map[string]any) bool { return set(o, "v2ray-http-upgrade") }

func httpUpgradeFastOpen(o map[string]any) bool {
	return httpUpgrade(o) && set(o, "v2ray-http-upgrade-fast-open")
}

// typeParam is the type parameter of ss and trojan: httpupgrade for a
// websocket with http-upgrade, the network otherwise.
func typeParam(f map[string]any) string {
	net := textOf(f, "network")
	if net == "ws" && httpUpgrade(transportOpts(f)) {
		return "httpupgrade"
	}
	return net
}

// earlyDataPath is the transport path of ss, trojan and the vmess JSON form
// after the path early-data rules (uri.md, "Path early-data rules").
func earlyDataPath(f map[string]any) string {
	o := transportOpts(f)
	path := transportPath(o)
	if f["network"] != "ws" {
		return path
	}
	if httpUpgradeFastOpen(o) {
		return httpUpgradeEarlyData(o, path)
	}
	if httpUpgrade(o) {
		return path
	}
	m, hasM := o["max-early-data"]
	if s, ok := safeInteger(m); ok {
		if set(o, "early-data-header-name") && text(o["early-data-header-name"]) != "Sec-WebSocket-Protocol" {
			return removeQueryParam(path, "ed")
		}
		return appendQueryParam(path, "ed", s)
	}
	if hasM && text(m) != "" {
		return removeQueryParam(path, "ed")
	}
	return path
}

// httpUpgradeEarlyData appends ed: the stored _v2ray-http-upgrade-ed when it
// is a safe integer, else the path's first non-empty ed value when that is
// one, else 2560.
func httpUpgradeEarlyData(o map[string]any, path string) string {
	ed := "2560"
	if s, ok := safeInteger(o["_v2ray-http-upgrade-ed"]); ok {
		ed = s
	} else if v, ok := firstQueryValue(path, "ed"); ok {
		if s, ok := safeInteger(v); ok {
			ed = s
		}
	}
	return appendQueryParam(path, "ed", ed)
}

// removeQueryParam drops every piece of the path's query whose decoded key is
// name, and empty pieces; the rest stay verbatim. With nothing left the "?"
// goes too.
func removeQueryParam(path, name string) string {
	base, q, ok := strings.Cut(path, "?")
	if !ok {
		return path
	}
	var kept []string
	for _, piece := range strings.Split(q, "&") {
		if piece == "" {
			continue
		}
		key, _, _ := strings.Cut(piece, "=")
		if k, ok := decodeQueryKey(key); ok && k == name {
			continue
		}
		kept = append(kept, piece)
	}
	if len(kept) == 0 {
		return base
	}
	return base + "?" + strings.Join(kept, "&")
}

// appendQueryParam replaces name in the path's query with name=value, both
// component-encoded. An absent or empty path is taken as "/".
func appendQueryParam(path, name, value string) string {
	if path == "" {
		path = "/"
	}
	path = removeQueryParam(path, name)
	piece := encodeComponent(name) + "=" + encodeComponent(value)
	switch {
	case !strings.Contains(path, "?"):
		return path + "?" + piece
	case strings.HasSuffix(path, "?"), strings.HasSuffix(path, "&"):
		return path + piece
	}
	return path + "&" + piece
}

// firstQueryValue is the first non-empty value of name in the path's query.
func firstQueryValue(path, name string) (string, bool) {
	_, q, ok := strings.Cut(path, "?")
	if !ok {
		return "", false
	}
	for _, piece := range strings.Split(q, "&") {
		key, value, _ := strings.Cut(piece, "=")
		if k, ok := decodeQueryKey(key); ok && k == name && value != "" {
			return value, true
		}
	}
	return "", false
}

// headersHost is <network>-opts.headers.Host, the first element of a list,
// when set: the only host ss and trojan write.
func headersHost(o map[string]any) (any, bool) {
	headers, _ := obj(o, "headers")
	if !set(headers, "Host") {
		return nil, false
	}
	return first(headers["Host"])
}

// transportHost is the transport host rule of vless and the vmess JSON form,
// the first element of a list.
func transportHost(f map[string]any) (any, bool) {
	o := transportOpts(f)
	headers, _ := obj(o, "headers")
	var candidates [3]any
	switch f["network"] {
	case "h2":
		candidates = [3]any{o["host"], headers["host"], headers["Host"]}
	case "xhttp":
		candidates = [3]any{o["host"], headers["Host"], headers["host"]}
	default:
		candidates = [3]any{headers["Host"], headers["host"], o["host"]}
	}
	for _, c := range candidates {
		if truthy(c) {
			return first(c)
		}
	}
	return nil, false
}

// alpnParam is the alpn rule of vless, trojan, ss and the anytls base: a list
// joins with ",", and any other set value makes the node fail.
func alpnParam(q *query, f map[string]any) error {
	if !set(f, "alpn") {
		return nil
	}
	l, ok := f["alpn"].([]any)
	if !ok {
		return fmt.Errorf("alpn is not a list")
	}
	q.enc("alpn", l)
	return nil
}

// vcnParam is vless row 18: a _vcn list, even an empty one, else
// name-cert-verify when set.
func vcnParam(q *query, f map[string]any) {
	if l, ok := f["_vcn"].([]any); ok {
		q.enc("vcn", l)
		return
	}
	q.ifSet("vcn", f, "name-cert-verify")
}

// grpcParams are serviceName, authority and mode, in that order, for ss and
// trojan.
func grpcParams(q *query, f map[string]any) {
	g, _ := obj(f, "grpc-opts")
	q.ifSet("serviceName", g, "grpc-service-name")
	q.ifSet("authority", g, "_grpc-authority")
	q.enc("mode", grpcMode(g))
}

func grpcMode(g map[string]any) any {
	if set(g, "_grpc-type") {
		return g["_grpc-type"]
	}
	return "gun"
}

func socksLine(f map[string]any) string {
	user, pass := "", ""
	if v, ok := f["username"]; ok {
		user = text(v)
	}
	if v, ok := f["password"]; ok {
		pass = text(v)
	}
	// The name is inserted raw (quirk 1).
	return "socks://" + encodeComponent(b64(user+":"+pass)) + "@" + authority(f) + "#" + textOf(f, "name")
}

func ssLine(f map[string]any) (string, error) {
	cipher, password := textOf(f, "cipher"), textOf(f, "password")
	userinfo := b64(cipher + ":" + password)
	if strings.HasPrefix(cipher, "2022-blake3-") {
		userinfo = encodeComponent(cipher) + ":" + encodeComponent(password)
	}
	line := "ss://" + userinfo + "@" + authority(f)
	var q query
	if set(f, "plugin") {
		line += "/"
		plugin, err := ssPlugin(f)
		if err != nil {
			return "", err
		}
		q.add("plugin", encodeComponent(plugin))
	}
	if set(f, "udp-over-tcp") {
		q.add("uot", "1")
	}
	if set(f, "tfo") {
		q.add("tfo", "1")
	}
	if set(f, "udp") {
		q.add("udp", "1")
	} else {
		q.add("udp", "0")
	}
	tls := set(f, "tls")
	if tls {
		q.enc("sni", sniOrServer(f))
		if set(f, "skip-cert-verify") {
			q.add("allowInsecure", "1")
		}
	}
	if set(f, "network") {
		q.enc("type", typeParam(f))
		if f["network"] == "grpc" {
			grpcParams(&q, f)
		}
		if path := earlyDataPath(f); path != "" {
			q.enc("path", path)
		}
		if h, ok := headersHost(transportOpts(f)); ok {
			q.enc("host", h)
		}
	}
	if err := alpnParam(&q, f); err != nil {
		return "", err
	}
	q.ifSet("fp", f, "client-fingerprint")
	reality := set(f, "reality-opts")
	switch {
	case reality:
		q.add("security", "reality")
	case tls:
		q.add("security", "tls")
	}
	if reality {
		r, _ := obj(f, "reality-opts")
		q.ifSet("sid", r, "short-id")
		q.ifSet("pbk", r, "public-key")
		q.ifSet("spx", r, "_spider-x")
		q.ifSet("mode", f, "_mode")
		q.ifSet("extra", f, "_extra")
	}
	return line + "?" + q.String() + fragment(f), nil
}

// sniOrServer is sni when set, else the (bracketed) server.
func sniOrServer(f map[string]any) any {
	if set(f, "sni") {
		return f["sni"]
	}
	return textOf(f, "server")
}

// ssPlugin is the plugin string before component encoding. A plugin other
// than obfs, v2ray-plugin and shadow-tls makes the node fail.
func ssPlugin(f map[string]any) (string, error) {
	o, _ := obj(f, "plugin-opts")
	switch f["plugin"] {
	case "obfs":
		s := "simple-obfs;obfs=" + textOf(o, "mode")
		if set(o, "host") {
			s += ";obfs-host=" + text(o["host"])
		}
		return s, nil
	case "v2ray-plugin":
		mode := textOf(o, "mode")
		s := "v2ray-plugin;obfs=" + mode + ";mode=" + mode
		if set(o, "host") {
			host := text(o["host"])
			s += ";obfs-host=" + host + ";host=" + host
		}
		if set(o, "path") {
			s += ";path=" + text(o["path"])
		}
		if set(o, "tls") {
			s += ";tls"
		}
		if set(o, "sni") {
			s += ";sni=" + text(o["sni"])
		}
		if set(o, "skip-cert-verify") {
			s += ";skip-cert-verify=" + text(o["skip-cert-verify"])
		}
		if mux, ok := o["mux"]; ok {
			s += ";mux=" + muxText(mux)
		}
		return s, nil
	case "shadow-tls":
		return "shadow-tls;host=" + textOf(o, "host") + ";password=" + textOf(o, "password") + ";version=" + textOf(o, "version"), nil
	}
	return "", fmt.Errorf("ss plugin %q has no URI form", text(f["plugin"]))
}

// muxText normalises the v2ray-plugin mux value.
func muxText(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "1"
		}
		return "0"
	case string:
		switch t := strings.ToLower(trimES(x)); {
		case t == "true":
			return "1"
		case t == "false":
			return "0"
		case digitsOnly(x):
			if n, err := strconv.ParseFloat(x, 64); err == nil {
				return text(n)
			}
		}
	}
	return text(v)
}

func ssrLine(f map[string]any) string {
	body := authority(f) + ":" + textOf(f, "protocol") + ":" + textOf(f, "cipher") + ":" + textOf(f, "obfs") + ":" +
		b64(textOf(f, "password")) + "/?remarks=" + b64(textOf(f, "name"))
	if set(f, "obfs-param") {
		body += "&obfsparam=" + b64(text(f["obfs-param"]))
	}
	if set(f, "protocol-param") {
		body += "&protoparam=" + b64(text(f["protocol-param"]))
	}
	return "ssr://" + b64(body)
}

// vmessSecurity is the VMess security normalisation of cipher.
func vmessSecurity(f map[string]any) string {
	c := ""
	if v, ok := f["cipher"]; ok {
		c = strings.ToLower(trimES(text(v)))
	}
	switch c {
	case "auto", "none", "zero", "aes-128-gcm", "chacha20-poly1305":
		return c
	case "chacha20-ietf-poly1305":
		return "chacha20-poly1305"
	}
	return "auto"
}

func vmessLine(f map[string]any) (string, error) {
	if set(f, "reality-opts") || set(f, "_finalmask") {
		return vmessQueryLine(f)
	}
	return vmessJSONLine(f)
}

// vmessJSONLine is the v2rayN JSON form.
func vmessJSONLine(f map[string]any) (string, error) {
	net, _ := f["network"].(string)
	o := transportOpts(f)
	var j object
	j.set("v", "2")
	setPresent(&j, "ps", f, "name")
	setPresent(&j, "add", f, "server")
	j.set("port", textOf(f, "port"))
	setPresent(&j, "id", f, "uuid")
	aid := "0"
	if set(f, "alterId") {
		aid = text(f["alterId"])
	}
	j.set("aid", aid)
	j.set("scy", vmessSecurity(f))
	switch {
	case !set(f, "network"):
		j.set("net", "tcp")
	case net == "http":
		j.set("net", "tcp")
	case net == "ws" && httpUpgrade(o):
		j.set("net", "httpupgrade")
	default:
		j.set("net", f["network"])
	}
	switch net {
	case "http":
		j.set("type", "http")
	case "grpc":
		g, _ := obj(f, "grpc-opts")
		j.set("type", grpcMode(g))
	case "kcp", "quic":
		if set(o, "_"+net+"-type") {
			j.set("type", o["_"+net+"-type"])
		} else {
			j.set("type", "none")
		}
	default:
		j.set("type", "")
	}
	tls := set(f, "tls")
	if tls {
		j.set("tls", "tls")
	} else {
		j.set("tls", "")
	}
	if a, ok := f["alpn"]; ok {
		if l, ok := a.([]any); ok {
			j.set("alpn", text(l))
		} else {
			j.set("alpn", a)
		}
	}
	setPresent(&j, "fp", f, "client-fingerprint")
	if tls {
		setPresent(&j, "sni", f, "sni")
	}
	switch net {
	case "grpc":
		g, _ := obj(f, "grpc-opts")
		setPresent(&j, "path", g, "grpc-service-name")
		setPresent(&j, "host", g, "_grpc-authority")
	case "kcp", "quic":
		setPresent(&j, "path", o, "_"+net+"-path")
		setPresent(&j, "host", o, "_"+net+"-host")
	default:
		if path := earlyDataPath(f); path != "" {
			j.set("path", path)
		}
		if h, ok := transportHost(f); ok {
			j.set("host", h)
		}
	}
	body, err := jsonText(&j)
	if err != nil {
		return "", err
	}
	return "vmess://" + b64(body), nil
}

// setPresent copies m[src] to key when it is present.
func setPresent(j *object, key string, m map[string]any, src string) {
	if v, ok := m[src]; ok {
		j.set(key, v)
	}
}

// vmessQueryLine is the VMess line built as a VLESS line, used when the node
// carries Reality or a finalmask. A non-AEAD node yields no line.
func vmessQueryLine(f map[string]any) (string, error) {
	if aead := f["aead"]; aead == false || (aead != true && nonZero(f["alterId"])) {
		return "", errors.New("the vmess query form needs an AEAD node")
	}
	c := make(map[string]any, len(f)+1)
	for k, v := range f {
		c[k] = v
	}
	bracketIPv6(c)
	if !set(c, "network") {
		c["network"] = "tcp"
	}
	c["encryption"] = vmessSecurity(f)
	delete(c, "flow")
	line, err := vlessLine(c)
	if err != nil {
		return "", err
	}
	return "vmess://" + strings.TrimPrefix(line, "vless://"), nil
}

// nonZero reports a set value that does not read as the number zero.
func nonZero(v any) bool {
	if !truthy(v) {
		return false
	}
	n, err := strconv.ParseFloat(trimES(text(v)), 64)
	return err != nil || n != 0
}

func vlessLine(f map[string]any) (string, error) {
	net := textOf(f, "network")
	o := transportOpts(f)
	ws, _ := obj(f, "ws-opts")
	reality := set(f, "reality-opts")
	var q query

	switch {
	case reality:
		q.add("security", "reality")
	case set(f, "tls"):
		q.add("security", "tls")
	default:
		q.add("security", "none")
	}
	switch {
	case net == "ws" && httpUpgrade(o):
		q.add("type", "httpupgrade")
	case net == "http":
		q.add("type", "tcp")
	case net == "h2":
		q.add("type", "http")
	default:
		q.enc("type", net)
	}
	if net == "http" {
		q.add("headerType", "http")
	}
	if net == "grpc" {
		g, _ := obj(f, "grpc-opts")
		q.enc("mode", grpcMode(g))
		q.ifSet("authority", g, "_grpc-authority")
	}
	if path := vlessPath(net, o); path != "" {
		q.enc("path", path)
	}
	if h, ok := transportHost(f); ok {
		q.enc("host", h)
	}
	q.ifSet("serviceName", o, net+"-service-name")
	if net == "http" {
		q.ifSet("method", o, "method")
	}
	if net == "kcp" {
		q.ifSet("seed", f, "seed")
		q.ifSet("headerType", f, "headerType")
	}
	upgrade := httpUpgrade(ws)
	if net == "ws" && !upgrade {
		if v, ok := ws["max-early-data"]; ok {
			if s, ok := safeInteger(v); ok {
				q.enc("ed", s)
			}
		}
	}
	if set(ws, "early-data-header-name") {
		_, hasMax := ws["max-early-data"]
		if upgrade || !hasMax || text(ws["early-data-header-name"]) != "Sec-WebSocket-Protocol" {
			q.enc("eh", ws["early-data-header-name"])
		}
	}
	if pe, ok := packetEncoding(f); ok {
		q.add("packetEncoding", pe)
	}
	if err := alpnParam(&q, f); err != nil {
		return "", err
	}
	if set(f, "skip-cert-verify") {
		q.add("allowInsecure", "1")
	}
	q.ifSet("pcs", f, "tls-fingerprint")
	vcnParam(&q, f)
	if v, ok := echValue(f["ech-opts"]); ok {
		q.enc("ech", v)
	} else if _, isMap := f["ech-opts"].(map[string]any); !isMap && notBlank(f["_echConfigList"]) {
		q.enc("ech", f["_echConfigList"])
	}
	if set(f, "_h2") {
		q.add("h2", "1")
	}
	q.ifSet("sni", f, "sni")
	q.ifSet("fp", f, "client-fingerprint")
	q.ifSet("flow", f, "flow")
	if reality {
		r, _ := obj(f, "reality-opts")
		q.ifSet("sid", r, "short-id")
		q.ifSet("spx", r, "_spider-x")
		if set(r, "public-key") {
			pbk := encodeComponent(text(r["public-key"]))
			if set(r, "support-x25519mlkem768") {
				pbk += "&support-x25519mlkem768=" + encodeComponent(text(r["support-x25519mlkem768"]))
			}
			q.add("pbk", pbk)
		}
	}
	xo, _ := obj(f, "xhttp-opts")
	if net == "xhttp" && set(xo, "mode") {
		q.enc("mode", xo["mode"])
	} else {
		q.ifSet("mode", f, "_mode")
	}
	extra, err := vlessExtra(f)
	if err != nil {
		return "", err
	}
	if extra != "" {
		q.add("extra", encodeComponent(extra))
	}
	switch fm := f["_finalmask"].(type) {
	case string:
		if fm != "" {
			q.add("fm", encodeComponent(fm))
		}
	case map[string]any:
		s, err := jsonText(fm)
		if err != nil {
			return "", err
		}
		q.add("fm", encodeComponent(s))
	}
	q.ifSet("pqv", f, "_pqv")
	q.ifSet("encryption", f, "encryption")
	return "vless://" + textOf(f, "uuid") + "@" + authority(f) + "?" + q.String() + fragment(f), nil
}

// vlessPath is the VLESS path rule: http-upgrade early data for a fast-open
// http-upgrade websocket; otherwise a websocket with max-early-data loses the
// path's ed parameters, which travel in the ed parameter instead.
func vlessPath(net string, o map[string]any) string {
	path := transportPath(o)
	if net != "ws" {
		return path
	}
	if httpUpgradeFastOpen(o) {
		return httpUpgradeEarlyData(o, path)
	}
	if _, ok := o["max-early-data"]; ok && path != "" {
		return removeQueryParam(path, "ed")
	}
	return path
}

// packetEncoding is the packet-encoding rule.
func packetEncoding(f map[string]any) (string, bool) {
	if v, ok := f["packet-encoding"]; ok {
		switch strings.ToLower(trimES(text(v))) {
		case "":
			return "none", true
		case "packetaddr":
			return "packet", true
		case "xudp":
			return "xudp", true
		}
		return "", false
	}
	switch {
	case set(f, "xudp"):
		return "xudp", true
	case set(f, "packet-addr"):
		return "packet", true
	case f["udp"] == true:
		return "none", true
	}
	return "", false
}

// echValue is the ECH value rule over an ech-opts mapping.
func echValue(v any) (string, bool) {
	e, ok := v.(map[string]any)
	if !ok || !echEnabled(e["enable"]) {
		return "", false
	}
	if notBlank(e["config"]) {
		return text(e["config"]), true
	}
	qsn := notBlank(e["query-server-name"])
	if notBlank(e["_dns"]) {
		if qsn {
			return text(e["query-server-name"]) + "+" + text(e["_dns"]), true
		}
		return text(e["_dns"]), true
	}
	if qsn {
		return text(e["query-server-name"]) + "+https://dns.alidns.com/dns-query", true
	}
	return "", false
}

// echEnabled is true or a non-zero integer.
func echEnabled(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0 && x == float64(int64(x))
	case int64:
		return x != 0
	case int:
		return x != 0
	}
	return false
}

func trojanLine(f map[string]any) (string, error) {
	var q query
	q.enc("sni", sniOrServer(f))
	if set(f, "skip-cert-verify") {
		q.add("allowInsecure", "1")
	}
	if set(f, "network") {
		q.enc("type", typeParam(f))
		if f["network"] == "grpc" {
			grpcParams(&q, f)
		}
		if path := earlyDataPath(f); path != "" {
			q.enc("path", path)
		}
		if h, ok := headersHost(transportOpts(f)); ok {
			q.enc("host", h)
		}
	}
	if err := alpnParam(&q, f); err != nil {
		return "", err
	}
	q.ifSet("fp", f, "client-fingerprint")
	q.ifSet("pcs", f, "tls-fingerprint")
	vcnParam(&q, f)
	if set(f, "reality-opts") {
		q.add("security", "reality")
		r, _ := obj(f, "reality-opts")
		q.ifSet("sid", r, "short-id")
		q.ifSet("pbk", r, "public-key")
		q.ifSet("spx", r, "_spider-x")
		q.ifSet("mode", f, "_mode")
		q.ifSet("extra", f, "_extra")
	}
	// The password is inserted raw (quirk 3).
	return "trojan://" + textOf(f, "password") + "@" + authority(f) + "?" + q.String() + fragment(f), nil
}

func hysteria2Line(f map[string]any) string {
	var q query
	if set(f, "hop-interval") {
		q.add("hop-interval", text(f["hop-interval"]))
	}
	if set(f, "keepalive") {
		q.add("keepalive", text(f["keepalive"]))
	}
	if set(f, "skip-cert-verify") {
		q.add("insecure", "1")
	}
	if set(f, "obfs") {
		q.enc("obfs", f["obfs"])
		q.ifSet("obfs-password", f, "obfs-password")
	}
	q.ifSet("sni", f, "sni")
	if set(f, "ports") {
		q.add("mport", text(f["ports"]))
	}
	q.ifSet("pinSHA256", f, "tls-fingerprint")
	if set(f, "tfo") {
		q.add("fastopen", "1")
	}
	if v, ok := echValue(f["ech-opts"]); ok {
		q.enc("ech", v)
	}
	return "hysteria2://" + encodeComponent(textOf(f, "password")) + "@" + authority(f) + "?" + q.String() + fragment(f)
}

// firstOrText is "the first element, or the string": a list's first element
// (undefined for an empty list), any other value as it is, as text.
func firstOrText(v any) string {
	if l, ok := v.([]any); ok {
		if len(l) == 0 {
			return "undefined"
		}
		return text(l[0])
	}
	return text(v)
}

func hysteriaLine(f map[string]any, added []string) string {
	var q query
	fastOpen := false
	for _, k := range keyOrder(f, added) {
		v := f[k]
		switch k {
		case "name", "type", "server", "port":
		case "alpn":
			if truthy(v) {
				q.add("alpn", encodeComponent(firstOrText(v)))
			}
		case "skip-cert-verify":
			if truthy(v) {
				q.add("insecure", "1")
			}
		case "tfo", "fast-open":
			if truthy(v) && !fastOpen {
				q.add("fastopen", "1")
				fastOpen = true
			}
		case "ports":
			q.add("mport", text(v))
		case "auth-str":
			q.add("auth", text(v))
		case "up":
			q.add("upmbps", text(v))
		case "down":
			q.add("downmbps", text(v))
		case "_obfs":
			q.add("obfs", text(v))
		case "obfs":
			q.add("obfsParam", text(v))
		case "sni":
			q.add("peer", text(v))
		default:
			if !strings.HasPrefix(k, "_") && truthy(v) {
				q.enc(strings.Replace(k, "-", "_", 1), v)
			}
		}
	}
	return "hysteria://" + authority(f) + "?" + q.String() + fragment(f)
}

func tuicLine(f map[string]any, added []string) (string, error) {
	if set(f, "token") {
		return "", fmt.Errorf("tuic v4 token node: %w", errNoForm)
	}
	var q query
	fastOpen := false
	for _, k := range keyOrder(f, added) {
		v := f[k]
		switch k {
		case "name", "type", "uuid", "password", "server", "port", "tls":
		case "alpn":
			if truthy(v) {
				q.add("alpn", encodeComponent(firstOrText(v)))
			}
		case "skip-cert-verify":
			if truthy(v) {
				q.add("allow_insecure", "1")
			}
		case "tfo", "fast-open":
			if truthy(v) && !fastOpen {
				q.add("fast_open", "1")
				fastOpen = true
			}
		case "disable-sni":
			if truthy(v) {
				q.add("disable_sni", "1")
			}
		case "reduce-rtt":
			if truthy(v) {
				q.add("reduce_rtt", "1")
			}
		case "congestion-controller":
			q.add("congestion_control", text(v))
		default:
			if !strings.HasPrefix(k, "_") && truthy(v) {
				q.enc(strings.ReplaceAll(k, "-", "_"), v)
			}
		}
	}
	return "tuic://" + encodeComponent(textOf(f, "uuid")) + ":" + encodeComponent(textOf(f, "password")) + "@" +
		authority(f) + "?" + q.String() + fragment(f), nil
}

// anytlsLine builds the VLESS line of a copy of the node, renames its
// scheme, and merges the node's own fields into its query (uri.md,
// "anytls").
func anytlsLine(f map[string]any, added []string) (string, error) {
	c := make(map[string]any, len(f)+1)
	for k, v := range f {
		c[k] = v
	}
	if v, ok := f["password"]; ok {
		c["uuid"] = v
	} else {
		delete(c, "uuid")
	}
	if !set(c, "network") {
		c["network"] = "tcp"
	}
	base, err := vlessLine(c)
	if err != nil {
		return "", err
	}
	base = strings.Replace(base, "vless", "anytls", 1)

	var extra query
	for _, k := range keyOrder(f, added) {
		v := f[k]
		switch k {
		case "name", "type", "password", "server", "port", "tls":
		case "alpn":
			if truthy(v) {
				extra.add("alpn", encodeComponent(firstOrText(v)))
			}
		case "skip-cert-verify":
			if truthy(v) {
				extra.add("insecure", "1")
			}
		case "udp":
			if truthy(v) {
				extra.add("udp", "1")
			}
		default:
			if strings.HasPrefix(k, "_") || strings.Contains(strings.ToLower(k), "client-fingerprint") || !truthy(v) {
				continue
			}
			switch v.(type) {
			case string, float64, int64, int, bool:
				extra.enc(strings.ReplaceAll(k, "-", "_"), v)
			}
		}
	}

	head, rest, _ := strings.Cut(base, "?")
	q, _, _ := strings.Cut(rest, "?")
	q, _, _ = strings.Cut(q, "#")
	var merged object
	mergePieces := func(pieces []string) {
		for _, piece := range pieces {
			key, value, ok := strings.Cut(piece, "=")
			if key == "" {
				continue
			}
			if !ok {
				value = "undefined"
			} else {
				value, _, _ = strings.Cut(value, "=")
			}
			merged.set(key, value)
		}
	}
	mergePieces(strings.Split(q, "&"))
	mergePieces(extra)
	pairs := make([]string, 0, merged.len())
	for _, k := range merged.keys {
		pairs = append(pairs, k+"="+merged.vals[k].(string))
	}
	frag := ""
	if i := strings.IndexByte(base, '#'); i >= 0 {
		frag = base[i:]
	}
	return head + "?" + strings.Join(pairs, "&") + frag, nil
}

func wireguardLine(f map[string]any, added []string) string {
	var q query
	for _, k := range keyOrder(f, added) {
		v := f[k]
		switch k {
		case "name", "type", "server", "port", "ip", "ipv6", "ip-cidr", "ipv6-cidr", "private-key":
		case "public-key":
			q.enc("publickey", v)
		case "udp":
			if truthy(v) {
				q.add("udp", "1")
			}
		default:
			if !strings.HasPrefix(k, "_") && truthy(v) {
				q.enc(k, v)
			}
		}
	}
	var addrs []string
	if a, ok := interfaceAddress(f, "ip", "ip-cidr", normalise.IsIPv4Literal, 32); ok {
		addrs = append(addrs, a)
	}
	if a, ok := interfaceAddress(f, "ipv6", "ipv6-cidr", normalise.IsIPv6Literal, 128); ok {
		addrs = append(addrs, a)
	}
	if len(addrs) > 0 {
		q.enc("address", strings.Join(addrs, ","))
	}
	return "wireguard://" + encodeComponent(textOf(f, "private-key")) + "@" + authority(f) + "/?" + q.String() + fragment(f)
}

// interfaceAddress is a WireGuard interface address with its prefix: the
// literal without brackets or "/n", then the prefix from the cidr field when
// it is digits within the family's limit, else the address's own valid
// prefix, else the limit. A value that is not a literal of the family gives
// nothing.
func interfaceAddress(f map[string]any, field, cidrKey string, literal func(string) bool, limit uint64) (string, bool) {
	v, ok := f[field]
	if !ok {
		return "", false
	}
	s := trimES(text(v))
	addr, suffix := s, ""
	if i := strings.LastIndexByte(s, '/'); i >= 0 && digitsOnly(s[i+1:]) {
		addr, suffix = s[:i], s[i+1:]
	}
	if len(addr) >= 2 && addr[0] == '[' && addr[len(addr)-1] == ']' {
		addr = addr[1 : len(addr)-1]
	}
	if !literal(addr) {
		return "", false
	}
	prefix := limit
	if c, ok := f[cidrKey]; ok && prefixWithin(text(c), limit) {
		prefix, _ = strconv.ParseUint(text(c), 10, 64)
	} else if prefixWithin(suffix, limit) {
		prefix, _ = strconv.ParseUint(suffix, 10, 64)
	}
	return addr + "/" + strconv.FormatUint(prefix, 10), true
}

func prefixWithin(s string, limit uint64) bool {
	if !digitsOnly(s) {
		return false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return err == nil && n <= limit
}

// vlessExtra is the extra parameter's JSON text before component encoding
// (uri.md, "VLESS extra"), or "" for none.
func vlessExtra(f map[string]any) (string, error) {
	switch x := f["_extra"].(type) {
	case string:
		return x, nil
	case map[string]any:
		return jsonText(x)
	}
	if f["network"] != "xhttp" {
		if set(f, "_extra") {
			return text(f["_extra"]), nil
		}
		return "", nil
	}
	xo, _ := obj(f, "xhttp-opts")
	o := xhttpStructured(xo)
	if x, ok := xmux(xo); ok {
		o.set("xmux", x)
	}
	if d, ok := downloadSettings(xo); ok {
		o.set("downloadSettings", d)
	}
	if u, ok := obj(f, "_extra_unsupported"); ok {
		mergeUnsupported(o, u)
	}
	if o.len() == 0 {
		return "", nil
	}
	return jsonText(o)
}

// rangeBound is the lower limit of each end of a range value.
type rangeBound struct{ lower, upper uint64 }

var (
	nonNegative    = rangeBound{0, 0}
	positive       = rangeBound{0, 1}
	strictPositive = rangeBound{1, 1}
)

// rangeValue reads a number or a text of one token or two tokens split by
// "-", each an optional "+" and digits making a safe integer. A single value
// meeting both limits is a JSON number; a range with lower <= upper and each
// end within its limit is the text "lower-upper".
func rangeValue(v any, b rangeBound) (any, bool) {
	var s string
	switch v.(type) {
	case string, float64, int64, int:
		s = trimES(text(v))
	default:
		return nil, false
	}
	parts := strings.Split(s, "-")
	if len(parts) > 2 {
		return nil, false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		p = strings.TrimPrefix(p, "+")
		if !digitsOnly(p) {
			return nil, false
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil || n > maxSafeInteger {
			return nil, false
		}
		nums[i] = n
	}
	if len(nums) == 1 {
		if nums[0] < b.lower || nums[0] < b.upper {
			return nil, false
		}
		return int64(nums[0]), true
	}
	lo, hi := nums[0], nums[1]
	if lo > hi || lo < b.lower || hi < b.upper {
		return nil, false
	}
	return strconv.FormatUint(lo, 10) + "-" + strconv.FormatUint(hi, 10), true
}

// xhttpField is one structured key read as it is when set.
type xhttpField struct{ src, dst string }

// xhttpStructured is the structured xhttp object without xmux and
// downloadSettings, from xhttp options or download settings.
func xhttpStructured(xo map[string]any) *object {
	o := &object{}
	if h, ok := obj(xo, "headers"); ok {
		hs := &object{}
		for _, k := range propertyOrder(h) {
			if s, ok := h[k].(string); ok && !strings.EqualFold(k, "host") {
				hs.set(k, s)
			}
		}
		if hs.len() > 0 {
			o.set("headers", hs)
		}
	}
	if xo["no-grpc-header"] == true {
		o.set("noGRPCHeader", true)
	}
	asIs := func(fields ...xhttpField) {
		for _, fl := range fields {
			if set(xo, fl.src) {
				o.set(fl.dst, xo[fl.src])
			}
		}
	}
	asIs(xhttpField{"x-padding-bytes", "xPaddingBytes"})
	if xo["x-padding-obfs-mode"] == true {
		o.set("xPaddingObfsMode", true)
	}
	asIs(
		xhttpField{"x-padding-key", "xPaddingKey"},
		xhttpField{"x-padding-header", "xPaddingHeader"},
		xhttpField{"x-padding-placement", "xPaddingPlacement"},
		xhttpField{"x-padding-method", "xPaddingMethod"},
		xhttpField{"uplink-http-method", "uplinkHTTPMethod"},
		xhttpField{"session-placement", "sessionIDPlacement"},
		xhttpField{"session-key", "sessionIDKey"},
	)
	if s, ok := xo["session-table"].(string); ok {
		o.set("sessionIDTable", s)
	}
	ranged := func(src, dst string, b rangeBound) {
		if v, ok := rangeValue(xo[src], b); ok {
			o.set(dst, v)
		}
	}
	ranged("session-length", "sessionIDLength", strictPositive)
	asIs(
		xhttpField{"seq-placement", "seqPlacement"},
		xhttpField{"seq-key", "seqKey"},
		xhttpField{"uplink-data-placement", "uplinkDataPlacement"},
		xhttpField{"uplink-data-key", "uplinkDataKey"},
	)
	ranged("uplink-chunk-size", "uplinkChunkSize", nonNegative)
	ranged("sc-max-each-post-bytes", "scMaxEachPostBytes", strictPositive)
	ranged("sc-min-posts-interval-ms", "scMinPostsIntervalMs", positive)
	return o
}

// xmux maps reuse-settings: the five counters as range texts, then the
// keep-alive period as a signed integer.
func xmux(xo map[string]any) (*object, bool) {
	rs, ok := obj(xo, "reuse-settings")
	if !ok {
		return nil, false
	}
	x := &object{}
	for _, fl := range []xhttpField{
		{"max-connections", "maxConnections"},
		{"max-concurrency", "maxConcurrency"},
		{"c-max-reuse-times", "cMaxReuseTimes"},
		{"h-max-request-times", "hMaxRequestTimes"},
		{"h-max-reusable-secs", "hMaxReusableSecs"},
	} {
		if v, ok := rangeValue(rs[fl.src], nonNegative); ok {
			x.set(fl.dst, text(v))
		}
	}
	if v, ok := rs["h-keep-alive-period"]; ok {
		switch v.(type) {
		case string, float64, int64, int:
			if n, err := strconv.ParseInt(strings.TrimPrefix(trimES(text(v)), "+"), 10, 64); err == nil {
				x.set("hKeepAlivePeriod", n)
			}
		}
	}
	return x, x.len() > 0
}

// downloadSettings maps xhttp-opts.download-settings.
func downloadSettings(xo map[string]any) (*object, bool) {
	ds, ok := obj(xo, "download-settings")
	if !ok {
		return nil, false
	}
	d := &object{}
	if set(ds, "server") {
		d.set("address", ds["server"])
	}
	d.set("network", "xhttp")
	if p, ok := nonNegativeInteger(ds["port"]); ok {
		d.set("port", p)
	}
	reality, isReality := obj(ds, "reality-opts")
	switch {
	case isReality:
		d.set("security", "reality")
	case set(ds, "tls"):
		d.set("security", "tls")
	}
	tls := &object{}
	if set(ds, "servername") {
		tls.set("serverName", ds["servername"])
	}
	if set(ds, "client-fingerprint") {
		tls.set("fingerprint", ds["client-fingerprint"])
	}
	if set(ds, "skip-cert-verify") {
		tls.set("allowInsecure", true)
	}
	if set(ds, "alpn") {
		if l, ok := ds["alpn"].([]any); ok {
			tls.set("alpn", l)
		} else {
			tls.set("alpn", []any{ds["alpn"]})
		}
	}
	if v, ok := echValue(ds["ech-opts"]); ok {
		e, _ := obj(ds, "ech-opts")
		tls.set("echConfigList", v)
		switch e["_force-query"] {
		case "none", "half", "full":
			tls.set("echForceQuery", e["_force-query"])
		}
		if s, ok := obj(e, "_sockopt"); ok {
			tls.set("echSockopt", s)
		}
	}
	if tls.len() > 0 {
		d.set("tlsSettings", tls)
	}
	if isReality {
		r := &object{}
		if set(ds, "servername") {
			r.set("serverName", ds["servername"])
		}
		if set(ds, "client-fingerprint") {
			r.set("fingerprint", ds["client-fingerprint"])
		}
		if set(reality, "public-key") {
			r.set("publicKey", reality["public-key"])
		}
		if set(reality, "short-id") {
			r.set("shortId", reality["short-id"])
		}
		if r.len() > 0 {
			d.set("realitySettings", r)
		}
	}
	xs := &object{}
	if set(ds, "path") {
		xs.set("path", ds["path"])
	} else if set(xo, "path") {
		xs.set("path", xo["path"])
	}
	if h, ok := xhttpHost(ds); ok {
		xs.set("host", h)
	} else if h, ok := xhttpHost(xo); ok {
		xs.set("host", h)
	}
	if set(ds, "mode") {
		xs.set("mode", ds["mode"])
	} else if set(xo, "mode") {
		xs.set("mode", xo["mode"])
	}
	inner := xhttpStructured(ds)
	for _, k := range inner.keys {
		xs.set(k, inner.vals[k])
	}
	if x, ok := xmux(ds); ok {
		e := &object{}
		e.set("xmux", x)
		xs.set("extra", e)
	}
	if xs.len() > 0 {
		d.set("xhttpSettings", xs)
	}
	// network is always written; the block stands only when something else
	// was, or when the settings named a network themselves.
	if _, explicit := ds["network"]; d.len() == 1 && !explicit {
		return nil, false
	}
	return d, true
}

// xhttpHost is the xhttp transport host rule over one options mapping.
func xhttpHost(m map[string]any) (any, bool) {
	headers, _ := obj(m, "headers")
	for _, c := range []any{m["host"], headers["Host"], headers["host"]} {
		if truthy(c) {
			return first(c)
		}
	}
	return nil, false
}

// nonNegativeInteger reads a number or a digits text as a non-negative
// integer.
func nonNegativeInteger(v any) (any, bool) {
	switch x := v.(type) {
	case float64:
		if x >= 0 && x == float64(int64(x)) {
			return x, true
		}
	case int64:
		if x >= 0 {
			return x, true
		}
	case int:
		if x >= 0 {
			return x, true
		}
	case string:
		if digitsOnly(x) {
			if n, err := strconv.ParseFloat(x, 64); err == nil {
				return n, true
			}
		}
	}
	return nil, false
}

// mergeUnsupported merges _extra_unsupported into the structured object: a
// missing key is appended, two mappings merge recursively, and otherwise the
// structured value wins. Lists never merge.
func mergeUnsupported(o *object, u map[string]any) {
	for _, k := range propertyOrder(u) {
		cur, ok := o.get(k)
		if !ok {
			o.set(k, u[k])
			continue
		}
		um, uIsMap := u[k].(map[string]any)
		if !uIsMap {
			continue
		}
		switch c := cur.(type) {
		case *object:
			mergeUnsupported(c, um)
		case map[string]any:
			// A mapping the structured object took as it is: copy it into
			// an ordered object first so the caller's node stays untouched.
			co := &object{}
			for _, ck := range propertyOrder(c) {
				co.set(ck, c[ck])
			}
			mergeUnsupported(co, um)
			o.set(k, co)
		}
	}
}
