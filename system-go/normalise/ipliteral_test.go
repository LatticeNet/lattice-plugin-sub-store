package normalise

import "testing"

// TestIPLiteralGrammar holds the grammar to every accepted and rejected form
// parser.md section 1.5 lists, including the spellings on which net/netip
// disagrees with it in either direction.
func TestIPLiteralGrammar(t *testing.T) {
	v4 := []struct {
		s    string
		want bool
	}{
		{"192.0.2.1", true},
		{"0.0.0.0", true},
		{"255.255.255.255", true},
		{"10.0.0.0", true},
		{"01.2.3.4", false},
		{"256.1.1.1", false},
		{"1.2.3", false},
		{"1.2.3.4.5", false},
		{"1.2.3.", false},
		{"1.2.3.4 ", false},
		{" 1.2.3.4", false},
		{"1.2.3.1000", false},
		{"a.b.c.d", false},
		{"", false},
	}
	for _, c := range v4 {
		if got := IsIPv4Literal(c.s); got != c.want {
			t.Errorf("IsIPv4Literal(%q) = %v, want %v", c.s, got, c.want)
		}
	}

	v6 := []struct {
		s    string
		want bool
	}{
		// Full.
		{"1:2:3:4:5:6:7:8", true},
		{"0001:2:3:4:5:6:7:8", true},
		{"1:2:3:4:5:6:7:8:9", false},
		{"1:2:3:4:5:6:7", false},
		// Compressed.
		{"::", true},
		{"::1", true},
		{"1::", true},
		{"2001:DB8::1", true},
		{"1:2:3:4:5:6:7::", true},
		{"1::2::3", false},
		{"1:::", false},
		{"00001::1", false},
		{"1:2:3:4:5:6:7::8", false},
		{"g::1", false},
		// Mapped tail.
		{"::192.0.2.1", true},
		{"::ffff:192.0.2.1", true},
		{"::ffff:0000:192.0.2.1", true},
		{"::ffff:01.2.3.4", true},
		{"::FFFF:192.0.2.1", false},
		{"::ffff:1:192.0.2.1", false},
		{"::ffff:0:0:192.0.2.1", false},
		{"::ffff:001.2.3.4", false},
		{"::ffff:00000:192.0.2.1", false},
		{"::ffff:256.0.2.1", false},
		// Prefixed tail.
		{"64:ff9b::192.0.2.1", true},
		{"FFFF::192.0.2.1", true},
		{"1:2:3:4::192.0.2.1", true},
		{"1:2:3:4:5::192.0.2.1", false},
		{"0:0:0:0:0:ffff:192.0.2.1", false},
		{"1:192.0.2.1", false},
		// Zoned link-local.
		{"fe80::1%eth0", true},
		{"fe80::%eth0", true},
		{"fe80::1:2:3:4%x", true},
		{"fe80:%eth0", true},
		{"fe80:::1%x", true},
		{"fe80::1%25eth0", true},
		{"FE80::1%eth0", false},
		{"FE80::1%25eth0", false},
		{"fe80::1%eth0.1", false},
		{"fe80::1%eth_0", false},
		{"fe80:0::1%x", false},
		{"fe80::1:2:3:4:5%x", false},
		{"fe80::12345%x", false},
		{"fe80::1%", false},
		{"2001:db8::1%eth0", false},
		{"::1%lo", false},
		// Whole text only: no brackets, no white space, no IPv4.
		{"[::1]", false},
		{" ::1", false},
		{"::1 ", false},
		{"192.0.2.1", false},
		{"", false},
	}
	for _, c := range v6 {
		if got := IsIPv6Literal(c.s); got != c.want {
			t.Errorf("IsIPv6Literal(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}
