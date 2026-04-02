package telegram

import (
	"testing"
)

func TestSanitizeResponse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text unchanged",
			input: "обычный текст без форматирования",
			want:  "обычный текст без форматирования",
		},
		{
			name:  "bold tag removed",
			input: "<b>жирный текст</b>",
			want:  "жирный текст",
		},
		{
			name:  "strong tag removed",
			input: "<strong>жирный текст</strong>",
			want:  "жирный текст",
		},
		{
			name:  "italic tag removed",
			input: "<i>курсив</i>",
			want:  "курсив",
		},
		{
			name:  "em tag removed",
			input: "<em>курсив</em>",
			want:  "курсив",
		},
		{
			name:  "underline tag removed",
			input: "<u>подчёркнутый</u>",
			want:  "подчёркнутый",
		},
		{
			name:  "strikethrough s tag removed",
			input: "<s>зачёркнутый</s>",
			want:  "зачёркнутый",
		},
		{
			name:  "strikethrough strike tag removed",
			input: "<strike>зачёркнутый</strike>",
			want:  "зачёркнутый",
		},
		{
			name:  "strikethrough del tag removed",
			input: "<del>зачёркнутый</del>",
			want:  "зачёркнутый",
		},
		{
			name:  "br tag converts to newline",
			input: "строка 1<br>строка 2",
			want:  "строка 1\nстрока 2",
		},
		{
			name:  "br slash tag converts to newline",
			input: "строка 1<br/>строка 2",
			want:  "строка 1\nстрока 2",
		},
		{
			name:  "paragraph adds spacing",
			input: "<p>параграф 1</p><p>параграф 2</p>",
			want:  "параграф 1\n\nпараграф 2\n\n",
		},
		{
			name:  "link with text and url",
			input: `<a href="https://example.com">ссылка</a>`,
			want:  "ссылка (https://example.com)",
		},
		{
			name:  "link with same text as url",
			input: `<a href="https://example.com">https://example.com</a>`,
			want:  "https://example.com",
		},
		{
			name:  "link with empty text",
			input: `<a href="https://example.com"></a>`,
			want:  "https://example.com",
		},
		{
			name:  "multiple links",
			input: `Смотрите <a href="https://site1.com">сайт 1</a> и <a href="https://site2.com">сайт 2</a>`,
			want:  "Смотрите сайт 1 (https://site1.com) и сайт 2 (https://site2.com)",
		},
		{
			name:  "code tag preserved",
			input: "Используйте <code>git clone</code> для клонирования",
			want:  "Используйте git clone для клонирования",
		},
		{
			name:  "pre tag preserved",
			input: "<pre>const x = 5;</pre>",
			want:  "const x = 5;",
		},
		{
			name:  "nested tags",
			input: "<b>жирный <i>и курсив</i> вместе</b>",
			want:  "жирный и курсив вместе",
		},
		{
			name:  "div adds newline",
			input: "текст<div>новый блок</div>",
			want:  "текст\nновый блок\n",
		},
		{
			name:  "ul list",
			input: "<ul><li>пункт 1</li><li>пункт 2</li></ul>",
			want:  "\n• пункт 1\n• пункт 2\n\n",
		},
		{
			name:  "ol list",
			input: "<ol><li>пункт 1</li><li>пункт 2</li></ol>",
			want:  "\n• пункт 1\n• пункт 2\n\n",
		},
		{
			name:  "section tag",
			input: "<section>секция</section>",
			want:  "\nсекция\n",
		},
		{
			name:  "article tag",
			input: "<article>статья</article>",
			want:  "\nстатья\n",
		},
		{
			name:  "header tag",
			input: "<header>заголовок</header>",
			want:  "\nзаголовок\n",
		},
		{
			name:  "footer tag",
			input: "<footer>футер</footer>",
			want:  "\nфутер\n",
		},
		{
			name:  "nav tag",
			input: "<nav>навигация</nav>",
			want:  "\nнавигация\n",
		},
		{
			name:  "span tag removed only",
			input: "<span>текст в span</span>",
			want:  "текст в span",
		},
		{
			name:  "unknown tag removed",
			input: "<custom>текст</custom>",
			want:  "текст",
		},
		{
			name:  "mixed html and plain text",
			input: "Обычный текст <b>жирный</b> и снова обычный",
			want:  "Обычный текст жирный и снова обычный",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only whitespace",
			input: "   \n\t   ",
			want:  "   \n\t   ",
		},
		{
			name:  "tags with attributes",
			input: `<a href="https://example.com" class="link" target="_blank">ссылка</a>`,
			want:  "ссылка (https://example.com)",
		},
		{
			name:  "link with query params",
			input: `<a href="https://example.com?param=value">ссылка</a>`,
			want:  "ссылка (https://example.com?param=value)",
		},
		{
			name:  "multiple br tags",
			input: "строка 1<br><br><br>строка 2",
			want:  "строка 1\n\n\nстрока 2",
		},
		{
			name:  "malformed tag without closing",
			input: "<b>жирный текст",
			want:  "жирный текст", // Malformed tags: unclosed opening tag text is extracted
		},
		{
			name:  "self-closing div-like tags",
			input: "текст<div/>текст",
			want:  "тексттекст", // Self-closing non-void tags are treated as void, no spacing
		},
		{
			name:  "complex nested structure",
			input: `<div><p><b>Заголовок</b></p><ul><li><a href="url">ссылка</a></li></ul></div>`,
			want:  "\nЗаголовок\n\n\n• ссылка (url)\n\n\n",
		},
		{
			name:  "HTML entities in text",
			input: "a &lt; b &gt; c &amp; d",
			want:  "a &lt; b &gt; c &amp; d",
		},
		{
			name:  "h1-h6 tags",
			input: "<h1>h1</h1><h2>h2</h2><h3>h3</h3>",
			want:  "h1h2h3",
		},
		{
			name:  "blockquote tag",
			input: "<blockquote>цитата</blockquote>",
			want:  "цитата",
		},
		{
			name:  "table tags removed",
			input: "<table><tr><td>ячейка</td></tr></table>",
			want:  "ячейка",
		},
		{
			name:  "img tag alt text only",
			input: `<img src="image.jpg" alt="изображение">`,
			want:  "", // img has no content, alt is an attribute not extracted
		},
		{
			name:  "code and pre together",
			input: "<pre><code>const x = 5;</code></pre>",
			want:  "const x = 5;",
		},
		{
			name:  "line breaks and paragraphs",
			input: "<p>Параграф 1</p><p>Параграф 2</p>",
			want:  "Параграф 1\n\nПараграф 2\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeResponse(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeResponse(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestConvertLinksToText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple link",
			input: `<a href="https://example.com">ссылка</a>`,
			want:  "ссылка (https://example.com)",
		},
		{
			name:  "link with same text as url",
			input: `<a href="https://example.com">https://example.com</a>`,
			want:  "https://example.com",
		},
		{
			name:  "multiple links",
			input: `link1 <a href="url1">text1</a> link2 <a href="url2">text2</a>`,
			want:  "link1 text1 (url1) link2 text2 (url2)",
		},
		{
			name:  "no links",
			input: "обычный текст",
			want:  "обычный текст",
		},
		{
			name:  "link with query params",
			input: `<a href="https://example.com?q=test">ссылка</a>`,
			want:  "ссылка (https://example.com?q=test)",
		},
		{
			name:  "link with attributes",
			input: `<a href="url" class="cls" target="_blank">текст</a>`,
			want:  "текст (url)",
		},
		{
			name:  "malformed link - no href",
			input: `<a>текст</a>`,
			want:  `<a>текст</a>`, // Not a valid link pattern
		},
		{
			name:  "malformed link - unclosed",
			input: `<a href="url">текст`,
			want:  `<a href="url">текст`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertLinksToText(tt.input)
			if got != tt.want {
				t.Errorf("convertLinksToText(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text unchanged",
			input: "обычный текст",
			want:  "обычный текст",
		},
		{
			name:  "single tag",
			input: "<b>текст</b>",
			want:  "текст",
		},
		{
			name:  "multiple tags",
			input: "<b>жирный</b> и <i>курсив</i>",
			want:  "жирный и курсив",
		},
		{
			name:  "nested tags",
			input: "<b><i>текст</i></b>",
			want:  "текст",
		},
		{
			name:  "br to newline",
			input: "a<br>b",
			want:  "a\nb",
		},
		{
			name:  "paragraphs",
			input: "<p>a</p><p>b</p>",
			want:  "a\n\nb\n\n",
		},
		{
			name:  "list items",
			input: "<ul><li>a</li><li>b</li></ul>",
			want:  "\n• a\n• b\n\n",
		},
		{
			name:  "div tags",
			input: "<div>a</div><div>b</div>",
			want:  "\na\n\nb\n",
		},
		{
			name:  "empty tags",
			input: "<span></span>",
			want:  "",
		},
		{
			name:  "tags with attributes",
			input: `<a href="url">текст</a>`,
			want:  "текст",
		},
		{
			name:  "mixed content",
			input: "text <b>bold</b> text <i>italic</i> text",
			want:  "text bold text italic text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTMLTags(tt.input)
			if got != tt.want {
				t.Errorf("stripHTMLTags(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.want)
			}
		})
	}
}
