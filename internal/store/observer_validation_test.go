package store

import (
	"strings"
	"testing"
)

func TestObserverTextCountsUnicodeCodePoints(t *testing.T) {
	for _, length := range []int{129, 256, 257} {
		value := strings.Repeat("🚀", length)
		if observerText(value, 256, false) != (length <= 256) {
			t.Fatal("text validity differs from Unicode code-point bound")
		}
	}
	if observerText("metadata\n", 256, false) {
		t.Fatal("control character accepted")
	}
}

func TestObserverUUIDCanonicalNonNil(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"11111111-1111-4111-8111-111111111111", true},
		{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", true},
		{"00000000-0000-0000-0000-000000000000", false},
		{"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", false},
		{"{11111111-1111-4111-8111-111111111111}", false},
		{"11111111111141118111111111111111", false},
		{"invalid", false},
		{"", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if observerUUID(tc.value) != tc.valid {
				t.Fatal("UUID validity differs from canonical non-nil contract")
			}
		})
	}
}
