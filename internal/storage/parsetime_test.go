package storage

// White-box unit tests for the parseSQLiteTime helper.
// The function is unexported, so this file must be in package storage (not storage_test).

import (
	"testing"
	"time"
)

func TestParseSQLiteTime(t *testing.T) {
	// Reference moment: 2026-05-28 13:26:46 UTC
	want := time.Date(2026, 5, 28, 13, 26, 46, 0, time.UTC)

	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantErr bool
	}{
		// RFC3339 with Z – produced by modernc.org/sqlite when a time.Time value
		// is passed as a bind parameter (the real-world trigger for the original bug).
		{
			name:  "RFC3339 Z suffix",
			input: "2026-05-28T13:26:46Z",
			want:  want,
		},
		// RFC3339Nano – same but with sub-second precision.
		{
			name:  "RFC3339Nano",
			input: "2026-05-28T13:26:46.000000000Z",
			want:  want,
		},
		// Space-separated – what SQLite's CURRENT_TIMESTAMP and strftime produce.
		{
			name:  "SQLite CURRENT_TIMESTAMP format",
			input: "2026-05-28 13:26:46",
			want:  want,
		},
		// Space-separated with fractional seconds.
		{
			name:  "space format with subseconds",
			input: "2026-05-28 13:26:46.999999999",
			want:  time.Date(2026, 5, 28, 13, 26, 46, 999999999, time.UTC),
		},
		// ISO-8601 without timezone – tolerated as UTC.
		{
			name:  "T-separated no timezone",
			input: "2026-05-28T13:26:46",
			want:  want,
		},
		// Result must always be in UTC regardless of input.
		{
			name:  "result is UTC",
			input: "2026-05-28T13:26:46Z",
			want:  want,
		},
		// Error cases.
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "garbage string",
			input:   "not-a-date",
			wantErr: true,
		},
		{
			name:    "date only",
			input:   "2026-05-28",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSQLiteTime(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for input %q, got time %v", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for input %q: %v", tc.input, err)
			}
			if got.Location() != time.UTC {
				t.Errorf("expected UTC location, got %v", got.Location())
			}
			// Truncate to second for the subseconds test case that has its own want.
			if tc.input == "2026-05-28 13:26:46.999999999" {
				if !got.Equal(tc.want) {
					t.Errorf("got %v, want %v", got, tc.want)
				}
				return
			}
			if !got.Equal(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
