package news_test

import (
	"testing"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/news"
)

// ── Sample feeds ──────────────────────────────────────────────────────────────

const sampleRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Feed</title>
    <link>https://example.com</link>
    <description>Test RSS Feed</description>
    <item>
      <title>First Article</title>
      <link>https://example.com/article/1</link>
      <description>Short description</description>
      <pubDate>Mon, 01 Jan 2024 12:00:00 GMT</pubDate>
      <guid>guid-001</guid>
      <category>Technology</category>
      <author>Jane Doe</author>
    </item>
    <item>
      <title>Second Article</title>
      <link>https://example.com/article/2</link>
      <description>Second description</description>
      <pubDate>Tue, 02 Jan 2024 12:00:00 GMT</pubDate>
    </item>
  </channel>
</rss>`

const sampleAtom = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Test Atom Feed</title>
  <entry>
    <title>Atom Article</title>
    <link href="https://example.com/atom/1"/>
    <id>atom-guid-001</id>
    <summary>Atom description</summary>
    <updated>2024-01-01T12:00:00Z</updated>
    <author><name>John Doe</name></author>
    <category term="Science"/>
  </entry>
</feed>`

const rssWithContent = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">
  <channel>
    <title>T</title>
    <item>
      <title>Content Article</title>
      <link>https://example.com/c</link>
      <description>Short desc</description>
      <content:encoded>Full article content here</content:encoded>
      <guid>g-content</guid>
      <pubDate>Mon, 01 Jan 2024 12:00:00 GMT</pubDate>
    </item>
  </channel>
</rss>`

const rssNoGUID = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>T</title>
    <item>
      <title>No GUID Article</title>
      <link>https://example.com/no-guid</link>
      <description>desc</description>
    </item>
  </channel>
</rss>`

const rssNoContent = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>T</title>
    <item>
      <title>Only Description</title>
      <link>https://example.com/1</link>
      <description>The description text</description>
      <guid>g1</guid>
    </item>
  </channel>
</rss>`

const emptyRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Empty</title></channel></rss>`

// ── Parse RSS ─────────────────────────────────────────────────────────────────

func TestGofeedParser_Parse_RSS_ItemCount(t *testing.T) {
	p := news.NewGofeedParser()
	items, err := p.Parse([]byte(sampleRSS), news.FeedTypeRSS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestGofeedParser_Parse_RSS_Fields(t *testing.T) {
	p := news.NewGofeedParser()
	items, err := p.Parse([]byte(sampleRSS), news.FeedTypeRSS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	first := items[0]
	if first.Title != "First Article" {
		t.Errorf("expected title 'First Article', got %q", first.Title)
	}
	if first.Link != "https://example.com/article/1" {
		t.Errorf("unexpected link: %q", first.Link)
	}
	if first.GUID != "guid-001" {
		t.Errorf("expected GUID 'guid-001', got %q", first.GUID)
	}
	if first.Category != "Technology" {
		t.Errorf("expected category 'Technology', got %q", first.Category)
	}
	if first.Description == "" {
		t.Error("expected non-empty description")
	}
}

func TestGofeedParser_Parse_RSS_Author(t *testing.T) {
	p := news.NewGofeedParser()
	items, err := p.Parse([]byte(sampleRSS), news.FeedTypeRSS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items[0].Author != "Jane Doe" {
		t.Errorf("expected author 'Jane Doe', got %q", items[0].Author)
	}
	// Second item has no author.
	if items[1].Author != "" {
		t.Errorf("expected empty author for second item, got %q", items[1].Author)
	}
}

func TestGofeedParser_Parse_RSS_PublishedAt(t *testing.T) {
	p := news.NewGofeedParser()
	items, err := p.Parse([]byte(sampleRSS), news.FeedTypeRSS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	if !items[0].PublishedAt.Equal(expected) {
		t.Errorf("expected published at %v, got %v", expected, items[0].PublishedAt)
	}
}

// ── Parse Atom ────────────────────────────────────────────────────────────────

func TestGofeedParser_Parse_Atom(t *testing.T) {
	p := news.NewGofeedParser()
	items, err := p.Parse([]byte(sampleAtom), news.FeedTypeAtom)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.Title != "Atom Article" {
		t.Errorf("expected title 'Atom Article', got %q", item.Title)
	}
	if item.Link != "https://example.com/atom/1" {
		t.Errorf("unexpected link: %q", item.Link)
	}
	if item.GUID != "atom-guid-001" {
		t.Errorf("expected GUID 'atom-guid-001', got %q", item.GUID)
	}
	if item.Author != "John Doe" {
		t.Errorf("expected author 'John Doe', got %q", item.Author)
	}
	if item.Category != "Science" {
		t.Errorf("expected category 'Science', got %q", item.Category)
	}
}

// ── content fallback ──────────────────────────────────────────────────────────

func TestGofeedParser_Parse_ContentFallback_ToDescription(t *testing.T) {
	p := news.NewGofeedParser()
	items, err := p.Parse([]byte(rssNoContent), news.FeedTypeRSS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	// When no content:encoded, Content should fall back to Description.
	if items[0].Content != "The description text" {
		t.Errorf("expected content to fall back to description, got %q", items[0].Content)
	}
}

func TestGofeedParser_Parse_ContentEncoded_Priority(t *testing.T) {
	p := news.NewGofeedParser()
	items, err := p.Parse([]byte(rssWithContent), news.FeedTypeRSS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	// content:encoded should take priority over description.
	if items[0].Content != "Full article content here" {
		t.Errorf("unexpected content: %q", items[0].Content)
	}
}

// ── GUID fallback ─────────────────────────────────────────────────────────────

func TestGofeedParser_Parse_GUID_Fallback_ToLink(t *testing.T) {
	p := news.NewGofeedParser()
	items, err := p.Parse([]byte(rssNoGUID), news.FeedTypeRSS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].GUID != "https://example.com/no-guid" {
		t.Errorf("expected GUID to fall back to link, got %q", items[0].GUID)
	}
}

// ── empty feed ────────────────────────────────────────────────────────────────

func TestGofeedParser_Parse_EmptyFeed(t *testing.T) {
	p := news.NewGofeedParser()
	items, err := p.Parse([]byte(emptyRSS), news.FeedTypeRSS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

// ── error cases ───────────────────────────────────────────────────────────────

func TestGofeedParser_Parse_InvalidData(t *testing.T) {
	p := news.NewGofeedParser()
	_, err := p.Parse([]byte("this is not valid xml or json feed data"), news.FeedTypeRSS)
	if err == nil {
		t.Fatal("expected error for invalid feed data")
	}
}

func TestGofeedParser_Parse_EmptyInput(t *testing.T) {
	p := news.NewGofeedParser()
	_, err := p.Parse([]byte(""), news.FeedTypeRSS)
	if err == nil {
		t.Fatal("expected error for empty feed data")
	}
}

// ── FeedType parameter is ignored (gofeed auto-detects) ─────────────────────

func TestGofeedParser_Parse_FeedTypeParamIsIgnored(t *testing.T) {
	p := news.NewGofeedParser()
	// Passing FeedTypeAtom while feeding RSS XML should still work because
	// gofeed auto-detects the format.
	items, err := p.Parse([]byte(sampleRSS), news.FeedTypeAtom)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}
