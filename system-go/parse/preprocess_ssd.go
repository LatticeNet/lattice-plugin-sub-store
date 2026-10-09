package parse

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// preprocessSSD is row 4 of parser.md section 2 (details in 2.4): an
// "ssd://" document is expanded to ss:// lines. A body that is not a JSON
// object with a servers list makes the row fail.
func preprocessSSD(text string) (string, bool, string, error) {
	if !strings.HasPrefix(text, "ssd://") {
		return "", false, "", nil
	}
	v, err := decodeJSON(Base64DecodeLenient(text[len("ssd://"):]))
	if err != nil || v == nil {
		return "", false, "", nil
	}
	top, _ := v.(map[string]any)
	servers, ok := top["servers"].([]any)
	if !ok {
		return "", false, "", nil
	}
	// The cipher, password and port of each server fall back to the values
	// used for the previous server, starting from the top-level defaults.
	cipher, hasCipher := top["encryption"]
	password, hasPassword := top["password"]
	port, hasPort := top["port"]
	var b strings.Builder
	for i, s := range servers {
		if s == nil {
			return "", false, "", nil // reading a property of null throws
		}
		srv, _ := s.(map[string]any)
		if v, ok := srv["encryption"]; truthy(v, ok) {
			cipher, hasCipher = v, true
		}
		if v, ok := srv["password"]; truthy(v, ok) {
			password, hasPassword = v, true
		}
		if v, ok := srv["port"]; truthy(v, ok) {
			port, hasPort = v, true
		}
		host, hasHost := srv["server"]
		userInfo := jsString(cipher, hasCipher) + ":" + jsString(password, hasPassword)
		pluginPart := ""
		if opts, ok := srv["plugin_options"]; truthy(opts, ok) {
			plugin, hasPlugin := srv["plugin"]
			pluginPart = "/?plugin=" + encodeURIComponent(jsString(plugin, hasPlugin)+";"+jsString(opts, true))
		}
		tag := strconv.Itoa(i)
		if r, ok := srv["remarks"]; truthy(r, ok) {
			tag = jsString(r, true)
		}
		hostText, portText := jsString(host, hasHost), jsString(port, hasPort)
		size := len("ss://") + base64.StdEncoding.EncodedLen(len(userInfo)) + 1 + len(hostText) + 1 + len(portText) + len(pluginPart) + 1 + len(tag) + 1
		if b.Len()+size > MaxExpandedBytes {
			return "", false, "", ErrExpansionTooLarge
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("ss://")
		b.WriteString(base64.StdEncoding.EncodeToString([]byte(userInfo)))
		b.WriteByte('@')
		b.WriteString(hostText)
		b.WriteByte(':')
		b.WriteString(portText)
		b.WriteString(pluginPart)
		b.WriteByte('#')
		b.WriteString(tag)
	}
	return b.String(), true, "", nil
}
