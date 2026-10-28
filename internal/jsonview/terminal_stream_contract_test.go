package jsonview

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"
)

func FuzzWriteTerminalTextChunking(f *testing.F) {
	for _, input := range []string{"", "plain\n\ttext", "é世界🌍", "\xff\xc2\xe2\x82", "\x00\x1b\u009b", strings.Repeat("a", 32767) + "🌍"} {
		f.Add([]byte(input), uint16(1), false)
		f.Add([]byte(input), uint16(32768), true)
	}
	f.Fuzz(func(t *testing.T, input []byte, chunk uint16, finalErrorWithData bool) {
		var source io.Reader = &terminalChunkReader{Reader: bytes.NewReader(input), size: max(1, int(chunk))}
		if finalErrorWithData {
			source = iotest.DataErrReader(source)
		}
		var output bytes.Buffer
		if err := WriteTerminalText(&output, source); err != nil {
			t.Fatalf("WriteTerminalText(%d bytes, chunk %d) = %v, want nil", len(input), chunk, err)
		}
		// Compare streaming against the established whole-string formatting
		// contract, preserving layout without making chunking part of the oracle.
		var want strings.Builder
		for _, r := range string(input) {
			if r == '\n' || r == '\t' {
				want.WriteRune(r)
			} else {
				want.WriteString(SanitizeTerminalString(string(r)))
			}
		}
		if output.String() != want.String() {
			t.Errorf("WriteTerminalText(%d bytes, chunk %d, EOF with data %t) differs from whole-string formatting: got %q, want %q", len(input), chunk, finalErrorWithData, output.String(), want.String())
		}
	})
}

func TestWriteTerminalTextPreservesUnicode(t *testing.T) {
	var input strings.Builder
	for r := rune(0xa0); r <= utf8.MaxRune; r++ {
		if utf8.ValidRune(r) {
			input.WriteRune(r)
		}
	}
	for _, chunk := range []int{7, 32767, 32768} {
		var output bytes.Buffer
		err := WriteTerminalText(&output, &terminalChunkReader{Reader: strings.NewReader(input.String()), size: chunk})
		if err != nil || output.String() != input.String() {
			t.Errorf("WriteTerminalText(valid Unicode above U+009F, chunk %d) = %d bytes, %v, want %d identical bytes, nil", chunk, output.Len(), err, input.Len())
		}
	}
}

func TestWriteTerminalTextDataAndReadError(t *testing.T) {
	for _, input := range []string{"", "text", "é世界🌍", "\xe2\x82"} {
		for _, readErr := range []error{io.EOF, context.Canceled, io.ErrUnexpectedEOF} {
			var output bytes.Buffer
			source := &terminalFinalErrorReader{Reader: strings.NewReader(input), err: readErr}
			err := WriteTerminalText(&output, source)
			wantErr := readErr
			if readErr == io.EOF {
				wantErr = nil
			}
			if !errors.Is(err, wantErr) || output.String() != string([]rune(input)) {
				t.Errorf("WriteTerminalText(%q, read error %v) = %q, %v, want %q, %v", input, readErr, output.String(), err, string([]rune(input)), wantErr)
			}
		}
	}
}

func TestWriteTerminalTextStopsReadingOnWriteError(t *testing.T) {
	source := strings.NewReader(strings.Repeat("x", 1<<20))
	wantErr := errors.New("synthetic output failure")
	err := WriteTerminalText(terminalErrorWriter{err: wantErr}, source)
	if !errors.Is(err, wantErr) {
		t.Errorf("WriteTerminalText(failing writer) = %v, want %v", err, wantErr)
	}
	if source.Len() < (1<<20)-(32<<10) {
		t.Errorf("WriteTerminalText(failing writer) consumed %d bytes, want at most %d", (1<<20)-source.Len(), 32<<10)
	}
}

type terminalChunkReader struct {
	io.Reader
	size int
}

func (r *terminalChunkReader) Read(p []byte) (int, error) {
	return r.Reader.Read(p[:min(len(p), r.size)])
}

type terminalFinalErrorReader struct {
	*strings.Reader
	err error
}

func (r *terminalFinalErrorReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if r.Len() == 0 {
		return n, r.err
	}
	return n, err
}

type terminalErrorWriter struct{ err error }

func (w terminalErrorWriter) Write(p []byte) (int, error) {
	return len(p) / 2, w.err
}
