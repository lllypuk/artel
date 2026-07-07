package safeguards

import (
	"fmt"
	"testing"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/transport"
	"github.com/stretchr/testify/assert"
)

func botMessage(threadID, n int, text string) transport.InboundMessage {
	return transport.InboundMessage{
		ChatID:     "chat-1",
		ThreadID:   threadIDForN(threadID, n),
		SenderID:   senderForN(n),
		SenderName: senderForN(n),
		IsBot:      true,
		Text:       text,
	}
}

func humanMessage(threadID, n int, text string) transport.InboundMessage {
	return transport.InboundMessage{
		ChatID:     "chat-1",
		ThreadID:   threadIDForN(threadID, n),
		SenderID:   senderForN(n),
		SenderName: "human",
		IsBot:      false,
		Text:       text,
	}
}

func threadIDForN(threadID, n int) string {
	if threadID == 0 {
		return ""
	}
	return fmt.Sprintf("thread-%d", threadID)
}

func senderForN(n int) string {
	return senderID("bot", n)
}

func senderID(prefix string, n int) string {
	return "u-" + prefix + "-" + string(rune('0'+n))
}

func TestNewAppliesDefaults(t *testing.T) {
	g := New(Config{})
	assert.Equal(t, 10, g.cfg.WarnThreshold, "default warn threshold")
	assert.Equal(t, 15, g.cfg.SuppressThreshold, "default suppress threshold")
	assert.Equal(t, 5, g.cfg.MaxRepliesPerMinute, "default rate limit")
}

func TestSelfSkip(t *testing.T) {
	g := New(Config{SelfID: "self-1"})

	d := g.BeforeHandle(transport.InboundMessage{SenderID: "self-1", Text: "echo"}, time.Now())
	assert.False(t, d.Allowed)
	assert.Equal(t, "self_skip", d.Reason)
}

func TestHumanMessageAllowed(t *testing.T) {
	g := New(Config{})
	m := humanMessage(0, 1, "hi")

	d := g.BeforeHandle(m, time.Now())
	assert.True(t, d.Allowed)
	assert.Equal(t, "ok", d.Reason)
	assert.False(t, d.Warn)

	count, replies := g.State(threadKey(m))
	assert.Equal(t, 0, count)
	assert.Equal(t, 0, replies)
}

func TestLoopWarnThreshold(t *testing.T) {
	cfg := Config{WarnThreshold: 3, SuppressThreshold: 6, MaxRepliesPerMinute: 10}
	g := New(cfg)
	base := time.Now()

	// First two bot messages are quiet.
	for i := range 2 {
		m := botMessage(0, i, "step")
		d := g.BeforeHandle(m, base)
		assert.True(t, d.Allowed, "message %d should be allowed", i)
		assert.False(t, d.Warn, "message %d should not warn", i)
	}

	// Third bot message triggers the warning once.
	m := botMessage(0, 2, "step")
	d := g.BeforeHandle(m, base)
	assert.True(t, d.Allowed)
	assert.True(t, d.Warn)

	// Subsequent messages are allowed but no additional warn flag.
	m = botMessage(0, 3, "step")
	d = g.BeforeHandle(m, base)
	assert.True(t, d.Allowed)
	assert.False(t, d.Warn)
}

func TestLoopSuppressThreshold(t *testing.T) {
	cfg := Config{WarnThreshold: 2, SuppressThreshold: 4, MaxRepliesPerMinute: 10}
	g := New(cfg)
	base := time.Now()

	for i := range 3 {
		m := botMessage(0, i, "step")
		d := g.BeforeHandle(m, base)
		assert.True(t, d.Allowed, "message %d should be allowed", i)
	}

	m := botMessage(0, 3, "step")
	d := g.BeforeHandle(m, base)
	assert.False(t, d.Allowed)
	assert.Equal(t, "loop_suppress", d.Reason)
	assert.True(t, d.Escalate)
}

func TestHumanMessageResetsLoopCounter(t *testing.T) {
	cfg := Config{WarnThreshold: 2, SuppressThreshold: 4, MaxRepliesPerMinute: 10}
	g := New(cfg)
	base := time.Now()

	// Reach warning threshold.
	for i := range 2 {
		_ = g.BeforeHandle(botMessage(0, i, "step"), base)
	}
	count, _ := g.State("chat-1")
	assert.Equal(t, 2, count)

	// Human message resets the counter.
	_ = g.BeforeHandle(humanMessage(0, 9, "stop bots"), base)
	count, _ = g.State("chat-1")
	assert.Equal(t, 0, count)

	// Bot messages start counting from zero again; need two more to warn.
	d := g.BeforeHandle(botMessage(0, 4, "again"), base)
	assert.True(t, d.Allowed)
	assert.False(t, d.Warn)
	d = g.BeforeHandle(botMessage(0, 5, "again"), base)
	assert.True(t, d.Allowed)
	assert.True(t, d.Warn)
}

func TestRateLimitBlocksAfterThreshold(t *testing.T) {
	cfg := Config{WarnThreshold: 100, SuppressThreshold: 200, MaxRepliesPerMinute: 3}
	g := New(cfg)
	base := time.Now()

	// Three replies fit inside the limit.
	m := humanMessage(0, 1, "go")
	for i := range 3 {
		d := g.BeforeHandle(m, base)
		assert.True(t, d.Allowed, "request %d should be allowed", i)
		g.RecordResponse(m, base)
	}

	// Fourth request in the same minute is rate-limited.
	d := g.BeforeHandle(m, base)
	assert.False(t, d.Allowed)
	assert.Equal(t, "rate_limited", d.Reason)
	assert.False(t, d.Escalate)
}

func TestRateLimitWindowSlides(t *testing.T) {
	cfg := Config{WarnThreshold: 100, SuppressThreshold: 200, MaxRepliesPerMinute: 2}
	g := New(cfg)
	base := time.Now()

	m := humanMessage(0, 1, "go")
	g.RecordResponse(m, base.Add(-30*time.Second))
	g.RecordResponse(m, base.Add(-30*time.Second))

	// At base time, the two old replies are still inside the one-minute window.
	d := g.BeforeHandle(m, base)
	assert.False(t, d.Allowed)
	assert.Equal(t, "rate_limited", d.Reason)

	// After the window slides past the oldest reply, the request is allowed again.
	later := base.Add(31 * time.Second)
	d = g.BeforeHandle(m, later)
	assert.True(t, d.Allowed)
}

func TestThreadsAreIsolated(t *testing.T) {
	cfg := Config{WarnThreshold: 3, SuppressThreshold: 6, MaxRepliesPerMinute: 10}
	g := New(cfg)
	base := time.Now()

	threadA := botMessage(1, 1, "a")
	threadB := botMessage(2, 1, "b")

	// Push thread A to warning: two messages are quiet, the third one warns.
	for range 2 {
		_ = g.BeforeHandle(threadA, base)
	}
	d := g.BeforeHandle(threadA, base)
	assert.True(t, d.Allowed)
	assert.True(t, d.Warn)

	// Thread B is still far from any threshold.
	d = g.BeforeHandle(threadB, base)
	assert.True(t, d.Allowed)
	assert.False(t, d.Warn)

	countA, _ := g.State(threadKey(threadA))
	countB, _ := g.State(threadKey(threadB))
	assert.Equal(t, 3, countA)
	assert.Equal(t, 1, countB)
}

func TestRateLimitPerThread(t *testing.T) {
	cfg := Config{WarnThreshold: 100, SuppressThreshold: 200, MaxRepliesPerMinute: 1}
	g := New(cfg)
	base := time.Now()

	threadA := humanMessage(1, 1, "go")
	threadB := humanMessage(2, 1, "go")

	g.RecordResponse(threadA, base)

	// Thread A is throttled.
	assert.False(t, g.BeforeHandle(threadA, base).Allowed)

	// Thread B is still allowed.
	assert.True(t, g.BeforeHandle(threadB, base).Allowed)
}
