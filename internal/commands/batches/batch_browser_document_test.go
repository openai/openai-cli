package batches

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func browserTestDocument(t *testing.T, text string) *batchBrowserDocument {
	t.Helper()
	document, err := newBatchBrowserDocument(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, document.close()) })
	_, err = io.WriteString(document, text)
	require.NoError(t, err)
	return document
}

func TestBatchBrowserDocumentPagesResizeAndExactPrinting(t *testing.T) {
	text := "first record\nsecond record\nthird record\nfourth record\n"
	document := browserTestDocument(t, text)
	require.True(t, document.hasContent())
	page, err := document.page(t.Context(), 20, 0, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"first record", "second record"}, page.Lines)
	require.True(t, page.More)
	require.Equal(t, int64(3), document.count, "index only enough rows to establish more content")
	page, err = document.page(t.Context(), 20, 2, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"third record", "fourth record"}, page.Lines)
	require.False(t, page.More)
	anchor := page.Offset
	page, err = document.pageAtOffset(t.Context(), 6, anchor, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"third ", "record"}, page.Lines)
	require.Equal(t, anchor, page.Offset)
	page, err = document.tail(t.Context(), 6, 2)
	require.NoError(t, err)
	require.Equal(t, []string{" recor", "d"}, page.Lines)
	var printed bytes.Buffer
	require.NoError(t, document.writeTo(t.Context(), &printed))
	require.Equal(t, text, printed.String())
}

func TestBatchBrowserDocumentEmptyNewlinesAndAppend(t *testing.T) {
	for _, tc := range []struct {
		text  string
		lines []string
	}{
		{"", nil},
		{"\n", []string{""}},
		{"a\n", []string{"a"}},
		{"a\n\n", []string{"a", ""}},
		{"a\n\nb", []string{"a", "", "b"}},
	} {
		t.Run(tc.text, func(t *testing.T) {
			d := browserTestDocument(t, tc.text)
			page, err := d.page(t.Context(), 80, 0, 10)
			require.NoError(t, err)
			require.Equal(t, tc.lines, page.Lines)
			require.False(t, page.More)
		})
	}
	d := browserTestDocument(t, "a")
	_, err := d.page(t.Context(), 80, 0, 2)
	require.NoError(t, err)
	_, err = io.WriteString(d, "b\nc")
	require.NoError(t, err)
	page, err := d.page(t.Context(), 80, 0, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"ab", "c"}, page.Lines)
}

func TestBatchBrowserDocumentUnicodeTabsAndSplitWrites(t *testing.T) {
	text := "A\t界👩‍💻éZ\nlast"
	d := browserTestDocument(t, "")
	for _, b := range []byte(text) {
		_, err := d.Write([]byte{b})
		require.NoError(t, err)
	}
	page, err := d.page(t.Context(), 8, 0, 8)
	require.NoError(t, err)
	require.Equal(t, []string{"A       ", "界👩‍💻éZ", "last"}, page.Lines)
	for _, width := range []int{1, 2, 3, 8} {
		page, err := d.page(t.Context(), width, 0, 100)
		require.NoError(t, err)
		for _, line := range page.Lines {
			require.True(t, utf8.ValidString(line))
			require.LessOrEqual(t, ansi.StringWidth(line), width)
		}
	}
	var out bytes.Buffer
	require.NoError(t, d.writeTo(t.Context(), &out))
	require.Equal(t, text, out.String())
}

func TestBatchBrowserDocumentLongLineAndBoundedIndex(t *testing.T) {
	d := browserTestDocument(t, "")
	chunk := bytes.Repeat([]byte("0123456789"), 4096)
	want := sha256.New()
	for range 64 {
		_, err := d.Write(chunk)
		require.NoError(t, err)
		_, _ = want.Write(chunk)
	}
	page, err := d.page(t.Context(), 80, 0, 10)
	require.NoError(t, err)
	require.Len(t, page.Lines, 10)
	require.True(t, page.More)
	require.Equal(t, int64(11), d.count)
	require.LessOrEqual(t, d.reader.Size(), batchBrowserDocumentChunk)
	page, err = d.tail(t.Context(), 80, 3)
	require.NoError(t, err)
	require.Len(t, page.Lines, 3)
	require.False(t, page.More)
	got := sha256.New()
	require.NoError(t, d.writeTo(t.Context(), got))
	require.Equal(t, want.Sum(nil), got.Sum(nil))
}

func TestBatchBrowserDocumentUnboundedCombiningRunPreservesBytes(t *testing.T) {
	text := "A" + strings.Repeat("́", batchBrowserDocumentChunk*3) + "Z"
	d := browserTestDocument(t, text)
	page, err := d.page(t.Context(), 20, 0, 20)
	require.NoError(t, err)
	require.Greater(t, len(page.Lines), 1)
	for _, line := range page.Lines {
		require.True(t, utf8.ValidString(line))
		require.LessOrEqual(t, len(line), batchBrowserDocumentChunk)
	}
	require.Equal(t, text, strings.Join(page.Lines, ""))
	var printed bytes.Buffer
	require.NoError(t, d.writeTo(t.Context(), &printed))
	require.Equal(t, text, printed.String())
}

type browserCancelDuringLayout struct {
	context.Context
	checks int
}

func (c *browserCancelDuringLayout) Err() error {
	c.checks--
	if c.checks <= 0 {
		return context.Canceled
	}
	return nil
}

func TestBatchBrowserDocumentCancellationCanResume(t *testing.T) {
	text := strings.Repeat("abcdefghij", 20000)
	d := browserTestDocument(t, text)
	ctx := &browserCancelDuringLayout{Context: t.Context(), checks: 250}
	_, err := d.tail(ctx, 80, 3)
	require.ErrorIs(t, err, context.Canceled)
	page, err := d.page(t.Context(), 80, 0, 5)
	require.NoError(t, err)
	require.Equal(t, text[:400], strings.Join(page.Lines, ""), "canceled partial rows must not lose text")
	ctx = &browserCancelDuringLayout{Context: t.Context(), checks: 3}
	var partial bytes.Buffer
	require.ErrorIs(t, d.writeTo(ctx, &partial), context.Canceled)
	require.Less(t, partial.Len(), len(text))
}

func TestBatchBrowserDocumentPrivateCleanupAndFailure(t *testing.T) {
	d := browserTestDocument(t, "complete\n")
	paths := make([]string, 0, len(d.store.files))
	for file := range d.store.files {
		info, err := file.Stat()
		require.NoError(t, err)
		require.Zero(t, info.Mode().Perm()&0077)
		paths = append(paths, file.Name())
	}
	failure := errors.New("synthetic print failure")
	require.ErrorIs(t, d.writeTo(t.Context(), browserDocumentFailWriter{err: failure}), failure)
	require.NoError(t, d.close())
	require.NoError(t, d.close())
	for _, path := range paths {
		_, err := os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	_, err := d.Write([]byte("more"))
	require.ErrorIs(t, err, io.ErrClosedPipe)
	_, err = d.page(t.Context(), 80, 0, 10)
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

type browserDocumentFailWriter struct{ err error }

func (w browserDocumentFailWriter) Write([]byte) (int, error) { return 0, w.err }
