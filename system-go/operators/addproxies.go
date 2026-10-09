package operators

import (
	"encoding/json"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// compileAddProxies is inert: the step acts only on a file pipeline, which
// this plugin does not run yet, and the bundle leaves the node list alone
// too (ui/src/operatorSchema.ts). It stays native so parity holds and no host
// call enters the chain.
func compileAddProxies(c *stepCompiler, _ json.RawMessage) stepFunc {
	c.diags = append(c.diags, Diagnostic{Step: c.index, Code: CodeInertUntilFiles, Message: "Add Proxies From Subscription acts only on a file pipeline; it leaves this node list unchanged"})
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node { return nodes }
}
