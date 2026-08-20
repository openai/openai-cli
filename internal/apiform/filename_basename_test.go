package apiform

import (
	"bytes"
	"io"
	"mime/multipart"
	"strings"
	"testing"
)

type pathNamedReader struct {
	io.Reader
	name string
}

func (r pathNamedReader) Name() string { return r.name }

func TestMultipartReaderNameUsesBaseFilenameAcrossPathStyles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		readerName string
		want       string
	}{
		{name: "posix", readerName: "/home/alice/reports/report.pdf", want: "report.pdf"},
		{name: "windows", readerName: `C:\Users\alice\reports\report.pdf`, want: "report.pdf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			writer := multipart.NewWriter(&buf)
			if err := writer.SetBoundary("xxx"); err != nil {
				t.Fatalf("set boundary: %v", err)
			}

			reader := pathNamedReader{
				Reader: strings.NewReader("payload"),
				name:   tt.readerName,
			}
			if err := Marshal(map[string]any{"file": reader}, writer); err != nil {
				t.Fatalf("marshal multipart: %v", err)
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("close writer: %v", err)
			}

			body := buf.String()
			if !strings.Contains(body, `filename="`+tt.want+`"`) {
				t.Fatalf("multipart filename not reduced to basename: %q", body)
			}
			if strings.Contains(body, "alice") || strings.Contains(body, "reports") {
				t.Fatalf("multipart filename leaked path components: %q", body)
			}
		})
	}
}
