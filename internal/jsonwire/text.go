// Package jsonwire contains strict JSON field decoders for local HTTP APIs.
package jsonwire

import (
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"
)

// Text rejects malformed Unicode instead of accepting encoding/json's
// replacement of unmatched UTF-16 surrogate escapes with U+FFFD.
type Text string

func (t *Text) UnmarshalJSON(raw []byte) error {
	var decoded string
	if len(raw) == 0 || raw[0] != '"' || !utf8.Valid(raw) || json.Unmarshal(raw, &decoded) != nil || !pairedSurrogates(raw) {
		return errors.New("invalid JSON text")
	}
	*t = Text(decoded)
	return nil
}

// The caller has already checked JSON string syntax, including escape lengths.
func pairedSurrogates(raw []byte) bool {
	var high bool
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] != '\\' || raw[i+1] != 'u' {
			if high {
				return false
			}
			if raw[i] == '\\' {
				i++ // Escaped backslash (among others) is not a Unicode escape.
			}
			continue
		}
		code, _ := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		low := code >= 0xdc00 && code <= 0xdfff
		if high != low {
			return false
		}
		high = code >= 0xd800 && code <= 0xdbff
		i += 5
	}
	return !high
}
