package perfgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

// FleetMix returns n node-model objects cycled from the conformance harness's
// synthetic fleet shapes: the parse goldens of its corpus/fleet cases under
// root, the vendored conformance directory, taken in file name order and node
// order. Where Nodes is VLESS Reality only, this is the fleet's protocol mix
// (VLESS over TCP, gRPC, XHTTP and ws, trojan, vmess, Hysteria2 with port
// hopping, TUIC, AnyTLS, Shadowsocks and SOCKS5), which a target without
// VLESS, such as Surge, needs to have anything to write. Past the first
// cycle, a node's name gets the cycle number appended. The input changes when
// the vendored fleet goldens do.
func FleetMix(root string, n int) ([]json.RawMessage, error) {
	files, err := filepath.Glob(filepath.Join(root, "goldens", "parse", "fleet-*.json"))
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	var shapes []map[string]any
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var nodes []map[string]any
		if err := json.Unmarshal(b, &nodes); err != nil {
			return nil, fmt.Errorf("perfgen: %s: %w", file, err)
		}
		shapes = append(shapes, nodes...)
	}
	if len(shapes) == 0 {
		return nil, fmt.Errorf("perfgen: no fleet parse goldens under %s", root)
	}
	out := make([]json.RawMessage, 0, max(n, 0))
	for i := range max(n, 0) {
		node := shapes[i%len(shapes)]
		if cycle := i / len(shapes); cycle > 0 {
			c := make(map[string]any, len(node))
			for k, v := range node {
				c[k] = v
			}
			c["name"] = fmt.Sprint(node["name"]) + " " + strconv.Itoa(cycle)
			node = c
		}
		b, err := json.Marshal(node)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}
