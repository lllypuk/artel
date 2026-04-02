package news

import "time"

// NewsFilter specifies filters for querying news items.
type NewsFilter struct {
	ChatID      int64 // Required for UnreadOnly/SavedOnly filtering
	Limit       int
	Offset      int
	Category    string
	SearchQuery string
	UnreadOnly  bool
	SavedOnly   bool
	Since       *time.Time
}
