package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// maxProviderResponseBytes bounds what one provider fetch may return. A provider
// that streams without end would otherwise be able to exhaust this process and
// then the snapshot the core stores.
const maxProviderResponseBytes = 8 << 20

// defaultProviderUA is sent when a subscription names no user agent. Providers
// commonly vary their output by client, so an absent UA is a real choice rather
// than a missing value - this one asks for the plain URI list.
const defaultProviderUA = "Lattice/1.0"

type fetchResult struct {
	Raw      string `json:"raw"`
	Userinfo string `json:"userinfo,omitempty"`
}

// fetchSubscription retrieves the record's current content.
//
// Every record kind resolves to its variable content here, because the core
// stores the answer as the snapshot a share renders from: without it a share
// of a collection or a file failed at the snapshot step before render ever
// ran. What each kind stores:
//
//   - sub: the provider body, the vpn-core export, or the pasted content.
//   - collection: a snapshotArtifacts envelope — every member's nodes after
//     its own chain, with the name a later stage filters on. Merging members
//     into one blob here would lose that name and the per-member chains at
//     render would have nothing to apply to.
//   - config file: its node source resolved the same way (chained, merged).
//   - script file: the same envelope as its node source — scripts read
//     per-member provenance, so the members travel whole.
//   - plain file / anything without a node source: the stored template, which
//     is constant until edited — edits invalidate the render cache directly.
func (rt *runtime) fetchSubscription(subscriptionID string) (fetchResult, error) {
	rec, err := rt.getSubscription(subscriptionID)
	if err != nil {
		return fetchResult{}, err
	}
	switch recordKind(rec) {
	case kindCollection:
		return rt.fetchCollectionSnapshot(rec)
	case kindFile:
		return rt.fetchFileSnapshot(rec)
	default:
		return rt.fetchRecordContent(rec)
	}
}

// snapshotArtifacts is the serialized variable content of a collection or a
// script-sourced file. The plugin writes it in fetch and reads it in render —
// the two never disagree, because both ends are this plugin.
type snapshotArtifacts struct {
	SourceID   string             `json:"source_id,omitempty"`
	SourceName string             `json:"source_name,omitempty"`
	SourceKind string             `json:"source_kind,omitempty"`
	Members    []fileScriptMember `json:"members"`
}

// fetchCollectionSnapshot resolves a collection's members to their chained
// node text. Member content moves at provider cadence; resolving it at refresh
// time is what lets a render skip the network entirely.
func (rt *runtime) fetchCollectionSnapshot(rec subscriptionRecord) (fetchResult, error) {
	members, err := rt.collectionMembers(rec)
	if err != nil {
		return fetchResult{}, err
	}
	out, err := rt.chainMembers(rec, members)
	if err != nil {
		return fetchResult{}, err
	}
	envelope, err := json.Marshal(snapshotArtifacts{Members: out})
	if err != nil {
		return fetchResult{}, err
	}
	return fetchResult{Raw: string(envelope)}, nil
}

// chainMembers renders each member through its own chain, honoring the
// collection's failure mode. Shared by the collection snapshot and the live
// render paths so the two can never drift apart.
func (rt *runtime) chainMembers(rec subscriptionRecord, members []subscriptionRecord) ([]fileScriptMember, error) {
	out := make([]fileScriptMember, 0, len(members))
	skipped := make([]string, 0)
	for _, member := range members {
		raw, err := rt.renderMemberNodes(member)
		if err != nil {
			// Strict is the default because serving only the survivors reaches a
			// client as "those nodes were removed", and the client acts on that
			// by deleting them. Skipping is available because one dead provider
			// should not take down a large collection — but it is a choice the
			// operator makes, not one made for them.
			if rec.FailureMode != failureModeSkip {
				return nil, fmt.Errorf("collection %q: %w", rec.ID, err)
			}
			skipped = append(skipped, member.ID)
			continue
		}
		if strings.TrimSpace(raw) != "" {
			out = append(out, fileScriptMember{SubName: memberSubName(member), Raw: raw})
		}
	}
	// Every member failing is not "skip the failures" — it is a collection with
	// nothing in it, and that must never be served as a success.
	if len(out) == 0 && len(members) > 0 {
		return nil, fmt.Errorf("collection %q: every member failed (%s)", rec.ID, strings.Join(skipped, ", "))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("collection %q produced no nodes", rec.ID)
	}
	return out, nil
}

// fetchFileSnapshot resolves what a file's render varies by: its node source,
// or nothing at all (the template is the answer until someone edits it).
func (rt *runtime) fetchFileSnapshot(rec subscriptionRecord) (fetchResult, error) {
	source := strings.TrimSpace(rec.NodeSource)
	if source == "" {
		// A file without a node source is static until edited; the template is
		// the honest content, and an edit changes its hash.
		return fetchResult{Raw: rec.Content}, nil
	}
	sourceRecord, err := rt.getSubscription(source)
	if err != nil {
		return fetchResult{}, fmt.Errorf("file %q: %w", rec.ID, err)
	}
	if recordKind(sourceRecord) == kindFile {
		return fetchResult{}, fmt.Errorf("file %q names another file as its node source", rec.ID)
	}
	if fileType(rec) == fileTypeConfig {
		nodes, err := rt.resolveNodesFor(sourceRecord)
		if err != nil {
			return fetchResult{}, fmt.Errorf("file %q: %w", rec.ID, err)
		}
		return fetchResult{Raw: nodes}, nil
	}
	// A script file reads per-member provenance, so its snapshot carries the
	// members whole rather than a merged blob.
	var members []fileScriptMember
	if recordKind(sourceRecord) == kindCollection {
		gathered, err := rt.collectionMembers(sourceRecord)
		if err != nil {
			return fetchResult{}, fmt.Errorf("file %q: %w", rec.ID, err)
		}
		members, err = rt.chainMembers(sourceRecord, gathered)
		if err != nil {
			return fetchResult{}, fmt.Errorf("file %q: %w", rec.ID, err)
		}
	} else {
		raw, err := rt.renderMemberNodes(sourceRecord)
		if err != nil {
			return fetchResult{}, fmt.Errorf("file %q: %w", rec.ID, err)
		}
		members = []fileScriptMember{{SubName: memberSubName(sourceRecord), Raw: raw}}
	}
	envelope, err := json.Marshal(snapshotArtifacts{
		SourceID:   sourceRecord.ID,
		SourceName: sourceRecord.Name,
		SourceKind: recordKind(sourceRecord),
		Members:    members,
	})
	if err != nil {
		return fetchResult{}, err
	}
	return fetchResult{Raw: string(envelope)}, nil
}

// fetchRecordContent resolves where a record's current content lives and reads
// it: the live vpn-core export over rpc:call, pasted text, or a provider URL
// behind guarded egress. It backs both the refresh path (a stored record) and
// the preview path (an unsaved draft — the engine otherwise never sees the
// draft's source, and a fleet-sourced preview would report "no content" while
// the nodes are right there).
func (rt *runtime) fetchRecordContent(rec subscriptionRecord) (fetchResult, error) {
	label := rec.ID
	if label == "" {
		label = "the unsaved draft"
	}
	// A vpn-core subscription has no provider to reach: its content is the
	// current node export, read over rpc:call. It is handled before the URL
	// checks because there is no URL involved and none should be required.
	if rec.Source == subscriptionSourceVPNCore {
		links, err := rt.fetchExport(subStoreRequest{UserID: rec.VPNIdentity})
		if err != nil {
			return fetchResult{}, err
		}
		if len(links) == 0 {
			// Serving nothing would reach a client as "you have no nodes" and
			// wipe its configuration, so an empty export is an error here
			// rather than empty content passed downstream.
			return fetchResult{}, fmt.Errorf("subscription %s: vpn-core returned no nodes", label)
		}
		return fetchResult{Raw: strings.Join(links, "\n")}, nil
	}

	// A manual subscription has nothing to fetch; its content is what was
	// pasted. Returning it here means "refresh" is harmless rather than an
	// error the operator has to learn to ignore.
	if rec.Source == subscriptionSourceLocal {
		if strings.TrimSpace(rec.Content) == "" {
			return fetchResult{}, fmt.Errorf("subscription %q has no pasted content", label)
		}
		return fetchResult{Raw: rec.Content}, nil
	}

	target := strings.TrimSpace(rec.URL)
	if target == "" {
		return fetchResult{}, fmt.Errorf("subscription %q has no URL to fetch", label)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return fetchResult{}, fmt.Errorf("subscription %q has an unparseable URL", label)
	}
	// The scheme is checked here as well as by the broker. A provider URL that is
	// not http(s) is a configuration mistake worth naming at its source, rather
	// than a broker rejection the operator has to trace back.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fetchResult{}, fmt.Errorf("subscription %q URL must be http or https", label)
	}

	ua := strings.TrimSpace(rec.UA)
	if ua == "" {
		ua = defaultProviderUA
	}
	raw, err := rt.callHost(latticeplugin.HostMethodHTTPDo, map[string]any{
		"method": "GET",
		"url":    target,
		"header": map[string]string{"User-Agent": ua},
	})
	if err != nil {
		return fetchResult{}, redactProviderError(label, err)
	}
	var out struct {
		StatusCode int               `json:"status_code"`
		Header     map[string]string `json:"header,omitempty"`
		BodyBase64 string            `json:"body_base64,omitempty"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fetchResult{}, fmt.Errorf("decode provider response: %w", err)
	}
	if out.StatusCode < 200 || out.StatusCode >= 300 {
		return fetchResult{}, fmt.Errorf("subscription %q provider returned status %d", label, out.StatusCode)
	}
	body, err := base64.StdEncoding.DecodeString(out.BodyBase64)
	if err != nil {
		return fetchResult{}, fmt.Errorf("decode provider body: %w", err)
	}
	if len(body) == 0 {
		// An empty body is a failed fetch, not a subscription with no nodes.
		// Returning it as success would overwrite a good snapshot with nothing.
		return fetchResult{}, fmt.Errorf("subscription %q provider returned an empty body", label)
	}
	if len(body) > maxProviderResponseBytes {
		return fetchResult{}, fmt.Errorf("subscription %q provider returned %d bytes, limit %d", label, len(body), maxProviderResponseBytes)
	}

	return fetchResult{Raw: string(body), Userinfo: userinfoHeader(out.Header)}, nil
}

// userinfoHeader finds the provider's traffic figures. Header names are matched
// case-insensitively because providers are inconsistent about the casing and the
// value is what a client displays as its remaining quota.
func userinfoHeader(header map[string]string) string {
	for name, value := range header {
		if strings.EqualFold(name, "subscription-userinfo") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// redactProviderError keeps a failing URL out of the error text. A provider URL
// is frequently a bearer credential in path form, and this error travels into
// the core's audit log and the operator's screen.
func redactProviderError(label string, err error) error {
	return fmt.Errorf("subscription %q provider request failed: %s", label, redactURLs(err.Error()))
}

func redactURLs(text string) string {
	var out []string
	for _, field := range strings.Fields(text) {
		if strings.Contains(field, "://") {
			out = append(out, "<redacted-url>")
			continue
		}
		out = append(out, field)
	}
	return strings.Join(out, " ")
}
