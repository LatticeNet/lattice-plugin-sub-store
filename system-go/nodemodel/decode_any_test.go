package nodemodel

import (
	"encoding/json"
	"reflect"
	"testing"
)

func sameAsUnmarshalIntoMap(t *testing.T, raw []byte) {
	t.Helper()
	got, ok := decodeObject(raw)
	if !ok {
		return
	}
	var want map[string]any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("decodeObject admits %q, which json.Unmarshal refuses: %v", raw, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decodeObject(%q) = %#v, json.Unmarshal %#v", raw, got, want)
	}
}

var decodeObjectCorpus = []string{
	`{}`, ` { "a" : [ 1 , 2.5 , -0 , 1e3 , {"b":null} ] , "c":"x\"yé😀", "d":true,"e":false } `,
	`{"a":1,"a":2}`, `{"a":"\ud800"}`, "{\"a\":\"\xff\"}", `{"a":1e999}`, `{"a":01}`, `{"a":1.}`, `{"a":"\x"}`, `{"a":}`, `[1]`,
	`{"a":1}x`, `{"a":[1,]}`, `{"a":{"b":}}`, `{"a":tru}`, `{"name":"Node 1","port":443,"reality-opts":{"public-key":"k","short-id":"0a"}}`,
}

func TestDecodeObjectIsJSONUnmarshal(t *testing.T) {
	for _, raw := range decodeObjectCorpus {
		sameAsUnmarshalIntoMap(t, []byte(raw))
	}
	if _, ok := decodeObject([]byte(decodeObjectCorpus[1])); !ok {
		t.Fatal("a plain node takes the slow path")
	}
}

func FuzzDecodeObjectIsJSONUnmarshal(f *testing.F) {
	for _, raw := range decodeObjectCorpus {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		sameAsUnmarshalIntoMap(t, raw)
	})
}
