package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// This fixture calls real input preparation. It uses no request or signal handler.
func TestSkillUploadRecoveryDeferredRead(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		for _, outcome := range []string{"unchanged", "truncated", "close_failed"} {
			truncate := outcome == "truncated"
			name := "generic_control"
			if deferred {
				name = "skills_deferred"
			}
			name += "/" + outcome
			t.Run(name, func(t *testing.T) {
				payload := []byte{'P', 'K', 0, 255, '\n', 'a', 'b', 'c'}
				directory := t.TempDir()
				path := filepath.Join(directory, "skill.zip")
				require.NoError(t, os.WriteFile(path, payload, 0o600))
				command := &cli.Command{Name: "create", Flags: []cli.Flag{
					&requestflag.Flag[[]string]{Name: "files", BodyPath: "files", FileInput: true},
				}}
				var state *skillUploadPreparation
				body := map[string]any{"files": []any{FilePathValue(path)}}
				if deferred {
					state = newSkillUploadPreparation(t.Context())
					t.Cleanup(state.finish)
					command.Metadata = map[string]any{skillUploadMetadata: state}
					deferSkillUploadInputs(command, body)
					require.IsType(t, deferredSkillFile{}, body["files"].([]any)[0])
				}
				stdin := &onceStdinReader{}
				embedded, err := embedRequestFiles(command, body, "body", EmbedIOReader, stdin)
				require.NoError(t, err)
				t.Cleanup(func() { _ = closeFileUploads(embedded) })
				if deferred {
					require.Empty(t, state.owned, "generic embedding must not prepare Skills paths")
					require.NoError(t, prepareSkillUploadInputs(command, embedded, stdin))
					require.Nil(t, state.stopSignals, "regular-file preparation must not install signals")
				}
				value := embedded.(map[string]any)["files"]
				if values, ok := value.([]any); ok {
					require.Len(t, values, 1)
					value = values[0]
				}
				upload, ok := value.(fileUpload)
				require.True(t, ok)
				require.Equal(t, "skill.zip", upload.Filename())
				require.Equal(t, "application/zip", upload.ContentType())
				require.True(t, upload.hasKnownSize())
				require.Equal(t, int64(8), upload.size)
				counter := skillRecoveryCountOpenedClose(t, upload.Reader)
				if outcome == "close_failed" {
					counter.failure = errors.Join(&os.PathError{Op: "close", Path: path, Err: os.ErrPermission}, cli.Exit("synthetic close failure", 19))
				}
				want := payload
				if truncate {
					require.NoError(t, os.Truncate(path, 4))
					want = payload[:4]
				}
				got, readErr := io.ReadAll(upload)
				require.Equal(t, want, got, "preserve every available byte")
				failure := errors.Join(readErr, upload.Close())
				if state != nil {
					failure = state.result(failure)
					require.Nil(t, state.stopSignals)
				}
				require.Equal(t, 1, counter.calls)
				require.NotErrorIs(t, failure, context.Canceled)
				onDisk, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, want, onDisk, "cleanup must preserve the input")
				if outcome == "unchanged" {
					require.NoError(t, failure)
					return
				}
				var exit cli.ExitCoder
				if truncate {
					require.ErrorIs(t, failure, io.ErrUnexpectedEOF)
					require.False(t, errors.As(failure, &exit), "preserve the default nonzero status")
				} else {
					require.ErrorIs(t, failure, counter.failure)
					require.ErrorAs(t, failure, &exit)
					require.Equal(t, 19, exit.ExitCode())
				}
				for _, format := range []string{"text", "json", "jsonl"} {
					message := skillRecoveryMessage(t, failure, format)
					t.Logf("format=%s message=%q bytes=%x closes=%d", format, message, got, counter.calls)
					require.NotContains(t, message, directory)
					if !strings.Contains(message, "--files") {
						t.Errorf("documented declared-option attribution missing in %s: %q", format, message)
					}
				}
			})
		}
	}
}

// This models the documented joined-error boundary. It is not a public request reproduction.
func TestSkillUploadRecoveryJoinedCleanup(t *testing.T) {
	state := newSkillUploadPreparation(t.Context())
	t.Cleanup(state.finish)
	missing := filepath.Join(t.TempDir(), "missing.zip")
	command := &cli.Command{Name: "create", Metadata: map[string]any{skillUploadMetadata: state}}
	body := map[string]any{"files": []any{FilePathValue(missing)}}
	deferSkillUploadInputs(command, body)
	primary := prepareSkillUploadInputs(command, body, &onceStdinReader{})
	require.ErrorIs(t, primary, os.ErrNotExist)
	var skillFailure *skillUploadError
	require.ErrorAs(t, primary, &skillFailure)
	var primaryPath *os.PathError
	require.ErrorAs(t, primary, &primaryPath)

	cleanupPath := &os.PathError{Op: "close", Path: "/synthetic-private-parent/opened.zip", Err: os.ErrPermission}
	exit := cli.Exit("synthetic-private-exit", 19)
	cleanupCause := errors.Join(cleanupPath, exit)
	underlying := &skillRecoveryCloser{Reader: strings.NewReader("unused synthetic bytes"), failure: cleanupCause}
	reader := &skillUploadReader{ReadCloser: &inputFileReader{
		Reader: underlying, source: fileInputSource{option: "--files"},
	}}
	state.body, state.owned = reader, []io.Closer{reader}
	failure := state.result(primary)
	require.ErrorIs(t, failure, primary)
	require.ErrorIs(t, failure, primaryPath)
	require.ErrorIs(t, failure, cleanupPath)
	require.ErrorIs(t, failure, cleanupCause)
	require.NotErrorIs(t, failure, context.Canceled)
	require.Equal(t, 1, underlying.calls)
	require.Nil(t, state.stopSignals)
	var gotExit cli.ExitCoder
	require.ErrorAs(t, withCommandError(command, failure), &gotExit)
	require.Same(t, exit, gotExit)
	require.Equal(t, 19, gotExit.ExitCode())
	cleanupMessage := "Could not close the file for --files. Check the path and permissions."
	control := inputFileErrorMessage(command, failure)
	require.Contains(t, control, primary.Error(), "#409 traversal retains the primary message")
	require.Contains(t, control, cleanupMessage, "#409 traversal retains cleanup guidance")
	for _, format := range []string{"text", "json", "jsonl"} {
		message := skillRecoveryMessage(t, failure, format)
		t.Logf("format=%s message=%q close_count=%d exit_identity=%d", format, message, underlying.calls, gotExit.ExitCode())
		require.Equal(t, 1, strings.Count(message, primary.Error()))
		require.NotContains(t, message, filepath.Dir(missing))
		require.NotContains(t, message, "synthetic-private")
		if strings.Count(message, cleanupMessage) != 1 {
			t.Errorf("joined cleanup guidance missing or duplicated in %s: %q", format, message)
		}
		writeFailure := errors.New("synthetic diagnostic sink failure")
		require.ErrorIs(t, ShowCommandError(readableErrorTestCommand(t, "--format-error", format), failure, errorSink{err: writeFailure}), writeFailure)
	}
	require.Equal(t, 1, underlying.calls)
}

func TestSkillUploadRecoveryPartialPreparation(t *testing.T) {
	state := newSkillUploadPreparation(t.Context())
	t.Cleanup(state.finish)
	directory := t.TempDir()
	path := filepath.Join(directory, "opened.zip")
	missing := filepath.Join(directory, "missing.zip")
	payload := []byte{'P', 'K', 0, 255}
	require.NoError(t, os.WriteFile(path, payload, 0o600))
	command := &cli.Command{Name: "create", Metadata: map[string]any{skillUploadMetadata: state}, Flags: []cli.Flag{
		&requestflag.Flag[[]string]{Name: "files", BodyPath: "files", FileInput: true},
	}}
	body := map[string]any{"files": []any{FilePathValue(path), FilePathValue(missing)}}
	deferSkillUploadInputs(command, body)
	primary := prepareSkillUploadInputs(command, body, &onceStdinReader{})
	require.ErrorIs(t, primary, os.ErrNotExist)
	require.Len(t, state.owned, 1)
	require.Nil(t, state.body, "preflight failure must precede request-body creation")
	require.Nil(t, state.stopSignals)
	upload := state.owned[0].(fileUpload)
	counter := skillRecoveryCountOpenedClose(t, upload.Reader)
	closeCause := &os.PathError{Op: "close", Path: path, Err: os.ErrPermission}
	counter.failure = errors.Join(closeCause, cli.Exit("synthetic close failure", 19))
	failure := state.result(primary)
	require.ErrorIs(t, failure, primary)
	require.ErrorIs(t, failure, closeCause)
	require.NotErrorIs(t, failure, context.Canceled)
	require.Equal(t, 1, counter.calls)
	var exit cli.ExitCoder
	require.ErrorAs(t, failure, &exit)
	require.Equal(t, 19, exit.ExitCode())
	onDisk, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, payload, onDisk)
	for _, format := range []string{"text", "json", "jsonl"} {
		message := skillRecoveryMessage(t, failure, format)
		require.Equal(t, 1, strings.Count(message, primary.Error()))
		require.Equal(t, 1, strings.Count(message, "Could not close the file for --files."))
		require.NotContains(t, message, directory)
	}
}

func TestSkillUploadRecoveryPreservesEarlyGuidanceAndCancellation(t *testing.T) {
	cause := (fileInputSource{option: "--files"}).failure("open", &os.PathError{
		Op: "open", Path: "/synthetic-private-parent/skill.zip", Err: os.ErrPermission,
	})
	primary := skillInputFailure(0, "skill.zip", cause)
	cleanup := (fileInputSource{option: "--files"}).failure("close", errors.New("synthetic private cleanup"))
	for _, format := range []string{"text", "json", "jsonl"} {
		require.Equal(t, primary.Error(), skillRecoveryMessage(t, primary, format))
		for _, cancellation := range []error{context.Canceled, context.DeadlineExceeded} {
			message := skillRecoveryMessage(t, errors.Join(primary, cleanup, cancellation), format)
			require.NotContains(t, message, "Could not close")
			require.NotContains(t, message, "Skill upload:")
			if cancellation == context.Canceled {
				require.Equal(t, "Request canceled.", message)
			} else {
				require.Contains(t, message, "timed out")
			}
		}
	}
}

type skillRecoveryCloser struct {
	io.Reader
	closer  io.Closer
	failure error
	calls   int
}

func (r *skillRecoveryCloser) Close() error {
	r.calls++
	var err error
	if r.closer != nil {
		err = r.closer.Close()
	}
	return errors.Join(err, r.failure)
}

func skillRecoveryCountOpenedClose(t *testing.T, reader io.Reader) *skillRecoveryCloser {
	t.Helper()
	for {
		switch value := reader.(type) {
		case *skillUploadReader:
			reader = value.ReadCloser
		case *inputFileReader:
			reader = value.Reader
		case *exactLengthReadCloser:
			counter := &skillRecoveryCloser{closer: value.closer}
			value.closer = counter
			return counter
		default:
			t.Fatalf("unexpected prepared reader %T", reader)
			return nil
		}
	}
}

func skillRecoveryMessage(t *testing.T, failure error, format string) string {
	t.Helper()
	root := readableErrorTestCommand(t, "--format-error", format)
	var output bytes.Buffer
	require.NoError(t, ShowCommandError(root, failure, &output))
	if format == "text" {
		return strings.TrimSuffix(output.String(), "\n")
	}
	var payload struct{ Message string }
	decoder := json.NewDecoder(&output)
	require.NoError(t, decoder.Decode(&payload))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF)
	return payload.Message
}
