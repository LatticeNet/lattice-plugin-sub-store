package operators

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
)

// StructuredSortArgs is "Structured Sort Operator": a stable sort by one
// Lattice key, unknown values last.
type StructuredSortArgs struct {
	Key   string `json:"key"`   // probe_cold_p50_ms, renewal_days, country, region, service_state, chain_role, node_name
	Order string `json:"order"` // "asc" (default) or "desc"
}

// Sort orders.
const (
	OrderAsc  = "asc"
	OrderDesc = "desc"
)

// sortKeys are the keys a Structured Sort may order by. Usage is not one:
// rows carry usage only when a request names an identity, which a fleet
// fetch never does (S2 plan section 12, decision 12).
var sortKeys = map[string]bool{
	"probe_cold_p50_ms": true,
	"renewal_days":      true,
	"country":           true,
	"region":            true,
	"service_state":     true,
	"chain_role":        true,
	"node_name":         true,
}

// StructuredSortKeys is every key a Structured Sort may order by, sorted.
func StructuredSortKeys() []string {
	out := make([]string, 0, len(sortKeys))
	for k := range sortKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ParseStructuredSort decodes and checks a Structured Sort step's
// arguments, filling the default order (asc).
func ParseStructuredSort(raw json.RawMessage) (StructuredSortArgs, error) {
	var args StructuredSortArgs
	if err := decodeStrict(raw, &args); err != nil {
		return StructuredSortArgs{}, err
	}
	if args.Order == "" {
		args.Order = OrderAsc
	}
	switch {
	case !sortKeys[args.Key]:
		return StructuredSortArgs{}, fmt.Errorf("key must be one of %s, not %q", strings.Join(StructuredSortKeys(), ", "), args.Key)
	case args.Order != OrderAsc && args.Order != OrderDesc:
		return StructuredSortArgs{}, fmt.Errorf(`order must be "asc" or "desc", not %q`, args.Order)
	}
	return args, nil
}

// sortValue is one node's sort key: a number (cold p50 in milliseconds,
// renewal as Unix nanoseconds) or a case-folded text. ok is false when the
// value is unknown: a provider node, a null block, an unresolved relay's
// geo, an empty text, or the catalogue's zero for "unknown".
type sortValue struct {
	num  int64
	text string
	ok   bool
}

// structuredSortValue reads key from a node. country and region read the
// exit geo, as a Structured Filter's default does.
func structuredSortValue(n *nodemodel.Node, key string) sortValue {
	if n == nil || n.Lattice == nil {
		return sortValue{}
	}
	l := n.Lattice
	text := func(s string) sortValue {
		if s == "" {
			return sortValue{}
		}
		return sortValue{text: strings.ToLower(s), ok: true}
	}
	switch key {
	case "probe_cold_p50_ms":
		if l.Probe == nil || l.Probe.ColdP50MS <= 0 {
			return sortValue{}
		}
		return sortValue{num: int64(l.Probe.ColdP50MS), ok: true}
	case "renewal_days":
		if l.Machine == nil || l.Machine.NextRenewal.IsZero() {
			return sortValue{}
		}
		return sortValue{num: l.Machine.NextRenewal.UnixNano(), ok: true}
	case "country", "region":
		g, ok := effectiveGeo(l, GeoExit)
		if !ok {
			return sortValue{}
		}
		if key == "country" {
			return text(g.Country)
		}
		return text(g.Region)
	case "service_state":
		return text(l.ServiceState)
	case "chain_role":
		if l.Chain == nil {
			return sortValue{}
		}
		return text(l.Chain.Role)
	case "node_name":
		return text(l.NodeName)
	}
	return sortValue{}
}

// compileStructuredSort orders nodes by one Lattice key, stable, with
// unknown values after every known one in either order. Numbers compare as
// numbers (renewal by instant, so a nearer renewal sorts first in asc);
// texts compare lowercased, and equal keys keep their input order.
func compileStructuredSort(c *stepCompiler, raw json.RawMessage) stepFunc {
	args, err := ParseStructuredSort(raw)
	if err != nil {
		return c.structured(err)
	}
	desc := args.Order == OrderDesc
	numeric := args.Key == "probe_cold_p50_ms" || args.Key == "renewal_days"
	return func(nodes []*nodemodel.Node, _ *Context) []*nodemodel.Node {
		keys := make(map[*nodemodel.Node]sortValue, len(nodes))
		for _, n := range nodes {
			keys[n] = structuredSortValue(n, args.Key)
		}
		slices.SortStableFunc(nodes, func(a, b *nodemodel.Node) int {
			ka, kb := keys[a], keys[b]
			switch {
			case !ka.ok || !kb.ok:
				return boolOrder(!ka.ok) - boolOrder(!kb.ok)
			case numeric:
				c := cmp.Compare(ka.num, kb.num)
				if desc {
					return -c
				}
				return c
			}
			c := strings.Compare(ka.text, kb.text)
			if desc {
				return -c
			}
			return c
		})
		return nodes
	}
}

func boolOrder(b bool) int {
	if b {
		return 1
	}
	return 0
}
