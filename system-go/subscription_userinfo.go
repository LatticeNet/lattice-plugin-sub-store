package main

import (
	"math"
	"strconv"
	"strings"
)

// providerUsage is a provider's subscription-userinfo header, parsed.
//
// The record keeps the header verbatim (Userinfo); this is what the list hands
// the console so the overview and the sources table can draw a traffic bar and
// an expiry without a `get` per row. Every field is optional: a provider that
// sends only `total` gets a total and nothing else, and a field that does not
// parse is left out rather than reported as zero, because zero used and zero
// total are both claims.
type providerUsage struct {
	Upload   *int64 `json:"upload,omitempty"`
	Download *int64 `json:"download,omitempty"`
	Total    *int64 `json:"total,omitempty"`
	// Expire is unix seconds.
	Expire *int64 `json:"expire,omitempty"`
}

// expireMillisThreshold separates seconds from milliseconds. Unix seconds do not
// reach 1e12 until the year 33658, while a millisecond timestamp for any date
// after 2001 is above it, so a value this large can only be milliseconds.
const expireMillisThreshold = 1e12

// parseProviderUsage reads `upload=1; download=2; total=3; expire=4`.
//
// Providers disagree about nearly everything in this header, so the parser is
// tolerant by design: keys in any case and order, spaces around separators and
// `=`, a comma instead of a semicolon, quoted values, and numbers written as
// floats or in exponent form (`1.5E10`). What it will not do is guess. A
// negative, non-finite or non-numeric value drops that field; a key it does not
// know is ignored; the first valid value for a key wins over a later repeat.
// An `expire` of zero is dropped because providers send it to mean "does not
// expire", and one large enough to be milliseconds is scaled to seconds.
func parseProviderUsage(raw string) providerUsage {
	var out providerUsage
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ',' })
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		number, ok := parseUsageNumber(value)
		if !ok {
			continue
		}
		var slot **int64
		switch key {
		case "upload":
			slot = &out.Upload
		case "download":
			slot = &out.Download
		case "total":
			slot = &out.Total
		case "expire":
			if number == 0 {
				continue
			}
			if float64(number) >= expireMillisThreshold {
				number /= 1000
			}
			slot = &out.Expire
		default:
			continue
		}
		if *slot == nil {
			n := number
			*slot = &n
		}
	}
	return out
}

// parseUsageNumber accepts a non-negative integer or float, optionally quoted,
// and returns it truncated to a whole number. Byte counts past 2^63 do not
// exist in any real quota, so a value that large is refused rather than wrapped.
func parseUsageNumber(text string) (int64, bool) {
	text = strings.Trim(strings.TrimSpace(text), `"'`)
	if text == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(text, 10, 64); err == nil {
		return n, n >= 0
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f >= math.MaxInt64 {
		return 0, false
	}
	return int64(f), true
}
