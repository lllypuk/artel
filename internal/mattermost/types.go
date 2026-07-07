package mattermost

import "time"

// ChannelType constants — values Mattermost reports in channel_type field.
const (
	ChannelTypeDirect  = "D" // direct message (DM)
	ChannelTypePrivate = "P" // private channel
	ChannelTypePublic  = "O" // public channel
)

// User represents a Mattermost user.
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	IsBot    bool   `json:"is_bot"`
}

// Channel represents a Mattermost channel.
type Channel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	TeamID      string `json:"team_id"`
	Type        string `json:"type"` // D | P | O
}

// Post represents a Mattermost post.
type Post struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	RootID    string `json:"root_id"`
	UserID    string `json:"user_id"`
	Message   string `json:"message"`
	CreateAt  int64  `json:"create_at"`
}

// CreatedAt returns post creation time.
func (p *Post) CreatedAt() time.Time {
	return time.UnixMilli(p.CreateAt)
}
