package news

import (
	"fmt"
	"time"

	"github.com/mmcdole/gofeed"
)

// Parser parses feed data into Items.
type Parser interface {
	Parse(data []byte, feedType FeedType) ([]Item, error)
}

// GofeedParser wraps gofeed parser.
type GofeedParser struct {
	parser *gofeed.Parser
}

// NewGofeedParser creates a new GofeedParser.
func NewGofeedParser() *GofeedParser {
	return &GofeedParser{
		parser: gofeed.NewParser(),
	}
}

// Parse parses feed data and returns items.
func (p *GofeedParser) Parse(data []byte, _ FeedType) ([]Item, error) {
	feed, err := p.parser.ParseString(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}

	items := make([]Item, 0, len(feed.Items))
	for _, fi := range feed.Items {
		pubDate := time.Time{}
		if fi.PublishedParsed != nil {
			pubDate = *fi.PublishedParsed
		} else if fi.UpdatedParsed != nil {
			pubDate = *fi.UpdatedParsed
		}

		var category string
		if len(fi.Categories) > 0 {
			category = fi.Categories[0]
		}

		content := fi.Content
		if content == "" {
			content = fi.Description
		}

		guid := fi.GUID
		if guid == "" {
			guid = fi.Link
		}

		items = append(items, Item{
			GUID:        guid,
			Title:       fi.Title,
			Link:        fi.Link,
			Description: fi.Description,
			Content:     content,
			Author:      getAuthor(fi),
			Category:    category,
			PublishedAt: pubDate,
			ImageURL:    getImage(fi),
		})
	}

	return items, nil
}

// getAuthor extracts author name from item.
func getAuthor(item *gofeed.Item) string {
	if item.Author != nil {
		return item.Author.Name
	}
	return ""
}

// getImage extracts image URL from item.
func getImage(item *gofeed.Item) string {
	if item.Image != nil {
		return item.Image.URL
	}
	return ""
}
