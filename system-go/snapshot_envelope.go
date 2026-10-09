package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-sdk/model"
)

// The snapshot envelope (plan section 2.5) is what fetch hands the core as
// raw and what render gets back. It is bounded by MaxSubscriptionRawBytes and
// carries nothing volatile: the core skips a render when raw is byte for byte
// what it already holds, so a timestamp in here would re-render every share on
// every refresh.
//
// A version 1 snapshot (anything a runtime before the envelope stored: a
// provider body, a resolved node list, a template, or the members object of a
// collection) is read as it always was and never rewritten in place; the next
// fetch writes version 2.

const (
	snapshotEnvelopeVersion = 2
	// snapshotTooLargeCode leads the refusal of an envelope the core would
	// refuse anyway, so the reason reaches the operator instead of a bare
	// size error from the server.
	snapshotTooLargeCode = "snapshot_too_large"
	// Why an envelope carries no nodes. size: the nodes would have taken it
	// past the raw bound, so render parses the text. fallback_chain: a chain
	// runs on the bundle, which parses the text itself.
	nodesOmittedSize     = "size"
	nodesOmittedFallback = "fallback_chain"
)

type snapshotEnvelope struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	// Raw is the provider text, the joined export or the graph raw, verbatim,
	// for the bundle fallback and re-parsing.
	Raw       string `json:"raw,omitempty"`
	RawSHA256 string `json:"raw_sha256,omitempty"`
	// Nodes is the compact model after parse and normalise, before the chain,
	// filled when the record's chain is native. NodesOmitted says why it is
	// absent when it should not be ("size", "fallback_chain"); render then
	// parses Raw.
	Nodes        []json.RawMessage `json:"nodes,omitempty"`
	NodesOmitted string            `json:"nodes_omitted,omitempty"`
	// A collection and a script file carry their members.
	SourceID   string           `json:"source_id,omitempty"`
	SourceName string           `json:"source_name,omitempty"`
	SourceKind string           `json:"source_kind,omitempty"`
	Members    []envelopeMember `json:"members,omitempty"`
	// SourceVersion is the graph composition version (vpn-core-graph).
	SourceVersion string `json:"source_version,omitempty"`
}

type envelopeMember struct {
	SubName string            `json:"sub_name"`
	Raw     string            `json:"raw"`
	Nodes   []json.RawMessage `json:"nodes,omitempty"`
}

// textEnvelope wraps one text snapshot.
func textEnvelope(kind, raw, sourceVersion string) snapshotEnvelope {
	sum := sha256.Sum256([]byte(raw))
	return snapshotEnvelope{Version: snapshotEnvelopeVersion, Kind: kind, Raw: raw, RawSHA256: hex.EncodeToString(sum[:]), SourceVersion: sourceVersion}
}

// membersEnvelope wraps a collection's or a script file's members.
func membersEnvelope(kind string, members []fileScriptMember) snapshotEnvelope {
	env := snapshotEnvelope{Version: snapshotEnvelopeVersion, Kind: kind, Members: make([]envelopeMember, 0, len(members))}
	for _, member := range members {
		env.Members = append(env.Members, envelopeMember{SubName: member.SubName, Raw: member.Raw})
	}
	return env
}

// encodeSnapshotEnvelope is the raw fetch returns. Nodes that would take it
// over the core's bound are left out with nodes_omitted "size"; an envelope
// still over the bound without them is refused here with a stated reason.
func encodeSnapshotEnvelope(env snapshotEnvelope) (string, error) {
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	if len(raw) > model.MaxSubscriptionRawBytes && envelopeHasNodes(env) {
		env.Nodes = nil
		env.Members = append([]envelopeMember(nil), env.Members...)
		for i := range env.Members {
			env.Members[i].Nodes = nil
		}
		env.NodesOmitted = nodesOmittedSize
		if raw, err = json.Marshal(env); err != nil {
			return "", err
		}
	}
	if len(raw) > model.MaxSubscriptionRawBytes {
		return "", fmt.Errorf("%s: the snapshot is %d bytes once enveloped, and the core keeps at most %d; the source returned more than one subscription can carry", snapshotTooLargeCode, len(raw), model.MaxSubscriptionRawBytes)
	}
	return string(raw), nil
}

func envelopeHasNodes(env snapshotEnvelope) bool {
	if len(env.Nodes) > 0 {
		return true
	}
	for _, member := range env.Members {
		if len(member.Nodes) > 0 {
			return true
		}
	}
	return false
}

// envelopeNodes is the nodes a subscription's version 2 envelope carries,
// decoded, or nil when it carries none (or any of them fails to decode), in
// which case render parses the text.
func envelopeNodes(env snapshotEnvelope) []*nodemodel.Node {
	if env.Kind != kindSub || len(env.Nodes) == 0 {
		return nil
	}
	nodes, ok := decodeNodes(env.Nodes)
	if !ok {
		return nil
	}
	return nodes
}

// decodeSnapshotEnvelope reports whether raw is a version 2 envelope. A JSON
// object counts only with version 2 and a kind this plugin writes, so a
// provider body that happens to be a JSON document with a version field is
// still read as the version 1 text it is.
func decodeSnapshotEnvelope(raw string) (snapshotEnvelope, bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return snapshotEnvelope{}, false
	}
	var env snapshotEnvelope
	if json.Unmarshal([]byte(trimmed), &env) != nil || env.Version != snapshotEnvelopeVersion {
		return snapshotEnvelope{}, false
	}
	switch env.Kind {
	case kindSub, kindCollection, kindFile:
		return env, true
	default:
		return snapshotEnvelope{}, false
	}
}

// snapshotText turns a snapshot of either version into the text the render
// paths read: a members envelope becomes the members object they already
// decode, a text envelope its raw, and a version 1 snapshot stays as it is.
func snapshotText(raw string) string {
	env, ok := decodeSnapshotEnvelope(raw)
	if !ok {
		return raw
	}
	return envelopeText(env)
}

// envelopeText is snapshotText for an envelope already decoded.
func envelopeText(env snapshotEnvelope) string {
	if len(env.Members) == 0 {
		return env.Raw
	}
	members := make([]fileScriptMember, 0, len(env.Members))
	for _, member := range env.Members {
		members = append(members, fileScriptMember{SubName: member.SubName, Raw: member.Raw})
	}
	text, err := json.Marshal(snapshotArtifacts{SourceID: env.SourceID, SourceName: env.SourceName, SourceKind: env.SourceKind, Members: members})
	if err != nil {
		// Unreachable for strings; an empty snapshot renders live.
		return ""
	}
	return string(text)
}
