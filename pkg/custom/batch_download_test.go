package custom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

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

func TestBatchDownloadRestrictiveUmask(t *testing.T) {
	const childDestination = "OPENAI_TEST_BATCH_DOWNLOAD_UMASK_DESTINATION"
	const childServer = "OPENAI_TEST_BATCH_DOWNLOAD_UMASK_SERVER"
	if path := os.Getenv(childDestination); path != "" {
		root := batchDownloadTestCommand(t, os.Getenv(childServer), io.Discard)
		require.NoError(t, root.Run(t.Context(), []string{"openai", "batches", "download", "batch_example", "--output", path}))
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use Unix umask")
	}
	payload := []byte("{\"custom_id\":\"synthetic\"}\r\n\x00\xff")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batches/batch_example" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"batch_example","output_file_id":"file_output"}`)
			return
		}
		if r.URL.Path != "/files/file_output/content" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, mask := range []os.FileMode{0o077, 0o400, 0o777} {
		t.Run(fmt.Sprintf("mask_%03o", mask), func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "result.jsonl")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "sh", "-c", `umask "$1"; shift; exec "$@"`, "batch-download-umask", fmt.Sprintf("%03o", mask), executable, "-test.run=^TestBatchDownloadRestrictiveUmask$")
			command.Env = append(os.Environ(), childDestination+"="+path, childServer+"="+server.URL)
			command.WaitDelay = time.Second
			output, err := command.CombinedOutput()
			require.NoError(t, err, "native umask child: %s", output)
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, payload, data)
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, "result.jsonl", entries[0].Name())
		})
	}
}

func TestBatchOutputDirectoryPreservesExistingPaths(t *testing.T) {
	for _, name := range []string{"directory", "directory_link", "existing_result", "file", "broken_link", "missing_parent"} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "results")
			var before os.FileInfo
			switch name {
			case "directory", "directory_link", "existing_result":
				require.NoError(t, os.Mkdir(path, 0700))
				require.NoError(t, os.Chmod(path, 0770))
				var err error
				before, err = os.Stat(path)
				require.NoError(t, err)
				if name == "directory_link" {
					link := filepath.Join(parent, "linked-results")
					if err := os.Symlink(path, link); err != nil {
						t.Skipf("directory symlinks unavailable: %v", err)
					}
					path = link
				} else if name == "existing_result" {
					require.NoError(t, os.WriteFile(filepath.Join(path, "output.jsonl"), []byte("preserved"), 0600))
				}
			case "file":
				require.NoError(t, os.WriteFile(path, []byte("preserved"), 0600))
			case "broken_link":
				if err := os.Symlink(filepath.Join(parent, "absent"), path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "missing_parent":
				path = filepath.Join(path, "nested")
			}
			destination, err := prepareBatchOutputDirectory(t.Context(), path)
			if name == "directory" || name == "directory_link" {
				require.NoError(t, err)
				require.True(t, os.SameFile(before, destination.info))
			} else {
				require.Error(t, err)
			}
			if before != nil {
				after, err := os.Stat(path)
				require.NoError(t, err)
				require.True(t, os.SameFile(before, after))
				require.Equal(t, before.Mode(), after.Mode())
			}
			if name == "existing_result" || name == "file" {
				file := path
				if name == "existing_result" {
					file = filepath.Join(path, "output.jsonl")
				}
				data, err := os.ReadFile(file)
				require.NoError(t, err)
				require.Equal(t, "preserved", string(data))
			}
			if name == "missing_parent" {
				entries, err := os.ReadDir(parent)
				require.NoError(t, err)
				require.Empty(t, entries)
			}
			if name == "broken_link" {
				info, err := os.Lstat(path)
				require.NoError(t, err)
				require.NotZero(t, info.Mode()&os.ModeSymlink)
			}
		})
	}
}

func TestBatchOutputDirectoryPreservesParentUmask(t *testing.T) {
	const childDirectory = "OPENAI_TEST_BATCH_DIRECTORY_UMASK"
	if directory := os.Getenv(childDirectory); directory != "" {
		create := func(name string) os.FileMode {
			file, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
			require.NoError(t, err)
			info, err := file.Stat()
			require.NoError(t, err)
			require.NoError(t, file.Close())
			return info.Mode().Perm()
		}
		before := create("before")
		output := filepath.Join(directory, "results")
		_, err := prepareBatchOutputDirectory(t.Context(), output)
		require.NoError(t, err)
		info, err := os.Stat(output)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0700), info.Mode().Perm())
		_, err = createPrivateBatchDirectory(t.Context(), filepath.Join(directory, "missing", "results"))
		require.ErrorIs(t, err, os.ErrNotExist)
		require.Equal(t, before, create("after"), "directory creation changed the parent's umask")
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use Unix umask")
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, mask := range []os.FileMode{0077, 0400, 0777} {
		t.Run(fmt.Sprintf("%03o", mask), func(t *testing.T) {
			directory := t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, "sh", "-c", `umask "$1"; shift; exec "$@"`, "batch-directory-umask", fmt.Sprintf("%03o", mask), executable, "-test.run=^TestBatchOutputDirectoryPreservesParentUmask$")
			child.Env = []string{childDirectory + "=" + directory, "GOMAXPROCS=2"}
			child.WaitDelay = time.Second
			output, err := child.CombinedOutput()
			require.NoError(t, err, "parent umask process: %s", output)
			for _, name := range []string{"before", "after"} {
				info, err := os.Stat(filepath.Join(directory, name))
				require.NoError(t, err)
				require.Equal(t, 0666&^mask, info.Mode().Perm())
			}
		})
	}
}

func TestBatchOutputDirectoryCreationErrors(t *testing.T) {
	for _, name := range []string{"existing", "missing_parent", "file_parent", "denied_parent", "canceled"} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "results")
			ctx := t.Context()
			var want error
			switch name {
			case "existing":
				require.NoError(t, os.Mkdir(path, 0700))
				want = os.ErrExist
			case "missing_parent":
				path = filepath.Join(parent, "missing", "results")
				want = os.ErrNotExist
			case "file_parent":
				require.NoError(t, os.WriteFile(path, nil, 0600))
				path += string(os.PathSeparator) + "results"
				if runtime.GOOS != "windows" {
					want = syscall.ENOTDIR
				}
			case "denied_parent":
				if runtime.GOOS == "windows" {
					t.Skip("Windows permission behavior has separate ACL tests")
				}
				if os.Geteuid() == 0 {
					t.Skip("root bypasses directory permissions")
				}
				require.NoError(t, os.Chmod(parent, 0500))
				defer os.Chmod(parent, 0700)
				want = os.ErrPermission
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			}
			_, err := createPrivateBatchDirectory(ctx, path)
			require.Error(t, err)
			if want != nil {
				require.ErrorIs(t, err, want)
			}
			if name == "canceled" {
				_, err := os.Lstat(path)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestBatchOutputDirectoryHelperLifelineStopsBlockedStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory helper uses inherited pipe descriptors")
	}
	for attempt := 0; attempt < 3; attempt++ {
		t.Run(fmt.Sprint(attempt), func(t *testing.T) {
			replyRead, replyWrite, err := os.Pipe()
			require.NoError(t, err)
			defer replyRead.Close()
			defer replyWrite.Close()
			// Fill only this fixture's pipe. The real helper cannot finish its
			// readiness write until the parent consumes bytes or closes its lifeline.
			require.NoError(t, replyWrite.SetWriteDeadline(time.Now().Add(50*time.Millisecond)))
			_, err = replyWrite.Write(bytes.Repeat([]byte{'x'}, 4<<20))
			require.ErrorIs(t, err, os.ErrDeadlineExceeded)
			require.NoError(t, replyWrite.SetWriteDeadline(time.Time{}))
			lifeRead, lifeWrite, err := os.Pipe()
			require.NoError(t, err)
			defer lifeRead.Close()
			defer lifeWrite.Close()
			executable, err := os.Executable()
			require.NoError(t, err)
			path := filepath.Join(t.TempDir(), "results")
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, executable, "__batch-directory", path, "3")
			child.Env = []string{"GOMAXPROCS=2"}
			child.ExtraFiles = []*os.File{lifeRead}
			child.Stdout = replyWrite
			child.WaitDelay = time.Second
			require.NoError(t, child.Start())
			require.NoError(t, lifeRead.Close())
			require.NoError(t, replyWrite.Close())
			// EOF is the same kernel event produced when the parent dies.
			require.NoError(t, lifeWrite.Close())
			err = child.Wait()
			require.Error(t, err)
			require.NoError(t, ctx.Err(), "lifeline did not stop and reap the helper")
			_, err = os.Lstat(path)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestBatchOutputDirectoryRepeatedFailuresCloseDescriptors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory helper owns inherited pipe descriptors")
	}
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing", "results")
	// Warm up runtime poll descriptors before measuring helper-owned pipes.
	_, err := createPrivateBatchDirectory(t.Context(), missing)
	require.ErrorIs(t, err, os.ErrNotExist)
	before, err := os.ReadDir("/dev/fd")
	require.NoError(t, err)
	for attempt := 0; attempt < 20; attempt++ {
		_, err := createPrivateBatchDirectory(t.Context(), missing)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	_, err = createPrivateBatchDirectory(t.Context(), filepath.Join(directory, "results"))
	require.NoError(t, err)
	after, err := os.ReadDir("/dev/fd")
	require.NoError(t, err)
	require.LessOrEqual(t, len(after), len(before), "repeated helper failures leaked descriptors")
}

func TestBatchOutputDirectoryPreservesPathTraversal(t *testing.T) {
	separator := string(os.PathSeparator)
	for _, component := range []string{"missing", "file", "symlink", "trailing_separator"} {
		t.Run(component, func(t *testing.T) {
			fixture := func() (string, string) {
				parent := t.TempDir()
				path := parent + separator + component + separator + ".." + separator + "results"
				if component == "file" {
					require.NoError(t, os.WriteFile(filepath.Join(parent, component), []byte("preserved"), 0600))
				}
				if component == "symlink" {
					target := filepath.Join(parent, "actual", "child")
					require.NoError(t, os.MkdirAll(target, 0700))
					if err := os.Symlink(target, filepath.Join(parent, component)); err != nil {
						t.Skipf("directory symlinks unavailable: %v", err)
					}
				}
				if component == "trailing_separator" {
					path = filepath.Join(parent, "results") + separator
				}
				return parent, path
			}
			controlParent, controlPath := fixture()
			parent, path := fixture()
			controlErr := os.Mkdir(controlPath, 0700)
			destination, err := prepareBatchOutputDirectory(t.Context(), path)
			require.Equal(t, controlErr == nil, err == nil, "os.Mkdir: %v; Batch: %v", controlErr, err)
			if err == nil {
				info, err := os.Stat(path)
				require.NoError(t, err)
				require.True(t, os.SameFile(destination.info, info))
			}
			for _, relative := range []string{"results", filepath.Join("actual", "results")} {
				_, controlErr := os.Lstat(filepath.Join(controlParent, relative))
				_, err := os.Lstat(filepath.Join(parent, relative))
				require.Equal(t, controlErr == nil, err == nil, "resolved location: %s", relative)
			}
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
