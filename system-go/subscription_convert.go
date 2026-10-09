package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
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

// convertUnsupportedCode leads the refusal of a convert input this release
// does not run yet, so the core can tell it from a malformed request.
const convertUnsupportedCode = "convert_input_unsupported"

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
// runs. And the conversion goes through the dispatcher with no chain: the
// five native targets are answered in Go, which keeps nothing between calls,
// and the nine others on the bundle's isolated path, a runtime that dies with
// the call (engine_dispatch.go).
//
// The request is the SDK's ConvertRequest, decoded as strictly as the plugin
// always decoded its own: exactly one of uris, raw and nodes. nodes are a
// typed-node plan's nodes after the core bound them. A document plan and a
// response chain are refused with a stated reason until the slices that run
// them (document plans with fleet-bound records in S2, response chains in
// S3).
//
// A document with no node for the client is refused with
// zeroNodesForTargetCode, the same rule the serve path applies.
func (rt *runtime) convertSubscription(payload json.RawMessage) (subscriptionConvertResult, error) {
	// The request bound is the SDK's convert bound, which core enforces when
	// it builds the request; the response bound was 2 MiB short of it.
	if len(payload) > model.MaxConvertRequestBytes {
		return subscriptionConvertResult{}, fmt.Errorf("convert payload exceeds %d bytes", model.MaxConvertRequestBytes)
	}
	req, err := model.DecodeConvertRequest(payload)
	if err != nil {
		return subscriptionConvertResult{}, fmt.Errorf("invalid convert payload: %w", err)
	}
	if req.Document != nil {
		return subscriptionConvertResult{}, fmt.Errorf("%s: a document plan is converted once fleet-bound records exist (S2); send uris, raw or nodes", convertUnsupportedCode)
	}
	if len(req.ResponseChain) > 0 {
		return subscriptionConvertResult{}, fmt.Errorf("%s: convert runs a response chain once scripts run in its isolate (S3); render applies a record's response chain", convertUnsupportedCode)
	}
	target := req.Target
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

	request := nodeConvertRequest{Target: target, Options: req.Options}
	if len(req.Nodes) > 0 {
		request.Nodes = make([]*nodemodel.Node, len(req.Nodes))
		for i, raw := range req.Nodes {
			request.Nodes[i] = &nodemodel.Node{}
			// The decoder's error names a type, never a value: the node
			// carries a credential.
			if err := request.Nodes[i].UnmarshalJSON(raw); err != nil {
				return subscriptionConvertResult{}, fmt.Errorf("convert node %d: %w", i, err)
			}
		}
	} else {
		raw, err := convertInput(req.URIs, req.Raw)
		if err != nil {
			return subscriptionConvertResult{}, err
		}
		request.Parts = []string{raw}
	}
	converted, _, err := rt.convertNodes(request)
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

// convertInput turns the request's text input into the text the engine
// parses. Errors never quote the input: it carries credentials.
func convertInput(uris []string, raw string) (string, error) {
	hasURIs, hasRaw := len(uris) > 0, strings.TrimSpace(raw) != ""
	if hasURIs == hasRaw {
		return "", errors.New("convert needs exactly one of uris, raw and nodes")
	}
	if hasRaw {
		return raw, nil
	}
	if len(uris) > maxExportLinks {
		return "", fmt.Errorf("convert accepts at most %d uris", maxExportLinks)
	}
	lines := make([]string, 0, len(uris))
	for i, uri := range uris {
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
