package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

// decodeExactMembers stands in for json.Unmarshal on render's request and
// on a snapshot envelope; where it decodes, it must decode the same value.
func sameAsUnmarshalForRequestAndEnvelope(t *testing.T, raw []byte) {
	t.Helper()
	var req renderCallRequest
	if decodeExactMembers(raw, map[string]any{
		"subscription_id": &req.SubscriptionID, "format": &req.Format, "ua_class": &req.UAClass, "ua_target": &req.UATarget,
		"target": &req.Target, "options": &req.Options, "raw": &req.Raw, "query": &req.Query, "explain": &req.Explain,
		"revision": &req.Revision, "member_revisions": &req.MemberRevisions,
	}) {
		var want renderCallRequest
		if err := json.Unmarshal(raw, &want); err != nil || !reflect.DeepEqual(req, want) {
			t.Fatalf("render request %q: fast %+v, json.Unmarshal %+v (%v)", raw, req, want, err)
		}
	}
	got, gotOK := decodeSnapshotEnvelope(string(raw))
	var want snapshotEnvelope
	wantOK := json.Unmarshal(raw, &want) == nil && want.Version == snapshotEnvelopeVersion &&
		(want.Kind == kindSub || want.Kind == kindCollection || want.Kind == kindFile)
	if gotOK != wantOK || gotOK && !reflect.DeepEqual(got, want) {
		t.Fatalf("envelope %q: %v %+v, json.Unmarshal %v %+v", raw, gotOK, got, wantOK, want)
	}
}

var strictMembersCorpus = []string{
	`{"subscription_id":"s","format":"plain","raw":"{\"version\":2}","options":{"a":true},"query":{"k":"v"},"member_revisions":{"m":""}}`,
	`{"subscription_id":"s","Format":"x"}`, `{"subscription_id":"s","extra":1}`, `{"subscription_id":1}`, `{"raw":null,"options":null}`,
	`{"version":2,"kind":"sub","source_kind":"fleet","catalogue_version":"lcv1-x","rows":[{"line_uuid":"a"}],"selector":{"leading":1,"step_hash":"h"}}`,
	`{"version":2,"kind":"sub","rows":null}`, `{"version":2,"kind":"collection","members":[{"sub_name":"a","raw":"x"}]}`,
	`{"version":2,"kind":"sub","nodes":[{"a":1},{"b":2}],"nodes_omitted":"size"}`, `{"version":1,"kind":"sub"}`, `{"version":2,"kind":"other"}`,
	`{"version":2,"kind":"sub","rows":[{"a":1,"a":2}]}`, `{"version":2,"kind":"sub","Rows":[]}`, ` {"version":2,"kind":"file"} `, `[]`, `x`,
}

func TestDecodeExactMembersIsJSONUnmarshal(t *testing.T) {
	for _, raw := range strictMembersCorpus {
		sameAsUnmarshalForRequestAndEnvelope(t, []byte(raw))
	}
}

func FuzzDecodeExactMembersIsJSONUnmarshal(f *testing.F) {
	for _, raw := range strictMembersCorpus {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		sameAsUnmarshalForRequestAndEnvelope(t, raw)
	})
}
