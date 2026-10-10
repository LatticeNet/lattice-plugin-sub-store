package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LatticeNet/lattice-sdk/model"
)

// replyFrameBytes is the size of the response frame that carries reply,
// rounded up the way hostCallFrameOverhead rounds a host_call frame.
func replyFrameBytes(t *testing.T, reply any) int {
	t.Helper()
	raw, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	return len(raw) + hostCallFrameOverhead
}

// Core counts every frame a plugin writes against the method's signed
// stdout_bytes, host calls included, and kills the call past it. A migration
// of script files with large programs must therefore split its chunks by
// bytes: each call stays inside the budget, and the store still verifies.
func TestMigrateStoreBoundsEachChunkByFrameBytes(t *testing.T) {
	host := &budgetCountingHost{kvHostCaller: newKVHostCaller()}
	rt := &runtime{host: host, engine: sharedWarmTestEngine(t)}
	program := `$content = "` + strings.Repeat("x", 1500<<10) + `";`
	var records []subscriptionRecord
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("big-%d", i)
		records = append(records, subscriptionRecord{ID: id, Name: "script " + id, Kind: kindFile, FileType: fileTypeScript, Content: program})
	}
	records = append(records, legacyFixture(20)...)
	seedLegacyStore(t, host.kvHostCaller, records)
	budget := ackedRuntimeBudgets()[pluginID+"/subscription/migrate_store"]

	migrated, calls := 0, 0
	var reply migrateStoreReply
	for calls < 20 {
		calls++
		host.total, host.frames = 0, 0
		reply = migrateStoreCall(t, rt, map[string]any{})
		out := host.frames + replyFrameBytes(t, reply)
		if out > budget.StdoutBytes || host.total > budget.HostCalls {
			t.Fatalf("call %d wrote %d bytes in %d host calls, over %d bytes and %d calls", calls, out, host.total, budget.StdoutBytes, budget.HostCalls)
		}
		migrated += reply.Migrated
		if reply.Verified {
			break
		}
	}
	if !reply.Verified || reply.StoreVersion != storeVersionSplit || migrated != len(records) {
		t.Fatalf("after %d calls: %+v, %d of %d records migrated", calls, reply, migrated, len(records))
	}
	// Six records of about 2 MiB of frames each cannot share one call.
	if calls < 4 {
		t.Fatalf("the migration took %d calls; a chunk ignored its byte bound", calls)
	}
	for _, rec := range records {
		got, err := rt.getSubscription(rec.ID)
		if err != nil || (isScriptFile(rec) && got.Content != rec.Content) {
			t.Fatalf("%s after the migration: %v", rec.ID, err)
		}
	}
}

// Every write method's signed stdout_bytes covers its largest frames, taken
// from the store's own bounds rather than from a fixture, so a budget cannot
// pass the tests and still be too small for a full store. The method list
// comes from the manifest: a write method nobody sized here fails the test.
func TestWriteBudgetsCoverTheirLargestFrames(t *testing.T) {
	budgets := ackedRuntimeBudgets()
	index := kvPutFrameBytes(storeIndexKey, make([]byte, maxIndexBytes))
	record := kvPutFrameBytes(recordKey("x"), make([]byte, maxRecordDocBytes))
	legacy := kvPutFrameBytes(subscriptionRecordsKey, make([]byte, maxSubscriptionDocBytes+maxLegacyDocSlackBytes))
	// A staged document is the record plus its base revision and time.
	staged := kvPutFrameBytes(stagedKey("x"), make([]byte, maxRecordDocBytes+512))
	settings := kvPutFrameBytes(settingsKey, make([]byte, maxSettingsBytes))
	small := hostCallFrameOverhead + 512
	reply := 64 << 10
	// import and migrate answer with a report naming each record and why it
	// was skipped.
	report := 128 << 10
	// get, save and restore answer with the record itself.
	withRecord := maxRecordDocBytes + reply
	// publish sends the body as base64 to a destination of at most
	// maxLinkBytes, beside the render's reads and the script requests.
	// A destination of maxLinkBytes can grow six times as escaped JSON.
	published := hostCallFrameOverhead + base64.StdEncoding.EncodedLen(maxPublishBytes) + 6*maxLinkBytes + 1<<10
	scripts := scriptHTTPMaxRequestBytes + scriptHTTPMaxCalls*hostCallFrameOverhead
	// migrate_record answers with up to MaxSubscriptionRecordNodes line uuids.
	uuids := model.MaxSubscriptionRecordNodes * 40
	// Every index write passes if_match and earns one retry (plan section
	// 2.7), so every write row carries a second index frame and the retry's
	// read.
	need := map[string]int{
		"subscription/save":    max(record, staged) + 2*index + 8*small + withRecord,
		"subscription/delete":  record + 2*index + 7*small + reply,
		"subscription/restore": max(record, staged) + 2*index + 6*small + withRecord,
		"subscription/purge":   2*index + 4*small + reply,
		"subscription/reorder": 2*index + 3*small + reply,
		// The larger of a chunk (its record frames, which stop before
		// migrateChunkFrameBytes unless one record is larger, the index and
		// the reads) and the verify call (the index twice and the legacy
		// document).
		"subscription/migrate_store": max(max(migrateChunkFrameBytes, record)+index+140*small, 2*index+legacy+8*small) + reply,
		// A batch refuses itself past maxBatchFrameBytes before writing; its
		// index retry adds one index frame beyond that bound.
		"subscription/import":        maxBatchFrameBytes + index + settings + 322*small + report,
		"subscription/migrate":       maxBatchFrameBytes + index + 327*small + report,
		"subscription/save_settings": settings + small + maxSettingsBytes,
		"subscription/publish":       published + scripts + 143*small + reply,
		// The record (or, for a delete, the archive) put, two index frames,
		// the claim and the reads.
		"subscription/apply_revision": record + 2*index + 9*small + reply,
		"subscription/migrate_record": staged + 2*index + 9*small + uuids + reply,
		"subscription/discard_staged": 2*index + 4*small + reply,
	}
	// Methods marked read that answer with a large reply are held the same way.
	// depends_on's v2 store-wide reply carries a row and the edges for every
	// entry of an index allowed to reach maxIndexBytes: about twice the
	// entries' bytes (plan section 2.7).
	for key, want := range map[string]int{
		"subscription/export":     maxExportReplyBytes + 320*small,
		"subscription/depends_on": 2*maxIndexBytes + reply,
	} {
		if got := budgets[pluginID+"/"+key].StdoutBytes; got < want {
			t.Errorf("%s signs %d stdout bytes; its largest reply needs %d", key, got, want)
		}
	}
	for _, iface := range loadManifestInterfaces(t) {
		// A core-backed method runs in lattice-server, which sizes its own
		// frames; only what this artifact writes is bounded here.
		if iface.Backing != "runtime" {
			continue
		}
		service := strings.TrimPrefix(iface.Service, pluginID+"/")
		for _, method := range iface.Methods {
			if method.Effect != "write" {
				continue
			}
			key := service + "/" + method.Name
			want, sized := need[key]
			if !sized {
				t.Errorf("%s writes, and nothing here bounds its frames; size it before signing", key)
				continue
			}
			delete(need, key)
			if got := budgets[pluginID+"/"+key].StdoutBytes; got < want {
				t.Errorf("%s signs %d stdout bytes; its largest frames need %d", key, got, want)
			}
		}
	}
	for key := range need {
		t.Errorf("%s is sized here but the manifest declares no such write method", key)
	}
}

// maxSettingsBytes is what the field limits in saveSettings allow a Settings
// document to encode to.
const maxSettingsBytes = 4 << 10

// The largest Settings document saveSettings accepts fits maxSettingsBytes,
// the bound the budget table above uses for it.
func TestSettingsFitTheirFrameBound(t *testing.T) {
	worst := pluginSettings{SchemaVersion: 1 << 30, DefaultTarget: strings.Repeat("<", 64), DefaultUA: strings.Repeat("<", 256)}
	raw, err := json.Marshal(worst)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > maxSettingsBytes {
		t.Fatalf("worst Settings encode to %d bytes, bound %d", len(raw), maxSettingsBytes)
	}
}

// A batch whose record frames cannot fit one call is refused before any write,
// so an import never lands half its records and then dies on the budget.
func TestImportRefusesABatchTooLargeToWriteBeforeWritingAny(t *testing.T) {
	rt, host := newKVRuntime(t)
	backup := subscriptionBackup{Format: subscriptionBackupFormat}
	for i := 0; i < 30; i++ {
		backup.Records = append(backup.Records, subscriptionRecord{
			ID: fmt.Sprintf("big-%02d", i), Name: "big", Kind: "sub", Source: "local",
			Content: strings.Repeat("vless://u@h:1#n\n", (maxSubscriptionInlineBytes-1024)/16),
		})
	}
	raw, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.importBackup(raw); err == nil || !strings.Contains(err.Error(), batchTooLargeCode) {
		t.Fatalf("an import past the frame bound was not refused: %v", err)
	}
	if host.puts != 0 {
		t.Fatalf("a refused import still wrote %d keys", host.puts)
	}
	// Half the batch fits, and lands whole.
	backup.Records = backup.Records[:10]
	if raw, err = json.Marshal(backup); err != nil {
		t.Fatal(err)
	}
	out, err := rt.importBackup(raw)
	if err != nil || len(out.Imported) != 10 {
		t.Fatalf("a batch inside the bound: %+v, %v", out, err)
	}
}

// An export too large for one reply is refused with a reason before core
// would kill it, and one inside the bound still goes out whole.
func TestExportRefusesABackupTooLargeForOneReply(t *testing.T) {
	rt, _ := newKVRuntime(t)
	content := strings.Repeat("vless://u@h:1?a=1&b=2#\"n\"\n", (maxSubscriptionInlineBytes-1024)/26)
	for i := 0; i < 30; i++ {
		if err := rt.saveSubscription(subscriptionRecord{ID: fmt.Sprintf("big-%02d", i), Name: "big", Kind: "sub", Source: "local", Content: content}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	res := callSubscription(t, rt, "export", map[string]any{})
	if res.OK || !strings.Contains(res.Error, exportTooLargeCode) {
		t.Fatalf("an export past the reply bound was not refused: ok=%v %s", res.OK, res.Error)
	}
	for i := 5; i < 30; i++ {
		if res := callSubscription(t, rt, "delete", map[string]any{"subscription_id": fmt.Sprintf("big-%02d", i)}); !res.OK {
			t.Fatalf("delete %d: %s", i, res.Error)
		}
	}
	res = callSubscription(t, rt, "export", map[string]any{})
	if !res.OK {
		t.Fatalf("an export inside the bound was refused: %s", res.Error)
	}
	if raw := mustJSON(res.Result); len(raw) > maxExportReplyBytes {
		t.Fatalf("export answered %d bytes, bound %d", len(raw), maxExportReplyBytes)
	}
}
