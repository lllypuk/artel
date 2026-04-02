package news

import "time"

// Subscription represents a user's subscription to a feed.
type Subscription struct {
	ID             int64     `json:"id" db:"id"`
	ChatID         int64     `json:"chat_id" db:"chat_id"`
	FeedID         int64     `json:"feed_id" db:"feed_id"`
	Enabled        bool      `json:"enabled" db:"enabled"`
	Notify         bool      `json:"notify" db:"notify"`                   // Push notifications
	FilterKeywords string    `json:"filter_keywords" db:"filter_keywords"` // Comma-separated
	FilterCategory string    `json:"filter_category" db:"filter_category"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// ReadStatus tracks which news items a user has read.
type ReadStatus struct {
	ID     int64     `json:"id" db:"id"`
	ChatID int64     `json:"chat_id" db:"chat_id"`
	ItemID int64     `json:"item_id" db:"item_id"`
	ReadAt time.Time `json:"read_at" db:"read_at"`
	Saved  bool      `json:"saved" db:"saved"` // Saved to favorites
}
