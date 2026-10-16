package main

import (
	"encoding/json"

	"github.com/LatticeNet/lattice-sdk/strictjson"
)

// decodeExactMembers is json.Unmarshal of raw into the struct whose fields
// targets names by their exact JSON names, for the plain case only: an
// object strictjson admits (no duplicate key at any depth) whose every
// member is one of targets, each decoding on its own. Then each member
// decodes as it would through the whole struct, because no member names a
// field twice, and the large ones (a snapshot's raw, its rows) are read once
// instead of once to validate, once to skip and once to copy. ok is false
// for anything else, and the caller runs json.Unmarshal, which decides and
// words the error; the targets may then hold partial values, which that
// decode overwrites from zero only if the caller reset them, so callers
// decode into a fresh value on fallback.
func decodeExactMembers(raw []byte, targets map[string]any) bool {
	fields, ok := strictjson.Fields(raw)
	if !ok {
		return false
	}
	for _, f := range fields {
		target, known := targets[f.Key]
		if !known {
			return false
		}
		switch t := target.(type) {
		case *json.RawMessage:
			*t = append(json.RawMessage(nil), f.Value...)
		default:
			if json.Unmarshal(f.Value, target) != nil {
				return false
			}
		}
	}
	return true
}
