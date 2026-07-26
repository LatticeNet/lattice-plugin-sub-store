package main

import (
	"reflect"
	"testing"
)

func TestParseNodeCounts(t *testing.T) {
	got, err := parseNodeCounts("100, 2500,,4306")
	if err != nil {
		t.Fatalf("parseNodeCounts returned error: %v", err)
	}
	want := []int{100, 2500, 4306}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseNodeCounts = %#v, want %#v", got, want)
	}
}

func TestParseNodeCountsRejectsInvalidValues(t *testing.T) {
	for _, input := range []string{"", "0", "-1", "abc"} {
		if _, err := parseNodeCounts(input); err == nil {
			t.Fatalf("parseNodeCounts(%q) returned nil error", input)
		}
	}
}

func TestSafeCaseName(t *testing.T) {
	got := safeCaseName("Sing Box / URI")
	if got != "sing-box-uri" {
		t.Fatalf("safeCaseName = %q, want %q", got, "sing-box-uri")
	}

	got = safeCaseName("!!!")
	if got != "target" {
		t.Fatalf("safeCaseName empty fallback = %q, want %q", got, "target")
	}
}
