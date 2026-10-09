// Command substore-conformance answers the conformance harness's oracle
// protocol for the native engine, so the vendored checker can judge it:
//
//	node conformance/oracle/check.mjs --candidate "$PWD/substore-conformance" ...
//
// It reads one JSON request per stdin line and writes one JSON reply per
// stdout line, in order, echoing each request's id (lattice-substore-conformance
// oracle/run.mjs and its README, "The oracle protocol"). stdout carries replies
// and nothing else; diagnostics go to stderr. The command is a test harness: it
// is never part of the plugin artifact.
//
// version answers {implementation:"lattice-go", commit, version} from the
// build information. produce answers {ok:true, output} from the native
// producer of the target (system-go/producers), with the request's options,
// and a stated refusal for a target whose producer has not landed. parse
// answers a stated refusal until the native parser (system-go/parse) lands;
// plan section 2.6 says what it then returns.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/nodemodel"
	"github.com/LatticeNet/lattice-plugin-sub-store/system-go/producers"
)

type request struct {
	ID      json.RawMessage   `json:"id"`
	Op      string            `json:"op"`
	Input   string            `json:"input,omitempty"`
	Target  string            `json:"target,omitempty"`
	Nodes   []json.RawMessage `json:"nodes,omitempty"`
	Options map[string]any    `json:"options,omitempty"`
}

type reply struct {
	ID             json.RawMessage   `json:"id"`
	OK             bool              `json:"ok"`
	Error          string            `json:"error,omitempty"`
	Nodes          []json.RawMessage `json:"nodes,omitempty"`
	Output         *string           `json:"output,omitempty"`
	Implementation string            `json:"implementation,omitempty"`
	Commit         string            `json:"commit,omitempty"`
	Version        string            `json:"version,omitempty"`
}

// platforms maps the harness target ids S1 builds natively to the platform
// names the producers are keyed by, as the harness's oracle/lib/targets.mjs
// does. Every other target is answered as having no native producer.
var platforms = map[string]string{
	"uri":       "URI",
	"v2ray":     "V2Ray",
	"json":      "JSON",
	"singbox":   "sing-box",
	"clashmeta": "ClashMeta",
}

func main() {
	if err := serve(os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "substore-conformance: %v\n", err)
		os.Exit(1)
	}
}

// serve answers requests until in ends. It reads with a bufio.Reader, which
// has no line limit, so a request larger than bufio.Scanner's 64 KiB default
// is answered like any other. Blank lines are skipped, as run.mjs skips them.
func serve(in io.Reader, out, diag io.Writer) error {
	r := bufio.NewReader(in)
	w := bufio.NewWriter(out)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for {
		line, readErr := r.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if len(bytes.TrimSpace(line)) > 0 {
			if err := enc.Encode(handle(line, diag)); err != nil {
				return err
			}
			// The checker waits for each reply before it sends the next
			// request, so every reply is flushed as soon as it is written.
			if err := w.Flush(); err != nil {
				return err
			}
		}
		if readErr != nil {
			return nil
		}
	}
}

func handle(line []byte, diag io.Writer) reply {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		// A syntax error leaves the id unset, so the reply carries id null as
		// run.mjs's does; a wrongly typed field still echoes the id it read.
		fmt.Fprintf(diag, "substore-conformance: bad request: %v\n", err)
		return reply{ID: req.ID, Error: "bad request: " + err.Error()}
	}
	switch req.Op {
	case "version":
		commit, version := buildIdentity()
		return reply{ID: req.ID, OK: true, Implementation: "lattice-go", Commit: commit, Version: version}
	case "parse":
		return reply{ID: req.ID, Error: "parse is not supported yet: the native parser (system-go/parse) has not landed"}
	case "produce":
		platform, ok := platforms[req.Target]
		if !ok {
			return reply{ID: req.ID, Error: fmt.Sprintf("produce: target %q has no native producer", req.Target)}
		}
		if req.Nodes == nil {
			return reply{ID: req.ID, Error: "produce needs a nodes array"}
		}
		p, ok := producers.Lookup(platform)
		if !ok {
			return reply{ID: req.ID, Error: fmt.Sprintf("produce is not supported yet: the native %s producer (system-go/producers) has not landed", platform)}
		}
		nodes := make([]*nodemodel.Node, len(req.Nodes))
		for i, raw := range req.Nodes {
			nodes[i] = &nodemodel.Node{}
			if err := json.Unmarshal(raw, nodes[i]); err != nil {
				return reply{ID: req.ID, Error: fmt.Sprintf("produce: node %d: %v", i, err)}
			}
		}
		var out bytes.Buffer
		if _, err := p.Produce(&out, nodes, platform, producers.Options(req.Options)); err != nil {
			return reply{ID: req.ID, Error: "produce: " + err.Error()}
		}
		output := out.String()
		return reply{ID: req.ID, OK: true, Output: &output}
	default:
		return reply{ID: req.ID, Error: fmt.Sprintf("unknown op %q", req.Op)}
	}
}

// buildIdentity names the build the checker measured: the VCS revision the
// go command stamped, and the main module version (a pseudo-version, with
// +dirty when the tree had local changes).
func buildIdentity() (commit, version string) {
	commit, version = "unknown", "unknown"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return commit, version
	}
	if info.Main.Version != "" {
		version = info.Main.Version
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			commit = s.Value
		}
	}
	return commit, version
}
