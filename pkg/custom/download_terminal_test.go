package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func TestAutomaticDownloadEscapesWholeStream(t *testing.T) {
	prefix := strings.Repeat("a", 1024)
	input := prefix + "\x1b[2J\x1b]2;synthetic\a\x1b]0;title\x1b\\\u009b31m\u009dtitle\u009c\x00\b\f\r\x7f\x9b\xff\n\t世界☕\xe2\x82"
	want := prefix + `\u001b[2J\u001b]2;synthetic\u0007\u001b]0;title\u001b\\u009b31m\u009dtitle\u009c\u0000\b\f\r\u007f` + "��\n\t世界☕��"
	for _, size := range []int{1, 2, 3, 511, 512, 515, 4096, 32768} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			body := &downloadChunkReader{Reader: strings.NewReader(input), size: size}
			var output bytes.Buffer
			_, err := writeAutomaticBinaryResponse(&http.Response{Body: io.NopCloser(body)}, &output)
			if err != nil {
				t.Fatal(err)
			}
			if output.String() != want {
				t.Errorf("automatic output = %q, want %q", output.String(), want)
			}
		})
	}
}

func TestAutomaticDownloadFlushesBeforeNextRead(t *testing.T) {
	first := strings.Repeat("x", 4096) + "\u009b"
	var output bytes.Buffer
	body := &inspectingDownloadReader{
		Reader: strings.NewReader(first + "end"),
		inspect: func() error {
			if output.Len() == 0 {
				return errors.New("no output before next read")
			}
			return nil
		},
	}
	_, err := writeAutomaticBinaryResponse(&http.Response{Body: io.NopCloser(body)}, &output)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticDownloadPreservesLateReadError(t *testing.T) {
	input := strings.Repeat("x", 4096) + "\x1b\xc2"
	for _, readErr := range []error{io.EOF, context.Canceled} {
		body := &errorAfterDownloadReader{Reader: strings.NewReader(input), err: readErr}
		var output bytes.Buffer
		_, err := writeAutomaticBinaryResponse(&http.Response{Body: io.NopCloser(iotest.OneByteReader(body))}, &output)
		if readErr == io.EOF {
			readErr = nil
		}
		if !errors.Is(err, readErr) {
			t.Errorf("error = %v, want %v", err, readErr)
		}
		if want := strings.Repeat("x", 4096) + `\u001b` + "�"; output.String() != want {
			t.Errorf("output length = %d, want %d; suffix = %q", output.Len(), len(want), output.String()[4096:])
		}
	}
}

func TestAutomaticDownloadHonorsHTTPContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first := strings.Repeat("x", 4096)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, first); err != nil {
			t.Errorf("write first chunk: %v", err)
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	stdout := &cancelingDownloadWriter{cancel: cancel}
	_, err = writeAutomaticBinaryResponse(response, stdout)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("writeAutomaticBinaryResponse(canceled HTTP stream) = %v, want %v", err, context.Canceled)
	}
	if stdout.written != int64(len(first)) {
		t.Errorf("writeAutomaticBinaryResponse(canceled HTTP stream) wrote %d bytes, want %d", stdout.written, len(first))
	}
}

type downloadChunkReader struct {
	*strings.Reader
	size int
}

func (r *downloadChunkReader) Read(p []byte) (int, error) {
	return r.Reader.Read(p[:min(len(p), r.size)])
}
