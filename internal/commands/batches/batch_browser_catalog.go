package batches

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/readable"
)

type batchBrowserRecordRow struct {
	ID             int64
	Label, Preview string
}

type batchBrowserRecordPage struct {
	Rows         []batchBrowserRecordRow
	First, Total int64
}

// The catalog keeps full decoded search text and record positions on disk.
// Labels and previews are bounded display hints, never accepted payload limits.
// The caller owns the immutable record store and closes it after all operations.
type batchBrowserCatalog struct {
	mu                           sync.Mutex
	store                        *batchRecordStore
	rows, hints, search, matches *os.File
	query, selection             string
	matchCount                   int64
	ready                        bool
	csvRows                      bool
}

const batchBrowserCatalogHint = 512

func newBatchBrowserCatalog(ctx context.Context, store *batchRecordStore, selection string) (*batchBrowserCatalog, error) {
	c := &batchBrowserCatalog{store: store, selection: selection, csvRows: store.recordCount > 0}
	for _, file := range []**os.File{&c.rows, &c.hints, &c.search, &c.matches} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		*file, _, err = store.temporaryFile()
		if err != nil {
			return nil, err
		}
	}
	var hintPosition, searchPosition int64
	for id := int64(1); id <= store.recordCount; id++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, err := store.record(id)
		if err != nil {
			return nil, err
		}
		label, preview, length, err := c.indexRecord(ctx, record, selection)
		if err != nil {
			return nil, err
		}
		if c.csvRows && !isBatchCSVRowID(label) {
			c.csvRows = false
		}
		var row [48]byte
		for i, value := range []int64{hintPosition, int64(len(label)), hintPosition + int64(len(label)), int64(len(preview)), searchPosition, length} {
			binary.LittleEndian.PutUint64(row[i*8:], uint64(value))
		}
		if err := store.write(c.rows, row[:], (id-1)*int64(len(row))); err != nil {
			return nil, err
		}
		if err := store.write(c.hints, []byte(label+preview), hintPosition); err != nil {
			return nil, err
		}
		hintPosition += int64(len(label) + len(preview))
		searchPosition += length
	}
	return c, nil
}

// indexRecord reuses the validated JSON scanner and streaming string decoder.
// It decodes every string into the search spool, including unrecognized fields.
func (c *batchBrowserCatalog) indexRecord(ctx context.Context, record batchStoredRecord, selection string) (string, string, int64, error) {
	reader := io.NewSectionReader(c.store.data, record.start, record.length)
	scanner := batchJSONScanner{ctx: ctx, in: bufio.NewReader(reader), out: bufio.NewWriter(io.Discard)}
	search := &batchBrowserCatalogWriter{out: bufio.NewWriterSize(c.search, 32*1024)}
	bestRank := 100
	var fields []string
	label, preview := "(no custom_id)", ""
	for scanner.position < record.length {
		b, err := scanner.peek()
		if err != nil {
			return "", "", 0, err
		}
		if b != '"' {
			if _, err := scanner.take(true); err != nil {
				return "", "", 0, err
			}
			switch b {
			case '{', '[':
				fields = append(fields, "")
			case '}', ']':
				fields = fields[:len(fields)-1]
			}
			if _, err := search.Write([]byte{b}); err != nil {
				return "", "", 0, batchRecordStorageError(err)
			}
			continue
		}
		start := scanner.position
		if _, err := scanner.stringValue(0); err != nil {
			return "", "", 0, err
		}
		capture := &batchBrowserCatalogCapture{}
		value := io.NewSectionReader(c.store.data, record.start+start, scanner.position-start)
		if err := readable.DecodeJSONStringBytes(ctx, io.MultiWriter(search, capture), value); err != nil {
			return "", "", 0, batchRecordStorageError(err)
		}
		if _, err := search.Write([]byte{'\n'}); err != nil {
			return "", "", 0, batchRecordStorageError(err)
		}
		for scanner.position < record.length {
			b, err = scanner.peek()
			if err != nil {
				return "", "", 0, err
			}
			if !strings.ContainsRune(" \t\r\n", rune(b)) {
				break
			}
			if _, err := scanner.take(true); err != nil {
				return "", "", 0, err
			}
		}
		if b == ':' {
			fields[len(fields)-1] = string(capture.data)
			if len(fields) == 1 && fields[0] == "custom_id" {
				label = "(invalid custom_id)"
			}
			continue
		}
		if len(fields) == 1 && fields[0] == "custom_id" {
			label = capture.hint()
			if label == "" {
				label = "(empty custom_id)"
			}
		}
		if rank := batchBrowserPreviewRank(selection, fields); rank < bestRank {
			if candidate := capture.hint(); candidate != "" {
				preview, bestRank = candidate, rank
			}
		}
	}
	if preview == "" {
		preview = "No text preview"
	}
	return label, preview, search.count, batchRecordStorageError(search.out.Flush())
}

func batchBrowserPreviewRank(selection string, fields []string) int {
	// Only recognized payload paths supply a preview. Metadata and unfamiliar
	// fields remain searchable without masquerading as an input or answer.
	var path [8]string
	n := 0
	for _, field := range fields {
		if field != "" {
			if n == len(path) {
				return 100
			}
			path[n], n = field, n+1
		}
	}
	name := strings.Join(path[:n], ".")
	if selection == "input" {
		switch name {
		case "body.input", "body.prompt", "body.input.text", "body.input.content", "body.input.content.text", "body.messages.content", "body.messages.content.text":
			return 0
		}
	} else {
		switch name {
		case "error.message", "response.body.error.message":
			return 0
		case "response.body.output_text", "response.body.output.content.text", "response.body.output.content.refusal", "response.body.choices.text", "response.body.choices.message.content", "response.body.choices.message.refusal":
			return 1
		case "error.code", "response.body.error.code":
			return 2
		}
	}
	return 100
}

// wait joins any active search after the controller cancels outstanding work.
// A canceled queued page returns before accessing the store.
func (c *batchBrowserCatalog) wait() {
	c.mu.Lock()
	c.mu.Unlock()
}

type batchBrowserCatalogWriter struct {
	out   *bufio.Writer
	count int64
}

func (w *batchBrowserCatalogWriter) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	w.count += int64(n)
	return n, err
}

type batchBrowserCatalogCapture struct {
	data      []byte
	truncated bool
}

func (w *batchBrowserCatalogCapture) Write(p []byte) (int, error) {
	n := min(len(p), max(0, batchBrowserCatalogHint-len(w.data)))
	w.data = append(w.data, p[:n]...)
	w.truncated = w.truncated || n < len(p)
	return len(p), nil
}

func (w *batchBrowserCatalogCapture) hint() string {
	data := w.data
	if w.truncated {
		start := len(data) - 1
		for start > 0 && !utf8.RuneStart(data[start]) {
			start--
		}
		if start >= 0 && !utf8.FullRune(data[start:]) {
			data = data[:start]
		}
	}
	text := readable.Text(strings.Join(strings.Fields(strings.ToValidUTF8(string(data), "�")), " "))
	if w.truncated {
		text += "…"
	}
	return text
}

// Queries use a cached disk index. Cancellation invalidates partial searches;
// another search cannot observe its incomplete match list.
func (c *batchBrowserCatalog) page(ctx context.Context, query string, first int64, limit int) (batchBrowserRecordPage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return batchBrowserRecordPage{}, err
	}
	query = strings.ToLower(query)
	total := c.store.recordCount
	if query != "" {
		if !c.ready || c.query != query {
			if err := c.find(ctx, query); err != nil {
				return batchBrowserRecordPage{}, err
			}
		}
		total = c.matchCount
	}
	limit = max(1, limit)
	first = max(0, min(first, max(0, total-int64(limit))))
	page := batchBrowserRecordPage{First: first, Total: total}
	for offset := first; offset < total && len(page.Rows) < limit; offset++ {
		if err := ctx.Err(); err != nil {
			return batchBrowserRecordPage{}, err
		}
		id := offset + 1
		if query != "" {
			var data [8]byte
			if err := c.store.read(c.matches, data[:], offset*8); err != nil {
				return batchBrowserRecordPage{}, err
			}
			id = int64(binary.LittleEndian.Uint64(data[:]))
		}
		metadata, err := c.metadata(id)
		if err != nil {
			return batchBrowserRecordPage{}, err
		}
		data := make([]byte, metadata[1]+metadata[3])
		if err := c.store.read(c.hints, data, metadata[0]); err != nil {
			return batchBrowserRecordPage{}, err
		}
		page.Rows = append(page.Rows, batchBrowserRecordRow{ID: id, Label: string(data[:metadata[1]]), Preview: string(data[metadata[1]:])})
	}
	return page, nil
}

func (c *batchBrowserCatalog) metadata(id int64) ([6]int64, error) {
	var data [48]byte
	var values [6]int64
	if err := c.store.read(c.rows, data[:], (id-1)*int64(len(data))); err != nil {
		return values, err
	}
	for i := range values {
		values[i] = int64(binary.LittleEndian.Uint64(data[i*8:]))
	}
	return values, nil
}

func (c *batchBrowserCatalog) find(ctx context.Context, query string) error {
	c.ready = false
	needle := []rune(query)
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
	count := int64(0)
	for id := int64(1); id <= c.store.recordCount; id++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := c.metadata(id)
		if err != nil {
			return err
		}
		reader := bufio.NewReader(batchRecordContextReader{ctx, io.NewSectionReader(c.search, metadata[4], metadata[5])})
		matched := 0
		for {
			value, _, err := reader.ReadRune()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return batchRecordStorageError(err)
			}
			value = unicode.ToLower(value)
			for matched > 0 && value != needle[matched] {
				matched = prefix[matched-1]
			}
			if value == needle[matched] {
				matched++
			}
			if matched == len(needle) {
				var data [8]byte
				binary.LittleEndian.PutUint64(data[:], uint64(id))
				if err := c.store.write(c.matches, data[:], count*8); err != nil {
					return err
				}
				count++
				break
			}
		}
	}
	c.query, c.matchCount, c.ready = query, count, true
	return ctx.Err()
}
