package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func batchDownloadTestCommand(t *testing.T, server string, out io.Writer) *cli.Command {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "synthetic-batch-key")
	t.Setenv("OPENAI_BASE_URL", server)
	t.Setenv("OPENAI_ORG_ID", "")
	t.Setenv("OPENAI_PROJECT_ID", "")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	return &cli.Command{
		Name: "openai", Writer: out, ErrWriter: io.Discard,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "json"},
			&cli.StringFlag{Name: "transform"},
			&cli.StringFlag{Name: "project"},
			&cli.BoolFlag{Name: "raw-output"},
			NewRequestHeaderFlag(),
		},
		Commands: []*cli.Command{{Name: "batches", Commands: []*cli.Command{newBatchDownloadCommand()}}},
	}
}

func TestBatchDownloadSelectsAvailableFilesAndPreservesRequestContext(t *testing.T) {
	for _, selected := range []string{"output", "error", "input"} {
		t.Run(selected, func(t *testing.T) {
			var paths []string
			body := []byte("{\"custom_id\":\"second\"}\r\n{\"custom_id\":\"first\"}\n\x00\xff")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				require.Equal(t, "Bearer synthetic-batch-key", r.Header.Get("Authorization"))
				require.Equal(t, "proj_example", r.Header.Get("OpenAI-Project"))
				require.Equal(t, "synthetic-context", r.Header.Get("X-Batch-Test"))
				if r.URL.Path == "/batches/batch_example" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"batch_example","status":"expired","input_file_id":"file_input","output_file_id":"file_output","error_file_id":"file_error"}`)
					return
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()
			var out bytes.Buffer
			root := batchDownloadTestCommand(t, server.URL, &out)
			path := filepath.Join(t.TempDir(), "selected.jsonl")
			err := root.Run(t.Context(), []string{"openai", "--project", "proj_example", "--header", "X-Batch-Test: synthetic-context", "batches", "download", "batch_example", "--file", selected, "--output", path})
			require.NoError(t, err)
			require.Equal(t, []string{"/batches/batch_example", "/files/file_" + selected + "/content"}, paths)
			saved, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, body, saved)
			receipt := gjson.ParseBytes(out.Bytes())
			require.Equal(t, int64(len(body)), receipt.Get("bytes").Int())
			require.Equal(t, selected, receipt.Get("file").String())
			require.Equal(t, path, receipt.Get("output").String())
			entries, err := os.ReadDir(filepath.Dir(path))
			require.NoError(t, err)
			require.Len(t, entries, 1)
		})
	}
}

func TestBatchDownloadStdoutIgnoresDataFormatting(t *testing.T) {
	body := "{\"custom_id\":\"b\"}\r\n{\"custom_id\":\"a\"}\n\x1b\x00\xff"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/batches/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	for _, format := range []string{"text", "json", "jsonl", "raw", "yaml"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			root := batchDownloadTestCommand(t, server.URL, &out)
			err := root.Run(t.Context(), []string{"openai", "--format", format, "--transform", "ignored", "--raw-output", "batches", "download", "--batch-id", "batch_example", "--output", "-"})
			require.NoError(t, err)
			require.Equal(t, body, out.String())
		})
	}
}

func TestBatchDownloadMissingFileAndAPIFailure(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "absent", false: "expired"}[missing], func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if strings.HasPrefix(r.URL.Path, "/batches/") {
					if missing {
						_, _ = io.WriteString(w, `{"id":"batch_example","status":"cancelled","output_file_id":null}`)
					} else {
						_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
					}
					return
				}
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":{"message":"synthetic expired file","type":"invalid_request_error"}}`)
			}))
			defer server.Close()
			root := batchDownloadTestCommand(t, server.URL, io.Discard)
			err := root.Run(t.Context(), []string{"openai", "batches", "download", "batch_example", "--output", "-"})
			require.Error(t, err)
			if missing {
				require.Equal(t, int32(1), calls.Load())
				require.Contains(t, err.Error(), "no selected file")
			} else {
				require.Equal(t, int32(2), calls.Load())
				var apierr *openai.Error
				require.ErrorAs(t, err, &apierr)
				require.Equal(t, http.StatusNotFound, apierr.StatusCode)
			}
		})
	}
}

func TestBatchDownloadRejectsInvalidFlagsBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, args := range [][]string{{"--file", "all", "--output", "-"}, {"--output", ""}, {"--output", "-", "unexpected"}} {
		root := batchDownloadTestCommand(t, server.URL, io.Discard)
		err := root.Run(t.Context(), append([]string{"openai", "batches", "download", "batch_example"}, args...))
		require.Error(t, err)
	}
	require.Zero(t, calls.Load())
}

type batchDownloadReader struct {
	read  func([]byte) (int, error)
	close func() error
}

func (r batchDownloadReader) Read(p []byte) (int, error) { return r.read(p) }
func (r batchDownloadReader) Close() error {
	if r.close != nil {
		return r.close()
	}
	return nil
}

func TestBatchDownloadAtomicFailures(t *testing.T) {
	for _, reason := range []string{"read", "close", "canceled", "existing", "symlink", "concurrent"} {
		t.Run(reason, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "result.jsonl")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if reason == "existing" {
				require.NoError(t, os.WriteFile(path, []byte("existing"), 0600))
			} else if reason == "symlink" {
				if err := os.Symlink("missing", path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			called := false
			closed := false
			body := batchDownloadReader{
				read: func(p []byte) (int, error) {
					if called {
						return 0, io.EOF
					}
					called = true
					if reason == "canceled" {
						cancel()
					} else if reason == "concurrent" {
						require.NoError(t, os.WriteFile(path, []byte("winner"), 0600))
					}
					n := copy(p, "partial")
					if reason == "read" {
						return n, io.ErrUnexpectedEOF
					}
					return n, nil
				},
				close: func() error {
					closed = true
					if reason == "close" {
						return io.ErrClosedPipe
					}
					return nil
				},
			}
			_, err := writeBatchDownload(ctx, path, body)
			require.Error(t, err)
			require.True(t, closed)
			entries, readErr := os.ReadDir(directory)
			require.NoError(t, readErr)
			if reason == "existing" || reason == "concurrent" || reason == "symlink" {
				require.Len(t, entries, 1)
				if reason != "symlink" {
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					require.NotEqual(t, "partial", string(data))
				}
			} else {
				require.Empty(t, entries)
			}
		})
	}
}

func TestBatchDownloadDoesNotRemoveReplacedTemporary(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "result.jsonl")
	replaced := false
	body := batchDownloadReader{read: func([]byte) (int, error) {
		entries, err := os.ReadDir(directory)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		temporary := filepath.Join(directory, entries[0].Name())
		require.NoError(t, os.Remove(temporary))
		require.NoError(t, os.WriteFile(temporary, []byte("replacement"), 0600))
		replaced = true
		return 0, io.EOF
	}}
	_, err := writeBatchDownload(t.Context(), path, body)
	require.Error(t, err)
	require.True(t, replaced)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	data, err := os.ReadFile(filepath.Join(directory, entries[0].Name()))
	require.NoError(t, err)
	require.Equal(t, "replacement", string(data))
	_, err = os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestBatchDownloadConcurrentPublicationHasOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.jsonl")
	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, content := range []string{"first", "second"} {
		go func() {
			reader := strings.NewReader(content)
			first := true
			body := batchDownloadReader{read: func(p []byte) (int, error) {
				if first {
					first = false
					ready.Done()
					<-start
				}
				return reader.Read(p)
			}}
			_, err := writeBatchDownload(t.Context(), path, body)
			results <- err
		}()
	}
	ready.Wait()
	close(start)
	first, second := <-results, <-results
	require.NotEqual(t, first == nil, second == nil)
	if first == nil {
		require.ErrorIs(t, second, os.ErrExist)
	} else {
		require.ErrorIs(t, first, os.ErrExist)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestBatchDownloadReadAndCloseFailuresRemainObservable(t *testing.T) {
	readErr, closeErr := errors.New("read failure"), errors.New("close failure")
	body := batchDownloadReader{read: func([]byte) (int, error) { return 0, readErr }, close: func() error { return closeErr }}
	_, err := writeBatchDownload(t.Context(), filepath.Join(t.TempDir(), "result.jsonl"), body)
	require.ErrorIs(t, err, readErr)
	require.ErrorIs(t, err, closeErr)
}

func TestBatchDownloadLargeContentUsesBoundedReads(t *testing.T) {
	const size = 70 << 20
	remaining := int64(size)
	var largest int
	body := batchDownloadReader{read: func(p []byte) (int, error) {
		largest = max(largest, len(p))
		if remaining == 0 {
			return 0, io.EOF
		}
		n := min(int64(len(p)), remaining)
		for i := range p[:n] {
			p[i] = 'x'
		}
		remaining -= n
		return int(n), nil
	}}
	path := filepath.Join(t.TempDir(), "large.jsonl")
	n, err := writeBatchDownload(t.Context(), path, body)
	require.NoError(t, err)
	require.Equal(t, int64(size), n)
	require.LessOrEqual(t, largest, 32<<10)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, int64(size), info.Size())
	if runtime.GOOS != "windows" {
		require.Zero(t, info.Mode().Perm()&0077)
	}
}

func TestBatchDownloadAnchorsCleanupWhenDirectoryMoves(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "original")
	moved := filepath.Join(parent, "moved")
	require.NoError(t, os.Mkdir(directory, 0700))
	body := batchDownloadReader{read: func([]byte) (int, error) {
		require.NoError(t, os.Rename(directory, moved))
		require.NoError(t, os.Mkdir(directory, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(directory, "unrelated"), []byte("keep"), 0600))
		return 0, io.EOF
	}}
	_, err := writeBatchDownload(t.Context(), filepath.Join(directory, "result.jsonl"), body)
	require.Error(t, err)
	entries, err := os.ReadDir(moved)
	require.NoError(t, err)
	require.Empty(t, entries)
	entries, err = os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "unrelated", entries[0].Name())
}

type batchDownloadFailWriter struct{}

func (batchDownloadFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestBatchDownloadOutputFailuresKeepCompletedFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/batches/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
			return
		}
		_, _ = io.WriteString(w, "exact\nbytes\n")
	}))
	defer server.Close()
	for _, stdout := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "result.jsonl")
		if stdout {
			path = "-"
		}
		root := batchDownloadTestCommand(t, server.URL, batchDownloadFailWriter{})
		err := root.Run(t.Context(), []string{"openai", "batches", "download", "batch_example", "--output", path})
		require.ErrorIs(t, err, io.ErrClosedPipe)
		if !stdout {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, "exact\nbytes\n", string(data))
		}
	}
}

func TestBatchDownloadCancellationStopsHTTPAndDiscardsIncompleteFile(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/batches/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
			return
		}
		w.Header().Set("Content-Length", "1000")
		_, _ = io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		cancel()
		<-r.Context().Done()
	}))
	defer server.Close()
	var out bytes.Buffer
	root := batchDownloadTestCommand(t, server.URL, &out)
	directory := t.TempDir()
	err := root.Run(ctx, []string{"openai", "batches", "download", "batch_example", "--output", filepath.Join(directory, "result.jsonl")})
	require.ErrorIs(t, err, context.Canceled)
	var exit cli.ExitCoder
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 130, exit.ExitCode())
	require.Empty(t, out.String())
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}
