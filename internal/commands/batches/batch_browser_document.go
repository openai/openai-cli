package batches

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// A document stores complete, already escaped readable text on disk. Only the
// current viewport and bounded decoding buffers enter memory. The row index is
// also disk-backed and extends lazily as the user scrolls. Methods are serialized;
// callers cancel obsolete page operations before starting their replacements.
type batchBrowserDocument struct {
	mu       sync.Mutex
	store    *batchRecordStore
	size     int64
	width    int
	position int64
	count    int64
	reader   *bufio.Reader
	ended    bool
	closed   bool
	styles   []RecordTextStyle
}

type batchBrowserDocumentPage struct {
	Lines  []string
	First  int64
	Offset int64
	More   bool
	Styles [][]RecordTextStyle
}

func (d *batchBrowserDocument) decorate(style RecordTextStyle) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if style.Start >= 0 && style.End > style.Start && style.End <= d.size {
		d.styles = append(d.styles, style)
	}
}

// This bounds work and viewport materialization, not accepted document size.
// Even an unbounded zero-width grapheme continues in later display fragments.
const batchBrowserDocumentChunk = 32 * 1024

func newBatchBrowserDocument(ctx context.Context) (*batchBrowserDocument, error) {
	store, err := newBatchRecordStore(ctx)
	if err != nil {
		return nil, err
	}
	return &batchBrowserDocument{store: store}, nil
}

func (d *batchBrowserDocument) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.store.ctx.Err(); err != nil {
		return err
	}
	if d.closed {
		return io.ErrClosedPipe
	}
	return nil
}

func (d *batchBrowserDocument) Write(data []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.width = 0
	n := 0
	for len(data) > 0 {
		if err := d.check(d.store.ctx); err != nil {
			return n, err
		}
		chunk := data[:min(len(data), batchBrowserDocumentChunk)]
		written, err := d.store.data.WriteAt(chunk, d.size)
		d.size += int64(written)
		n += written
		if err == nil && written != len(chunk) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return n, batchRecordStorageError(err)
		}
		data = data[written:]
	}
	return n, nil
}

func (d *batchBrowserDocument) hasContent() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.size > 0
}

func (d *batchBrowserDocument) prepare(ctx context.Context, width int) error {
	if err := d.check(ctx); err != nil {
		return err
	}
	width = max(1, width)
	if d.width == width {
		return nil
	}
	if err := d.store.records.Truncate(0); err != nil {
		return batchRecordStorageError(err)
	}
	d.width, d.position, d.count, d.ended = width, 0, 0, d.size == 0
	d.reader = bufio.NewReaderSize(io.NewSectionReader(d.store.data, 0, d.size), batchBrowserDocumentChunk)
	return nil
}

// nextCluster keeps ordinary graphemes intact across reader boundaries. An
// exceptionally large grapheme is divided at UTF-8 boundaries, retaining all
// source bytes and preventing a single cell from allocating the whole payload.
func (d *batchBrowserDocument) nextCluster(ctx context.Context) ([]byte, int, error) {
	if err := d.check(ctx); err != nil {
		return nil, 0, err
	}
	remaining := d.size - d.position
	if remaining == 0 {
		return nil, 0, io.EOF
	}
	if d.reader.Buffered() < 64 {
		if _, err := d.reader.Peek(int(min(remaining, batchBrowserDocumentChunk))); err != nil {
			return nil, 0, batchRecordStorageError(err)
		}
	}
	data, _ := d.reader.Peek(d.reader.Buffered())
	cluster, width := ansi.FirstGraphemeCluster(data, ansi.GraphemeWidth)
	if len(cluster) == len(data) && int64(len(data)) < remaining && len(data) < batchBrowserDocumentChunk {
		data, _ = d.reader.Peek(int(min(remaining, batchBrowserDocumentChunk)))
		cluster, width = ansi.FirstGraphemeCluster(data, ansi.GraphemeWidth)
	}
	if int64(len(cluster)) < remaining && len(cluster) == len(data) {
		// A refill boundary can end inside a multi-byte rune.
		for end := len(cluster); end > 0 && end > len(cluster)-utf8.UTFMax; end-- {
			if utf8.RuneStart(cluster[end-1]) && !utf8.FullRune(cluster[end-1:]) {
				cluster = cluster[:end-1]
				width = ansi.StringWidth(string(cluster))
				break
			}
		}
	}
	return cluster, width, nil
}

func (d *batchBrowserDocument) nextRow(ctx context.Context) (err error) {
	start, end, cells := d.position, d.position, 0
	defer func() {
		if err != nil {
			d.position = start
			d.reader = bufio.NewReaderSize(io.NewSectionReader(d.store.data, start, d.size-start), batchBrowserDocumentChunk)
		}
	}()
	for d.position < d.size {
		cluster, width, err := d.nextCluster(ctx)
		if err != nil {
			return err
		}
		if cluster[0] == '\n' {
			_, _ = d.reader.Discard(1)
			d.position++
			break
		}
		if cluster[0] == '\t' {
			cluster, width = cluster[:1], 8-cells%8
		}
		if d.position > start && (cells+width > d.width || d.position-start+int64(len(cluster)) > batchBrowserDocumentChunk) {
			break
		}
		_, _ = d.reader.Discard(len(cluster))
		d.position += int64(len(cluster))
		end = d.position
		cells += width
	}
	var span [16]byte
	binary.LittleEndian.PutUint64(span[:8], uint64(start))
	binary.LittleEndian.PutUint64(span[8:], uint64(end))
	if err := d.store.write(d.store.records, span[:], d.count*16); err != nil {
		return err
	}
	d.count++
	d.ended = d.position == d.size
	return nil
}

func (d *batchBrowserDocument) span(row int64) (int64, int64, error) {
	var data [16]byte
	if err := d.store.read(d.store.records, data[:], row*16); err != nil {
		return 0, 0, err
	}
	return int64(binary.LittleEndian.Uint64(data[:8])), int64(binary.LittleEndian.Uint64(data[8:])), nil
}

func (d *batchBrowserDocument) page(ctx context.Context, width int, first int64, rows int) (batchBrowserDocumentPage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.prepare(ctx, width); err != nil {
		return batchBrowserDocumentPage{}, err
	}
	return d.pageLocked(ctx, max(0, first), max(1, rows))
}

func (d *batchBrowserDocument) pageLocked(ctx context.Context, first int64, rows int) (batchBrowserDocumentPage, error) {
	for !d.ended && d.count-first <= int64(rows) {
		if err := d.nextRow(ctx); err != nil {
			return batchBrowserDocumentPage{}, err
		}
	}
	if d.ended {
		first = min(first, max(0, d.count-int64(rows)))
	}
	page := batchBrowserDocumentPage{First: first, More: !d.ended || d.count-first > int64(rows)}
	for row := first; row < d.count && len(page.Lines) < rows; row++ {
		if err := d.check(ctx); err != nil {
			return batchBrowserDocumentPage{}, err
		}
		start, end, err := d.span(row)
		if err != nil {
			return batchBrowserDocumentPage{}, err
		}
		if row == first {
			page.Offset = start
		}
		data := make([]byte, end-start)
		if err := d.store.read(d.store.data, data, start); err != nil {
			return batchBrowserDocumentPage{}, err
		}
		page.Lines = append(page.Lines, batchBrowserDocumentLine(data, d.width))
		if len(d.styles) > 0 {
			var styles []RecordTextStyle
			for _, style := range d.styles {
				lo, hi := max(start, style.Start), min(end, style.End)
				if lo < hi {
					// Map source offsets through tab expansion and narrow-cell replacement.
					style.Start = int64(len(batchBrowserDocumentLine(data[:lo-start], d.width)))
					style.End = int64(len(batchBrowserDocumentLine(data[:hi-start], d.width)))
					styles = append(styles, style)
				}
			}
			page.Styles = append(page.Styles, styles)
		}
	}
	return page, nil
}

func batchBrowserDocumentLine(data []byte, width int) string {
	var text strings.Builder
	cells := 0
	for len(data) > 0 {
		cluster, size := ansi.FirstGraphemeCluster(data, ansi.GraphemeWidth)
		if cluster[0] == '\t' {
			cluster = cluster[:1]
			size = min(8-cells%8, max(0, width-cells))
			text.WriteString(strings.Repeat(" ", size))
		} else if size > width {
			// A two-cell glyph cannot fit a one-cell viewport. Printing keeps it exact.
			text.WriteRune(utf8.RuneError)
			size = 1
		} else {
			text.Write(cluster)
		}
		cells += size
		data = data[len(cluster):]
	}
	return text.String()
}

func (d *batchBrowserDocument) tail(ctx context.Context, width, rows int) (batchBrowserDocumentPage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.prepare(ctx, width); err != nil {
		return batchBrowserDocumentPage{}, err
	}
	for !d.ended {
		if err := d.nextRow(ctx); err != nil {
			return batchBrowserDocumentPage{}, err
		}
	}
	return d.pageLocked(ctx, max(0, d.count-int64(max(1, rows))), max(1, rows))
}

func (d *batchBrowserDocument) pageAtOffset(ctx context.Context, width int, offset int64, rows int) (batchBrowserDocumentPage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.prepare(ctx, width); err != nil {
		return batchBrowserDocumentPage{}, err
	}
	for !d.ended && d.position <= offset {
		if err := d.nextRow(ctx); err != nil {
			return batchBrowserDocumentPage{}, err
		}
	}
	low, high := int64(0), d.count
	for low < high {
		if err := d.check(ctx); err != nil {
			return batchBrowserDocumentPage{}, err
		}
		middle := low + (high-low)/2
		start, _, err := d.span(middle)
		if err != nil {
			return batchBrowserDocumentPage{}, err
		}
		if start <= offset {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return d.pageLocked(ctx, max(0, low-1), max(1, rows))
}

func (d *batchBrowserDocument) writeTo(ctx context.Context, out io.Writer) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.check(ctx); err != nil {
		return err
	}
	reader := batchBrowserDocumentReader{ctx: ctx, document: d, source: io.NewSectionReader(d.store.data, 0, d.size)}
	_, err := io.CopyBuffer(out, reader, make([]byte, batchBrowserDocumentChunk))
	return err
}

func (d *batchBrowserDocument) close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.store.close()
}

// The print path checks both lifetimes without loading the complete document.
type batchBrowserDocumentReader struct {
	ctx      context.Context
	document *batchBrowserDocument
	source   io.Reader
}

func (r batchBrowserDocumentReader) Read(data []byte) (int, error) {
	if err := r.document.check(r.ctx); err != nil {
		return 0, err
	}
	return r.source.Read(data[:min(len(data), batchBrowserDocumentChunk)])
}
