package news

import "time"

// Feed represents an RSS/Atom/JSON feed source.
type Feed struct {
	ID            int64         `json:"id" db:"id"`
	URL           string        `json:"url" db:"url"`
	Title         string        `json:"title" db:"title"`
	Description   string        `json:"description" db:"description"`
	Type          FeedType      `json:"type" db:"type"`
	Category      string        `json:"category" db:"category"`
	Language      string        `json:"language" db:"language"`
	Enabled       bool          `json:"enabled" db:"enabled"`
	ProxyURL      string        `json:"proxy_url,omitempty" db:"proxy_url"`
	FetchInterval time.Duration `json:"fetch_interval" db:"fetch_interval"`
	LastFetched   *time.Time    `json:"last_fetched,omitempty" db:"last_fetched"`
	LastError     string        `json:"last_error,omitempty" db:"last_error"`
	CreatedAt     time.Time     `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at" db:"updated_at"`
}

// FeedType represents the type of feed format.
type FeedType string

const (
	FeedTypeRSS  FeedType = "rss"
	FeedTypeAtom FeedType = "atom"
	FeedTypeJSON FeedType = "json"
)

// FetchIntervalSeconds returns the fetch interval in seconds for database storage.
func (f *Feed) FetchIntervalSeconds() int64 {
	return int64(f.FetchInterval / time.Second)
}

// SetFetchIntervalFromSeconds sets the fetch interval from seconds.
func (f *Feed) SetFetchIntervalFromSeconds(seconds int64) {
	f.FetchInterval = time.Duration(seconds) * time.Second
}
