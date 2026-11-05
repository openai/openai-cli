package batches

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestBatchWatchReadableReusesSnapshotWriter(t *testing.T) {
	for _, kind := range []string{"pipe", "custom", "partial"} {
		t.Run(kind, func(t *testing.T) {
			directory := batchRecordsPrivateTemp(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("after") != "" {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"message":"synthetic later-page failure"}}`)
					return
				}
				_, _ = fmt.Fprintf(w, `{"data":[{"id":"batch_a"},{"id":"batch_b"},{"id":"batch_c"},{"id":"batch_d"}],"has_more":%t}`, kind == "partial")
			}))
			defer server.Close()
			var output bytes.Buffer
			var destination io.Writer = &output
			finish := func() {}
			if kind != "custom" {
				reader, writer, err := os.Pipe()
				require.NoError(t, err)
				defer reader.Close()
				defer writer.Close()
				done := make(chan error, 1)
				go func() { _, err := io.Copy(&output, reader); done <- err }()
				destination = writer
				finish = func() {
					require.NoError(t, writer.Close())
					require.NoError(t, <-done)
				}
			}
			writers := map[*os.File]bool{}
			var ids []string
			options := testOptions()
			options.ShowResult = func(ctx context.Context, command *cli.Command, item gjson.Result, operation string, out, diagnostics io.Writer) error {
				require.Equal(t, "watch", operation)
				require.Equal(t, "list", command.Name)
				require.Same(t, os.Stderr, diagnostics.(terminal.BorrowedWriter).File)
				if kind == "custom" {
					require.Same(t, destination, out, "custom writers must retain their identity")
				} else {
					writer, ok := out.(batchPipeWriter)
					require.True(t, ok, "pipe output must use the owned output helper")
					writers[writer.file] = true
				}
				ids = append(ids, item.Get("id").Str)
				_, err := fmt.Fprintln(out, item.Get("id").Str)
				return err
			}
			root := batchWatchTestCommand(t, server.URL, destination, options, nil)
			err := root.Run(t.Context(), []string{"openai", "--format", "text", "batches", "list", "--watch", "--watch-count", "2", "--poll-interval", "1ns"})
			finish()
			snapshots := 2
			if kind == "partial" {
				snapshots = 1
				var apiError *openai.Error
				require.ErrorAs(t, err, &apiError)
				require.Equal(t, http.StatusBadRequest, apiError.StatusCode)
				require.Contains(t, output.String(), " (partial)\n")
			} else {
				require.NoError(t, err)
			}
			if kind != "custom" {
				require.Len(t, writers, snapshots, "all records in one snapshot must share its output writer")
			}
			require.Equal(t, snapshots, strings.Count(output.String(), "Batch snapshot "))
			require.Equal(t, snapshots, strings.Count(output.String(), "batch_a\nbatch_b\nbatch_c\nbatch_d\n"))
			require.Len(t, ids, snapshots*4)
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestBatchWatchReadableSnapshotFailures(t *testing.T) {
	for _, kind := range []string{"empty", "render-error", "cancel", "write-error"} {
		t.Run(kind, func(t *testing.T) {
			directory := batchRecordsPrivateTemp(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if kind == "empty" {
					_, _ = io.WriteString(w, `{"data":[],"has_more":false}`)
					return
				}
				_, _ = io.WriteString(w, `{"data":[{"id":"batch_a"},{"id":"batch_b"}],"has_more":false}`)
			}))
			defer server.Close()
			want := errors.New("synthetic output failure")
			var output bytes.Buffer
			var destination io.Writer = &output
			if kind == "write-error" {
				destination = failedBatchRecordWriter{want}
			}
			calls := 0
			options := testOptions()
			options.ShowResult = func(ctx context.Context, command *cli.Command, item gjson.Result, operation string, out, diagnostics io.Writer) error {
				calls++
				if kind == "render-error" {
					return &WorkflowError{message: "synthetic render failure", code: 7, cause: want}
				}
				_, err := fmt.Fprintln(out, item.Get("id").Str)
				cancel()
				return err
			}
			root := batchWatchTestCommand(t, server.URL, destination, options, nil)
			err := root.Run(ctx, []string{"openai", "--format", "text", "batches", "list", "--watch", "--watch-count", "1"})
			switch kind {
			case "empty":
				require.NoError(t, err)
				require.Zero(t, calls)
				require.Contains(t, output.String(), "\nNo results.\n")
			case "render-error":
				require.ErrorIs(t, err, want)
				var exit interface{ ExitCode() int }
				require.ErrorAs(t, err, &exit)
				require.Equal(t, 7, exit.ExitCode())
				require.Equal(t, 1, calls)
			case "cancel":
				require.ErrorIs(t, err, context.Canceled)
				require.Equal(t, 1, calls)
				require.Contains(t, output.String(), "\nbatch_a\n")
				require.NotContains(t, output.String(), "batch_b")
			case "write-error":
				require.ErrorIs(t, err, want)
				require.Zero(t, calls)
			}
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestBatchWatchReadableDrainsBeforeFailure(t *testing.T) {
	const marker = "first-record-finished"
	first := `{"id":"batch_a","payload":"` + strings.Repeat("x", 512*1024) + `","marker":"` + marker + `"}`
	for _, kind := range []string{"storage", "renderer", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			directory := batchRecordsPrivateTemp(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("after") != "" {
					truncateBatchRecordPayload(t, directory, first, int64(len(first)))
					_, _ = io.WriteString(w, `{"data":[],"has_more":false}`)
					return
				}
				_, _ = fmt.Fprintf(w, `{"data":[%s,{"id":"batch_b"}],"has_more":%t}`, first, kind == "storage")
			}))
			defer server.Close()
			reader, writer, err := os.Pipe()
			require.NoError(t, err)
			defer reader.Close()
			defer writer.Close()
			var output bytes.Buffer
			stopDrain, drainStopped := make(chan struct{}), make(chan struct{})
			readDone := make(chan error, 1)
			go func() {
				var chunk [32 * 1024]byte
				var paceUntil time.Time
				for {
					select {
					case <-stopDrain:
						close(drainStopped)
						<-ctx.Done()
						readDone <- nil
						return
					default:
					}
					n, err := reader.Read(chunk[:])
					output.Write(chunk[:n])
					if err != nil {
						if errors.Is(err, io.EOF) {
							err = nil
						}
						readDone <- err
						return
					}
					// Pace only the tail, for a bounded period. Small pipe reads
					// must not add a delay for every byte in the large record.
					if output.Len() >= len(first)-64*1024 {
						if paceUntil.IsZero() {
							paceUntil = time.Now().Add(250 * time.Millisecond)
						}
						if time.Now().Before(paceUntil) {
							time.Sleep(time.Millisecond)
						}
					}
				}
			}()
			want := errors.New("synthetic later rendering failure")
			renderFailed := make(chan struct{})
			options := testOptions()
			options.ShowResult = func(ctx context.Context, command *cli.Command, item gjson.Result, operation string, out, diagnostics io.Writer) error {
				if item.Get("id").Str == "batch_a" {
					_, err := fmt.Fprintln(out, item.Raw)
					if kind == "cancel" {
						close(stopDrain)
					}
					return err
				}
				if kind == "cancel" {
					select {
					case <-drainStopped:
					case <-ctx.Done():
						return ctx.Err()
					}
					close(renderFailed)
				}
				return &WorkflowError{message: "synthetic render failure", code: 7, cause: want}
			}
			root := batchWatchTestCommand(t, server.URL, writer, options, nil)
			commandDone := make(chan error, 1)
			go func() {
				commandDone <- root.Run(ctx, []string{"openai", "--format", "text", "batches", "list", "--watch", "--watch-count", "1"})
			}()
			if kind == "cancel" {
				select {
				case <-renderFailed:
					cancel()
				case <-ctx.Done():
					t.Fatal("the deferred rendering failure did not reach blocked output")
				}
			}
			err = <-commandDone
			require.NoError(t, writer.Close())
			require.NoError(t, <-readDone)
			if kind == "cancel" {
				require.ErrorIs(t, err, context.Canceled)
				require.ErrorIs(t, err, want)
			} else {
				require.True(t, strings.HasSuffix(output.String(), first+"\n"), "previous record was incomplete; received %d bytes", output.Len())
				if kind == "storage" {
					require.ErrorIs(t, err, io.ErrUnexpectedEOF)
				} else {
					require.ErrorIs(t, err, want)
					var exit interface{ ExitCode() int }
					require.ErrorAs(t, err, &exit)
					require.Equal(t, 7, exit.ExitCode())
				}
			}
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}
