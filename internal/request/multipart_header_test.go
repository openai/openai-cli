package request

import (
	"io"
	"testing"

	"github.com/openai/openai-cli/internal/apiform"
	"github.com/stretchr/testify/require"
)

func TestMultipartRequestOptionsRejectInvalidHeadersBeforeReadingKnownLengthUploads(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		field     string
		filename  string
		mediaType string
		component string
	}{
		{
			name:      "field name",
			field:     "upload\r\nX-Injected: value",
			filename:  "valid.txt",
			mediaType: "text/plain",
			component: "field name",
		},
		{
			name:      "filename",
			field:     "file",
			filename:  "upload\r\nX-Injected: value",
			mediaType: "text/plain",
			component: "filename",
		},
		{
			name:      "content type",
			field:     "file",
			filename:  "valid.txt",
			mediaType: "text/plain\r\nX-Injected: value",
			component: "content type",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := &countingReader{remaining: 7}
			upload := &recordingReadCloser{reader: source}
			_, err := MultipartRequestOptions(map[string]any{
				test.field: FileUpload{
					Reader:    upload,
					FileName:  test.filename,
					MediaType: test.mediaType,
					KnownSize: true,
					Size:      7,
				},
			}, apiform.FormatBrackets)

			require.ErrorContains(t, err, test.component)
			require.Zero(t, source.bytesRead.Load(), "validation must not read the upload")
			require.Equal(t, int32(1), upload.closeCount.Load(), "validation must close the owned upload")
		})
	}
}

func TestMultipartRequestBodyRejectsInvalidHeadersBeforeReadingUnknownLengthUploads(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		field     string
		filename  string
		mediaType string
		component string
	}{
		{name: "field name", field: "bad\nname", filename: "valid.txt", mediaType: "text/plain", component: "field name"},
		{name: "filename", field: "file", filename: "bad\nname.txt", mediaType: "text/plain", component: "filename"},
		{name: "content type", field: "file", filename: "valid.txt", mediaType: "text/plain\nbad", component: "content type"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := &countingReader{remaining: 7}
			upload := &recordingReadCloser{reader: source}
			body := newMultipartRequestBody(map[string]any{
				test.field: FileUpload{
					Reader:    upload,
					FileName:  test.filename,
					MediaType: test.mediaType,
				},
			}, apiform.FormatBrackets)

			contents, err := io.ReadAll(body)
			require.ErrorContains(t, err, test.component)
			require.Empty(t, contents, "invalid headers must not write multipart framing")
			waitForMultipartBody(t, body)
			require.Zero(t, source.bytesRead.Load(), "validation must not read the upload")
			require.Equal(t, int32(1), upload.closeCount.Load(), "validation must close the owned upload")
			require.NoError(t, body.Close())
		})
	}
}

func TestMultipartRequestOptionsRejectInvalidBufferedFieldNames(t *testing.T) {
	t.Parallel()

	_, err := MultipartRequestOptions(map[string]any{
		"outer": map[string]any{"bad\r\nX-Injected: value": "contents"},
	}, apiform.FormatBrackets)
	require.ErrorContains(t, err, "field name")
}
