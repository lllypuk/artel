package transport

import "testing"

func TestAuthGateAllowOwnerDM(t *testing.T) {
	g := NewAuthGate(AuthConfig{SelfID: "bot1", SelfUsername: "bot", OwnerIDs: []string{"owner"}})

	allowed, reason := g.Allowed(InboundMessage{
		SenderID: "owner",
		Text:     "do it",
		IsDM:     true,
	})
	if !allowed || reason != "owner" {
		t.Fatalf("expected owner in DM allowed, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestAuthGateAllowOwnerChannelSelfMention(t *testing.T) {
	g := NewAuthGate(AuthConfig{SelfID: "bot1", SelfUsername: "analyst-bot", OwnerIDs: []string{"owner"}})

	allowed, reason := g.Allowed(InboundMessage{
		SenderID: "owner",
		Text:     "@analyst-bot check this",
		Mentions: []string{"analyst-bot"},
	})
	if !allowed || reason != "owner" {
		t.Fatalf("expected owner channel self-mention allowed, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestAuthGateRejectOwnerChannelNoMention(t *testing.T) {
	g := NewAuthGate(AuthConfig{SelfID: "bot1", SelfUsername: "analyst-bot", OwnerIDs: []string{"owner"}})

	allowed, reason := g.Allowed(InboundMessage{
		SenderID: "owner",
		Text:     "just a general broadcast",
	})
	if allowed {
		t.Fatalf("expected owner channel message without mention blocked, got allowed reason=%q", reason)
	}
}

func TestAuthGateRejectOwnerChannelDifferentBotMention(t *testing.T) {
	g := NewAuthGate(AuthConfig{SelfID: "analyst1", SelfUsername: "analyst-bot", OwnerIDs: []string{"owner"}})

	allowed, reason := g.Allowed(InboundMessage{
		SenderID: "owner",
		Text:     "@developer-bot handle this",
		Mentions: []string{"developer-bot"},
	})
	if allowed {
		t.Fatalf("expected owner channel message mentioning different bot blocked, got allowed reason=%q", reason)
	}
}

func TestAuthGateAllowHumanMention(t *testing.T) {
	g := NewAuthGate(AuthConfig{SelfID: "bot1", SelfUsername: "bot", OwnerIDs: []string{"owner"}})

	allowed, reason := g.Allowed(InboundMessage{
		SenderID:   "human",
		SenderName: "alice",
		Text:       "hi @bot",
		Mentions:   []string{"bot"},
	})
	if !allowed || reason != "direct_or_mention" {
		t.Fatalf("expected mention allowed, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestAuthGateAllowDM(t *testing.T) {
	g := NewAuthGate(AuthConfig{SelfID: "bot1", SelfUsername: "bot", OwnerIDs: []string{"owner"}})

	allowed, reason := g.Allowed(InboundMessage{
		SenderID:   "human",
		SenderName: "alice",
		Text:       "hi",
		IsDM:       true,
	})
	if !allowed || reason != "direct_or_mention" {
		t.Fatalf("expected DM allowed, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestAuthGateAllowTeamBotMention(t *testing.T) {
	g := NewAuthGate(AuthConfig{SelfID: "bot1", SelfUsername: "bot", OwnerIDs: []string{"owner"}, TeamBots: []string{"helper-bot"}})

	allowed, reason := g.Allowed(InboundMessage{
		SenderID:   "helper",
		SenderName: "helper-bot",
		IsBot:      true,
		Text:       "@bot check this",
		Mentions:   []string{"bot"},
	})
	if !allowed || reason != "team_mention" {
		t.Fatalf("expected team bot mention allowed, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestAuthGateIgnoreTeamBotWithoutMention(t *testing.T) {
	g := NewAuthGate(AuthConfig{SelfID: "bot1", SelfUsername: "bot", OwnerIDs: []string{"owner"}, TeamBots: []string{"helper-bot"}})

	allowed, reason := g.Allowed(InboundMessage{
		SenderID:   "helper",
		SenderName: "helper-bot",
		IsBot:      true,
		Text:       "general broadcast",
	})
	if allowed {
		t.Fatalf("expected team bot without mention blocked, got allowed reason=%q", reason)
	}
}

func TestAuthGateIgnoreUnknownBot(t *testing.T) {
	g := NewAuthGate(AuthConfig{SelfID: "bot1", SelfUsername: "bot", OwnerIDs: []string{"owner"}, TeamBots: []string{"helper-bot"}})

	allowed, reason := g.Allowed(InboundMessage{
		SenderID:   "intruder",
		SenderName: "evil-bot",
		IsBot:      true,
		Text:       "@bot do bad",
		Mentions:   []string{"bot"},
	})
	if allowed {
		t.Fatalf("expected unknown bot blocked, got allowed reason=%q", reason)
	}
}

func TestAuthGateIgnoreSelf(t *testing.T) {
	g := NewAuthGate(AuthConfig{SelfID: "bot1", SelfUsername: "bot", OwnerIDs: []string{"owner"}})

	allowed, reason := g.Allowed(InboundMessage{SenderID: "bot1", Text: "echo"})
	if allowed {
		t.Fatalf("expected self-message blocked, got allowed reason=%q", reason)
	}
}

func TestAuthGateIgnoreUnaddressedChannelMessage(t *testing.T) {
	g := NewAuthGate(AuthConfig{SelfID: "bot1", SelfUsername: "bot", OwnerIDs: []string{"owner"}})

	allowed, reason := g.Allowed(InboundMessage{
		SenderID:   "stranger",
		SenderName: "bob",
		Text:       "just chatting",
	})
	if allowed {
		t.Fatalf("expected unaddressed channel message blocked, got allowed reason=%q", reason)
	}
}

func TestAuthGateOwnerByChatID(t *testing.T) {
	g := NewAuthGate(AuthConfig{SelfID: "bot1", SelfUsername: "bot", OwnerIDs: []string{"chat-42"}})

	// Recognition by ChatID represents a private chat (e.g. Telegram DM), so it
	// must still be allowed even without an explicit mention.
	allowed, reason := g.Allowed(InboundMessage{
		ChatID:   "chat-42",
		SenderID: "someone",
		Text:     "do it",
		IsDM:     true,
	})
	if !allowed || reason != "owner" {
		t.Fatalf("expected owner by chat id allowed, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestAuthGateOwnerByChatIDWithoutSelfID(t *testing.T) {
	// Owner recognition by ChatID must work even when SelfID is not yet known.
	g := NewAuthGate(AuthConfig{OwnerIDs: []string{"chat-42"}})

	allowed, reason := g.Allowed(InboundMessage{
		ChatID:   "chat-42",
		SenderID: "someone",
		Text:     "do it",
		IsDM:     true,
	})
	if !allowed || reason != "owner" {
		t.Fatalf("expected owner by chat id allowed without self id, got allowed=%v reason=%q", allowed, reason)
	}
}
