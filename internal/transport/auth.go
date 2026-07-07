package transport

import (
	"slices"
	"strings"
)

// AuthConfig configures who is allowed to trigger the agent.
type AuthConfig struct {
	// SelfID is the agent's own platform user ID. Messages from SelfID are ignored.
	SelfID string
	// SelfUsername is the agent's own username without the leading '@'.
	// Used to detect @-mentions in channel conversations.
	SelfUsername string
	// OwnerIDs are human supervisors with full access. A message from any of
	// these IDs is always handled, even in a public channel without a mention.
	OwnerIDs []string
	// TeamBots are known bot teammates. They are allowed to trigger the agent
	// only when they @-mention it (or in a DM, depending on transport rules).
	TeamBots []string
}

// AuthGate decides whether an inbound message should be passed to the agent.
type AuthGate struct {
	cfg AuthConfig
}

// NewAuthGate creates an authorization gate from the provided config.
func NewAuthGate(cfg AuthConfig) *AuthGate {
	return &AuthGate{cfg: cfg}
}

// Allowed returns true when the message is authorized to trigger the agent.
// The reason string is for diagnostics and tests only.
func (g *AuthGate) Allowed(m InboundMessage) (allowed bool, reason string) {
	if g.IsSelf(m) {
		return false, "self"
	}
	if g.IsOwner(m) {
		return true, "owner"
	}
	if m.IsBot {
		if g.IsTeamBot(m) && g.MentionsSelf(m) {
			return true, "team_mention"
		}
		return false, "unknown_bot"
	}
	if m.IsDM || g.MentionsSelf(m) {
		return true, "direct_or_mention"
	}
	return false, "not_addressed"
}

// IsSelf reports whether the message was sent by this agent itself.
func (g *AuthGate) IsSelf(m InboundMessage) bool {
	if g.cfg.SelfID == "" {
		return false
	}
	return m.SenderID == g.cfg.SelfID
}

// IsOwner reports whether the sender is a configured human supervisor.
// Owner identity may be expressed as a sender ID or as a chat/channel ID,
// depending on the transport.
func (g *AuthGate) IsOwner(m InboundMessage) bool {
	if g.contains(g.cfg.OwnerIDs, m.SenderID) {
		return true
	}
	return g.cfg.SelfID != "" && g.contains(g.cfg.OwnerIDs, m.ChatID)
}

// IsTeamBot reports whether the sender is a whitelisted bot teammate.
func (g *AuthGate) IsTeamBot(m InboundMessage) bool {
	if !m.IsBot {
		return false
	}
	return g.containsCI(g.cfg.TeamBots, m.SenderID) || g.containsCI(g.cfg.TeamBots, m.SenderName)
}

// MentionsSelf reports whether the message mentions the agent.
// For direct messages this is always considered true if the message is not
// from a disallowed bot.
func (g *AuthGate) MentionsSelf(m InboundMessage) bool {
	if g.cfg.SelfUsername == "" {
		return false
	}
	for _, mention := range m.Mentions {
		if strings.EqualFold(mention, g.cfg.SelfUsername) {
			return true
		}
	}
	return false
}

func (g *AuthGate) contains(list []string, value string) bool {
	return slices.Contains(list, value)
}

func (g *AuthGate) containsCI(list []string, value string) bool {
	if value == "" {
		return false
	}
	for _, item := range list {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}
