package operators

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Step is one stored chain entry as the record carries it: the raw JSON is
// kept so fields this plugin does not interpret round-trip (customName, id).
type Step struct {
	Type       string
	CustomName string
	Disabled   bool
	Args       json.RawMessage
	Raw        json.RawMessage
}

// DecodeStep reads one stored chain entry.
func DecodeStep(raw json.RawMessage) (Step, error) {
	var wire struct {
		Type       string          `json:"type"`
		CustomName string          `json:"customName"`
		Disabled   bool            `json:"disabled"`
		Args       json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Step{}, fmt.Errorf("process step is not an object: %w", err)
	}
	return Step{Type: wire.Type, CustomName: wire.CustomName, Disabled: wire.Disabled, Args: wire.Args, Raw: raw}, nil
}

// compileFunc compiles one native operator's arguments. It returns nil, with
// a diagnostic recorded on c, when the step has to fall back.
type compileFunc func(c *stepCompiler, args json.RawMessage) stepFunc

// nativeOperators are the fourteen non-script operators (slices.md:53).
var nativeOperators = map[string]compileFunc{
	"Quick Setting Operator":                 compileQuickSetting,
	"Useless Filter":                         compileUselessFilter,
	"Region Filter":                          compileRegionFilter,
	"Type Filter":                            compileTypeFilter,
	"Regex Filter":                           compileRegexFilter,
	"Conditional Filter":                     compileConditionalFilter,
	"Remove Duplicate Filter":                compileRemoveDuplicateFilter,
	"Flag Operator":                          compileFlag,
	"Sort Operator":                          compileSort,
	"Regex Sort Operator":                    compileRegexSort,
	"Regex Rename Operator":                  compileRegexRename,
	"Regex Delete Operator":                  compileRegexDelete,
	"Handle Duplicate Operator":              compileHandleDuplicate,
	"Add Proxies From Subscription Operator": compileAddProxies,
}

// fallbackOperators always run on the bundle in S1: Resolve Domain needs the
// network (its Go resolver is S4) and the two script steps run operator
// JavaScript.
var fallbackOperators = map[string]bool{
	"Resolve Domain Operator": true,
	"Script Operator":         true,
	"Script Filter":           true,
}

// responseOperators run on the response stage, over the produced document.
var responseOperators = map[string]bool{
	"Response Transformer": true,
}

// Vocabulary is every step type a chain may hold, sorted. Package main's
// processVocabulary must be the same set; a test there holds them together.
func Vocabulary() []string {
	out := make([]string, 0, len(nativeOperators)+len(fallbackOperators)+len(responseOperators))
	for name := range nativeOperators {
		out = append(out, name)
	}
	for name := range fallbackOperators {
		out = append(out, name)
	}
	for name := range responseOperators {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// decodeArgs decodes a step's arguments into model values. present is false
// when the step carries no args at all, which upstream sees as undefined.
func decodeArgs(raw json.RawMessage) (value any, present bool, err error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false, nil
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, true, err
	}
	return value, true, nil
}

// argsObject decodes arguments that must be an object. Absent and null
// arguments read as an empty object, the way every object-shaped operator in
// the bundle reads them.
func argsObject(c *stepCompiler, raw json.RawMessage) (map[string]any, bool) {
	v, _, err := decodeArgs(raw)
	if err != nil {
		c.shape("arguments are not JSON: %v", err)
		return nil, false
	}
	switch x := v.(type) {
	case nil:
		return map[string]any{}, true
	case map[string]any:
		return x, true
	}
	c.shape("arguments must be an object")
	return nil, false
}

// stringList reads a list of texts. ok is false for anything else, a list
// holding something other than text included.
func stringList(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(list))
	for i, e := range list {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

// keepArg is a filter's keep argument: absent means keep, and anything else
// is read with ECMAScript truthiness (false, 0 and "" drop). A null keep is
// where the filters differ in the bundle: the Region and Type filters keep,
// the Regex Filter drops (a destructuring default does not replace null).
func keepArg(args map[string]any, nullKeeps bool) bool {
	v, ok := args["keep"]
	if !ok || (v == nil && nullKeeps) {
		return true
	}
	return truthy(v)
}
