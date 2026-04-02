package news

import "time"

// NewsFilter specifies filters for querying news items.
type NewsFilter struct {
	Limit       int
	Offset      int
	Category    string
	SearchQuery string
	UnreadOnly  bool
	SavedOnly   bool
	Since       *time.Time
}
