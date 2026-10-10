package operators

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
