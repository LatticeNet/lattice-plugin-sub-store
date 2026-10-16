package main

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/LatticeNet/lattice-sdk/model"
	"github.com/LatticeNet/lattice-sdk/strictjson"
)

// appendSelectionPlan appends plan exactly as json.Marshal writes it. A
// 1000-node plan is most of a megabyte of node objects the plugin's own
// writer produced; json.Marshal compacted every one of them again and grew
// its buffer by doubling, about a third of the plan computation. Here a node
// already in canonical form (compact, none of the bytes encoding/json
// escapes) is copied as it is and any other node is compacted and escaped
// the way encoding/json does it, so the bytes are the same either way;
// TestAppendSelectionPlanIsJSONMarshal holds them to json.Marshal. err is
// set only for a node that is not valid JSON, which json.Marshal refuses
// too.
func appendSelectionPlan(dst []byte, plan model.SelectionPlan) ([]byte, error) {
	canonical := make([]bool, len(plan.Nodes))
	for i, node := range plan.Nodes {
		canonical[i] = canonicalRawJSON(node.Node)
	}
	return appendSelectionPlanChecked(dst, plan, canonical)
}

// appendSelectionPlanChecked is appendSelectionPlan with each node's
// canonicalRawJSON already answered, so a caller can answer them in
// parallel.
func appendSelectionPlanChecked(dst []byte, plan model.SelectionPlan, canonical []bool) ([]byte, error) {
	dst = append(dst, `{"kind":`...)
	dst = appendCanonicalString(dst, plan.Kind)
	dst = append(dst, `,"nodes":`...)
	if plan.Nodes == nil {
		dst = append(dst, "null"...)
	} else {
		dst = append(dst, '[')
		for i, node := range plan.Nodes {
			if i > 0 {
				dst = append(dst, ',')
			}
			var err error
			if dst, err = appendSelectionPlanNode(dst, node, canonical[i]); err != nil {
				return nil, err
			}
		}
		dst = append(dst, ']')
	}
	if plan.Document != "" {
		dst = appendCanonicalString(append(dst, `,"document":`...), plan.Document)
	}
	if len(plan.ResponseChain) > 0 {
		raw, err := json.Marshal(plan.ResponseChain)
		if err != nil {
			return nil, err
		}
		dst = append(append(dst, `,"response_chain":`...), raw...)
	}
	if s := plan.Selection; s != nil {
		dst = append(dst, `,"selection":{"catalogue_version":`...)
		dst = appendCanonicalString(dst, s.CatalogueVersion)
		dst = append(dst, `,"line_uuids":`...)
		if s.LineUUIDs == nil {
			dst = append(dst, "null"...)
		} else {
			dst = append(dst, '[')
			for i, id := range s.LineUUIDs {
				if i > 0 {
					dst = append(dst, ',')
				}
				dst = appendCanonicalString(dst, id)
			}
			dst = append(dst, ']')
		}
		dst = append(dst, '}')
	}
	if plan.Policy != nil {
		raw, err := json.Marshal(plan.Policy)
		if err != nil {
			return nil, err
		}
		dst = append(append(dst, `,"policy":`...), raw...)
	}
	return append(dst, '}'), nil
}

func appendSelectionPlanNode(dst []byte, node model.SelectionPlanNode, canonical bool) ([]byte, error) {
	dst = append(dst, '{')
	comma := false
	if node.LineUUID != "" {
		dst = appendCanonicalString(append(dst, `"line_uuid":`...), node.LineUUID)
		comma = true
	}
	if len(node.Placeholders) > 0 {
		if comma {
			dst = append(dst, ',')
		}
		dst = append(dst, `"placeholders":{`...)
		keys := make([]string, 0, len(node.Placeholders))
		for k := range node.Placeholders {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendCanonicalString(dst, k)
			dst = appendCanonicalString(append(dst, ':'), node.Placeholders[k])
		}
		dst = append(dst, '}')
		comma = true
	}
	if node.Provider {
		if comma {
			dst = append(dst, ',')
		}
		dst = append(dst, `"provider":true`...)
		comma = true
	}
	if comma {
		dst = append(dst, ',')
	}
	dst = append(dst, `"node":`...)
	switch {
	case node.Node == nil:
		// A nil json.RawMessage marshals as null; an empty one is refused
		// below, as json.Marshal refuses it.
		dst = append(dst, "null"...)
	case canonical:
		dst = append(dst, node.Node...)
	default:
		var compacted bytes.Buffer
		if err := json.Compact(&compacted, node.Node); err != nil {
			return nil, err
		}
		var escaped bytes.Buffer
		json.HTMLEscape(&escaped, compacted.Bytes())
		dst = append(dst, escaped.Bytes()...)
	}
	return append(dst, '}'), nil
}

// canonicalRawJSON reports whether raw is already what encoding/json writes
// for a json.RawMessage holding it: compact, and free of the bytes it
// escapes ('<', '>', '&', U+2028, U+2029), none of which can occur outside a
// string.
func canonicalRawJSON(raw []byte) bool {
	return strictjson.Compact(raw) && !bytes.ContainsAny(raw, "<>&") &&
		!bytes.Contains(raw, []byte(" ")) && !bytes.Contains(raw, []byte(" "))
}

// appendCanonicalString appends s as json.Marshal writes a string. A string
// of printable ASCII with nothing encoding/json escapes (line uuids,
// placeholders, versions) is written as it is; any other goes through
// json.Marshal.
func appendCanonicalString(dst []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7e || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
			raw, _ := json.Marshal(s)
			return append(dst, raw...)
		}
	}
	dst = append(dst, '"')
	dst = append(dst, s...)
	return append(dst, '"')
}
