package llm

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"sort"
	"sync"
	"time"
)

// ProviderHealth reports the health status of a single provider.
type ProviderHealth struct {
	Name              string
	Priority          int
	Healthy           bool
	ConsecutiveErrors int
	LastErrorTime     time.Time
}

// providerEntry wraps a Client with metadata for fallback management.
type providerEntry struct {
	name     string
	client   *Client
	models   map[string]string // alias → provider-specific model name
	priority int

	mu                sync.Mutex
	consecutiveErrors int
	lastErrorTime     time.Time
}

func (p *providerEntry) isHealthy(cooldown time.Duration, maxErrors int) bool {
	if p.consecutiveErrors < maxErrors {
		return true
	}
	return time.Since(p.lastErrorTime) >= cooldown
}

func (p *providerEntry) recordSuccess() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.consecutiveErrors = 0
}

func (p *providerEntry) recordError() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.consecutiveErrors++
	p.lastErrorTime = time.Now()
}

// MultiClientConfig configures the MultiClient.
type MultiClientConfig struct {
	MaxRetries       int           // retries per provider before fallback (default: 2)
	InitialBackoff   time.Duration // initial backoff duration (default: 500ms)
	MaxBackoff       time.Duration // max backoff duration (default: 5s)
	CooldownDuration time.Duration // health cooldown after max errors (default: 30s)
	MaxConsecErrors  int           // consecutive errors to mark unhealthy (default: 3)
}

// ProviderClientConfig describes a single provider for MultiClient.
type ProviderClientConfig struct {
	Name     string
	Client   *Client
	Models   map[string]string // alias → model name (e.g. "default" → "google/gemini-2.5-flash")
	Priority int               // lower = higher priority
}

// MultiClient implements Provider with multi-provider fallback, retry,
// and circuit-breaker health tracking.
type MultiClient struct {
	providers []*providerEntry
	modelMap  map[string]string // "model name" → alias (reverse lookup for cross-provider mapping)

	maxRetries       int
	initialBackoff   time.Duration
	maxBackoff       time.Duration
	cooldownDuration time.Duration
	maxConsecErrors  int

	logger *slog.Logger
}

// NewMultiClient creates a MultiClient wrapping multiple providers.
// Providers are tried in priority order (lowest first).
func NewMultiClient(providers []ProviderClientConfig, cfg MultiClientConfig, logger *slog.Logger) *MultiClient {
	entries := make([]*providerEntry, len(providers))
	for i, p := range providers {
		models := p.Models
		if models == nil {
			models = make(map[string]string)
		}
		entries[i] = &providerEntry{
			name:     p.Name,
			client:   p.Client,
			models:   models,
			priority: p.Priority,
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].priority < entries[j].priority
	})

	// Build reverse model map: "model name" → alias.
	// First provider's mappings take precedence.
	modelMap := make(map[string]string)
	for _, e := range entries {
		for alias, name := range e.models {
			if _, exists := modelMap[name]; !exists {
				modelMap[name] = alias
			}
		}
	}

	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 2
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 500 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 5 * time.Second
	}
	if cfg.CooldownDuration <= 0 {
		cfg.CooldownDuration = 30 * time.Second
	}
	if cfg.MaxConsecErrors <= 0 {
		cfg.MaxConsecErrors = 3
	}

	return &MultiClient{
		providers:        entries,
		modelMap:         modelMap,
		maxRetries:       cfg.MaxRetries,
		initialBackoff:   cfg.InitialBackoff,
		maxBackoff:       cfg.MaxBackoff,
		cooldownDuration: cfg.CooldownDuration,
		maxConsecErrors:  cfg.MaxConsecErrors,
		logger:           logger,
	}
}

// resolveModel maps a model name to the equivalent model for a specific provider.
// 1. If the model is a known alias (e.g. "default", "fast") → resolve directly.
// 2. If the model is a name from another provider → find alias → resolve for target provider.
// 3. Otherwise pass through as-is.
func (mc *MultiClient) resolveModel(model string, provider *providerEntry) string {
	// Direct alias lookup.
	if resolved, ok := provider.models[model]; ok {
		return resolved
	}

	// Reverse lookup: model name → alias → target provider's model.
	if alias, ok := mc.modelMap[model]; ok {
		if resolved, ok := provider.models[alias]; ok {
			return resolved
		}
	}

	// Pass through as-is (e.g. the model name is valid for this provider directly).
	return model
}

// Chat sends a non-streaming chat completion request, trying providers in
// priority order with per-provider retries and exponential backoff.
func (mc *MultiClient) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	var lastErr error
	tried := 0

	for _, provider := range mc.providers {
		provider.mu.Lock()
		healthy := provider.isHealthy(mc.cooldownDuration, mc.maxConsecErrors)
		provider.mu.Unlock()

		if !healthy {
			mc.logger.Debug("skipping unhealthy provider", "provider", provider.name)
			continue
		}

		tried++
		providerReq := req
		providerReq.Model = mc.resolveModel(req.Model, provider)

		for attempt := 0; attempt <= mc.maxRetries; attempt++ {
			if attempt > 0 {
				backoff := mc.calculateBackoff(attempt)
				mc.logger.Debug("retrying provider",
					"provider", provider.name,
					"attempt", attempt,
					"backoff", backoff,
				)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(backoff):
				}
			}

			resp, err := provider.client.Chat(ctx, providerReq)
			if err == nil {
				if tried > 1 {
					mc.logger.Info("provider succeeded after fallback",
						"provider", provider.name,
						"model", providerReq.Model,
					)
				}
				provider.recordSuccess()
				return resp, nil
			}

			lastErr = fmt.Errorf("provider %s: %w", provider.name, err)

			if !IsRetriable(err) {
				mc.logger.Warn("non-retriable error, moving to next provider",
					"provider", provider.name,
					"error", err,
				)
				provider.recordError()
				break
			}

			mc.logger.Warn("retriable error from provider",
				"provider", provider.name,
				"attempt", attempt+1,
				"max_retries", mc.maxRetries,
				"error", err,
			)
		}

		provider.recordError()
	}

	// If all providers were unhealthy and none were tried, try them all regardless.
	if tried == 0 {
		return mc.chatForcedFallback(ctx, req)
	}

	if lastErr == nil {
		return nil, fmt.Errorf("no providers available")
	}
	return nil, fmt.Errorf("all providers failed: %w", lastErr)
}

// chatForcedFallback tries all providers once when all are marked unhealthy.
func (mc *MultiClient) chatForcedFallback(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	mc.logger.Warn("all providers unhealthy, attempting forced fallback")
	var lastErr error
	for _, provider := range mc.providers {
		providerReq := req
		providerReq.Model = mc.resolveModel(req.Model, provider)

		resp, err := provider.client.Chat(ctx, providerReq)
		if err == nil {
			provider.recordSuccess()
			mc.logger.Info("forced fallback succeeded", "provider", provider.name)
			return resp, nil
		}
		lastErr = fmt.Errorf("provider %s: %w", provider.name, err)
	}
	if lastErr == nil {
		return nil, fmt.Errorf("no providers configured")
	}
	return nil, fmt.Errorf("all providers failed (forced fallback): %w", lastErr)
}

// ChatStream sends a streaming chat completion request with provider fallback.
func (mc *MultiClient) ChatStream(ctx context.Context, req ChatRequest, onDelta func(StreamDelta)) error {
	var lastErr error
	tried := 0

	for _, provider := range mc.providers {
		provider.mu.Lock()
		healthy := provider.isHealthy(mc.cooldownDuration, mc.maxConsecErrors)
		provider.mu.Unlock()

		if !healthy {
			continue
		}

		tried++
		providerReq := req
		providerReq.Model = mc.resolveModel(req.Model, provider)

		for attempt := 0; attempt <= mc.maxRetries; attempt++ {
			if attempt > 0 {
				backoff := mc.calculateBackoff(attempt)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(backoff):
				}
			}

			err := provider.client.ChatStream(ctx, providerReq, onDelta)
			if err == nil {
				provider.recordSuccess()
				return nil
			}

			lastErr = fmt.Errorf("provider %s: %w", provider.name, err)
			if !IsRetriable(err) {
				provider.recordError()
				break
			}

			mc.logger.Warn("retriable stream error",
				"provider", provider.name,
				"attempt", attempt+1,
				"error", err,
			)
		}

		provider.recordError()
	}

	// Forced fallback when all providers are unhealthy.
	if tried == 0 {
		return mc.chatStreamForcedFallback(ctx, req, onDelta)
	}

	if lastErr == nil {
		return fmt.Errorf("no providers available")
	}
	return fmt.Errorf("all providers failed: %w", lastErr)
}

func (mc *MultiClient) chatStreamForcedFallback(ctx context.Context, req ChatRequest, onDelta func(StreamDelta)) error {
	mc.logger.Warn("all providers unhealthy, attempting forced stream fallback")
	var lastErr error
	for _, provider := range mc.providers {
		providerReq := req
		providerReq.Model = mc.resolveModel(req.Model, provider)
		err := provider.client.ChatStream(ctx, providerReq, onDelta)
		if err == nil {
			provider.recordSuccess()
			return nil
		}
		lastErr = fmt.Errorf("provider %s: %w", provider.name, err)
	}
	if lastErr == nil {
		return fmt.Errorf("no providers configured")
	}
	return fmt.Errorf("all providers failed (forced fallback): %w", lastErr)
}

func (mc *MultiClient) calculateBackoff(attempt int) time.Duration {
	backoff := float64(mc.initialBackoff) * math.Pow(2, float64(attempt-1))
	if backoff > float64(mc.maxBackoff) {
		backoff = float64(mc.maxBackoff)
	}
	// Add jitter: ±25%.
	jitter := backoff * 0.25 * (2*rand.Float64() - 1) //nolint:gosec
	return time.Duration(backoff + jitter)
}

// ProviderStatus returns the health status of all configured providers.
func (mc *MultiClient) ProviderStatus() []ProviderHealth {
	result := make([]ProviderHealth, len(mc.providers))
	for i, p := range mc.providers {
		p.mu.Lock()
		result[i] = ProviderHealth{
			Name:              p.name,
			Priority:          p.priority,
			Healthy:           p.isHealthy(mc.cooldownDuration, mc.maxConsecErrors),
			ConsecutiveErrors: p.consecutiveErrors,
			LastErrorTime:     p.lastErrorTime,
		}
		p.mu.Unlock()
	}
	return result
}
