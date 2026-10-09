package normalise

import (
	"errors"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// The hostile-content stage of normaliser.md section 4. Hostile content is
// any text Lattice parses: remote provider bodies, pasted local content,
// collection members, files used as node sources and text a script hands to
// the parse helper. The stage has one switch, allowExternal, true only for a
// local record whose operator set the explicit external opt-in.
//
//   - H1 removes underscore keys from object input. It runs before N1, in the
//     Clash object parser, through nodemodel.StripFromInput.
//   - H2 forbids file reads. It lives inside N33 (certificateFingerprint),
//     which reads only ca-str and ca_str and never a path a _ca names.
//   - H3 drops exec-shaped nodes after N35 unless allowExternal is set.
//   - H4 applies the size bounds of section 5 after H3.
//
// A dropped node is reported by position by the caller, never by content.

// ReasonExecShaped is the drop reason H3 gives.
const ReasonExecShaped = "exec_shaped"

// hostile applies H3 and H4 to a node that has passed N1 to N35. A bounds
// violation gives the reason "bound:<rule>:<path>", which names the field and
// never its value.
func hostile(n *nodemodel.Node, allowExternal bool) (drop bool, reason string) {
	if !allowExternal && nodemodel.IsExecShaped(n) {
		return true, ReasonExecShaped
	}
	if err := nodemodel.Bounds(n); err != nil {
		var be *nodemodel.BoundError
		if errors.As(err, &be) {
			return true, "bound:" + be.Rule + ":" + be.Path
		}
		return true, "bound:" + err.Error()
	}
	return false, ""
}
