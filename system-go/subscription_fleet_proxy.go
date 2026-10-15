package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/operators"
	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// The runtime proxy methods the editor reads vpn-core through (plan sections
// 0 and 2.7). The dashboard's plugin bridge admits a call only for a service
// the calling plugin's own manifest declares, and a manifest declares
// interfaces only under its own id, so the sub-store UI cannot call vpn-core's
// catalogue or identity list directly. These methods relay them over rpc.call.
//
// Both are declared [substore:read, vpncore:read]: core exempts a plugin's
// rpc.call from the vpncore:read check it applies to an operator's direct
// catalogue read (lattice-server line_catalogue.go vpnCoreReadAllowed), and
// the gateway enforces every scope a method declares, so the scope is
// enforced here, at the proxy, or nowhere. Neither gets a script network.

const (
	vpnCoreLinesService      = "latticenet.vpn-core/lines"
	vpnCoreIdentitiesService = "latticenet.vpn-core/identities"
	// maxIdentitiesReplyBytes is what identities may relay inside its signed
	// 256 KiB stdout, less the response frame.
	maxIdentitiesReplyBytes = 256<<10 - 4<<10
)

// rpcCall calls another plugin's service method over the host's rpc.call.
func (rt *runtime) rpcCall(service, method string, request any) (json.RawMessage, error) {
	return rt.callHost(latticeplugin.HostMethodRPCCall, map[string]any{
		"service": service,
		"method":  method,
		"request": request,
	})
}

// identitiesCall relays latticenet.vpn-core/identities list verbatim
// ({identities: [{id, name, group, status, reason, bound_lines, expires_at}],
// count}), after re-screening its key names. One host call.
func (rt *runtime) identitiesCall(payload json.RawMessage) response {
	if trimmed := bytes.TrimSpace(payload); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("{}")) {
		var req struct{}
		if err := decodeStrictVPNCoreGraphJSON(payload, &req); err != nil {
			return latticeplugin.ErrorResponse(errors.New("identities takes an empty object"))
		}
	}
	raw, err := rt.rpcCall(vpnCoreIdentitiesService, "list", map[string]any{})
	if err != nil {
		return latticeplugin.ErrorResponse(errors.New("vpn-core identities failed"))
	}
	if len(raw) == 0 || len(raw) > maxIdentitiesReplyBytes {
		return latticeplugin.ErrorResponse(fmt.Errorf("vpn-core identities answered %d bytes; at most %d are relayed", len(raw), maxIdentitiesReplyBytes))
	}
	if err := screenCredentialKeys(raw); err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(append(json.RawMessage(nil), raw...), "")
}

const (
	// maxFleetPreviewLookup bounds fleet_preview's row lookup: a bind.preview
	// list can hold 4096 entries at about 1 KB per fleetRow against the 1 MiB
	// stdout budget, so the detail drawer pages in batches of this size.
	maxFleetPreviewLookup = 256
	// defaultFleetPreviewSample and maxFleetPreviewSample bound the sample.
	defaultFleetPreviewSample = 50
	maxFleetPreviewSample     = 200
	// lineUUIDsTooManyCode refuses a lookup past maxFleetPreviewLookup.
	lineUUIDsTooManyCode = "line_uuids_too_many"
	// maxFleetPreviewReplyBytes is what the reply may carry inside the signed
	// 1 MiB, less the response frame.
	maxFleetPreviewReplyBytes = 1<<20 - 8<<10
)

type fleetPreviewRequest struct {
	Process   []json.RawMessage `json:"process,omitempty"`
	Sample    *int              `json:"sample,omitempty"`
	LineUUIDs []string          `json:"line_uuids,omitempty"`
}

// fleetPreviewStep is one leading structured step's count. A pushed step ran
// in core together with the other pushed steps, so only the last pushed step
// reports the rows core answered, as its out.
type fleetPreviewStep struct {
	Index  int  `json:"index"`
	In     *int `json:"in,omitempty"`
	Out    int  `json:"out"`
	Pushed bool `json:"pushed"`
}

type fleetPreviewReply struct {
	CatalogueVersion       string                       `json:"catalogue_version"`
	SelectorFields         []string                     `json:"selector_fields"`
	Unavailable            []string                     `json:"unavailable,omitempty"`
	Pushed                 *model.LineCatalogueSelector `json:"pushed,omitempty"`
	Covered                int                          `json:"covered"`
	Steps                  []fleetPreviewStep           `json:"steps"`
	Count                  int                          `json:"count"`
	Sample                 []fleetRow                   `json:"sample,omitempty"`
	Rows                   []fleetRow                   `json:"rows,omitempty"`
	EstimatedEnvelopeBytes int                          `json:"estimated_envelope_bytes"`
}

// fleetPreviewCall serves fleet_preview (plan section 2.7). The process form
// {"process": [...], "sample": n} reads the catalogue under the selector the
// leading Structured Filter run pushes (ten pages at 10000 lines, doubled
// once for a restart on a version move), runs the rest of the run one step
// at a time, and answers the counts per step, the selection's size, a sample
// and the envelope size it would take. The lookup form {"line_uuids": [...]}
// answers those rows for the detail drawer, at most 256 per request. Rows
// are credential-free by the catalogue's contract; the reply's key names are
// screened again before it leaves.
func (rt *runtime) fleetPreviewCall(payload json.RawMessage) response {
	var req fleetPreviewRequest
	if err := decodeStrictVPNCoreGraphJSON(payload, &req); err != nil {
		return latticeplugin.ErrorResponse(errors.New("invalid fleet_preview payload"))
	}
	var reply fleetPreviewReply
	var err error
	switch {
	case len(req.LineUUIDs) > 0 && req.Process != nil:
		return latticeplugin.ErrorResponse(errors.New("invalid fleet_preview payload: process and line_uuids are two requests"))
	case len(req.LineUUIDs) > 0:
		reply, err = rt.fleetPreviewLookup(req.LineUUIDs)
	default:
		sample := defaultFleetPreviewSample
		if req.Sample != nil {
			sample = min(max(*req.Sample, 0), maxFleetPreviewSample)
		}
		reply, err = rt.fleetPreviewProcess(req.Process, sample)
	}
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	body, err := json.Marshal(reply)
	if err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	if len(body) > maxFleetPreviewReplyBytes {
		return latticeplugin.ErrorResponse(fmt.Errorf("fleet_preview would answer %d bytes, at most %d; ask for a smaller sample", len(body), maxFleetPreviewReplyBytes))
	}
	if err := screenCredentialKeys(body); err != nil {
		return latticeplugin.ErrorResponse(err)
	}
	return latticeplugin.RawResultResponse(body, "")
}

// fleetPreviewLookup answers the rows of up to maxFleetPreviewLookup lines.
func (rt *runtime) fleetPreviewLookup(lineUUIDs []string) (fleetPreviewReply, error) {
	if len(lineUUIDs) > maxFleetPreviewLookup {
		return fleetPreviewReply{}, fmt.Errorf("%s: fleet_preview looks up at most %d lines per request, not %d; page the list", lineUUIDsTooManyCode, maxFleetPreviewLookup, len(lineUUIDs))
	}
	read, err := rt.readFleetCatalogue(&model.LineCatalogueSelector{LineUUIDs: lineUUIDs}, nil, maxFleetPreviewLookup)
	if err != nil {
		return fleetPreviewReply{}, err
	}
	return fleetPreviewReply{
		CatalogueVersion: read.CatalogueVersion, SelectorFields: nonNilStrings(read.SelectorFields),
		Unavailable: read.Unavailable, Steps: []fleetPreviewStep{}, Count: len(read.Rows), Rows: read.Rows,
	}, nil
}

// fleetPreviewProcess counts the leading structured run of a chain over the
// live catalogue. It reads the catalogue once under the selector the run
// pushes against every field the SDK defines, then evaluates the rest of the
// run in Go one step at a time, so each step reports what it removed. A core
// that advertises fewer selector fields than the pushed selector names did
// not filter by them and answered a superset (core refuses a selector it
// cannot parse, and the read would have failed); the run is then evaluated
// whole in Go over that superset, which gives the same set, because the
// plugin's evaluator and core's matcher are one function (S2 plan section
// 2.4). Reading again under a narrower selector would double the pages and
// break the signed 22 host calls. Pushed steps run together in core, so they
// report one row: the last pushed step's index, the rows core answered as
// out, and no in, which no catalogue page states.
func (rt *runtime) fleetPreviewProcess(steps []json.RawMessage, sample int) (fleetPreviewReply, error) {
	if err := validateProcess(steps); err != nil {
		return fleetPreviewReply{}, err
	}
	plan, err := operators.Compile("", steps)
	if err != nil {
		return fleetPreviewReply{}, err
	}
	leading := operators.LeadingRun(plan)
	sel, covered := operators.Pushdown(plan, model.LineCatalogueSelectorFields())
	read, err := rt.readFleetCatalogue(sel, nil, 0)
	if err != nil {
		return fleetPreviewReply{}, err
	}
	if sel != nil && len(sel.UnsupportedFields(read.SelectorFields)) > 0 {
		sel, covered = nil, 0
	}
	reply := fleetPreviewReply{
		CatalogueVersion: read.CatalogueVersion, SelectorFields: nonNilStrings(read.SelectorFields),
		Unavailable: read.Unavailable, Pushed: sel, Covered: covered, Steps: []fleetPreviewStep{},
	}
	rows := read.Rows
	lastPushed := -1
	for i := 0; i < covered; i++ {
		if plan.Steps[i].Kind != operators.KindDisabled {
			lastPushed = i
		}
	}
	if lastPushed >= 0 {
		reply.Steps = append(reply.Steps, fleetPreviewStep{Index: lastPushed, Out: len(rows), Pushed: true})
	}
	now := fleetNow()
	for i := covered; i < leading; i++ {
		if plan.Steps[i].Kind == operators.KindDisabled {
			continue
		}
		in := len(rows)
		kept, err := runStructuredStep(steps[i], rows, now)
		if err != nil {
			return fleetPreviewReply{}, fmt.Errorf("step %d: %w", i+1, err)
		}
		rows = kept
		reply.Steps = append(reply.Steps, fleetPreviewStep{Index: i, In: &in, Out: len(rows)})
	}
	reply.Count = len(rows)
	if sample > 0 {
		reply.Sample = rows[:min(sample, len(rows))]
	}
	encoded, err := encodeFleetRows(rows)
	if err != nil {
		return fleetPreviewReply{}, err
	}
	// The envelope a fetch would write is these rows plus the version, the
	// selector and the framing, which a few hundred bytes cover.
	reply.EstimatedEnvelopeBytes = len(encoded) + fleetEnvelopeOverheadBytes
	return reply, nil
}

// fleetEnvelopeOverheadBytes is what a fleet envelope carries beside its rows.
const fleetEnvelopeOverheadBytes = 512

// runStructuredStep runs one Structured Filter step over rows as a fleet
// fetch evaluates it: each row becomes a node carrying the row's Lattice
// block, which is all a structured predicate reads, and the step reads the
// one instant the whole preview reads.
func runStructuredStep(step json.RawMessage, rows []fleetRow, now time.Time) ([]fleetRow, error) {
	plan, err := operators.Compile("", []json.RawMessage{step})
	if err != nil {
		return nil, err
	}
	nodes := make([]*nodemodel.Node, len(rows))
	byNode := make(map[*nodemodel.Node]int, len(rows))
	for i := range rows {
		nodes[i] = &nodemodel.Node{Fields: map[string]any{"name": rows[i].Label}, Lattice: fleetLattice(rows[i])}
		byNode[nodes[i]] = i
	}
	out := plan.Run(nodes, &operators.Context{Now: now})
	kept := make([]fleetRow, 0, len(out))
	for _, node := range out {
		if i, ok := byNode[node]; ok {
			kept = append(kept, rows[i])
		}
	}
	return kept, nil
}

func nonNilStrings(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// screenCredentialKeys refuses a relayed reply carrying any object key a
// proxy protocol keeps a credential under (the SDK's credentialName rule,
// without the bare "id" an identity row legitimately carries): uuid, psk,
// auth, a username, and anything naming a password, secret, token, private
// or pre-shared key, or credential. Case, hyphens and underscores are
// ignored. The catalogue and the identity list are credential-free by
// contract; this is the proxy holding them to it.
func screenCredentialKeys(raw json.RawMessage) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return errors.New("relayed reply is not JSON")
	}
	if key, found := credentialKeyIn(value); found {
		return fmt.Errorf("relayed reply carries the credential-named field %q; refused", key)
	}
	return nil
}

func credentialKeyIn(value any) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if credentialKeyName(key) {
				return key, true
			}
			if found, ok := credentialKeyIn(child); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range typed {
			if found, ok := credentialKeyIn(child); ok {
				return found, true
			}
		}
	}
	return "", false
}

func credentialKeyName(name string) bool {
	folded := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(name))
	switch folded {
	case "uuid", "auth", "authstr", "psk", "username", "pass":
		return true
	}
	for _, stem := range []string{"password", "passwd", "secret", "token", "privatekey", "presharedkey", "credential"} {
		if strings.Contains(folded, stem) {
			return true
		}
	}
	return false
}
