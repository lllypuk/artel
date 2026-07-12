package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lllypuk/artel/internal/config"
	"github.com/lllypuk/artel/internal/news"
	"github.com/lllypuk/artel/internal/storage"
	"github.com/lllypuk/artel/internal/tools"
)

func setupNews(
	ctx context.Context,
	cfg *config.Config,
	db *storage.DB,
	registry *tools.Registry,
	logger *slog.Logger,
) (*tools.NewsTool, *news.NewsManager) {
	newsRepo := storage.NewNewsRepository(db)

	if err := initFeeds(ctx, cfg, newsRepo, logger); err != nil {
		logger.Error("init feeds", "error", err)
	}

	parser := news.NewGofeedParser()

	timeout := time.Duration(cfg.News.FetchTimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	fetcher := news.NewHTTPFetcher(timeout)

	newsManager := news.NewNewsManager(newsRepo, parser, fetcher, cfg.News.Proxy, logger)
	nt := tools.NewNewsTool(newsManager)
	registry.Register(nt)

	logger.Info("News tool registered")
	return nt, newsManager
}

func initFeeds(ctx context.Context, cfg *config.Config, newsRepo storage.NewsRepository, logger *slog.Logger) error {
	dbFeeds, err := newsRepo.ListFeeds(ctx, 1000, 0)
	if err != nil {
		return fmt.Errorf("list feeds from database: %w", err)
	}

	dbFeedsMap := make(map[string]*news.Feed, len(dbFeeds))
	for i := range dbFeeds {
		dbFeedsMap[dbFeeds[i].URL] = &dbFeeds[i]
	}

	configFeedsMap := make(map[string]*config.FeedConfig, len(cfg.News.Feeds))
	for i := range cfg.News.Feeds {
		configFeedsMap[cfg.News.Feeds[i].URL] = &cfg.News.Feeds[i]
	}

	changes := &feedChanges{}

	for i := range cfg.News.Feeds {
		feedCfg := &cfg.News.Feeds[i]
		existing, existsInDB := dbFeedsMap[feedCfg.URL]
		feedType := determineFeedType(feedCfg.URL)

		if !existsInDB {
			if addFeed(ctx, newsRepo, *feedCfg, feedType) {
				changes.add(feedCfg.Title, feedCfg.URL)
			}
		} else {
			if updateFeedIfNeeded(ctx, newsRepo, existing, *feedCfg) {
				changes.update(feedCfg.Title, feedCfg.URL)
			}
		}
	}

	for _, dbFeed := range dbFeeds {
		if _, existsInConfig := configFeedsMap[dbFeed.URL]; !existsInConfig {
			if deleteFeed(ctx, newsRepo, dbFeed) {
				changes.del(dbFeed.Title, dbFeed.URL)
			}
		}
	}

	changes.log(logger, len(dbFeeds))
	return nil
}

type feedChanges struct {
	added   []string
	updated []string
	deleted []string
}

func (c *feedChanges) add(title, url string) {
	c.added = append(c.added, fmt.Sprintf("%s (%s)", title, url))
}

func (c *feedChanges) update(title, url string) {
	c.updated = append(c.updated, fmt.Sprintf("%s (%s)", title, url))
}

func (c *feedChanges) del(title, url string) {
	c.deleted = append(c.deleted, fmt.Sprintf("%s (%s)", title, url))
}

func (c *feedChanges) log(logger *slog.Logger, totalFeeds int) {
	for _, f := range c.added {
		logger.Info("feed added", "feed", f)
	}
	for _, f := range c.updated {
		logger.Info("feed updated", "feed", f)
	}
	for _, f := range c.deleted {
		logger.Info("feed deleted", "feed", f)
	}
	if len(c.added)+len(c.updated)+len(c.deleted) == 0 {
		logger.Info("feeds configuration is up to date", "total", totalFeeds)
	}
}

func determineFeedType(url string) news.FeedType {
	switch {
	case strings.Contains(url, ".atom"):
		return news.FeedTypeAtom
	case strings.Contains(url, ".json"):
		return news.FeedTypeJSON
	default:
		return news.FeedTypeRSS
	}
}

func addFeed(ctx context.Context, repo storage.NewsRepository, feedCfg config.FeedConfig, feedType news.FeedType) bool {
	feed := &news.Feed{
		URL:           feedCfg.URL,
		Title:         feedCfg.Title,
		Description:   feedCfg.Title,
		Type:          feedType,
		Category:      feedCfg.Category,
		Language:      feedCfg.Language,
		Enabled:       feedCfg.Enabled,
		ProxyURL:      feedCfg.Proxy,
		FetchInterval: time.Hour,
	}
	_, err := repo.CreateFeed(ctx, feed)
	return err == nil
}

func updateFeedIfNeeded(ctx context.Context, repo storage.NewsRepository, existing *news.Feed, feedCfg config.FeedConfig) bool {
	needUpdate := existing.Title != feedCfg.Title ||
		existing.Category != feedCfg.Category ||
		existing.Language != feedCfg.Language ||
		existing.Enabled != feedCfg.Enabled ||
		existing.ProxyURL != feedCfg.Proxy

	if !needUpdate {
		return false
	}

	existing.Title = feedCfg.Title
	existing.Description = feedCfg.Title
	existing.Category = feedCfg.Category
	existing.Language = feedCfg.Language
	existing.Enabled = feedCfg.Enabled
	existing.ProxyURL = feedCfg.Proxy

	return repo.UpdateFeed(ctx, existing) == nil
}

func deleteFeed(ctx context.Context, repo storage.NewsRepository, feed news.Feed) bool {
	return repo.DeleteFeed(ctx, feed.ID) == nil
}

func fetchNewsLoop(ctx context.Context, manager *news.NewsManager, logger *slog.Logger) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	if err := manager.FetchAllFeeds(ctx); err != nil {
		logger.Error("initial fetch all feeds", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("news fetch loop stopped")
			return
		case <-ticker.C:
			if err := manager.FetchAllFeeds(ctx); err != nil {
				logger.Error("fetch all feeds", "error", err)
			}
		}
	}
}
