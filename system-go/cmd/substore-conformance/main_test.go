package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// runnerEnv makes the test binary run main instead of the tests, so the
// protocol test drives the real command over real pipes, the way check.mjs
// spawns it, without a separate go build.
const runnerEnv = "SUBSTORE_CONFORMANCE_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runnerEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestConformanceRunnerSpeaksProtocol(t *testing.T) {
	// One request larger than bufio.Scanner's 64 KiB default, so a reader
	// with a line limit fails here before the corpus grows into it.
	big := "vless://" + strings.Repeat("a", 200<<10)
	node := `{"name":"n","port":443,"server":"a.example.com","type":"vless","uuid":"00000000-0000-4000-8000-000000000000"}`
	socks := `{"name":"s 1","port":1080,"server":"a.example.com","supported":{"URI":false},"type":"socks5","udp":true}`
	lines := []string{
		`{"id":1,"op":"version"}`,
		`{"id":2,"op":"parse","input":"vless://00000000-0000-4000-8000-000000000000@a.example.com:443#n"}`,
		`{"id":"three","op":"produce","target":"clashmeta","nodes":[` + node + `],"options":{}}`,
		``,
		`{"id":4,"op":"produce","target":"stash","nodes":[` + node + `]}`,
		`{"id":5,"op":"produce","target":"uri"}`,
		`{"id":6,"op":"frobnicate"}`,
		`{"id":7,"op":`,
		`{"id":8,"op":"parse","input":` + mustJSON(t, big) + `}`,
		`{"id":9,"op":"parse","input":42}`,
		`{"id":"uri","op":"produce","target":"uri","nodes":[` + socks + `],"options":{"include-unsupported-proxy":true}}`,
		`{"id":"bad node","op":"produce","target":"v2ray","nodes":[42]}`,
		// The last request has no trailing newline; it is still answered.
		`{"id":10,"op":"version"}`,
	}
	stdin := strings.Join(lines, "\n")

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), runnerEnv+"=1")
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("runner exited with %v at end of input; stderr:\n%s", err, stderr.String())
	}

	// stdout holds replies and nothing else: every line is one JSON object,
	// in request order, and the blank request line has no reply.
	outLines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	wantIDs := []string{`1`, `2`, `"three"`, `4`, `5`, `6`, `null`, `8`, `9`, `"uri"`, `"bad node"`, `10`}
	if len(outLines) != len(wantIDs) {
		t.Fatalf("got %d reply lines for %d non-blank requests:\n%s", len(outLines), len(wantIDs), clip(stdout.String()))
	}
	replies := make([]reply, len(outLines))
	for i, l := range outLines {
		dec := json.NewDecoder(strings.NewReader(l))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&replies[i]); err != nil {
			t.Fatalf("stdout line %d is not a protocol reply (%v): %s", i+1, err, clip(l))
		}
		if got := string(replies[i].ID); got != wantIDs[i] {
			t.Fatalf("reply %d has id %s, want %s", i+1, got, wantIDs[i])
		}
	}

	for _, i := range []int{0, 11} {
		v := replies[i]
		if !v.OK || v.Implementation != "lattice-go" || v.Commit == "" || v.Version == "" {
			t.Fatalf("version reply %d = %+v, want ok with implementation lattice-go, a commit and a version", i+1, v)
		}
	}

	refusals := []struct {
		reply int
		says  []string
	}{
		{1, []string{"parse", "not supported"}},
		{3, []string{`"stash"`, "no native producer"}},
		{4, []string{"nodes"}},
		{5, []string{`unknown op "frobnicate"`}},
		{6, []string{"bad request"}},
		{7, []string{"parse", "not supported"}},
		{8, []string{"bad request"}},
		{10, []string{"node 0"}},
	}
	for _, r := range refusals {
		v := replies[r.reply]
		if v.OK || v.Output != nil || v.Nodes != nil {
			t.Fatalf("reply %d = %+v, want a refusal with no output and no nodes", r.reply+1, v)
		}
		for _, s := range r.says {
			if !strings.Contains(v.Error, s) {
				t.Fatalf("reply %d error %q does not say %q", r.reply+1, v.Error, s)
			}
		}
	}

	// produce answers from the native producer with the request's options:
	// include-unsupported-proxy keeps the node supported.URI=false drops.
	if v := replies[9]; !v.OK || v.Output == nil || *v.Output != "socks://Og%3D%3D@a.example.com:1080#s 1" || v.Error != "" {
		t.Fatalf("uri produce reply = %+v, want ok with the socks link", v)
	}
	// Every harness id of the five native targets has its producer.
	if v, want := replies[2], "proxies:\n  - "+node+"\n"; !v.OK || v.Output == nil || *v.Output != want || v.Error != "" {
		t.Fatalf("clashmeta produce reply = %+v, want ok with %q", v, want)
	}

	// Diagnostics go to stderr, never into the reply stream.
	if !strings.Contains(stderr.String(), "bad request") {
		t.Fatalf("stderr does not carry the bad-request diagnostic:\n%s", stderr.String())
	}

	// check.mjs waits for each reply before it sends the next request, so a
	// reply must reach stdout while stdin is still open.
	live := exec.Command(os.Args[0], "-test.run=^$")
	live.Env = append(os.Environ(), runnerEnv+"=1")
	in, err := live.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := live.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		in.Close()
		live.Wait()
	}()
	if _, err := io.WriteString(in, `{"id":11,"op":"version"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		l, _ := bufio.NewReader(out).ReadString('\n')
		got <- l
	}()
	select {
	case l := <-got:
		if !strings.HasPrefix(l, `{"id":11,"ok":true,`) {
			t.Fatalf("live reply = %q, want the version reply for id 11", l)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no reply within 10s while stdin stayed open: replies are not flushed per request")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func clip(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}
