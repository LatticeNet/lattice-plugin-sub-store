package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// keyRecordingHost records every key the plugin reads.
type keyRecordingHost struct {
	*kvHostCaller
	gets  []string
	total int
}

func (h *keyRecordingHost) call(method string, params any) (json.RawMessage, error) {
	h.total++
	if method == "kv.get" {
		var p struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(mustJSON(params), &p)
		h.gets = append(h.gets, p.Key)
	}
	return h.kvHostCaller.call(method, params)
}

// list is the management view's one read: the index, whatever the store
// holds, with each row's refresh bookkeeping riding in it.
func TestListReadsOneKey(t *testing.T) {
	host := &keyRecordingHost{kvHostCaller: newKVHostCaller()}
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	for _, rec := range []subscriptionRecord{
		{ID: "a", Name: "a", Content: "x", Remark: strings.Repeat("r", 400)},
		{ID: "b", Name: "b", Kind: kindFile, FileType: fileTypeScript, Content: `$content = "x";`},
		{ID: "c", Name: "c", Kind: kindCollection, Members: []string{"a"}},
	} {
		if err := rt.saveSubscription(rec); err != nil {
			t.Fatal(err)
		}
	}
	rt.noteFetchOutcome("a", fetchOutcome{at: fixedTime(), userinfo: "upload=1; download=2; total=3"})
	if err := rt.deleteSubscription("c"); err != nil {
		t.Fatal(err)
	}
	host.total, host.gets = 0, nil
	var listed struct {
		Subscriptions []listView `json:"subscriptions"`
		Archived      []listView `json:"archived"`
		StoreVersion  int        `json:"store_version"`
		Order         string     `json:"order"`
	}
	decodeResult(t, callSubscription(t, rt, "list", map[string]any{}), &listed)
	if host.total != 1 || len(host.gets) != 1 || host.gets[0] != storeIndexKey {
		t.Fatalf("list made %d host calls reading %v, want one read of %s", host.total, host.gets, storeIndexKey)
	}
	if listed.StoreVersion != storeVersionSplit || listed.Order != storeOrderManual || len(listed.Subscriptions) != 2 {
		t.Fatalf("list = %+v", listed)
	}
	a := listed.Subscriptions[0]
	if a.LastFetchAt == "" || a.Userinfo == "" || !a.UserinfoParsed || a.Revision == "" || len(a.Remark) != maxIndexRemarkBytes {
		t.Fatalf("row a = %+v", a)
	}
	if b := listed.Subscriptions[1]; b.HasInline || b.Order != 1 {
		t.Fatalf("row b = %+v: a script's program is not inline content, and order follows creation", b)
	}
	if len(listed.Archived) != 1 || listed.Archived[0].ID != "c" || listed.Archived[0].ArchivedAt == "" {
		t.Fatalf("archived = %+v", listed.Archived)
	}
}

// Delete archives and keeps the id; restore brings the record back under the
// same id with the same content and revision, and a collection that names it
// resolves again.
func TestDeleteArchivesAndRestoreBringsBackSameID(t *testing.T) {
	host := &budgetCountingHost{kvHostCaller: newKVHostCaller()}
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	if err := rt.saveSubscription(subscriptionRecord{ID: "home", Name: "home", Source: subscriptionSourceLocal, Content: scriptNodeHome, Tags: []string{"t"}}); err != nil {
		t.Fatal(err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "all", Name: "all", Kind: kindCollection, Members: []string{"home"}}); err != nil {
		t.Fatal(err)
	}
	before, err := rt.getSubscription("home")
	if err != nil {
		t.Fatal(err)
	}

	host.total = 0
	var deleted struct {
		Deleted  bool `json:"deleted"`
		Archived bool `json:"archived"`
	}
	decodeResult(t, callSubscription(t, rt, "delete", map[string]any{"subscription_id": "home"}), &deleted)
	if !deleted.Deleted || !deleted.Archived || host.total != 5 {
		t.Fatalf("delete = %+v in %d calls, want archived in 5", deleted, host.total)
	}
	if _, ok := host.values[recordKey("home")]; ok {
		t.Fatal("the record key outlived the delete")
	}
	if _, ok := host.values[archiveKey("home")]; !ok {
		t.Fatal("the delete wrote no archive")
	}
	if res := callSubscription(t, rt, "get", map[string]any{"subscription_id": "home"}); res.OK {
		t.Fatal("an archived record still reads as live")
	}
	if res := callSubscription(t, rt, "render", map[string]any{"subscription_id": "all", "format": "plain"}); res.OK || !strings.Contains(res.Error, "no longer exists") {
		t.Fatalf("a collection over an archived member rendered: %+v", res)
	}
	saved := saveRecord(t, rt, map[string]any{"subscription": map[string]any{"id": "home", "name": "squatter", "content": "x"}})
	if saved.Saved || saved.Conflict == nil || saved.Conflict.Reason != "archived" {
		t.Fatalf("a save over an archived id = %+v, want an archived conflict", saved)
	}

	host.total = 0
	var restored struct {
		Restored     bool               `json:"restored"`
		Subscription subscriptionRecord `json:"subscription"`
	}
	decodeResult(t, callSubscription(t, rt, "restore", map[string]any{"subscription_id": "home"}), &restored)
	if !restored.Restored || host.total != 5 {
		t.Fatalf("restore = %+v in %d calls, want 5", restored, host.total)
	}
	if restored.Subscription.ID != "home" || restored.Subscription.Content != before.Content || restored.Subscription.Revision != before.Revision {
		t.Fatalf("restore brought back %+v, want the record as it was", restored.Subscription)
	}
	if _, ok := host.values[archiveKey("home")]; ok {
		t.Fatal("the archive outlived the restore")
	}
	if got := host.deletes; len(got) != 2 || got[0] != recordKey("home") || got[1] != archiveKey("home") {
		t.Fatalf("kv.delete calls = %v, want the record key then the archive key", got)
	}
	if res := callSubscription(t, rt, "render", map[string]any{"subscription_id": "all", "format": "plain"}); !res.OK {
		t.Fatalf("the collection did not resolve its restored member: %s", res.Error)
	}
	if res := callSubscription(t, rt, "restore", map[string]any{"subscription_id": "home"}); res.OK {
		t.Fatal("restoring a live record was accepted")
	}
}

// Purge removes an archived record for good with kv.delete, frees its id,
// and refuses a live record.
func TestPurgeDeletesWithKVDelete(t *testing.T) {
	host := &budgetCountingHost{kvHostCaller: newKVHostCaller()}
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	if err := rt.saveSubscription(subscriptionRecord{ID: "old", Name: "old", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if res := callSubscription(t, rt, "purge", map[string]any{"subscription_id": "old"}); res.OK {
		t.Fatal("purge of a live record was accepted")
	}
	if err := rt.deleteSubscription("old"); err != nil {
		t.Fatal(err)
	}
	host.total, host.deletes = 0, nil
	if res := callSubscription(t, rt, "purge", map[string]any{"subscription_id": "old"}); !res.OK {
		t.Fatalf("purge: %s", res.Error)
	}
	if host.total != 3 || len(host.deletes) != 1 || host.deletes[0] != archiveKey("old") {
		t.Fatalf("purge made %d calls and deleted %v, want 3 calls and the archive key", host.total, host.deletes)
	}
	if _, ok := host.values[archiveKey("old")]; ok {
		t.Fatal("the archive outlived the purge")
	}
	listing, err := rt.storeListing()
	if err != nil || len(listing.Archived) != 0 || len(listing.Records) != 0 {
		t.Fatalf("after purge live=%v archived=%v err=%v", listing.Records, listing.Archived, err)
	}
	if err := rt.saveSubscription(subscriptionRecord{ID: "old", Name: "new life", Content: "y"}); err != nil {
		t.Fatalf("a purged id is not free again: %v", err)
	}
}

// A store that has not migrated refuses every write with the stable code and
// touches nothing, while reads and refresh bookkeeping keep working.
func TestWritesOnALegacyStoreAreRefusedWithCode(t *testing.T) {
	host := &httpKVHost{kvHostCaller: newKVHostCaller(), status: 200, body: []byte(scriptNodeHome)}
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	seedLegacyStore(t, host.kvHostCaller, []subscriptionRecord{
		{ID: "local", Name: "local", Source: subscriptionSourceLocal, Content: scriptNodeHome},
		{ID: "remote", Name: "remote", Source: subscriptionSourceRemote, URL: "https://provider.invalid/sub"},
	})
	legacy := append([]byte(nil), host.values[subscriptionRecordsKey]...)
	writes := map[string]map[string]any{
		"save":    {"subscription": map[string]any{"id": "new", "name": "new", "content": "x"}},
		"delete":  {"subscription_id": "local"},
		"restore": {"subscription_id": "local"},
		"purge":   {"subscription_id": "local"},
		"reorder": {"ids": []string{"remote", "local"}},
		"import":  {"backup": `{"format":"lattice.sub-store.subscriptions.v1","records":[{"id":"imp","name":"imp","content":"x"}]}`},
	}
	for method, payload := range writes {
		puts := host.puts
		res := callSubscription(t, rt, method, payload)
		if res.OK || !strings.HasPrefix(res.Error, storeMigrationRequiredCode+":") {
			t.Errorf("%s on a legacy store = %+v, want the %s refusal", method, res, storeMigrationRequiredCode)
		}
		if host.puts != puts {
			t.Errorf("%s wrote %d keys on a legacy store", method, host.puts-puts)
		}
	}
	if !bytes.Equal(host.values[subscriptionRecordsKey], legacy) {
		t.Fatal("a refused write changed the legacy document")
	}
	var listed struct {
		Subscriptions []listView `json:"subscriptions"`
		StoreVersion  int        `json:"store_version"`
	}
	decodeResult(t, callSubscription(t, rt, "list", map[string]any{}), &listed)
	if listed.StoreVersion != storeVersionLegacy || len(listed.Subscriptions) != 2 {
		t.Fatalf("list on a legacy store = %+v", listed)
	}
	if res := callSubscription(t, rt, "render", map[string]any{"subscription_id": "local", "format": "plain"}); !res.OK {
		t.Fatalf("render on a legacy store: %s", res.Error)
	}
	if res := fetchViaMethod(t, rt, "remote"); !res.OK {
		t.Fatalf("fetch on a legacy store: %s", res.Error)
	}
	var doc subscriptionRecordsDocument
	if err := json.Unmarshal(host.values[subscriptionRecordsKey], &doc); err != nil {
		t.Fatal(err)
	}
	for _, rec := range doc.Records {
		if rec.ID == "remote" && (rec.LastFetchAt == "" || !rec.LastFetchOK) {
			t.Fatalf("refresh bookkeeping did not land in the legacy document: %+v", rec)
		}
	}
	if _, ok := host.values[storeIndexKey]; ok {
		t.Fatal("a read or a refresh created the index on a legacy store")
	}
}

// The "pre" column of the plan's host-call table: what each method costs on a
// store that has not migrated. The legacy document is read once per
// invocation, so a record read costs its own key's miss and nothing more
// after the first, and every count fits the signed budget. The Settings read
// for a default user agent or target costs the same one call as on a split
// store.
func TestHostCallCountsOnALegacyStore(t *testing.T) {
	scenarios := []struct {
		name    string
		method  string
		payload map[string]any
		want    int
		ok      bool
	}{
		{name: "list", method: "list", payload: map[string]any{}, want: 2, ok: true},
		{name: "get a plain sub", method: "get", payload: map[string]any{"subscription_id": "local-a"}, want: 2, ok: true},
		{name: "get a script file", method: "get", payload: map[string]any{"subscription_id": "scripty"}, want: 3, ok: true},
		{name: "fetch a remote sub", method: "fetch", payload: map[string]any{"subscription_id": "remote-a"}, want: 6, ok: true},
		{name: "fetch a vpn-core sub", method: "fetch", payload: map[string]any{"subscription_id": "vpn-a"}, want: 5, ok: true},
		{name: "fetch a collection of remote subs", method: "fetch", payload: map[string]any{"subscription_id": "coll"}, want: 9, ok: true},
		{name: "fetch a script file over a remote collection", method: "fetch", payload: map[string]any{"subscription_id": "scripty"}, want: 11, ok: true},
		{name: "render a plain local sub", method: "render", payload: map[string]any{"subscription_id": "local-a", "format": "plain"}, want: 3, ok: true},
		{name: "render a collection from its snapshot", method: "render", payload: map[string]any{
			"subscription_id": "coll", "format": "plain",
			"raw": `{"members":[{"sub_name":"remote-a","raw":"` + "vless://11111111-1111-1111-1111-111111111111@a.example:443?security=reality&sni=a.com&fp=chrome&pbk=x#HK-01" + `"}]}`,
		}, want: 3, ok: true},
		{name: "render a collection of remote subs", method: "render", payload: map[string]any{"subscription_id": "coll", "format": "plain"}, want: 7, ok: true},
		{name: "preview a saved local sub", method: "preview", payload: map[string]any{"subscription_id": "local-a"}, want: 2, ok: true},
		{name: "export", method: "export", payload: map[string]any{}, want: 4, ok: true},
		{name: "depends_on", method: "depends_on", payload: map[string]any{}, want: 2, ok: true},
		{name: "save is refused", method: "save", payload: map[string]any{"subscription": map[string]any{"id": "n", "name": "n"}}, want: 2},
		{name: "delete is refused", method: "delete", payload: map[string]any{"subscription_id": "local-a"}, want: 2},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			host := &budgetCountingHost{kvHostCaller: newKVHostCaller()}
			rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
			seedLegacyStore(t, host.kvHostCaller, budgetStoreRecords())
			res := callSubscription(t, rt, scenario.method, scenario.payload)
			if res.OK != scenario.ok {
				t.Fatalf("%s ok=%v error=%s", scenario.method, res.OK, res.Error)
			}
			if host.total != scenario.want {
				t.Errorf("%s made %d host calls on a legacy store, pinned at %d", scenario.method, host.total, scenario.want)
			}
			if budget := ackedRuntimeBudgets()[pluginID+"/subscription/"+scenario.method].HostCalls; host.total > budget {
				t.Errorf("%s needs %d host calls on a legacy store, over the signed budget of %d", scenario.method, host.total, budget)
			}
		})
	}
}

// A refresh records the source node count it read on the index, and list
// shows it, so the record table has its "nodes in" column without a render.
func TestFetchRecordsNodesInOnTheIndex(t *testing.T) {
	rt, host := newFetchRuntime(t)
	host.body = []byte("ss://YWVzLTEyOC1nY206cHc@192.0.2.10:8388#one\nss://YWVzLTEyOC1nY206cHc@192.0.2.11:8388#two")
	if err := rt.saveSubscription(subscriptionRecord{ID: "s1", Name: "p", URL: "https://provider.invalid/sub"}); err != nil {
		t.Fatal(err)
	}
	if res := fetchViaMethod(t, rt, "s1"); !res.OK {
		t.Fatal(res.Error)
	}
	if entry := indexEntryOf(t, rt, "s1"); entry.NodesIn == nil || *entry.NodesIn != 2 {
		t.Fatalf("nodes_in = %v, want 2", entry.NodesIn)
	}
	var listed struct {
		Subscriptions []listView `json:"subscriptions"`
	}
	decodeResult(t, callSubscription(t, rt, "list", map[string]any{}), &listed)
	if row := listed.Subscriptions[0]; row.NodesIn == nil || *row.NodesIn != 2 {
		t.Fatalf("list nodes_in = %v, want 2", row.NodesIn)
	}
}
