// Package safeguards provides loop-detection and rate-limiting guards for
// autonomous multi-agent conversations.
package safeguards

import (
	"sync"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/transport"
)

// EscalationText is sent to the conversation thread when the bot loop
// suppress threshold is reached, asking for human intervention.
const EscalationText = "⚠️ Автономный диалог ботов остановлен: превышен лимит итераций. Нужен человек."

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
	cfg         Config
	mu          sync.Mutex
	threads     map[string]*threadState
	lastCleanup time.Time
}

type threadState struct {
	botCount   int
	lastUpdate time.Time
	warned     bool
	replies    []time.Time
}

const (
	threadTTL       = 24 * time.Hour
	cleanupInterval = 10 * time.Minute
)

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
		cfg:         cfg,
		threads:     make(map[string]*threadState),
		lastCleanup: time.Now(),
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
// It updates per-thread counters, resets on human messages, and issues loop
// warnings/suppressions. It does NOT enforce the per-thread rate-limit; callers
// must use RecordResponse/AllowReply for reply-side throttling.
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

	// Evict stale thread state periodically to avoid unbounded memory growth.
	if now.Sub(g.lastCleanup) >= cleanupInterval {
		g.cleanupLocked(now)
		g.lastCleanup = now
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

// AllowReply reports whether the agent is allowed to send a reply in the
// message's thread. It checks the per-thread rate-limit window and returns the
// outcome. Does not mutate loop counters.
func (g *Guard) AllowReply(m transport.InboundMessage, now time.Time) Decision {
	if g.cfg.MaxRepliesPerMinute <= 0 {
		return Decision{Allowed: true, Reason: "unlimited"}
	}

	key := threadKey(m)

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.threads[key]
	if state == nil {
		state = &threadState{}
		g.threads[key] = state
	}

	state.replies = filterWindow(state.replies, now.Add(-time.Minute))
	if len(state.replies) >= g.cfg.MaxRepliesPerMinute {
		return Decision{Allowed: false, Reason: "rate_limited"}
	}
	return Decision{Allowed: true, Reason: "ok"}
}

// RecordResponse records that the agent sent a response in the message's thread.
// It is used for the per-thread rate-limit window and also increments the loop
// counter so that each instance tracks both inbound and outbound bot messages
// in the thread (approximating the total bot-message count).
func (g *Guard) RecordResponse(m transport.InboundMessage, now time.Time) {
	key := threadKey(m)

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.threads[key]
	if state == nil {
		state = &threadState{}
		g.threads[key] = state
	}

	state.replies = append(state.replies, now)
	state.botCount++
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

func (g *Guard) cleanupLocked(now time.Time) {
	for k, st := range g.threads {
		if now.Sub(st.lastUpdate) > threadTTL {
			delete(g.threads, k)
		}
	}
}

func threadKey(m transport.InboundMessage) string {
	if m.ThreadID != "" {
		return m.ThreadID
	}
	return m.ChatID
}

// filterWindow returns a new slice containing only the timestamps at or after
// cutoff. It does not mutate the input slice; callers must reassign the result
// if they want the filtered view to persist.
func filterWindow(times []time.Time, cutoff time.Time) []time.Time {
	out := make([]time.Time, 0, len(times))
	for _, t := range times {
		if t.After(cutoff) || t.Equal(cutoff) {
			out = append(out, t)
		}
	}
	return out
}
