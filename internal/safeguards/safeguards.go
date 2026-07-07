// Package safeguards provides loop-detection and rate-limiting guards for
// autonomous multi-agent conversations.
package safeguards

import (
	"sync"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/transport"
)

// EscalationMessage is sent to the conversation thread when the bot loop
// suppress threshold is reached, asking for human intervention.
const EscalationMessage = "⚠️ Автономный диалог ботов остановлен: превышен лимит итераций. Нужен человек."

// Config configures the safeguard thresholds.
type Config struct {
	// SelfID is the agent's own platform user ID. Messages from SelfID are ignored.
	SelfID string
	// WarnThreshold is the number of observed bot messages in a thread after which
	// a warning is logged. Zero defaults to 10.
	WarnThreshold int
	// SuppressThreshold is the number of observed bot messages in a thread after
	// which the agent stops replying and escalates to a human. Zero defaults to 15.
	SuppressThreshold int
	// MaxRepliesPerMinute limits how many responses the agent sends per thread per
	// sliding minute. Zero defaults to 5.
	MaxRepliesPerMinute int
}

// Decision is the result of a safeguard check.
type Decision struct {
	Allowed  bool
	Reason   string
	Warn     bool // reached warn threshold on this check
	Escalate bool // reached suppress threshold; caller should notify humans
}

// Guard tracks per-thread bot message counters and reply rate windows to prevent
// runaway bot-to-bot conversations.
type Guard struct {
	cfg     Config
	mu      sync.Mutex
	threads map[string]*threadState
}

type threadState struct {
	botCount   int
	lastUpdate time.Time
	warned     bool
	replies    []time.Time
}

// New creates a Guard from the supplied config, applying safe defaults for any
// zero threshold.
func New(cfg Config) *Guard {
	if cfg.WarnThreshold == 0 {
		cfg.WarnThreshold = 10
	}
	if cfg.SuppressThreshold == 0 {
		cfg.SuppressThreshold = 15
	}
	if cfg.MaxRepliesPerMinute == 0 {
		cfg.MaxRepliesPerMinute = 5
	}
	return &Guard{
		cfg:     cfg,
		threads: make(map[string]*threadState),
	}
}

// SetSelfID updates the agent's own platform ID (available after the transport
// identifies itself). It can be called with an empty string to disable self-skip.
func (g *Guard) SetSelfID(selfID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cfg.SelfID = selfID
}

// BeforeHandle decides whether an incoming message should be processed.
// It updates per-thread counters, resets on human messages, and checks the rate
// limit before the agent replies.
func (g *Guard) BeforeHandle(m transport.InboundMessage, now time.Time) Decision {
	if g.cfg.SelfID != "" && m.SenderID == g.cfg.SelfID {
		return Decision{Allowed: false, Reason: "self_skip"}
	}

	key := threadKey(m)

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.threads[key]
	if state == nil {
		state = &threadState{}
		g.threads[key] = state
	}

	// Human messages reset the loop counter for this conversation.
	if !m.IsBot && m.SenderID != "" {
		state.botCount = 0
		state.warned = false
	}

	if m.IsBot {
		state.botCount++
	}

	state.lastUpdate = now

	// Check rate limit first so a busy thread is throttled even before the
	// loop counter crosses the suppress threshold.
	if g.cfg.MaxRepliesPerMinute > 0 {
		state.replies = filterWindow(state.replies, now.Add(-time.Minute))
		if len(state.replies) >= g.cfg.MaxRepliesPerMinute {
			return Decision{Allowed: false, Reason: "rate_limited"}
		}
	}

	// Loop suppression.
	if g.cfg.SuppressThreshold > 0 && state.botCount >= g.cfg.SuppressThreshold {
		return Decision{Allowed: false, Reason: "loop_suppress", Escalate: true}
	}

	// Loop warning: fire once when crossing the warn threshold.
	warn := false
	if g.cfg.WarnThreshold > 0 && state.botCount >= g.cfg.WarnThreshold && !state.warned {
		state.warned = true
		warn = true
	}

	return Decision{Allowed: true, Reason: "ok", Warn: warn}
}

// RecordResponse records that the agent sent a response in the message's thread.
// It is used for the per-thread rate-limit window.
func (g *Guard) RecordResponse(m transport.InboundMessage, now time.Time) {
	key := threadKey(m)

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.threads[key]
	if state == nil {
		state = &threadState{}
		g.threads[key] = state
	}

	if g.cfg.MaxRepliesPerMinute > 0 {
		state.replies = filterWindow(state.replies, now.Add(-time.Minute))
		state.replies = append(state.replies, now)
	}
	state.lastUpdate = now
}

// State returns the current loop counter and reply-window size for a thread.
// It is intended for tests and diagnostics.
func (g *Guard) State(threadKey string) (botCount, replyCount int) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.threads[threadKey]
	if state == nil {
		return 0, 0
	}

	replies := len(state.replies)
	if g.cfg.MaxRepliesPerMinute > 0 {
		cutoff := time.Now().Add(-time.Minute)
		replies = len(filterWindow(state.replies, cutoff))
	}

	return state.botCount, replies
}

func threadKey(m transport.InboundMessage) string {
	if m.ThreadID != "" {
		return m.ThreadID
	}
	return m.ChatID
}

func filterWindow(times []time.Time, cutoff time.Time) []time.Time {
	out := times[:0]
	for _, t := range times {
		if t.After(cutoff) || t.Equal(cutoff) {
			out = append(out, t)
		}
	}
	return out
}
