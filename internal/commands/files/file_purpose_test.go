package files

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	localcommands "github.com/openai/openai-cli/internal/commands"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

var purposeValidationRoutes = [][2]string{{"files", "create"}, {"files", "upload"}, {"files", "list"}, {"uploads", "create"}}

func purposeValidationTree(resource, operation string, action cli.ActionFunc) (*cli.Command, *cli.Command) {
	command := &cli.Command{Name: operation, Action: action}
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{
		{Name: resource, Commands: []*cli.Command{command}},
	}}
	Configure(root)
	return root, command
}

func purposeValidationContents(route [2]string, fields map[string]any) requestflag.RequestContents {
	if route == [2]string{"files", "list"} {
		return requestflag.RequestContents{Queries: fields}
	}
	return requestflag.RequestContents{Body: fields}
}

func TestFilePurposeValidationPreservesAllowedScalars(t *testing.T) {
	for _, route := range purposeValidationRoutes {
		t.Run(strings.Join(route[:], "/"), func(t *testing.T) {
			for _, value := range []any{"batch", "user_data", "assistants", "vision", "evals", "future-purpose", "", nil, false, 0} {
				fields := map[string]any{"purpose": value, "filename": "private filename.jsonl", "bytes": int64(9007199254740993)}
				contents := purposeValidationContents(route, fields)
				calls := 0
				root, command := purposeValidationTree(route[0], route[1], func(_ context.Context, command *cli.Command) error {
					calls++
					require.Equal(t, route[0], localcommands.ResourceName(command))
					return ValidatePurpose(command, contents)
				})
				require.NoError(t, root.Run(t.Context(), []string{"openai", route[0], route[1]}))
				require.Equal(t, 1, calls)
				require.Equal(t, value, fields["purpose"])
				require.Equal(t, "private filename.jsonl", fields["filename"])
				require.Equal(t, int64(9007199254740993), fields["bytes"])
				require.NotContains(t, command.Metadata, purposeContextMetadata)
			}
		})
	}
}

func TestFilePurposeValidationRejectsRetiredPurposes(t *testing.T) {
	for _, route := range purposeValidationRoutes {
		for _, purpose := range []string{"fine-tune", "fine-tune-results"} {
			t.Run(strings.Join(route[:], "/")+"/"+purpose, func(t *testing.T) {
				for _, fromReader := range []bool{false, true} {
					var value any = purpose
					var source *purposeOwnedReader
					if fromReader {
						source = &purposeOwnedReader{reader: strings.NewReader(purpose)}
						value = requestconfig.FileUpload{Reader: source, FileName: "private-purpose.txt", MediaType: "text/plain"}
					}
					fields := map[string]any{"purpose": value}
					contents := purposeValidationContents(route, fields)
					root, command := purposeValidationTree(route[0], route[1], func(_ context.Context, command *cli.Command) error {
						return ValidatePurpose(command, contents)
					})
					err := root.Run(t.Context(), []string{"openai", route[0], route[1]})
					var localErr *localcommands.LocalError
					require.ErrorAs(t, err, &localErr)
					require.Contains(t, err.Error(), "file purpose is unavailable")
					require.NotContains(t, err.Error(), purpose)
					require.NotContains(t, err.Error(), "private-purpose.txt")
					require.NotContains(t, command.Metadata, purposeContextMetadata)
					if source != nil {
						require.EqualValues(t, 1, source.closes.Load())
						require.NoError(t, requestconfig.CloseFileUploads(fields))
						require.EqualValues(t, 1, source.closes.Load())
					}
				}
			})
		}
	}
}

func TestFilePurposeValidationReplaysScalarReaders(t *testing.T) {
	for _, route := range purposeValidationRoutes {
		for _, owned := range []bool{false, true} {
			t.Run(strings.Join(route[:], "/")+map[bool]string{false: "/borrowed", true: "/owned"}[owned], func(t *testing.T) {
				const body = "vision"
				source := &purposeOwnedReader{reader: strings.NewReader(body)}
				var value any = source
				if owned {
					value = requestconfig.FileUpload{Reader: source, FileName: "purpose with spaces.txt", MediaType: "text/plain", Size: 99}
				}
				fields := map[string]any{"purpose": value}
				root, _ := purposeValidationTree(route[0], route[1], func(_ context.Context, command *cli.Command) error {
					return ValidatePurpose(command, purposeValidationContents(route, fields))
				})
				require.NoError(t, root.Run(t.Context(), []string{"openai", route[0], route[1]}))
				if owned {
					upload, ok := fields["purpose"].(requestconfig.FileUpload)
					require.True(t, ok)
					require.Equal(t, "purpose with spaces.txt", upload.Filename())
					require.Equal(t, "text/plain", upload.ContentType())
					require.EqualValues(t, len(body), upload.Size)
					require.True(t, upload.KnownSize)
					require.EqualValues(t, 1, source.closes.Load())
				} else {
					require.Zero(t, source.closes.Load(), "borrowed readers remain caller-owned")
				}
				replayed, err := io.ReadAll(fields["purpose"].(io.Reader))
				require.NoError(t, err)
				require.Equal(t, body, string(replayed))
				require.NoError(t, requestconfig.CloseFileUploads(fields))
				require.EqualValues(t, map[bool]int{false: 0, true: 1}[owned], source.closes.Load())
			})
		}
	}
}

func TestFilePurposeValidationLeavesUnrelatedFieldsUntouched(t *testing.T) {
	for _, route := range append(append([][2]string{}, purposeValidationRoutes...), [2]string{"models", "create"}, [2]string{"files", "retrieve"}, [2]string{"uploads", "list"}) {
		t.Run(strings.Join(route[:], "/"), func(t *testing.T) {
			source := &purposeOwnedReader{reader: strings.NewReader("vision")}
			upload := requestconfig.FileUpload{Reader: source, FileName: "unrelated.txt"}
			body := map[string]any{"file": upload, "metadata": map[string]any{"purpose": "fine-tune"}, "filename": "vision"}
			queries := map[string]any{"unrelated": upload}
			contents := requestflag.RequestContents{Body: body, Queries: queries}
			if route == [2]string{"files", "list"} {
				body["purpose"] = upload
			} else if route == [2]string{"files", "create"} || route == [2]string{"files", "upload"} || route == [2]string{"uploads", "create"} {
				queries["purpose"] = upload
			} else {
				body["purpose"], queries["purpose"] = upload, upload
			}
			root, _ := purposeValidationTree(route[0], route[1], func(_ context.Context, command *cli.Command) error {
				return ValidatePurpose(command, contents)
			})
			require.NoError(t, root.Run(t.Context(), []string{"openai", route[0], route[1]}))
			require.Zero(t, source.reads.Load())
			require.Zero(t, source.closes.Load())
			require.Equal(t, upload, body["file"])
			require.Equal(t, map[string]any{"purpose": "fine-tune"}, body["metadata"])
			require.Equal(t, "vision", body["filename"])
		})
	}
}

func TestFilePurposeValidationPreservesOwnedReaderFailuresAndPrivacy(t *testing.T) {
	for _, kind := range []string{"read", "close", "both"} {
		t.Run(kind, func(t *testing.T) {
			readErr := errors.New("private read body /synthetic/private/source.txt")
			closeErr := errors.New("private close body /synthetic/private/source.txt")
			source := &purposeOwnedReader{reader: strings.NewReader("batch")}
			if kind != "close" {
				source.readErr = readErr
			}
			if kind != "read" {
				source.closeErr = closeErr
			}
			fields := map[string]any{"purpose": requestconfig.FileUpload{Reader: source, FileName: "private-source.txt"}}
			root, command := purposeValidationTree("files", "create", func(_ context.Context, command *cli.Command) error {
				return ValidatePurpose(command, requestflag.RequestContents{Body: fields})
			})
			err := root.Run(t.Context(), []string{"openai", "files", "create"})
			var scalarErr *requestconfig.MultipartScalarError
			require.ErrorAs(t, err, &scalarErr)
			require.Equal(t, "could not read --purpose from its file", err.Error())
			if source.readErr != nil {
				require.ErrorIs(t, err, readErr)
			}
			if source.closeErr != nil {
				require.ErrorIs(t, err, closeErr)
			}
			require.EqualValues(t, 1, source.closes.Load())
			require.NoError(t, requestconfig.CloseFileUploads(fields))
			require.EqualValues(t, 1, source.closes.Load())
			require.NotContains(t, command.Metadata, purposeContextMetadata)
		})
	}
}

func TestFilePurposeValidationUsesConfiguredCancellationContext(t *testing.T) {
	for _, route := range purposeValidationRoutes {
		t.Run(strings.Join(route[:], "/"), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			source := &purposeBlockingReader{entered: make(chan struct{}), released: make(chan struct{})}
			defer source.release()
			fields := map[string]any{"purpose": requestconfig.FileUpload{Reader: source, FileName: "private-blocked-purpose.txt"}}
			root, command := purposeValidationTree(route[0], route[1], func(_ context.Context, command *cli.Command) error {
				return ValidatePurpose(command, purposeValidationContents(route, fields))
			})
			result := make(chan error, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				result <- root.Run(ctx, []string{"openai", route[0], route[1]})
			}()
			defer func() {
				cancel()
				source.release()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Error("configured action did not finish during cleanup")
				}
			}()
			select {
			case <-source.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("configured action did not start scalar inspection")
			}
			cancel()
			select {
			case err := <-result:
				require.ErrorIs(t, err, context.Canceled)
				require.NotContains(t, err.Error(), "private-blocked-purpose")
			case <-time.After(3 * time.Second):
				t.Fatal("cancellation did not close the owned scalar reader")
			}
			require.EqualValues(t, 1, source.closes.Load())
			require.NoError(t, requestconfig.CloseFileUploads(fields))
			require.EqualValues(t, 1, source.closes.Load())
			require.NotContains(t, command.Metadata, purposeContextMetadata)
		})
	}
}

func TestFilePurposeValidationCancelsBorrowedStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("internal/input.ReadFile does not support cancellation of borrowed Windows pipes")
	}
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer reader.Close()
	defer writer.Close()
	const initial = "user_data"
	_, err = io.WriteString(writer, initial)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	boundary := make(chan struct{})
	fields := map[string]any{}
	root, command := purposeValidationTree("files", "create", func(_ context.Context, command *cli.Command) error {
		fields["purpose"] = &purposeReadBoundary{reader: RequestInput(command, reader), boundary: boundary}
		return ValidatePurpose(command, requestflag.RequestContents{Body: fields})
	})
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		result <- root.Run(ctx, []string{"openai", "files", "create"})
	}()
	defer func() {
		cancel()
		// Closing belongs to this test's caller, never to scalar inspection.
		_ = writer.Close()
		_ = reader.Close()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("borrowed stdin command did not finish during cleanup")
		}
	}()
	select {
	case <-boundary:
	case <-time.After(3 * time.Second):
		t.Fatal("scalar inspection did not reach the next pipe read")
	}
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not release scalar inspection while the pipe writer stayed open")
	}
	require.NotContains(t, command.Metadata, purposeContextMetadata)
	replayed, err := io.ReadAll(fields["purpose"].(io.Reader))
	require.NoError(t, err)
	require.Equal(t, initial, string(replayed))
	require.NoError(t, requestconfig.CloseFileUploads(fields))
	const following = "borrowed descriptor remains usable"
	_, err = io.WriteString(writer, following)
	require.NoError(t, err)
	require.NoError(t, reader.SetReadDeadline(time.Now().Add(3*time.Second)))
	data := make([]byte, len(following))
	_, err = io.ReadFull(reader, data)
	require.NoError(t, err)
	require.Equal(t, following, string(data))
}

type purposeReadBoundary struct {
	reader   io.Reader
	boundary chan struct{}
	reads    int
}

func (r *purposeReadBoundary) Read(data []byte) (int, error) {
	r.reads++
	if r.reads == 2 {
		close(r.boundary)
	}
	return r.reader.Read(data)
}

type purposeOwnedReader struct {
	reader   io.Reader
	readErr  error
	closeErr error
	reads    atomic.Int32
	closes   atomic.Int32
}

func (r *purposeOwnedReader) Read(p []byte) (int, error) {
	r.reads.Add(1)
	n, err := r.reader.Read(p)
	if errors.Is(err, io.EOF) && r.readErr != nil {
		err = r.readErr
	}
	return n, err
}

func (r *purposeOwnedReader) Close() error {
	r.closes.Add(1)
	return r.closeErr
}

type purposeBlockingReader struct {
	entered, released chan struct{}
	enterOnce         sync.Once
	releaseOnce       sync.Once
	closes            atomic.Int32
}

func (r *purposeBlockingReader) Read([]byte) (int, error) {
	r.enterOnce.Do(func() { close(r.entered) })
	<-r.released
	return 0, errors.New("private blocked scalar source closed")
}

func (r *purposeBlockingReader) Close() error {
	r.closes.Add(1)
	r.release()
	return nil
}

func (r *purposeBlockingReader) release() { r.releaseOnce.Do(func() { close(r.released) }) }
