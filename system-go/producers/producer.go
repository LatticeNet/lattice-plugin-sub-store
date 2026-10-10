// Package producers writes the native engine's client documents from
// normalised nodes: the Producer contract of the S1 plan (section 2.4), the
// steps every target runs before its producer (presteps.go), and one producer
// per native target. Each producer follows its behaviour specification in the
// conformance harness (specs/producers/<id>.md) and is judged against the
// vendored produce goldens.
//
// Nothing in this package imports package main, the SDK or the script engine,
// and no producer writes output bytes with encoding/json: jsonwrite.go is the
// ECMAScript-compatible writer.
package producers

import (
	"bytes"
	"math"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// Options are produce flags under upstream's names. The plugin boundary sends
// booleans (subStoreConversionRequest.Options); the conformance runner
// receives arbitrary JSON from case meta.
type Options map[string]any

// Truthy reports whether the option is set under JavaScript truthiness: an
// absent key, null, false, zero, not-a-number and the empty text are false;
// everything else, empty objects and lists included, is true.
func (o Options) Truthy(key string) bool {
	v, ok := o[key]
	return ok && truthy(v)
}

// Result is what one Produce reports beside the bytes.
type Result struct {
	// Entries counts the produced entries or links. The zero-node rule is
	// Entries == 0, never a comparison of the text with the empty document:
	// a V2Ray document of unsupported nodes is "Cg==" and holds no link.
	Entries int
	// Dropped lists, in input order, every node that yielded no entry.
	Dropped []Dropped
}

// Dropped is one node that yielded no entry.
type Dropped struct {
	Index  int    // position in the caller's list
	Type   string // the node's type, "" when it has none
	Reason string // one of the Reason* constants
}

// Why a node yielded no entry. The first five are the steps before the
// producer (presteps.go); the last two are the producer's own.
const (
	ReasonSupportMap     = "support_map"
	ReasonRootHeaders    = "root_headers"
	ReasonSSTLS          = "ss_tls"
	ReasonBrokenReality  = "broken_reality"
	ReasonXHTTPStreamOne = "xhttp_stream_one"
	ReasonUnsupported    = "unsupported" // the target has no form for the node
	ReasonFailed         = "failed"      // producing the node failed
)

// Producer writes one target's document.
type Producer interface {
	// ID is the harness id: uri, v2ray, json, singbox, clashmeta, stash,
	// shadowrocket, surge or quantumultx.
	ID() string
	// Produce writes the document for target, the exact string the caller
	// used (any name Lookup accepts), into dst. It runs the steps before the
	// producer itself and never changes the caller's nodes. It never returns
	// an error for a node: a failing node yields nothing and is reported in
	// Result.Dropped.
	Produce(dst *bytes.Buffer, nodes []*nodemodel.Node, target string, opts Options) (Result, error)
	// EmptyDocument is what Produce writes for no nodes under opts.
	EmptyDocument(opts Options) []byte
}

// targetIDs maps every caller target string the nine native specifications
// name (specs/producers/<id>.md, "Target names") to its harness id. Keys are
// exact: the per-node supported map is keyed by the same exact string, and a
// spelling no specification lists is not a native target.
var targetIDs = map[string]string{
	"URI": "uri", "uri": "uri",
	"V2Ray": "v2ray", "v2ray": "v2ray", "v2": "v2ray",
	"JSON": "json", "json": "json",
	"sing-box": "singbox", "singbox": "singbox",
	"ClashMeta": "clashmeta", "clashmeta": "clashmeta", "meta": "clashmeta",
	"clash.meta": "clashmeta", "Clash.Meta": "clashmeta",
	"mihomo": "clashmeta", "Mihomo": "clashmeta",
	"Stash": "stash", "stash": "stash",
	"Shadowrocket": "shadowrocket", "ShadowRocket": "shadowrocket", "shadowrocket": "shadowrocket",
	"Surge": "surge", "surge": "surge",
	"QX": "quantumultx", "qx": "quantumultx", "QuantumultX": "quantumultx",
}

// registry holds the producers that exist, by harness id.
var registry = map[string]Producer{
	"uri":          uriProducer{},
	"v2ray":        v2rayProducer{},
	"json":         jsonProducer{},
	"clashmeta":    clashMetaProducer{},
	"singbox":      singBoxProducer{},
	"stash":        stashProducer{},
	"shadowrocket": shadowrocketProducer{},
	"surge":        surgeProducer{},
	"quantumultx":  quantumultXProducer{},
}

// routed holds the harness ids whose targets the dispatcher serves natively
// (Native). A registered producer that is not routed is reached through
// Lookup, which is how the conformance runner and the tests judge it, while
// its targets stay on the bundle; serving one natively is adding its id here.
var routed = map[string]bool{"uri": true, "v2ray": true, "json": true, "clashmeta": true, "singbox": true}

// Lookup maps a caller target string to its producer. It accepts the platform
// names and the aliases each specification lists, exactly as written; it
// returns ok=false for every other string and for a target whose native
// producer does not exist yet.
func Lookup(target string) (p Producer, ok bool) {
	p, ok = registry[targetIDs[target]]
	return p, ok
}

// Native reports whether the dispatcher serves target natively: it has a
// producer and its harness id is routed. Every other target goes to the
// bundle.
func Native(target string) bool {
	id := targetIDs[target]
	_, ok := registry[id]
	return ok && routed[id]
}

// truthy is ECMAScript truthiness over model values.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	case int64:
		return x != 0
	case int:
		return x != 0
	}
	return true
}
