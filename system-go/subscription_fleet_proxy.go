package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

// fleetPreviewCall serves fleet_preview: the editor's counts per structured
// step over the live catalogue, a sample of rows, and the row lookup the
// detail drawer pages through (plan section 2.7).
func (rt *runtime) fleetPreviewCall(payload json.RawMessage) response {
	return latticeplugin.ErrorResponse(errors.New("fleet_preview_unavailable: this build declares fleet_preview and answers it once the structured steps land"))
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
