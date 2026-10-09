package perfgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The perf gate compares medians with a committed baseline, which only means
// something while the input is the same. These digests pin the 4096-node
// input; changing the generator changes them, and the baseline is re-recorded
// in the same commit.
const (
	uris4096SHA256  = "9b32c035de7a9b7fa4840276a3ba6f9ca970c9ab70457f9642d0368302421d36"
	nodes4096SHA256 = "9eb29932c7ad3aa7eb12c8a2028467c93fde6e7a4722f4e7b13c67ef3018da12"
)

func digest(parts [][]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func uriBytes(uris []string) [][]byte {
	out := make([][]byte, len(uris))
	for i, u := range uris {
		out[i] = []byte(u)
	}
	return out
}

func rawEqual(a, b json.RawMessage) bool { return bytes.Equal(a, b) }

func nodeBytes(nodes []json.RawMessage) [][]byte {
	out := make([][]byte, len(nodes))
	for i, n := range nodes {
		out[i] = n
	}
	return out
}

func TestGeneratorIsDeterministic(t *testing.T) {
	uris, nodes := URIs(4096), Nodes(4096)
	if len(uris) != 4096 || len(nodes) != 4096 {
		t.Fatalf("got %d links and %d nodes, want 4096 of each", len(uris), len(nodes))
	}
	if !slices.Equal(uris, URIs(4096)) {
		t.Fatal("two runs of URIs(4096) differ")
	}
	if !slices.EqualFunc(nodes, Nodes(4096), rawEqual) {
		t.Fatal("two runs of Nodes(4096) differ")
	}
	// Node i depends on i alone, so a smaller run is a prefix of a larger.
	if !slices.Equal(URIs(1000), uris[:1000]) || !slices.EqualFunc(Nodes(1000), nodes[:1000], rawEqual) {
		t.Fatal("the first 1000 nodes differ from a 1000-node run")
	}

	// The mix is the fixture's 50:6:6 exactly over every whole period.
	count := map[string]int{}
	for _, raw := range Nodes(620) {
		var n struct{ Network string }
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Fatal(err)
		}
		count[n.Network]++
	}
	if count[TCPVision] != 500 || count[GRPC] != 60 || count[XHTTP] != 60 || len(count) != 3 {
		t.Fatalf("transport mix over 620 nodes = %v, want tcp 500, grpc 60, xhttp 60", count)
	}
	if len(URIs(0)) != 0 || len(Nodes(-1)) != 0 {
		t.Fatal("a non-positive count must give no nodes")
	}
	if got := digest(uriBytes(uris)); got != uris4096SHA256 {
		t.Fatalf("URIs(4096) digest is %s, pinned %s: the perf input changed, so re-record the bench baseline with it", got, uris4096SHA256)
	}
	if got := digest(nodeBytes(nodes)); got != nodes4096SHA256 {
		t.Fatalf("Nodes(4096) digest is %s, pinned %s: the perf input changed, so re-record the bench baseline with it", got, nodes4096SHA256)
	}
}

var (
	syntheticHost = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)*\.example\.(com|net|org)$`)
	uuidV4        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	shortID       = regexp.MustCompile(`^[0-9a-f]{8}$`)
	nameShape     = regexp.MustCompile(`^(HK|JP|SG|US|DE|GB|NL|TW|KR|FR|CA|AU) [a-z][a-z0-9]{4} \d{4}$`)
	benchmarkNets = netip.MustParsePrefix("198.18.0.0/15")
)

type model struct {
	Name        string `json:"name"`
	Network     string `json:"network"`
	Port        int    `json:"port"`
	Server      string `json:"server"`
	SNI         string `json:"sni"`
	UUID        string `json:"uuid"`
	Flow        string `json:"flow"`
	RealityOpts struct {
		PublicKey string `json:"public-key"`
		ShortID   string `json:"short-id"`
	} `json:"reality-opts"`
	GRPCOpts struct {
		ServiceName string `json:"grpc-service-name"`
	} `json:"grpc-opts"`
	XHTTPOpts struct {
		Mode string `json:"mode"`
		Path string `json:"path"`
	} `json:"xhttp-opts"`
}

func decode(t *testing.T, raw json.RawMessage) model {
	t.Helper()
	var m model
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// No generated value may be a real host, address, key or name: hosts and SNIs
// under the example domains, addresses in the benchmarking range, keys of the
// real length and alphabet, and names from the fixture's country codes.
func TestGeneratorIsSynthetic(t *testing.T) {
	names := map[string]bool{}
	for i, raw := range Nodes(4096) {
		m := decode(t, raw)
		if addr, err := netip.ParseAddr(m.Server); err == nil {
			if !benchmarkNets.Contains(addr) {
				t.Fatalf("node %d server %s is outside 198.18.0.0/15", i, m.Server)
			}
		} else if !syntheticHost.MatchString(m.Server) {
			t.Fatalf("node %d server %q is not under an example domain", i, m.Server)
		}
		if !syntheticHost.MatchString(m.SNI) {
			t.Fatalf("node %d sni %q is not under an example domain", i, m.SNI)
		}
		if !uuidV4.MatchString(m.UUID) {
			t.Fatalf("node %d uuid %q is not a version 4 UUID", i, m.UUID)
		}
		if key, err := base64.RawURLEncoding.DecodeString(m.RealityOpts.PublicKey); err != nil || len(key) != 32 {
			t.Fatalf("node %d public key %q is not 32 bytes of unpadded base64url", i, m.RealityOpts.PublicKey)
		}
		if !shortID.MatchString(m.RealityOpts.ShortID) {
			t.Fatalf("node %d short id %q is not 8 hex digits", i, m.RealityOpts.ShortID)
		}
		if !nameShape.MatchString(m.Name) || names[m.Name] {
			t.Fatalf("node %d name %q is not a unique fixture-shaped name", i, m.Name)
		}
		names[m.Name] = true
		if m.Port != 443 && (m.Port < 20000 || m.Port >= 60000) {
			t.Fatalf("node %d port %d is neither 443 nor in 20000-59999", i, m.Port)
		}
	}
}

// The two forms must be the same nodes, or the parse path and the node path
// of the perf gate would time different work.
func TestURIsAndNodesDescribeTheSameNodes(t *testing.T) {
	uris, nodes := URIs(4096), Nodes(4096)
	for i := range uris {
		u, err := url.Parse(uris[i])
		if err != nil {
			t.Fatalf("link %d does not parse: %v", i, err)
		}
		m := decode(t, nodes[i])
		q := u.Query()
		got := []string{u.Scheme, u.User.Username(), u.Hostname(), u.Port(), u.Fragment,
			q.Get("security"), q.Get("fp"), q.Get("spx"), q.Get("encryption"),
			q.Get("sni"), q.Get("pbk"), q.Get("sid"), q.Get("type"), q.Get("flow"),
			q.Get("serviceName"), q.Get("path"), q.Get("mode")}
		want := []string{"vless", m.UUID, m.Server, strconv.Itoa(m.Port), m.Name,
			"reality", "chrome", "/", "none",
			m.SNI, m.RealityOpts.PublicKey, m.RealityOpts.ShortID, m.Network, m.Flow,
			m.GRPCOpts.ServiceName, m.XHTTPOpts.Path, m.XHTTPOpts.Mode}
		if !slices.Equal(got, want) {
			t.Fatalf("link %d and node %d differ:\nlink %q\nnode %q\n%s\n%s", i, i, got, want, uris[i], nodes[i])
		}
		if strings.ContainsAny(uris[i], " \n") {
			t.Fatalf("link %d carries a raw space or newline: %q", i, uris[i])
		}
		switch m.Network {
		case TCPVision:
			if m.Flow != "xtls-rprx-vision" || m.GRPCOpts.ServiceName != "" || m.XHTTPOpts.Path != "" {
				t.Fatalf("tcp node %d = %s", i, nodes[i])
			}
		case GRPC:
			if m.Flow != "" || m.GRPCOpts.ServiceName == "" || m.XHTTPOpts.Path != "" {
				t.Fatalf("grpc node %d = %s", i, nodes[i])
			}
		case XHTTP:
			if m.Flow != "" || m.XHTTPOpts.Mode != "auto" || !strings.HasPrefix(m.XHTTPOpts.Path, "/") {
				t.Fatalf("xhttp node %d = %s", i, nodes[i])
			}
		default:
			t.Fatalf("node %d network %q", i, m.Network)
		}
	}
}
