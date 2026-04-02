package news

import "time"

// Item represents a news article from a feed.
type Item struct {
	ID          int64     `json:"id" db:"id"`
	FeedID      int64     `json:"feed_id" db:"feed_id"`
	GUID        string    `json:"guid" db:"guid"` // Unique ID from feed
	Title       string    `json:"title" db:"title"`
	Link        string    `json:"link" db:"link"`
	Description string    `json:"description" db:"description"` // Short description
	Content     string    `json:"content" db:"content"`         // Full text
	Author      string    `json:"author" db:"author"`
	Category    string    `json:"category" db:"category"`
	PublishedAt time.Time `json:"published_at" db:"published_at"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	ImageURL    string    `json:"image_url,omitempty" db:"image_url"`
}

// ItemScore is used for ranking news items.
type ItemScore struct {
	ItemID     int64   `json:"item_id"`
	Relevance  float64 `json:"relevance"`   // Relevance to query
	Popularity float64 `json:"popularity"`  // Based on clicks/saves
	Freshness  float64 `json:"freshness"`   // How new the item is
	FinalScore float64 `json:"final_score"` // Weighted sum
}
