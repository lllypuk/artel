package telegram

import (
	"testing"
)

func TestMarkdownToHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "bold",
			input: "**жирный текст**",
			want:  "<b>жирный текст</b>",
		},
		{
			name:  "italic",
			input: "*курсив*",
			want:  "<i>курсив</i>",
		},
		{
			name:  "inline code",
			input: "`код`",
			want:  "<code>код</code>",
		},
		{
			name:  "h2 header",
			input: "## Заголовок",
			want:  "<b>Заголовок</b>",
		},
		{
			name:  "h1 header",
			input: "# Заголовок",
			want:  "<b>Заголовок</b>",
		},
		{
			name:  "h3 header",
			input: "### Заголовок",
			want:  "<b>Заголовок</b>",
		},
		{
			name:  "horizontal rule",
			input: "---",
			want:  "",
		},
		{
			name:  "link",
			input: "[текст](https://example.com)",
			want:  `<a href="https://example.com">текст</a>`,
		},
		{
			name:  "html escape in text",
			input: "a & b < c > d",
			want:  "a &amp; b &lt; c &gt; d",
		},
		{
			name:  "code block",
			input: "```\nfmt.Println(\"hello\")\n```",
			want:  "<pre>fmt.Println(\"hello\")</pre>",
		},
		{
			name:  "bold with inline code",
			input: "**bold** and `code`",
			want:  "<b>bold</b> and <code>code</code>",
		},
		{
			name:  "mixed line",
			input: "1. **Manchester Evening News** — RSS: `https://example.com`",
			want:  "1. <b>Manchester Evening News</b> — RSS: <code>https://example.com</code>",
		},
		{
			name:  "plain text unchanged",
			input: "обычный текст без форматирования",
			want:  "обычный текст без форматирования",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := markdownToHTML(tt.input)
			if got != tt.want {
				t.Errorf("markdownToHTML(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.want)
			}
		})
	}
}
