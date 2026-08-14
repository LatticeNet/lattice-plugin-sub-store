package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

const vpnCoreGraphService = "latticenet.vpn-core/subscription-sources"

var vpnCoreGraphUUIDv4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var vpnCoreGraphCredentialUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type vpnCoreGraphComposeRequest struct {
	SchemaVersion int      `json:"schema_version"`
	IdentityID    string   `json:"identity_id"`
	EntryRoots    []string `json:"entry_roots"`
}

type vpnCoreGraphSelection struct {
	SchemaVersion  int      `json:"schema_version"`
	OptionsVersion string   `json:"options_version"`
	IdentityID     string   `json:"identity_id"`
	EntryRoots     []string `json:"entry_roots"`
}

func (selection vpnCoreGraphSelection) record(id string) subscriptionRecord {
	return subscriptionRecord{ID: id, Source: subscriptionSourceVPNCoreGraph, VPNIdentity: selection.IdentityID,
		EntryRoots: append([]string(nil), selection.EntryRoots...), GraphOptionsVersion: selection.OptionsVersion}
}

func validateVPNCoreGraphSelectionShape(selection vpnCoreGraphSelection) error {
	if selection.SchemaVersion != 1 || !validVPNCoreGraphOptionsVersion(selection.OptionsVersion) {
		return errors.New("invalid vpn-core graph selection authority")
	}
	return validateVPNCoreGraphConfig(selection.IdentityID, selection.EntryRoots)
}

type vpnCoreGraphComposeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type vpnCoreGraphComposeResponse struct {
	SchemaVersion  int                       `json:"schema_version"`
	OK             bool                      `json:"ok"`
	SourceVersion  string                    `json:"source_version,omitempty"`
	SourceManifest json.RawMessage           `json:"source_manifest,omitempty"`
	Entries        []string                  `json:"entries,omitempty"`
	Raw            string                    `json:"raw,omitempty"`
	Error          *vpnCoreGraphComposeError `json:"error,omitempty"`
}

type vpnCoreGraphIdentityOption struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Selectable bool   `json:"selectable"`
}

type vpnCoreGraphRootOption struct {
	LineUUID            string   `json:"line_uuid"`
	Label               string   `json:"label"`
	SourceNode          string   `json:"source_node_id"`
	Source              string   `json:"source"`
	TargetLabel         string   `json:"target_label,omitempty"`
	Status              string   `json:"status"`
	PathSummary         string   `json:"path_summary"`
	Reason              string   `json:"reason,omitempty"`
	EligibleIdentityIDs []string `json:"eligible_identity_ids"`
	Selectable          bool     `json:"selectable"`
}

type vpnCoreGraphOptionsResponse struct {
	SchemaVersion  int                          `json:"schema_version"`
	OK             bool                         `json:"ok"`
	OptionsVersion string                       `json:"options_version"`
	Identities     []vpnCoreGraphIdentityOption `json:"identities"`
	Roots          []vpnCoreGraphRootOption     `json:"roots"`
	Error          *vpnCoreGraphComposeError    `json:"error,omitempty"`
}

func (response vpnCoreGraphOptionsResponse) clone() vpnCoreGraphOptionsResponse {
	response.Identities = append(make([]vpnCoreGraphIdentityOption, 0, len(response.Identities)), response.Identities...)
	response.Roots = append(make([]vpnCoreGraphRootOption, 0, len(response.Roots)), response.Roots...)
	for i := range response.Roots {
		response.Roots[i].EligibleIdentityIDs = append(make([]string, 0, len(response.Roots[i].EligibleIdentityIDs)), response.Roots[i].EligibleIdentityIDs...)
	}
	if response.Error != nil {
		cloned := *response.Error
		response.Error = &cloned
	}
	return response
}

func (rt *runtime) fetchVPNCoreGraphOptions() (vpnCoreGraphOptionsResponse, error) {
	raw, err := rt.callHost(latticeplugin.HostMethodRPCCall, map[string]any{
		"service": vpnCoreGraphService,
		"method":  "graph_options",
		"request": map[string]any{},
	})
	if err != nil {
		return vpnCoreGraphOptionsResponse{}, errors.New("vpn-core graph options failed")
	}
	if len(raw) == 0 || len(raw) > model.MaxSubscriptionResponseBytes {
		return vpnCoreGraphOptionsResponse{}, errors.New("invalid vpn-core graph options size")
	}
	var response vpnCoreGraphOptionsResponse
	if err := decodeStrictVPNCoreGraphJSON(raw, &response); err != nil || validateVPNCoreGraphOptions(response) != nil {
		return vpnCoreGraphOptionsResponse{}, errors.New("invalid vpn-core graph options")
	}
	return response.clone(), nil
}

func validateVPNCoreGraphOptions(response vpnCoreGraphOptionsResponse) error {
	if response.SchemaVersion != 1 || !response.OK || response.Error != nil || !validVPNCoreGraphOptionsVersion(response.OptionsVersion) || response.Identities == nil || response.Roots == nil {
		return errors.New("incomplete graph options response")
	}
	if len(response.Identities) > model.MaxSubscriptionSourceVisits || len(response.Roots) > model.MaxSubscriptionSourceRoots {
		return errors.New("graph options exceed bounds")
	}
	identitySeen := make(map[string]struct{}, len(response.Identities))
	lastIdentity := ""
	for _, option := range response.Identities {
		if !safeVPNCoreGraphOptionText(option.ID, true) || !safeVPNCoreGraphOptionText(option.Label, true) || !safeVPNCoreGraphOptionText(option.Status, true) || !safeVPNCoreGraphOptionText(option.Reason, false) || option.ID <= lastIdentity || (option.Selectable && option.Reason != "") || (!option.Selectable && option.Reason == "") {
			return errors.New("invalid graph identity option")
		}
		if _, exists := identitySeen[option.ID]; exists {
			return errors.New("duplicate graph identity option")
		}
		identitySeen[option.ID] = struct{}{}
		lastIdentity = option.ID
	}
	rootSeen := make(map[string]struct{}, len(response.Roots))
	lastRoot := ""
	for _, option := range response.Roots {
		if !vpnCoreGraphUUIDv4.MatchString(option.LineUUID) || option.LineUUID <= lastRoot || !safeVPNCoreGraphOptionText(option.Label, true) || !safeVPNCoreGraphOptionText(option.SourceNode, false) || !safeVPNCoreGraphOptionText(option.Source, false) || !safeVPNCoreGraphOptionText(option.TargetLabel, false) || !safeVPNCoreGraphOptionText(option.Status, true) || !safeVPNCoreGraphOptionText(option.PathSummary, true) || !safeVPNCoreGraphOptionText(option.Reason, false) || option.Selectable != (len(option.EligibleIdentityIDs) > 0) || (option.Selectable && option.Reason != "") || (!option.Selectable && option.Reason == "") {
			return errors.New("invalid graph root option")
		}
		lastEligibleIdentity := ""
		for _, identityID := range option.EligibleIdentityIDs {
			if !safeVPNCoreGraphOptionText(identityID, true) || identityID <= lastEligibleIdentity {
				return errors.New("invalid graph root identity authority")
			}
			if _, exists := identitySeen[identityID]; !exists {
				return errors.New("unknown graph root identity authority")
			}
			lastEligibleIdentity = identityID
		}
		if _, exists := rootSeen[option.LineUUID]; exists {
			return errors.New("duplicate graph root option")
		}
		rootSeen[option.LineUUID] = struct{}{}
		lastRoot = option.LineUUID
	}
	return nil
}

func safeVPNCoreGraphOptionText(value string, required bool) bool {
	if value != strings.TrimSpace(value) || len(value) > 128 || strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	if required && value == "" {
		return false
	}
	lower := strings.ToLower(value)
	return !strings.Contains(lower, "://") && !strings.Contains(lower, "private key") && !strings.Contains(lower, "token") && !strings.Contains(lower, "secret") && !strings.HasPrefix(lower, "lat$")
}

func validateVPNCoreGraphConfig(identityID string, roots []string) error {
	if identityID == "" || identityID != strings.TrimSpace(identityID) {
		return errors.New("vpn-core-graph requires an enabled identity")
	}
	if len(roots) == 0 || len(roots) > model.MaxSubscriptionSourceRoots {
		return errors.New("vpn-core-graph requires bounded ordered entry roots")
	}
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		if !vpnCoreGraphUUIDv4.MatchString(root) {
			return errors.New("vpn-core-graph entry roots must be canonical lowercase UUIDv4 values")
		}
		if _, exists := seen[root]; exists {
			return errors.New("vpn-core-graph entry roots must be unique")
		}
		seen[root] = struct{}{}
	}
	return nil
}

func validVPNCoreGraphOptionsVersion(value string) bool {
	if len(value) != len("ov1:")+64 || !strings.HasPrefix(value, "ov1:") {
		return false
	}
	for _, char := range value[len("ov1:"):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validateVPNCoreGraphSelection(rec subscriptionRecord, options vpnCoreGraphOptionsResponse) error {
	if rec.GraphOptionsVersion == "" || rec.GraphOptionsVersion != options.OptionsVersion {
		return errors.New("vpn-core graph options changed; reload before saving")
	}
	identitySelectable := false
	for _, option := range options.Identities {
		if option.ID == rec.VPNIdentity {
			identitySelectable = option.Selectable
			break
		}
	}
	if !identitySelectable {
		return errors.New("vpn-core graph identity is no longer eligible")
	}
	selectableRoots := make(map[string]bool, len(options.Roots))
	for _, option := range options.Roots {
		for _, identityID := range option.EligibleIdentityIDs {
			if identityID == rec.VPNIdentity {
				selectableRoots[option.LineUUID] = option.Selectable
			}
		}
	}
	for _, root := range rec.EntryRoots {
		if !selectableRoots[root] {
			return errors.New("vpn-core graph root is no longer eligible")
		}
	}
	return nil
}

func (rt *runtime) fetchVPNCoreGraph(rec subscriptionRecord) (vpnCoreGraphComposeResponse, error) {
	if err := validateVPNCoreGraphConfig(rec.VPNIdentity, rec.EntryRoots); err != nil {
		return vpnCoreGraphComposeResponse{}, fmt.Errorf("subscription %q: %w", rec.ID, err)
	}
	request := vpnCoreGraphComposeRequest{SchemaVersion: 1, IdentityID: rec.VPNIdentity, EntryRoots: append([]string(nil), rec.EntryRoots...)}
	raw, err := rt.callHost(latticeplugin.HostMethodRPCCall, map[string]any{
		"service": vpnCoreGraphService,
		"method":  "compose",
		"request": request,
	})
	if err != nil {
		return vpnCoreGraphComposeResponse{}, fmt.Errorf("subscription %q: vpn-core graph compose failed", rec.ID)
	}
	if len(raw) == 0 || len(raw) > model.MaxSubscriptionResponseBytes {
		return vpnCoreGraphComposeResponse{}, fmt.Errorf("subscription %q: invalid vpn-core graph response size", rec.ID)
	}
	var response vpnCoreGraphComposeResponse
	if err := decodeStrictVPNCoreGraphJSON(raw, &response); err != nil {
		return vpnCoreGraphComposeResponse{}, fmt.Errorf("subscription %q: invalid vpn-core graph response", rec.ID)
	}
	if err := validateVPNCoreGraphResponse(response, request); err != nil {
		return vpnCoreGraphComposeResponse{}, fmt.Errorf("subscription %q: invalid vpn-core graph response", rec.ID)
	}
	return response, nil
}

func (rt *runtime) previewVPNCoreGraph(selection vpnCoreGraphSelection) (vpnCoreGraphComposeResponse, error) {
	if err := validateVPNCoreGraphSelectionShape(selection); err != nil {
		return vpnCoreGraphComposeResponse{}, err
	}
	record := selection.record("preview")
	options, err := rt.fetchVPNCoreGraphOptions()
	if err != nil {
		return vpnCoreGraphComposeResponse{}, err
	}
	if err := validateVPNCoreGraphSelection(record, options); err != nil {
		return vpnCoreGraphComposeResponse{}, err
	}
	return rt.fetchVPNCoreGraph(record)
}

func validateVPNCoreGraphResponse(response vpnCoreGraphComposeResponse, request vpnCoreGraphComposeRequest) error {
	if response.SchemaVersion != 1 || !response.OK || response.Error != nil || response.SourceVersion == "" || len(response.SourceManifest) == 0 || len(response.Entries) == 0 || response.Raw == "" {
		return errors.New("compose response is not a complete success")
	}
	if len(response.SourceManifest) > model.MaxSubscriptionSourceManifestBytes || len(response.Raw) > model.MaxSubscriptionRawBytes || len(response.Entries) != len(request.EntryRoots) {
		return errors.New("compose response exceeds bounds")
	}
	manifest, err := model.DecodeSubscriptionSourceManifest(response.SourceManifest)
	if err != nil || response.SourceVersion != model.SubscriptionSourceVersion(response.SourceManifest) || manifest.Identity.ID != request.IdentityID || !reflect.DeepEqual(manifest.EntryRoots, request.EntryRoots) {
		return errors.New("compose response manifest binding mismatch")
	}
	total := 0
	for i, entry := range response.Entries {
		if len(entry) > model.MaxSubscriptionURIBytes || manifest.Entries[i].Root != request.EntryRoots[i] || !canonicalVPNCoreGraphEntry(entry, manifest.Entries[i].Endpoint) || vpnCoreGraphEntryCredentialIsRoot(entry, request.EntryRoots) {
			return errors.New("compose response entry mismatch")
		}
		total += len(entry)
		if i > 0 {
			total++
		}
		if total > model.MaxSubscriptionRawBytes {
			return errors.New("compose response raw exceeds bounds")
		}
	}
	if response.Raw != strings.Join(response.Entries, "\n") {
		return errors.New("compose response raw does not match entries")
	}
	return nil
}

func vpnCoreGraphEntryCredentialIsRoot(entry string, roots []string) bool {
	parsed, err := url.Parse(entry)
	if err != nil || parsed.User == nil {
		return true
	}
	credential := parsed.User.Username()
	for _, root := range roots {
		if credential == root {
			return true
		}
	}
	return false
}

func redactVPNCoreGraphPreviewEntries(entries, roots []string) (string, error) {
	reserved := make(map[string]struct{}, len(entries)+len(roots))
	for _, root := range roots {
		reserved[root] = struct{}{}
	}
	parsedEntries := make([]*url.URL, 0, len(entries))
	for _, entry := range entries {
		parsed, err := url.Parse(entry)
		if err != nil || parsed.User == nil || parsed.User.Username() == "" {
			return "", errors.New("invalid vpn-core graph preview authority")
		}
		reserved[parsed.User.Username()] = struct{}{}
		parsedEntries = append(parsedEntries, parsed)
	}
	redacted := make([]string, 0, len(parsedEntries))
	for i, parsed := range parsedEntries {
		sequence := i + 1
		var synthetic string
		for {
			synthetic = fmt.Sprintf("00000000-0000-4000-8000-%012x", sequence)
			if _, exists := reserved[synthetic]; !exists {
				break
			}
			sequence += len(parsedEntries) + 1
		}
		reserved[synthetic] = struct{}{}
		parsed.User = url.User(synthetic)
		redacted = append(redacted, parsed.String())
	}
	return strings.Join(redacted, "\n"), nil
}

func canonicalVPNCoreGraphEntry(entry string, endpoint model.SubscriptionSourceManifestEndpoint) bool {
	if entry == "" || strings.ContainsAny(entry, "\r\n") {
		return false
	}
	parsed, err := url.Parse(entry)
	if err != nil || parsed.Scheme != "vless" || parsed.User == nil || parsed.User.Username() == "" || parsed.User.Username() != strings.ToLower(parsed.User.Username()) || !vpnCoreGraphCredentialUUID.MatchString(parsed.User.Username()) || parsed.User.String() != parsed.User.Username() || parsed.Hostname() != endpoint.Host {
		return false
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port != endpoint.Port {
		return false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	expected := url.Values{
		"type":       []string{"tcp"},
		"encryption": []string{"none"},
		"security":   []string{"reality"},
		"flow":       []string{endpoint.Flow},
		"pbk":        []string{endpoint.PublicKey},
		"sid":        []string{endpoint.ShortID},
		"sni":        []string{endpoint.SNI},
		"fp":         []string{endpoint.Fingerprint},
	}
	if len(endpoint.ALPN) > 0 {
		expected.Set("alpn", strings.Join(endpoint.ALPN, ","))
	}
	if !reflect.DeepEqual(query, expected) || parsed.RawQuery != expected.Encode() {
		return false
	}
	expectedURI := "vless://" + parsed.User.Username() + "@" + net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)) + "?" + expected.Encode() + "#" + url.PathEscape(endpoint.Label)
	return entry == expectedURI
}

func decodeStrictVPNCoreGraphJSON(raw []byte, out any) error {
	if err := scanUniqueVPNCoreGraphJSON(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func scanUniqueVPNCoreGraphJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim == '{' {
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid JSON field")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate JSON field")
			}
			seen[key] = struct{}{}
			if err := scanUniqueVPNCoreGraphJSON(decoder); err != nil {
				return err
			}
		}
	} else if delim == '[' {
		for decoder.More() {
			if err := scanUniqueVPNCoreGraphJSON(decoder); err != nil {
				return err
			}
		}
	}
	_, err = decoder.Token()
	return err
}
