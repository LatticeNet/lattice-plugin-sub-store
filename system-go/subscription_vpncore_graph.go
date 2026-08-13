package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"

	"github.com/LatticeNet/lattice-sdk/model"
	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

const vpnCoreGraphService = "latticenet.vpn-core/subscription-sources"

var vpnCoreGraphUUIDv4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type vpnCoreGraphComposeRequest struct {
	SchemaVersion int      `json:"schema_version"`
	IdentityID    string   `json:"identity_id"`
	EntryRoots    []string `json:"entry_roots"`
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

func validateVPNCoreGraphConfig(identityID string, roots []string) error {
	if identityID == "" || identityID != strings.TrimSpace(identityID) {
		return errors.New("vpn-core-graph requires an enabled identity")
	}
	if roots == nil || len(roots) == 0 || len(roots) > model.MaxSubscriptionSourceRoots {
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
		if entry == "" || len(entry) > model.MaxSubscriptionURIBytes || manifest.Entries[i].Root != request.EntryRoots[i] {
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
