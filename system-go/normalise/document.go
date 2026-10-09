package normalise

import "github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"

// Note is what a document rule logged about one node: its 0-based position
// in the list Document received and the rule, never the node's content.
// Notes are not part of the conformance comparison (normaliser.md section
// 7); they let the parser turn a position into a line warning.
type Note struct {
	Index   int
	Rule    string // D1 or D2
	Message string
}

// Document applies D1 and D2 (section 3) to the whole list.
func Document(nodes []*nodemodel.Node) []*nodemodel.Node {
	out, _ := DocumentNotes(nodes)
	return out
}

// DocumentNotes is Document with what the rules logged. D1 removes a
// Hysteria2 node that still has a truthy obfs and no truthy obfs-password
// after normalisation (in practice salamander without a password). D2 keeps a
// VMess or VLESS node whose uuid is not text in the canonical 8-4-4-4-12
// hexadecimal form and notes it; design 28's pipeline text says such nodes
// are dropped, upstream keeps them, and the specification follows upstream.
// The zero-node refusal and the 4096-node ceiling (D3) belong to the record
// pipeline, not here.
func DocumentNotes(nodes []*nodemodel.Node) ([]*nodemodel.Node, []Note) {
	out := make([]*nodemodel.Node, 0, len(nodes))
	var notes []Note
	for i, n := range nodes {
		f := n.Fields
		switch f["type"] {
		case "hysteria2":
			if truthyKey(f, "obfs") && !truthyKey(f, "obfs-password") {
				notes = append(notes, Note{Index: i, Rule: "D1", Message: "hysteria2 obfs without a password; node removed"})
				continue
			}
		case "vmess", "vless":
			if u, ok := f["uuid"].(string); !ok || !canonicalUUID(u) {
				notes = append(notes, Note{Index: i, Rule: "D2", Message: "uuid is not in canonical form; node kept"})
			}
		}
		out = append(out, n)
	}
	return out, notes
}

// canonicalUUID reports the 8-4-4-4-12 hexadecimal form, in either letter
// case.
func canonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			if !isHex(s[i]) {
				return false
			}
		}
	}
	return true
}
