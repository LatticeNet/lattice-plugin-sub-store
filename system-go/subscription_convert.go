package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LatticeNet/lattice-sdk/model"
)

// subscriptionConvertTargets is the bounded set of clients convert produces
// for. It is the core's own share target allowlist (subscriptionShareTargets in
// lattice-server), so a link the core serves can always be converted and
// nothing else can be asked for.
var subscriptionConvertTargets = map[string]bool{
	"URI": true, "Stash": true, "ClashMeta": true, "Egern": true,
	"Surfboard": true, "Surge": true, "SurgeMac": true, "Loon": true,
	"Shadowrocket": true, "QX": true, "sing-box": true, "V2Ray": true,
	"Clash": true, "JSON": true,
}

// maxConvertOptions bounds the produce flags one call may carry. Upstream has a
// handful; the bound only keeps a caller from sending thousands.
const maxConvertOptions = 16

// subscriptionConvertRequest is the whole input of a convert: node text, the
// client, and how a URI list is carried. There is no record id, no operator
// chain and no source to resolve, so a call can only ever produce a document
// from what it was handed.
type subscriptionConvertRequest struct {
	// URIs is one share link per entry. Raw is the alternative: any node text
	// the engine parses (a URI list, a base64 list, a Clash document). Exactly
	// one of them is set.
	URIs []string `json:"uris,omitempty"`
	Raw  string   `json:"raw,omitempty"`
	// Target is the client, from subscriptionConvertTargets.
	Target string `json:"target"`
	// Format is "" or "base64" for the client-native default (a URI list in
	// its base64 envelope, every other target as its producer writes it), or
	// "plain" for a bare URI list.
	Format string `json:"format,omitempty"`
	// Options are produce() flags under upstream's own names, booleans only.
	Options map[string]bool `json:"options,omitempty"`
}

type subscriptionConvertResult struct {
	Content     string `json:"content"`
	ContentType string `json:"content_type"`
	Target      string `json:"target"`
	NodeCount   int    `json:"node_count"`
}

// convertSubscription is the stateless converter behind per-identity links:
// the core builds one identity's own URI list and asks for that client's
// document. The plugin never sees the link's token, never reads or writes its
// store, and keeps nothing from one call to the next.
//
// Statelessness is enforced in three places rather than promised in one. The
// manifest declares the method with zero host calls, so the runner refuses any
// store or network access outright. The request has no operator chain (an
// operators field is an unknown field and is refused), so no user JavaScript
// runs and the call qualifies for the warm runtime. And the only engine calls
// are parse and produce, which keep no data between calls: the source audit
// of the pinned core (tools/substore-core/state-audit.json, bound to the
// embedded bundle by TestEmbeddedCoreIsTheStateAuditedCore) and the twelve-call
// isolation test (TestConvertCallsOnOneWarmRuntimeNeverCarryEachOthersCredentials)
// are what that rests on. If either ever says otherwise, the fallback is
// runIsolatedScript, at about 0.85 s per call locally.
//
// A document with no node for the client is refused with
// zeroNodesForTargetCode, the same rule the serve path applies.
func (rt *runtime) convertSubscription(payload json.RawMessage) (subscriptionConvertResult, error) {
	// The request bound is the SDK's convert bound, which core enforces when
	// it builds the request; the response bound was 2 MiB short of it.
	if len(payload) > model.MaxConvertRequestBytes {
		return subscriptionConvertResult{}, fmt.Errorf("convert payload exceeds %d bytes", model.MaxConvertRequestBytes)
	}
	var req subscriptionConvertRequest
	if len(payload) > 0 {
		if err := decodeStrictVPNCoreGraphJSON(payload, &req); err != nil {
			return subscriptionConvertResult{}, fmt.Errorf("invalid convert payload: %w", err)
		}
	}
	raw, err := convertInput(req)
	if err != nil {
		return subscriptionConvertResult{}, err
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		return subscriptionConvertResult{}, errors.New("convert needs a target")
	}
	if !subscriptionConvertTargets[target] {
		return subscriptionConvertResult{}, fmt.Errorf("target %q is not a client convert produces for", target)
	}
	switch strings.ToLower(strings.TrimSpace(req.Format)) {
	case "", "base64", "plain":
	default:
		return subscriptionConvertResult{}, errors.New("convert format must be base64 (the client-native default) or plain")
	}
	if len(req.Options) > maxConvertOptions {
		return subscriptionConvertResult{}, fmt.Errorf("convert accepts at most %d options", maxConvertOptions)
	}

	converted, err := rt.subStoreEngine().convert(subStoreConversionRequest{
		Raw:     raw,
		Target:  target,
		Options: req.Options,
	})
	if err != nil {
		return subscriptionConvertResult{}, err
	}
	if converted.ZeroNodes {
		return subscriptionConvertResult{}, zeroNodesForTargetError("the convert input", target)
	}
	body, contentType, err := encodeSubscriptionOutput(converted.Output, req.Format, target)
	if err != nil {
		return subscriptionConvertResult{}, err
	}
	return subscriptionConvertResult{Content: body, ContentType: contentType, Target: target, NodeCount: converted.NodeCount}, nil
}

// convertInput turns the request's node input into the text the engine
// parses. Errors never quote the input: it carries credentials.
func convertInput(req subscriptionConvertRequest) (string, error) {
	hasURIs, hasRaw := len(req.URIs) > 0, strings.TrimSpace(req.Raw) != ""
	if hasURIs == hasRaw {
		return "", errors.New("convert needs exactly one of uris or raw")
	}
	if hasRaw {
		return req.Raw, nil
	}
	if len(req.URIs) > maxExportLinks {
		return "", fmt.Errorf("convert accepts at most %d uris", maxExportLinks)
	}
	lines := make([]string, 0, len(req.URIs))
	for i, uri := range req.URIs {
		uri = strings.TrimSpace(uri)
		switch {
		case uri == "":
			return "", fmt.Errorf("uris[%d] is empty", i)
		case strings.ContainsAny(uri, "\r\n"):
			// One entry must be one node. A line break would let a single entry
			// carry several, which the count and every later check would miss.
			return "", fmt.Errorf("uris[%d] must be one line", i)
		case len(uri) > maxLinkBytes:
			return "", fmt.Errorf("uris[%d] exceeds %d bytes", i, maxLinkBytes)
		}
		lines = append(lines, uri)
	}
	return strings.Join(lines, "\n"), nil
}
