package telegram

import (
	"strings"
)

// markdownToHTML converts standard LLM markdown to Telegram HTML format.
//
// Supported:
//   - **bold** → <b>bold</b>
//   - *italic* → <i>italic</i>
//   - `inline code` → <code>inline code</code>
//   - ```code block``` → <pre>code block</pre>
//   - # / ## / ### heading → <b>heading</b>
//   - [text](url) → <a href="url">text</a>
//   - --- → (empty line)
//   - &, <, > are HTML-escaped in plain text regions
func markdownToHTML(text string) string {
	lines := strings.Split(text, "\n")
	var result []string
	inCodeBlock := false
	var codeLines []string

	for _, line := range lines {
		// ── code block boundary ──
		if strings.HasPrefix(line, "```") {
			if inCodeBlock {
				content := strings.Join(codeLines, "\n")
				result = append(result, "<pre>"+escapeHTML(content)+"</pre>")
				codeLines = nil
				inCodeBlock = false
			} else {
				inCodeBlock = true
			}
			continue
		}
		if inCodeBlock {
			codeLines = append(codeLines, line)
			continue
		}

		// ── headers ──
		if strings.HasPrefix(line, "### ") {
			result = append(result, "<b>"+inlineToHTML(line[4:])+"</b>")
			continue
		}
		if strings.HasPrefix(line, "## ") {
			result = append(result, "<b>"+inlineToHTML(line[3:])+"</b>")
			continue
		}
		if strings.HasPrefix(line, "# ") {
			result = append(result, "<b>"+inlineToHTML(line[2:])+"</b>")
			continue
		}

		// ── horizontal rule ──
		if line == "---" || line == "***" || line == "___" {
			result = append(result, "")
			continue
		}

		// ── normal line ──
		result = append(result, inlineToHTML(line))
	}

	// unclosed code block
	if inCodeBlock && len(codeLines) > 0 {
		result = append(result, "<pre>"+escapeHTML(strings.Join(codeLines, "\n"))+"</pre>")
	}

	return strings.Join(result, "\n")
}

// inlineToHTML converts inline markdown constructs to HTML within a single line.
// Processes: **bold**, *italic*, `code`, [text](url).
// Everything else is HTML-escaped.
func inlineToHTML(text string) string {
	var sb strings.Builder
	i := 0
	for i < len(text) {
		if n, s := tryInlineCode(text, i); n > 0 {
			sb.WriteString(s)
			i += n
			continue
		}
		if n, s := tryBold(text, i); n > 0 {
			sb.WriteString(s)
			i += n
			continue
		}
		if n, s := tryItalic(text, i); n > 0 {
			sb.WriteString(s)
			i += n
			continue
		}
		if n, s := tryLink(text, i); n > 0 {
			sb.WriteString(s)
			i += n
			continue
		}
		// plain character — HTML-escape it
		switch text[i] {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		default:
			sb.WriteByte(text[i])
		}
		i++
	}
	return sb.String()
}

// tryInlineCode matches `...` at position i. Returns (consumed bytes, HTML) or (0, "").
func tryInlineCode(text string, i int) (consumed int, html string) {
	if text[i] != '`' {
		return 0, ""
	}
	end := strings.Index(text[i+1:], "`")
	if end < 0 {
		return 0, ""
	}
	return end + 2, "<code>" + escapeHTML(text[i+1:i+1+end]) + "</code>"
}

// tryBold matches **...** at position i. Returns (consumed bytes, HTML) or (0, "").
func tryBold(text string, i int) (consumed int, html string) {
	if i+1 >= len(text) || text[i] != '*' || text[i+1] != '*' {
		return 0, ""
	}
	end := strings.Index(text[i+2:], "**")
	if end < 0 {
		return 0, ""
	}
	return end + 4, "<b>" + inlineToHTML(text[i+2:i+2+end]) + "</b>"
}

// tryItalic matches *...* (but not **) at position i. Returns (consumed bytes, HTML) or (0, "").
func tryItalic(text string, i int) (consumed int, html string) {
	if text[i] != '*' {
		return 0, ""
	}
	if i+1 < len(text) && text[i+1] == '*' {
		return 0, "" // it's **bold**, not italic
	}
	end := strings.Index(text[i+1:], "*")
	if end < 0 {
		return 0, ""
	}
	closeIdx := i + 1 + end
	if closeIdx+1 < len(text) && text[closeIdx+1] == '*' {
		return 0, "" // closing * is actually part of **
	}
	return end + 2, "<i>" + inlineToHTML(text[i+1:closeIdx]) + "</i>"
}

// tryLink matches [text](url) at position i. Returns (consumed bytes, HTML) or (0, "").
func tryLink(text string, i int) (consumed int, html string) {
	if text[i] != '[' {
		return 0, ""
	}
	closeBracket := strings.Index(text[i+1:], "]")
	if closeBracket < 0 {
		return 0, ""
	}
	afterClose := i + 1 + closeBracket + 1
	if afterClose >= len(text) || text[afterClose] != '(' {
		return 0, ""
	}
	closeParen := strings.Index(text[afterClose+1:], ")")
	if closeParen < 0 {
		return 0, ""
	}
	linkText := text[i+1 : i+1+closeBracket]
	url := text[afterClose+1 : afterClose+1+closeParen]
	consumed = afterClose + 1 + closeParen + 1 - i
	return consumed, `<a href="` + escapeHTML(url) + `">` + escapeHTML(linkText) + `</a>`
}

// escapeHTML escapes &, <, > for use inside HTML tags.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
