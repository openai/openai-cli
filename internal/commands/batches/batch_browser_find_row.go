package batches

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

// The catalog and row finder use the same streaming, case-insensitive literal match.
type batchBrowserPattern struct {
	needle []rune
	prefix []int
}

func newBatchBrowserPattern(query string) batchBrowserPattern {
	needle := []rune(strings.ToLower(query))
	prefix := make([]int, len(needle))
	for i, length := 1, 0; i < len(needle); i++ {
		for length > 0 && needle[i] != needle[length] {
			length = prefix[length-1]
		}
		if needle[i] == needle[length] {
			length++
		}
		prefix[i] = length
	}
	return batchBrowserPattern{needle, prefix}
}

func (p batchBrowserPattern) advance(value rune, matched *int) bool {
	value = unicode.ToLower(value)
	n := *matched
	for n > 0 && value != p.needle[n] {
		n = p.prefix[n-1]
	}
	if value == p.needle[n] {
		n++
	}
	*matched = n
	return n == len(p.needle)
}

// The verified CSV join records these positions while writing the display text.
// A search never needs to wrap preceding rows or fetch batch results again.
type batchBrowserCSVOffset struct{ start, label, result, end int64 }
type batchBrowserCSVHit struct {
	row         int
	start, line int64
}
type batchBrowserCSVRows struct {
	document    *batchBrowserDocument
	firstColumn string
	fullScreen  bool // Shared by the CSV list and its row detail.
	rows        []batchBrowserCSVOffset
	mu          sync.Mutex
	query       string
	hits        []batchBrowserCSVHit
}

func (c *batchBrowserCSVRows) wait() { c.mu.Lock(); c.mu.Unlock() }

func (c *batchBrowserCSVRows) find(ctx context.Context, query string) error {
	if c.query == query {
		return nil
	}
	pattern := newBatchBrowserPattern(query)
	starts := make([]int64, len(pattern.needle))
	hits := make([]batchBrowserCSVHit, 0)
	input := bufio.NewReaderSize(batchRecordContextReader{ctx, io.NewSectionReader(c.document.store.data, 0, c.document.size)}, 32*1024)
	var pos int64
	for i, row := range c.rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		matched, next := 0, 0
		line := pos
		for pos < row.end {
			r, size, err := input.ReadRune()
			if err != nil {
				return err
			}
			starts[next] = pos
			next = (next + 1) % len(starts)
			pos += int64(size)
			if r == '\n' {
				line, matched = pos, 0
				continue
			}
			if pattern.advance(r, &matched) {
				hits = append(hits, batchBrowserCSVHit{i, starts[next], line})
				for pos < row.end {
					n, err := input.Discard(int(min(row.end-pos, 32*1024)))
					if err != nil {
						return err
					}
					pos += int64(n)
				}
				break
			}
		}
	}
	c.query, c.hits = query, hits
	return nil
}

// Read only a bounded, single-line display hint. The full row remains on disk.
func (c *batchBrowserCSVRows) excerpt(start, end int64) (string, error) {
	if start >= end {
		return "", nil
	}
	var buf [batchBrowserCatalogHint]byte
	b := buf[:min(int64(len(buf)), end-start)]
	if err := c.document.store.read(c.document.store.data, b, start); err != nil {
		return "", err
	}
	for len(b) > 0 && !utf8.RuneStart(b[0]) {
		b = b[1:]
	}
	line, _, complete := strings.Cut(string(b), "\n")
	if !complete && end-start > int64(len(b)) {
		return strings.ToValidUTF8(line, "") + "…", nil
	}
	return line, nil
}

func (c *batchBrowserCSVRows) page(ctx context.Context, query string, first int64, limit int) (batchBrowserRecordPage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return batchBrowserRecordPage{}, err
	}
	total := int64(len(c.rows))
	if query != "" {
		if err := c.find(ctx, query); err != nil {
			return batchBrowserRecordPage{}, err
		}
		total = int64(len(c.hits))
	}
	first = max(0, min(first, total-int64(limit)))
	page := batchBrowserRecordPage{First: first, Total: total}
	for i := first; i < total && len(page.Rows) < max(1, limit); i++ {
		if err := ctx.Err(); err != nil {
			return batchBrowserRecordPage{}, err
		}
		idx := int(i)
		var hit batchBrowserCSVHit
		if query != "" {
			hit = c.hits[i]
			idx = hit.row
		}
		row := c.rows[idx]
		label, err := c.excerpt(row.label, row.end)
		if err != nil {
			return batchBrowserRecordPage{}, err
		}
		start := row.result
		if query != "" {
			start = max(hit.line, hit.start-16)
		}
		preview, err := c.excerpt(start, row.end)
		if err != nil {
			return batchBrowserRecordPage{}, err
		}
		if query != "" && start > hit.line {
			preview = "…" + preview
		}
		preview = strings.TrimSpace(preview)
		if query == "" {
			preview = strings.TrimPrefix(preview, "Answer: ")
		}
		page.Rows = append(page.Rows, batchBrowserRecordRow{ID: int64(idx + 1), Label: label, Preview: preview})
	}
	return page, nil
}

func (c *batchBrowserCSVRows) preview(ctx context.Context, id int64) ([]batchBrowserPreviewLine, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row := c.rows[id-1]
	var excerpt batchBrowserPreviewText
	_, err := io.CopyN(&excerpt, io.NewSectionReader(c.document.store.data, row.start, row.end-row.start), min(4096, row.end-row.start))
	if err != nil {
		return nil, err
	}
	_, body, _ := strings.Cut(excerpt.text.String(), "\n")
	var lines []batchBrowserPreviewLine
	for line := range strings.SplitSeq(strings.TrimSpace(body), "\n") {
		lines = append(lines, batchBrowserPreviewLine{text: strings.TrimSpace(strings.ToValidUTF8(line, ""))})
	}
	return lines, nil
}

func (c *batchBrowserCSVRows) showRow(ctx context.Context, id int64, source string, show func(context.Context, *batchBrowserScreen) error) (action string, err error) {
	if id < 1 || id > int64(len(c.rows)) {
		return "", batchError("Choose a row from the list.")
	}
	doc, err := newBatchBrowserDocument(ctx)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, doc.close()) }()
	row := c.rows[id-1]
	title := fmt.Sprintf("Row %d", id)
	start := row.start + int64(len(title)+1) // The inline heading already identifies the row.
	if _, err := io.Copy(doc, batchRecordContextReader{ctx, io.NewSectionReader(c.document.store.data, start, row.end-start)}); err != nil {
		return "", err
	}
	m := newBatchBrowserDocumentScreen(ctx, doc, title, source)
	m.documentState.csv = c
	defer m.documentState.stop()
	if err = show(ctx, m); err != nil {
		return "", err
	}
	return m.chosen, m.documentState.err
}

func newBatchBrowserCSVList(ctx context.Context, rows *batchBrowserCSVRows, source string) *batchBrowserScreen {
	m := newBatchBrowserScreen("requests", "Rows with answers", source, nil)
	m.input.Placeholder = "ID or text"
	m.requestState = &batchBrowserRequestState{ctx: ctx, csv: rows}
	m.requestState.preview.read = rows.preview
	return m
}

func csvHighlight(text, query string, style lipgloss.Style, colors bool) string {
	if !colors || query == "" || len(query) > utf8.UTFMax*len(text) {
		return text
	}
	pattern := newBatchBrowserPattern(query)
	var matched int
	for start, r := range text {
		if pattern.advance(r, &matched) {
			end := start + utf8.RuneLen(r)
			begin := end
			for range pattern.needle {
				_, n := utf8.DecodeLastRuneInString(text[:begin])
				begin -= n
			}
			return style.Render(text[:begin]) + style.Bold(true).Render(text[begin:end]) + style.Render(text[end:])
		}
	}
	return text
}
