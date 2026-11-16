package batches

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/stretchr/testify/require"
)

func TestBatchSubmitEndpointSupportedRequests(t *testing.T) {
	for _, expected := range batchSubmitEndpoints {
		t.Run(string(expected), func(t *testing.T) {
			input := fmt.Sprintf("\r\n  {\"url\":%q,\"body\":{\"url\":\"private nested value\"}}\r\n\t{\"url\":%q}\t", expected, expected)
			endpoint, err := inferBatchSubmitJSONL(t.Context(), strings.NewReader(input))
			require.NoError(t, err)
			require.Equal(t, string(expected), endpoint)
		})
	}
	endpoint, err := inferBatchSubmitJSONL(t.Context(), strings.NewReader(`{"\u0075\u0072\u006c":"\/v1\/responses"}`))
	require.NoError(t, err)
	require.Equal(t, "/v1/responses", endpoint)
}

func TestBatchSubmitEndpointLargeRecords(t *testing.T) {
	for _, size := range []int{128 * 1024, 8 * 1024 * 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			// Put the endpoint after the large body to require a complete scan.
			input := `{"custom_id":"large","body":{"input":"` + strings.Repeat("x", size) + `"},"url":"/v1/responses"}`
			path := filepath.Join(t.TempDir(), "large.jsonl")
			require.NoError(t, os.WriteFile(path, []byte(input), 0600))
			endpoint, snapshot, err := inferBatchSubmitEndpoint(t.Context(), path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, snapshot.close()) })
			require.Equal(t, "/v1/responses", endpoint)
			require.Equal(t, int64(len(input)), snapshot.Size())
		})
	}
}

func TestBatchSubmitEndpointRejectsAmbiguousInputWithoutEcho(t *testing.T) {
	for _, test := range []struct {
		name, input, problem string
		line                 int
	}{
		{"missing", `{"body":{"url":"/v1/responses"}}`, "no top-level url", 1},
		{"null", `{"url":null}`, "non-string url", 1},
		{"number", `{"url":42}`, "non-string url", 1},
		{"array url", `{"url":["/v1/responses"]}`, "non-string url", 1},
		{"object url", `{"url":{"value":"/v1/responses"}}`, "non-string url", 1},
		{"duplicate", `{"url":"/v1/responses","url":"/v1/responses"}`, "repeats url", 1},
		{"escaped duplicate", `{"url":"/v1/responses","\u0075rl":"/v1/responses"}`, "repeats url", 1},
		{"mixed", "{\"url\":\"/v1/responses\"}\n\n{\"url\":\"/v1/embeddings\"}", "different endpoint", 3},
		{"empty url", `{"url":""}`, "unsupported url", 1},
		{"unknown url", `{"url":"/private-canary"}`, "unsupported url", 1},
		{"absolute url", `{"url":"https://fixture.invalid/v1/responses?secret=private-canary"}`, "unsupported url", 1},
		{"long url", `{"url":"/v1/responses` + strings.Repeat("x", 1024*1024) + `"}`, "unsupported url", 1},
		{"incomplete", `{"url":"/v1/responses","body":"private-canary`, "not a valid JSON request object", 1},
		{"invalid escape", `{"url":"/v1/responses","body":"\qprivate-canary"}`, "not a valid JSON request object", 1},
		{"root array", `[{"url":"/v1/responses"}]`, "not a valid JSON request object", 1},
		{"missing comma", `{"url":"/v1/responses" "body":{}}`, "not a valid JSON request object", 1},
		{"trailing comma", `{"url":"/v1/responses",}`, "not a valid JSON request object", 1},
		{"trailing object", `{"url":"/v1/responses"}{"url":"/v1/responses"}`, "content after its request object", 1},
		{"malformed later line", "{\"url\":\"/v1/responses\"}\r\n\r\nprivate-canary", "not a valid JSON request object", 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint, err := inferBatchSubmitJSONL(t.Context(), strings.NewReader(test.input))
			require.ErrorContains(t, err, test.problem)
			require.ErrorContains(t, err, fmt.Sprintf("line %d", test.line))
			require.Empty(t, endpoint)
			require.NotContains(t, err.Error(), "private-canary")
		})
	}
	for _, input := range []string{"", " \r\n\n\t"} {
		_, err := inferBatchSubmitJSONL(t.Context(), strings.NewReader(input))
		require.ErrorContains(t, err, "input has no requests")
	}
}

func TestBatchSubmitEndpointSnapshotRejectsReplacedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"url":"/v1/responses"}`), 0600))
	_, snapshot, err := inferBatchSubmitEndpoint(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshot.close()) })
	require.NoError(t, os.Rename(path, path+".original"))
	require.NoError(t, os.WriteFile(path, []byte(`{"url":"/v1/embeddings"}`), 0600))
	_, err = openBatchSubmissionFile(t.Context(), path, snapshot)
	require.ErrorContains(t, err, "input file changed")
}

func TestBatchSubmitEndpointRejectsNonRegularAndMissingFile(t *testing.T) {
	for _, path := range []string{t.TempDir(), filepath.Join(t.TempDir(), "private-canary.jsonl")} {
		endpoint, snapshot, err := inferBatchSubmitEndpoint(t.Context(), path)
		require.Error(t, err)
		require.Empty(t, endpoint)
		require.Nil(t, snapshot)
		if errors.Is(err, os.ErrNotExist) {
			var input *requestconfig.InputFileError
			require.ErrorAs(t, err, &input, "retain metadata for the safe public diagnostic presenter")
			require.Equal(t, "--file", input.Option)
		}
	}
}

type batchEndpointCancelReader struct {
	reader    io.Reader
	cancel    context.CancelFunc
	remaining int
}

func (r *batchEndpointCancelReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.remaining -= n
	if r.remaining <= 0 {
		r.cancel()
	}
	return n, err
}

func TestBatchSubmitEndpointCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	input := `{"body":"` + strings.Repeat("x", 8*1024*1024) + `","url":"/v1/responses"}`
	reader := &batchEndpointCancelReader{reader: strings.NewReader(input), cancel: cancel, remaining: 128 * 1024}
	_, err := inferBatchSubmitJSONL(ctx, reader)
	require.ErrorIs(t, err, context.Canceled)
	require.GreaterOrEqual(t, reader.remaining, -4096, "cancelled scanning must stop promptly")
	_, snapshot, err := inferBatchSubmitEndpoint(ctx, "missing-private-canary.jsonl")
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, snapshot)
}

type batchEndpointFailReader struct{ err error }

func (r batchEndpointFailReader) Read([]byte) (int, error) { return 0, r.err }

func TestBatchSubmitEndpointPreservesReadFailure(t *testing.T) {
	want := errors.New("synthetic read failure")
	_, err := inferBatchSubmitJSONL(t.Context(), batchEndpointFailReader{want})
	require.ErrorIs(t, err, want)
	_, err = inferBatchSubmitJSONL(t.Context(), io.MultiReader(strings.NewReader(`{"url":"/v1/responses"}`), batchEndpointFailReader{errors.Join(io.EOF, want)}))
	require.ErrorIs(t, err, want, "EOF joined with a failure must not become successful inference")
}

func TestBatchRecordURLCaptureRemainsOptIn(t *testing.T) {
	input := `{"url":null,"url":{"nested":true}}`
	scanner := batchJSONScanner{ctx: t.Context(), in: bufio.NewReader(strings.NewReader(input)), out: bufio.NewWriter(io.Discard)}
	require.NoError(t, scanner.value(0), "record viewing must keep accepting unknown URL values")
}
