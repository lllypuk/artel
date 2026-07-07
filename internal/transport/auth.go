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
	// StrictBotWhitelist fails closed: a message from a bot whose real IsBot
	// status cannot be verified, but whose username/name matches TeamBots, is
	// rejected. When false, the gate conservatively treats unverified bot-matching
	// senders as regular users (less safe, mainly for transports with no bot
	// metadata).
	StrictBotWhitelist bool
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
//
// Authorization rules:
//   - Messages from self are always ignored.
//   - Owners (human supervisors) are allowed in DMs unconditionally. In a
//     channel, an owner is allowed only when the message @-mentions this bot's
//     own SelfUsername.
//   - Known team bots are allowed only when they @-mention this bot.
//   - Other regular users are allowed in DMs or when they @-mention this bot.
func (g *AuthGate) Allowed(m InboundMessage) (allowed bool, reason string) {
	if g.isSelf(m) {
		return false, "self"
	}
	if g.isOwner(m) {
		if m.IsDM || g.mentionsSelf(m) {
			return true, "owner"
		}
		return false, "owner_not_addressed"
	}
	if g.isUnverifiedTeamBot(m) {
		return false, "unverified_bot"
	}
	if m.IsBot {
		if g.isTeamBot(m) && g.mentionsSelf(m) {
			return true, "team_mention"
		}
		return false, "unknown_bot"
	}
	if m.IsDM || g.mentionsSelf(m) {
		return true, "direct_or_mention"
	}
	return false, "not_addressed"
}

// isUnverifiedTeamBot reports whether the sender looks like a team bot but IsBot
// is false. Such senders are treated conservatively to prevent a non-bot account
// whose username matches a whitelisted bot name from triggering the agent.
func (g *AuthGate) isUnverifiedTeamBot(m InboundMessage) bool {
	if !g.cfg.StrictBotWhitelist {
		return false
	}
	if m.IsBot {
		return false
	}
	return containsCI(g.cfg.TeamBots, m.SenderID) || containsCI(g.cfg.TeamBots, m.SenderName)
}

func (g *AuthGate) isSelf(m InboundMessage) bool {
	if g.cfg.SelfID == "" {
		return false
	}
	return m.SenderID == g.cfg.SelfID
}

// isOwner reports whether the sender is a configured human supervisor.
// Owner identity may be expressed as a sender ID or as a chat/channel ID,
// depending on the transport.
func (g *AuthGate) isOwner(m InboundMessage) bool {
	if slices.Contains(g.cfg.OwnerIDs, m.SenderID) {
		return true
	}
	return slices.Contains(g.cfg.OwnerIDs, m.ChatID)
}

func (g *AuthGate) isTeamBot(m InboundMessage) bool {
	if !m.IsBot {
		return false
	}
	return containsCI(g.cfg.TeamBots, m.SenderName)
}

func (g *AuthGate) mentionsSelf(m InboundMessage) bool {
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

func containsCI(list []string, value string) bool {
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
