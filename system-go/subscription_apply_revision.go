package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// apply_revision is the plugin's half of an approved Sub-Store plan (design
// 28, S2; plan section 2.7). Core claims the approval, takes the share-write
// lock and the plugin gate, and asks the plugin to make one record's staged
// revision live, or to delete a fleet-bound record. Core refuses the method
// outside its own apply and on every path but its own (lattice-server
// server_plugin_invoke.go, substore_svc_plans.go subStoreCoreOnlyMethod).
//
// The plugin claims the one-time grant first, over rpc.call to
// latticenet.sub-store/plans claim_apply, and writes nothing without
// {"granted": true}: a grant lives on core's call context and never crosses
// into the plugin, so the claim is the only proof that this call is core's
// apply of this approved plan.
//
// Its refusals are errors core reads from its own call (the gateway never
// relays this method), each led by its code: apply_not_granted,
// revision_unknown, expected_revision_mismatch, fleet_to_legacy_refused,
// fleet_file_unavailable, promotion_pending, store_inconsistent and
// kv_conflict.

const (
	applyNotGrantedCode          = "apply_not_granted"
	revisionUnknownCode          = "revision_unknown"
	expectedRevisionMismatchCode = "expected_revision_mismatch"
	applyActionPromote           = "promote"
	applyActionDelete            = "delete"
	subStorePlansService         = pluginID + "/plans"
)

// applyRevisionRequest is core's subStoreApplyRevisionRequest plus action.
type applyRevisionRequest struct {
	SubscriptionID   string `json:"subscription_id"`
	Revision         string `json:"revision"`
	ExpectedRevision string `json:"expected_revision"`
	ApprovalID       string `json:"approval_id"`
	PlanSHA256       string `json:"plan_sha256"`
	// Action is "promote" (the default) or "delete".
	Action string `json:"action,omitempty"`
}

// applyRevisionCall serves apply_revision.
func (rt *runtime) applyRevisionCall(payload json.RawMessage) response {
	reply, err := rt.applyRevision(payload)
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(mustJSON(reply), "")
}

// applyRevision is the whole method: the claim, then the promotion or the
// delete. Host calls for a promotion: the claim, the index, the record, the
// staged document, the index put (two more on a kv_conflict), the record
// put, the staged key's deletion and, for a promoted restore, the archive
// key's deletion. For a delete: the claim, the index, the record, the
// archive put, the index put and its retry, the record key's deletion and
// the staged key's.
func (rt *runtime) applyRevision(payload json.RawMessage) (map[string]string, error) {
	var req applyRevisionRequest
	if len(bytes.TrimSpace(payload)) == 0 || json.Unmarshal(payload, &req) != nil {
		return nil, errors.New("invalid apply_revision payload")
	}
	if strings.TrimSpace(req.SubscriptionID) == "" || strings.TrimSpace(req.ApprovalID) == "" {
		return nil, errors.New("invalid apply_revision payload: subscription_id and approval_id are required")
	}
	action := req.Action
	if action == "" {
		action = applyActionPromote
	}
	if action != applyActionPromote && action != applyActionDelete {
		return nil, fmt.Errorf("invalid apply_revision payload: action %q is neither promote nor delete", req.Action)
	}
	if err := rt.claimApply(payload); err != nil {
		return nil, err
	}
	if action == applyActionDelete {
		return rt.applyDelete(req)
	}
	return rt.applyPromote(req)
}

// claimApply asks core for the grant of this call, passing core's own request
// back exactly as it arrived so the claim compares what core sent.
func (rt *runtime) claimApply(payload json.RawMessage) error {
	raw, err := rt.rpcCall(subStorePlansService, "claim_apply", json.RawMessage(bytes.TrimSpace(payload)))
	if err != nil {
		return fmt.Errorf("%s: core granted no apply of this plan", applyNotGrantedCode)
	}
	var out struct {
		Granted bool `json:"granted"`
	}
	if json.Unmarshal(raw, &out) != nil || !out.Granted {
		return fmt.Errorf("%s: core granted no apply of this plan", applyNotGrantedCode)
	}
	return nil
}

// applyPromote makes the staged revision live.
func (rt *runtime) applyPromote(req applyRevisionRequest) (map[string]string, error) {
	idx, err := rt.writableIndex()
	if err != nil {
		return nil, err
	}
	pos := idx.position(req.SubscriptionID)
	if pos < 0 || !idx.Records[pos].hasStaged() || idx.Records[pos].StagedRevision != req.Revision || req.Revision == "" {
		return nil, fmt.Errorf("%s: subscription %q has no staged revision %s; the plan is stale", revisionUnknownCode, req.SubscriptionID, orNone(req.Revision))
	}
	loaded, err := rt.storeLoadRecord(idx, req.SubscriptionID, true)
	if err != nil {
		return nil, err
	}
	if r, refused := loaded.writeRefusal(); refused {
		return nil, refusalErr(r)
	}
	doc := loaded.pendingStaged()
	if doc == nil || doc.Record.Revision != req.Revision {
		return nil, fmt.Errorf("%s: subscription %q has no staged document at revision %s; the plan is stale", revisionUnknownCode, req.SubscriptionID, req.Revision)
	}
	live := ""
	if loaded.HasLive {
		live = loaded.LiveRevision
	}
	if live != req.ExpectedRevision || doc.BaseRevision != req.ExpectedRevision {
		return nil, fmt.Errorf("%s: subscription %q is live at %s and its staged revision was staged over %s, not %s", expectedRevisionMismatchCode, req.SubscriptionID, orNone(live), orNone(doc.BaseRevision), orNone(req.ExpectedRevision))
	}
	if r := promotionRefusal(idx, loaded.Entry, doc.Record); r != nil {
		return nil, refusalErr(*r)
	}
	if err := rt.storePromote(idx, doc); err != nil {
		return nil, err
	}
	return map[string]string{"subscription_id": req.SubscriptionID, "revision": doc.Record.Revision}, nil
}

// promotionRefusal runs the two guards apply_revision runs over the staged
// document, so a staged key an older binary wrote cannot promote either:
//
//   - fleet_to_legacy_refused, when the record is fleet-bound live or would
//     be after the promotion, with the gathered records resolved by their
//     live state, because what goes live is the live result (plan section
//     2.1);
//   - fleet_file_unavailable, when the promotion would make a file's node
//     source fleet-bound, over the union the staged index state gives.
//
// The mixing guard does not run here: a multi-record migration promotes its
// records one call at a time, so the collection is mixed by design between
// the first promotion and the last, and fetch and render refuse it meanwhile.
func promotionRefusal(idx *indexDocument, entry indexEntry, staged subscriptionRecord) *storeRefusal {
	facts := factsOfRecord(staged)
	live := newStoreGraph(idx.Records, graphLive).without(staged.ID)
	liveWith := live.with(facts)
	boundLive := entry.hasLiveRevision() && (entry.Flags.FleetBound || isFleetSource(entry.Source))
	boundAfter := facts.Kind != kindFile && liveWith.closure(seedFleet, false)[staged.ID]
	if boundLive || boundAfter {
		if isVPNCoreSource(staged.Source) {
			return &storeRefusal{
				Code: refusedFleetToLegacy, IDs: []string{staged.ID},
				Message: fmt.Sprintf("subscription %q is fleet-bound and its staged revision takes the legacy source %q", staged.ID, staged.Source),
			}
		}
		if facts.Kind == kindCollection {
			if legacy := sourcesGathered(liveWith, facts, seedLegacy); len(legacy) > 0 {
				return &storeRefusal{
					Code: refusedFleetToLegacy, IDs: append([]string{staged.ID}, legacy...),
					Message: fmt.Sprintf("collection %q would go live gathering the legacy records %s; promote their migrations first", staged.ID, strings.Join(legacy, ", ")),
				}
			}
		}
	}
	union := newStoreGraph(idx.Records, graphUnion).without(staged.ID)
	if own, ok := liveFactsOf(entry); ok {
		union.facts = append(union.facts, own)
	}
	ctx := writeContext{rec: staged, facts: facts, entry: entry, hasEntry: true, live: live, union: union, effective: union}
	return ctx.fileRefusal()
}

// applyDelete archives then deletes a fleet-bound record (a delete plan's
// apply). The archive copy goes first because it is idempotent and harmless
// on its own: a conflict exhaustion at the index put leaves an archive copy
// the next delete overwrites, with the live record and the index untouched.
func (rt *runtime) applyDelete(req applyRevisionRequest) (map[string]string, error) {
	idx, err := rt.writableIndex()
	if err != nil {
		return nil, err
	}
	pos := idx.position(req.SubscriptionID)
	if pos < 0 || !idx.Records[pos].hasLiveRevision() {
		return nil, fmt.Errorf("%s: subscription %q has no live revision to delete; the plan is stale", revisionUnknownCode, req.SubscriptionID)
	}
	loaded, err := rt.storeLoadRecord(idx, req.SubscriptionID, false)
	if err != nil {
		return nil, err
	}
	if r, refused := loaded.writeRefusal(); refused {
		return nil, refusalErr(r)
	}
	if loaded.LiveRevision != req.ExpectedRevision {
		return nil, fmt.Errorf("%s: subscription %q is live at %s, not %s", expectedRevisionMismatchCode, req.SubscriptionID, orNone(loaded.LiveRevision), orNone(req.ExpectedRevision))
	}
	if err := rt.storeArchive(idx, loaded); err != nil {
		return nil, err
	}
	return map[string]string{"subscription_id": req.SubscriptionID, "revision": loaded.LiveRevision}, nil
}

// storeArchive archives an opened live record: the archive put, the index put
// (the entry moved to Archived with its staged facts cleared; two more on a
// kv_conflict), the record key's deletion and, when a staged revision
// existed, the staged key's. The index lands before the record key goes, so
// a failure in between leaves an orphan record key the next restore
// overwrites, never an entry pointing at nothing.
func (rt *runtime) storeArchive(idx *indexDocument, loaded loadedRecord) error {
	id := loaded.ID
	archivedAt := time.Now().UTC().Format(time.RFC3339)
	archive, err := json.Marshal(archivedRecord{subscriptionRecord: stripBookkeeping(loaded.Live), ArchivedAt: archivedAt})
	if err != nil {
		return err
	}
	hadStaged := false
	mutate := func(target *indexDocument) error {
		at := target.position(id)
		if at < 0 {
			return fmt.Errorf("subscription %q was not found", id)
		}
		entry := target.Records[at]
		hadStaged = hadStaged || entry.hasStaged()
		entry.clearStaged()
		entry.ArchivedAt = archivedAt
		target.Records = append(target.Records[:at], target.Records[at+1:]...)
		target.Archived = append(target.Archived, entry)
		return nil
	}
	if err := mutate(idx); err != nil {
		return err
	}
	indexRaw, err := encodeIndex(idx, false)
	if err != nil {
		return err
	}
	if err := rt.kvPut(archiveKey(id), archive); err != nil {
		return err
	}
	if err := rt.putIndexRetrying(idx, indexRaw, false, mutate); err != nil {
		return err
	}
	if err := rt.kvDelete(recordKey(id)); err != nil {
		return err
	}
	if hadStaged {
		return rt.kvDelete(stagedKey(id))
	}
	return nil
}
