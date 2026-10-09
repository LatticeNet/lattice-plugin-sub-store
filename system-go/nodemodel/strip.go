package nodemodel

import "strings"

// StripReport says what StripFromInput removed and found.
type StripReport struct {
	UnderscoreKeys int
	ExecShaped     bool
}

// StripFromInput removes what hostile content may not carry (normaliser.md
// section 4). Every key starting with "_" at any depth, objects inside lists
// included, is removed (H1). The report also says whether the node is
// exec-shaped (H3), so the caller can drop it when the external opt-in is
// off.
//
// It is for nodes built from Clash and mihomo objects only. Parser
// annotations (normaliser.md section 6) are created by the URI and line
// parsers, which never call this, and H1 runs before N1, so the annotations
// the normaliser itself reads are never affected.
func StripFromInput(n *Node) StripReport {
	report := StripReport{}
	if n == nil {
		return report
	}
	report.UnderscoreKeys = stripUnderscore(n.Fields)
	report.ExecShaped = IsExecShaped(n)
	return report
}

func stripUnderscore(v any) int {
	removed := 0
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if strings.HasPrefix(k, "_") {
				delete(x, k)
				removed++
				continue
			}
			removed += stripUnderscore(e)
		}
	case []any:
		for _, e := range x {
			removed += stripUnderscore(e)
		}
	}
	return removed
}

// execKeys are the top-level keys that make a node launch a local process or
// listen locally, compared case-insensitively (H3).
var execKeys = map[string]bool{
	"exec":          true,
	"args":          true,
	"local-port":    true,
	"local-address": true,
	"local_address": true,
}

// IsExecShaped reports H3's test: the node's type is "external" in any letter
// case, or it has a top-level key named, case-insensitively, exec, args,
// local-port, local-address or local_address. URI parsers that copy unknown
// query keys (TUIC, for one) can produce such a key, so the normaliser applies
// this to every node, not only to object input.
func IsExecShaped(n *Node) bool {
	if n == nil {
		return false
	}
	if t, ok := n.Fields["type"].(string); ok && strings.EqualFold(t, "external") {
		return true
	}
	for k := range n.Fields {
		if execKeys[strings.ToLower(k)] {
			return true
		}
	}
	return false
}
