package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// The plugin's KV seam. The plugin never used the SDK HostClient for KV: every
// call goes through the runtime's host seam so a test host counts it like any
// other host call, and the wire shape is the SDK's own (kv.get, kv.put and
// kv.delete with value_base64).
//
// S2 adds the compare-and-swap the S1 plan deferred (plan sections 2.5 and
// 2.7): kv.put takes an optional if_match, the sha256 of the value the caller
// last read, and the host refuses with kv_conflict when the stored value's
// digest differs, or when if_match is empty and the key exists (a create).
// kv.get answers the stored value's digest beside it from a124 on. An older
// host ignores if_match and answers no digest, which is why the plugin's
// compatibility floor is a124; kvGetWithDigest computes the digest itself
// when the host sends none, so the same code runs against both.
//
// Every index write goes through kvPutIfMatch (store_v2.go putIndex), so no
// index write is ever blind again.

// kvConflictCode is the code the host's kv.put refusal carries and the code a
// write method answers after its one retry was refused too.
const kvConflictCode = "kv_conflict"

// errKVConflict is a refused compare-and-swap. isKVConflict also recognises
// the host's own error text, which is how the refusal crosses the host seam.
var errKVConflict = errors.New(kvConflictCode + ": the stored value moved since it was read")

// isKVConflict reports whether err is a refused compare-and-swap.
func isKVConflict(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errKVConflict) || strings.Contains(err.Error(), kvConflictCode)
}

// kvDigest is the digest kv.put's if_match compares and kv.get answers: the
// lowercase hex sha256 of the stored bytes.
func kvDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func (rt *runtime) kvPut(key string, value []byte) error {
	_, err := rt.callHost(latticeplugin.HostMethodKVPut, map[string]any{
		"key":          key,
		"value_base64": base64.StdEncoding.EncodeToString(value),
	})
	return err
}

// kvPutIfMatch writes value under key only when the stored value's digest is
// ifMatch; an empty ifMatch writes only when the key does not exist. A refusal
// comes back as an error isKVConflict recognises, wrapping errKVConflict.
func (rt *runtime) kvPutIfMatch(key string, value []byte, ifMatch string) error {
	_, err := rt.callHost(latticeplugin.HostMethodKVPut, map[string]any{
		"key":          key,
		"value_base64": base64.StdEncoding.EncodeToString(value),
		"if_match":     ifMatch,
	})
	if err != nil && isKVConflict(err) && !errors.Is(err, errKVConflict) {
		return fmt.Errorf("%w (%s)", errKVConflict, key)
	}
	return err
}

// kvDelete removes one key. It speaks the SDK's kv.delete exactly as
// HostClient.KVDelete does, through the runtime's host seam so a test host
// counts it like every other call. Deleting a missing key is not an error.
func (rt *runtime) kvDelete(key string) error {
	_, err := rt.callHost(latticeplugin.HostMethodKVDelete, map[string]any{"key": key})
	return err
}

func (rt *runtime) kvGet(key string) ([]byte, bool, error) {
	value, _, found, err := rt.kvGetWithDigest(key)
	return value, found, err
}

// kvGetWithDigest is kv.get returning the stored value's digest beside it, so
// the caller passes it straight back as kvPutIfMatch's ifMatch. A missing key
// answers found false and an empty digest, which kvPutIfMatch reads as "create".
func (rt *runtime) kvGetWithDigest(key string) ([]byte, string, bool, error) {
	raw, err := rt.callHost(latticeplugin.HostMethodKVGet, map[string]any{"key": key})
	if err != nil {
		return nil, "", false, err
	}
	var out struct {
		OK          bool   `json:"ok"`
		Value       string `json:"value,omitempty"`
		ValueBase64 string `json:"value_base64,omitempty"`
		SHA256      string `json:"sha256,omitempty"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", false, err
	}
	if !out.OK {
		return nil, "", false, nil
	}
	value := []byte(out.Value)
	if out.ValueBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(out.ValueBase64)
		if err != nil {
			return nil, "", false, err
		}
		value = decoded
	}
	digest := strings.ToLower(strings.TrimSpace(out.SHA256))
	if digest == "" {
		digest = kvDigest(value)
	}
	return value, digest, true, nil
}
