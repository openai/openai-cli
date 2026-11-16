package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestBatchRecordStreamingJSONFormatsAcrossChunks(t *testing.T) {
	document := `{"batch_id":"batch_test","data":[{"empty":{},"array":[],"str":"a\n\u202e\\\"😀","number":9007199254740993,"bools":[true,false,null]}]}`
	for _, indent := range []bool{false, true} {
		for _, color := range []bool{false, true} {
			var out bytes.Buffer
			stream := batchJSONOutput{out: bufio.NewWriter(&out), indent: indent, color: color}
			for i := 0; i < len(document); i++ {
				n, err := stream.Write([]byte{document[i]})
				require.NoError(t, err)
				require.Equal(t, 1, n)
			}
			require.NoError(t, stream.out.Flush())
			var compact bytes.Buffer
			require.NoError(t, json.Compact(&compact, []byte(ansi.Strip(out.String()))))
			require.Equal(t, document, compact.String())
		}
	}
}

// The response remains open after its first record. A failing destination must
// stop the next HTTP read before joining the raw-text producer.
func TestBatchRawTextOutputFailureStopsStalledDownload(t *testing.T) {
	for _, closeFailure := range []error{nil, errors.New("synthetic body close failure")} {
		name := "output_failure"
		if closeFailure != nil {
			name = "output_and_close_failure"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			requestClosed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/batches/batch_synthetic" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"batch_synthetic","output_file_id":"file_synthetic"}`)
					return
				}
				if r.URL.Path != "/files/file_synthetic/content" {
					http.NotFound(w, r)
					return
				}
				_, _ = io.WriteString(w, "{\"custom_id\":\"first-visible-record\"}\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(requestClosed)
			}))
			defer server.Close()
			defer cancel()
			nextRead := make(chan struct{})
			bodyClosed := make(chan struct{})
			transport := server.Client().Transport
			client := &http.Client{Transport: batchRecordsTransport(func(r *http.Request) (*http.Response, error) {
				response, err := transport.RoundTrip(r)
				if err == nil && r.URL.Path == "/files/file_synthetic/content" {
					response.Body = &batchStalledResponse{ReadCloser: response.Body, nextRead: nextRead, closed: bodyClosed, closeFailure: closeFailure}
				}
				return response, err
			})}
			failure := errors.New("synthetic output failure")
			var visible bytes.Buffer
			sink := resourceSummaryTestWriter(func(data []byte) (int, error) {
				n, _ := visible.Write(data)
				if bytes.Contains(visible.Bytes(), []byte("first-visible-record")) {
					select {
					case <-nextRead:
					case <-ctx.Done():
						return 0, ctx.Err()
					}
					return 0, failure
				}
				return n, nil
			})
			root := &cli.Command{
				Name: "openai", Writer: sink, ErrWriter: io.Discard,
				ExitErrHandler: func(context.Context, *cli.Command, error) {},
				Flags:          []cli.Flag{&cli.StringFlag{Name: "format"}, &cli.BoolFlag{Name: "raw-output"}},
				Commands:       []*cli.Command{{Name: "batches"}},
			}
			batchcommands.Configure(root, batchcommands.Options{
				DefaultRequestOptions: func(*cli.Command) []option.RequestOption {
					return []option.RequestOption{option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-batch-key"), option.WithHTTPClient(client), option.WithMaxRetries(0)}
				},
				RequestOptions: func(*cli.Command, io.Reader) ([]option.RequestOption, error) { return nil, nil },
				ShowRecords:    showBatchRecords,
			})
			done := make(chan error, 1)
			go func() {
				done <- root.Run(ctx, []string{"openai", "batches", "results", "batch_synthetic", "--format", "text", "--raw-output"})
			}()
			var err error
			select {
			case err = <-done:
			case <-time.After(3 * time.Second):
				cancel()
				<-done
				t.Fatal("output failure waited for the stalled HTTP response")
			}
			require.ErrorIs(t, err, failure)
			if closeFailure != nil {
				require.ErrorIs(t, err, closeFailure)
			}
			require.NotErrorIs(t, err, context.Canceled)
			require.Empty(t, batchcommands.WorkflowErrorMessage(err))
			require.NoError(t, ctx.Err(), "an output failure must not cancel the command context")
			select {
			case <-bodyClosed:
			default:
				t.Fatal("response body was not closed before returning")
			}
			select {
			case <-requestClosed:
			case <-time.After(3 * time.Second):
				t.Fatal("server request remained open after the output failure")
			}
			require.Contains(t, visible.String(), "first-visible-record")
		})
	}
}

type batchStalledResponse struct {
	io.ReadCloser
	recordRead       bool
	nextRead, closed chan struct{}
	closeFailure     error
}

func (r *batchStalledResponse) Read(data []byte) (int, error) {
	if r.recordRead && r.nextRead != nil {
		close(r.nextRead)
		r.nextRead = nil
	}
	n, err := r.ReadCloser.Read(data)
	r.recordRead = r.recordRead || bytes.Contains(data[:n], []byte("\n"))
	return n, err
}

func (r *batchStalledResponse) Close() error {
	defer close(r.closed)
	return errors.Join(r.ReadCloser.Close(), r.closeFailure)
}

type batchRecordsTransport func(*http.Request) (*http.Response, error)

func (f batchRecordsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
