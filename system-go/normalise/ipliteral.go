package normalise

import "strings"

// The IP literal grammar of parser.md section 1.5. Upstream decides whether a
// text is an IP address with two fixed patterns that reject several standard
// spellings and accept a few non-standard ones, and the answer drives Host
// pinning (N22), SNI pinning (N24), WireGuard addresses (N35), the Trojan
// host check and Surge external addresses. net/netip and net.ParseIP
// disagree with the grammar in both directions, so it is implemented here
// directly and never through them. The whole text must match: nothing is
// trimmed, and square brackets are not part of a literal.

// IsIPv4Literal reports whether s is four decimal octets joined by ".", each
// from 0 to 255 and written without a leading zero ("0" itself is allowed).
func IsIPv4Literal(s string) bool {
	for i := 0; i < 4; i++ {
		if i > 0 {
			if s == "" || s[0] != '.' {
				return false
			}
			s = s[1:]
		}
		n := digitRun(s, 3)
		if n == 0 || !ipv4Octet(s[:n]) {
			return false
		}
		s = s[n:]
	}
	return s == ""
}

// IsIPv6Literal reports whether s is one of the five IPv6 forms of parser.md
// 1.5: full, compressed, mapped tail, prefixed tail, or zoned link-local.
func IsIPv6Literal(s string) bool {
	return ipv6Full(s) || ipv6Compressed(s) || ipv6MappedTail(s) || ipv6PrefixedTail(s) || ipv6ZonedLinkLocal(s)
}

// ipv4Octet is an IPv4 literal octet: 0 to 255 without a leading zero.
func ipv4Octet(d string) bool {
	switch len(d) {
	case 1:
		return true
	case 2:
		return d[0] != '0'
	case 3:
		return d[0] != '0' && d <= "255"
	}
	return false
}

// tailOctet is an IPv4-tail octet: one digit, two digits (a leading zero is
// allowed), or three digits from 100 to 255.
func tailOctet(d string) bool {
	switch len(d) {
	case 1, 2:
		return true
	case 3:
		return d >= "100" && d <= "255"
	}
	return false
}

// ipv4Tail is four tail octets joined by ".".
func ipv4Tail(s string) bool {
	for i := 0; i < 4; i++ {
		if i > 0 {
			if s == "" || s[0] != '.' {
				return false
			}
			s = s[1:]
		}
		n := digitRun(s, 3)
		if n == 0 || !tailOctet(s[:n]) {
			return false
		}
		s = s[n:]
	}
	return s == ""
}

// digitRun is the length of the leading decimal digits of s, read up to
// limit+1 so an over-long run is visible to the caller as too long.
func digitRun(s string, limit int) int {
	n := 0
	for n < len(s) && n <= limit && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	if n > limit {
		return 0
	}
	return n
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// hexGroups reports whether s is one or more hex groups (one to four hex
// digits) joined by ":", and how many there are.
func hexGroups(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	groups := strings.Split(s, ":")
	for _, g := range groups {
		if len(g) < 1 || len(g) > 4 {
			return 0, false
		}
		for i := 0; i < len(g); i++ {
			if !isHex(g[i]) {
				return 0, false
			}
		}
	}
	return len(groups), true
}

// ipv6Full: eight hex groups joined by ":".
func ipv6Full(s string) bool {
	n, ok := hexGroups(s)
	return ok && n == 8
}

// ipv6Compressed: L::R, each side empty or hex groups, at most seven groups
// in both together.
func ipv6Compressed(s string) bool {
	i := strings.Index(s, "::")
	if i < 0 {
		return false
	}
	left, right := s[:i], s[i+2:]
	total := 0
	for _, side := range []string{left, right} {
		if side == "" {
			continue
		}
		n, ok := hexGroups(side)
		if !ok {
			return false
		}
		total += n
	}
	return total <= 7
}

// ipv6MappedTail: "::" then a tail; "::ffff:" (lower case exactly) then a
// tail; or "::ffff:", one to four "0" characters, ":" and a tail.
func ipv6MappedTail(s string) bool {
	rest, ok := strings.CutPrefix(s, "::")
	if !ok {
		return false
	}
	if ipv4Tail(rest) {
		return true
	}
	rest, ok = strings.CutPrefix(rest, "ffff:")
	if !ok {
		return false
	}
	if ipv4Tail(rest) {
		return true
	}
	zeros := 0
	for zeros < len(rest) && rest[zeros] == '0' {
		zeros++
	}
	if zeros < 1 || zeros > 4 || zeros >= len(rest) || rest[zeros] != ':' {
		return false
	}
	return ipv4Tail(rest[zeros+1:])
}

// ipv6PrefixedTail: one to four hex groups, then "::", then a tail.
func ipv6PrefixedTail(s string) bool {
	i := strings.Index(s, "::")
	if i <= 0 {
		return false
	}
	n, ok := hexGroups(s[:i])
	return ok && n <= 4 && ipv4Tail(s[i+2:])
}

// ipv6ZonedLinkLocal: "fe80:" (lower case exactly), zero to four pieces each
// made of ":" and zero to four hex digits, then "%" and one or more ASCII
// letters or digits.
func ipv6ZonedLinkLocal(s string) bool {
	rest, ok := strings.CutPrefix(s, "fe80:")
	if !ok {
		return false
	}
	for pieces := 0; rest != "" && rest[0] == ':'; pieces++ {
		if pieces == 4 {
			return false
		}
		rest = rest[1:]
		digits := 0
		for digits < len(rest) && isHex(rest[digits]) {
			digits++
		}
		if digits > 4 {
			return false
		}
		rest = rest[digits:]
	}
	zone, ok := strings.CutPrefix(rest, "%")
	if !ok || zone == "" {
		return false
	}
	for i := 0; i < len(zone); i++ {
		c := zone[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}
