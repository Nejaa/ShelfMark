package metadata

import (
	"log/slog"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"golang.org/x/net/html"
)

// frontBack bounds sampled text without cutting through UTF-8 sequences.
func frontBack(value string, front, back int) string {
	if len(value) <= front+back {
		return value
	}

	frontEnd := min(front, len(value))
	for frontEnd < len(value) && !utf8.RuneStart(value[frontEnd]) {
		frontEnd++
	}

	backStart := max(0, len(value)-back)
	for backStart < len(value) && !utf8.RuneStart(value[backStart]) {
		backStart++
	}

	return value[:frontEnd] + "\n" + value[backStart:]
}

func htmlText(source string) string {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return ""
	}

	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return strings.Join(strings.Fields(b.String()), " ")
}

// pdfText samples opening and closing pages, deduplicating overlapping ranges.
func pdfText(path string) string {
	file, reader, err := pdf.Open(path)
	if err != nil {
		slog.Warn("PDF content inspection unavailable", "path", path, "error", err)
		return ""
	}

	defer func() { _ = file.Close() }()
	total := reader.NumPage()
	pages := map[int]bool{}
	for i := 1; i <= min(total, 20); i++ {
		pages[i] = true
	}

	for i := max(1, total-9); i <= total; i++ {
		pages[i] = true
	}

	indices := make([]int, 0, len(pages))
	for i := range pages {
		indices = append(indices, i)
	}

	sort.Ints(indices)
	var b strings.Builder
	for _, i := range indices {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}

		text, err := page.GetPlainText(nil)
		if err != nil {
			slog.Warn("PDF page text could not be extracted; skipping page", "path", path, "page", i, "error", err)
		}
		if err == nil {
			b.WriteString(text)
			b.WriteByte('\n')
		}
	}

	if b.Len() == 0 {
		slog.Warn("PDF has no extractable text for matching", "path", path, "hint", "use a PDF with a text layer; OCR of PDF pages is not supported")
	}
	return frontBack(b.String(), 200000, 50000)
}
