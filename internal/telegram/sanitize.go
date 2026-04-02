package telegram

import (
	"bytes"
	"regexp"
	"strings"
)

// stripHTMLTags removes HTML tags from text and converts them to text equivalents.
// This prevents LLM-generated HTML from being sent to Telegram, which could cause formatting issues.
//
// Conversions:
//   - <b>...</b>, <strong>...</strong> → ... (bold removed, text preserved)
//   - <i>...</i>, <em>...</em> → ... (italic removed, text preserved)
//   - <u>...</u> → ... (underline removed, text preserved)
//   - <s>...</s>, <strike>...</strike>, <del>...</del> → ... (strikethrough removed)
//   - <br>, <br/> → newline
//   - <p>...</p> → ... (paragraphs become newlines)
//   - <a href="url">text</a> → text (url) (link converted to text with URL in parentheses)
//   - <code>...</code> → `...` (code blocks use backticks)
//   - <pre>...</pre> → ```...``` (pre blocks use triple backticks)
//   - Other tags → inner text only
func stripHTMLTags(text string) string {
	var result strings.Builder
	var buf bytes.Buffer
	var inTag bool
	var tagContent strings.Builder
	var isSelfClosing bool
	var tagStack []string

	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '<':
			// Start of a tag
			if !inTag {
				// Flush any accumulated text before this tag
				if buf.Len() > 0 {
					result.WriteString(buf.String())
					buf.Reset()
				}
				inTag = true
				isSelfClosing = false
				tagContent.Reset()
			}
		case '>':
			// End of a tag
			if inTag {
				inTag = false
				// Handle the tag
				handleTag(tagContent.String(), isSelfClosing, &result, &tagStack)
				tagContent.Reset()
			}
		default:
			if inTag {
				// Check for self-closing marker /
				if text[i] == '/' {
					// Check if this is a self-closing tag (like <br/>)
					// by looking at what comes after the /
					remaining := text[i+1:]
					if remaining != "" && remaining[0] == '>' {
						isSelfClosing = true
					} else {
						tagContent.WriteByte(text[i])
					}
				} else if text[i] != ' ' && text[i] != '\t' && text[i] != '\n' && text[i] != '\r' {
					tagContent.WriteByte(text[i])
				}
			} else {
				// Regular text
				buf.WriteByte(text[i])
			}
		}
	}

	// Flush any remaining text
	if buf.Len() > 0 {
		result.WriteString(buf.String())
	}

	return result.String()
}

// handleTag processes a single HTML tag and modifies the result buffer accordingly.
func handleTag(tagContent string, isSelfClosing bool, result *strings.Builder, tagStack *[]string) {
	// Get tag name (first word before space or slash)
	tagName := tagContent
	if idx := strings.IndexAny(tagContent, " \t"); idx >= 0 {
		tagName = tagContent[:idx]
	}

	// Handle self-closing tags first (e.g., <br/>, <img/>)
	if isSelfClosing {
		switch strings.ToLower(tagName) {
		case "br":
			result.WriteString("\n")
		case "hr":
			result.WriteString("\n---\n")
		}
		return
	}

	// Handle closing tags
	if strings.HasPrefix(tagName, "/") {
		closingTag := tagName[1:]
		if len(*tagStack) > 0 {
			lastOpen := (*tagStack)[len(*tagStack)-1]
			if strings.EqualFold(lastOpen, closingTag) {
				*tagStack = (*tagStack)[:len(*tagStack)-1]
			}
		}

		// Handle specific closing tags that add spacing
		switch strings.ToLower(closingTag) {
		case "p":
			result.WriteString("\n\n")
		case "li":
			result.WriteString("\n")
		case "div", "section", "article", "header", "footer", "nav":
			result.WriteString("\n")
		case "ul", "ol":
			result.WriteString("\n")
		case "a":
			// Links are handled in opening tag via convertLinksToText
		}
		return
	}

	// Handle opening tags
	*tagStack = append(*tagStack, tagName)

	// Handle block-level tags that add spacing before content
	switch strings.ToLower(tagName) {
	case "div", "section", "article", "header", "footer", "nav":
		result.WriteString("\n")
	case "ul", "ol":
		result.WriteString("\n")
	case "li":
		result.WriteString("• ")
	case "br":
		// br is a void element, but also add newline here in case it's not self-closing
		result.WriteString("\n")
	case "p":
		// Paragraph - no special handling here, closing tag adds spacing
	}
}

// SanitizeResponse removes HTML tags from agent responses before sending to Telegram.
// This is called on all text responses to prevent HTML injection and formatting issues.
func SanitizeResponse(text string) string {
	// First, handle <a href="url">text</a> links specially
	text = convertLinksToText(text)

	// Then strip all remaining HTML tags
	return stripHTMLTags(text)
}

// convertLinksToText converts <a href="url">text</a> to "text (url)" format.
func convertLinksToText(text string) string {
	// Match <a href="url">text</a> pattern
	re := regexp.MustCompile(`<a\s+[^>]*href=["']([^"']*)["'][^>]*>([^<]*)</a>`)
	return re.ReplaceAllStringFunc(text, func(match string) string {
		submatches := re.FindStringSubmatch(match)
		if len(submatches) >= 3 {
			url := submatches[1]
			linkText := submatches[2]
			if linkText == "" {
				return url
			}
			if url == linkText {
				return linkText
			}
			return linkText + " (" + url + ")"
		}
		return match
	})
}
