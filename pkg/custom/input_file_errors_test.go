package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestInputFileErrorsUseDeclaredRequestOptions(t *testing.T) {
	missing := "@" + filepath.Join(t.TempDir(), "synthetic-private-missing.txt")
	message := &requestflag.Flag[[]map[string]any]{Name: "message", BodyPath: "messages"}
	command := &cli.Command{Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "document", BodyPath: "shared"},
		&requestflag.Flag[string]{Name: "cursor", QueryPath: "shared"},
		&requestflag.Flag[string]{Name: "H", HeaderPath: "shared"},
		message,
		&requestflag.InnerFlag[string]{Name: "message.content", OuterFlag: message, InnerField: "content"},
	}}
	for _, test := range []struct {
		name, area, option string
		body               any
	}{
		{"body", "body", "--document", map[string]any{"shared": missing}},
		{"query", "query", "--cursor", map[string]any{"shared": missing}},
		{"header", "header", "-H", map[string]any{"shared": missing}},
		{"nested array", "body", "--message.content", map[string]any{"messages": []any{map[string]any{"content": missing}}}},
		{"unknown nested field", "body", "--message", map[string]any{"messages": map[string]any{"synthetic-private-field": missing}}},
		{"unknown root field", "body", "", map[string]any{"synthetic-private-field": missing}},
		{"literal dotted field", "body", "", map[string]any{"messages.content": missing}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := embedRequestFiles(command, test.body, test.area, EmbedText, nil)
			require.ErrorIs(t, err, os.ErrNotExist)
			var input *inputFileError
			if test.option == "" {
				require.False(t, errors.As(err, &input))
			} else {
				require.ErrorAs(t, err, &input)
				require.Equal(t, test.option, input.option)
				require.Contains(t, localErrorMessage(command, err), "the file for "+test.option+".")
			}
			require.NotContains(t, localErrorMessage(command, err), "synthetic-private")
		})
	}
}

func TestInputFileErrorsUseBodyRootOption(t *testing.T) {
	command := &cli.Command{Flags: []cli.Flag{
		&requestflag.Flag[any]{Name: "request", BodyRoot: true},
	}}
	_, err := embedRequestFiles(command, []any{"@" + filepath.Join(t.TempDir(), "missing.txt")}, "body", EmbedText, nil)
	var input *inputFileError
	require.ErrorAs(t, err, &input)
	require.Equal(t, "--request", input.option)
}

func TestInputFileExpansionPreservesNonFileValues(t *testing.T) {
	command := &cli.Command{Flags: []cli.Flag{
		&requestflag.Flag[any]{Name: "input", BodyPath: "input"},
	}}
	missing := "@" + filepath.Join(t.TempDir(), "synthetic-private-missing.txt")
	for _, style := range []FileEmbedStyle{EmbedText, EmbedIOReader} {
		body := map[string]any{"input": []any{untrustedStdinValue(missing), "\\" + missing, nil, false, 0}}
		embedded, err := embedRequestFiles(command, body, "body", style, nil)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"input": []any{missing, missing, nil, false, 0}}, embedded)
	}
}

func TestInputFileFailurePreservesJoinedCausesAndExitStatus(t *testing.T) {
	path := &os.PathError{Op: "open", Path: "synthetic-private-path", Err: os.ErrNotExist}
	link := &os.LinkError{Op: "rename", Old: "synthetic-private-old", New: "synthetic-private-new", Err: os.ErrPermission}
	exit := cli.Exit("synthetic-private-exit", 19)
	cause := errors.Join(path, link, exit)
	err := (fileInputSource{option: "--file"}).failure("open", cause)
	require.ErrorIs(t, err, path)
	require.ErrorIs(t, err, link)
	var gotPath *os.PathError
	var gotLink *os.LinkError
	var gotExit cli.ExitCoder
	require.ErrorAs(t, err, &gotPath)
	require.ErrorAs(t, err, &gotLink)
	require.ErrorAs(t, err, &gotExit)
	require.Same(t, path, gotPath)
	require.Same(t, link, gotLink)
	require.Equal(t, 19, gotExit.ExitCode())
	require.Same(t, cause, (fileInputSource{}).failure("open", cause))
	require.NoError(t, (fileInputSource{option: "--file"}).failure("open", nil))
}

func TestInputFileReadRetainsActualPathErrorPrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-private-missing.txt")
	_, err := (fileInputSource{option: "--input"}).readFile(path)
	var cause *os.PathError
	require.ErrorAs(t, err, &cause)
	require.Equal(t, path, cause.Path)
	require.ErrorIs(t, err, os.ErrNotExist)
	var out bytes.Buffer
	require.NoError(t, ShowCommandError(readableErrorTestCommand(t), err, &out))
	require.Contains(t, out.String(), "the file for --input. Check the path and permissions.")
	require.NotContains(t, out.String(), path)
}

func TestInputFileReaderPreservesEOFAndJoinedFailures(t *testing.T) {
	readFailure := errors.New("synthetic-private-read")
	for _, test := range []struct {
		name string
		err  error
	}{
		{"EOF", io.EOF},
		{"joined EOF", errors.Join(io.EOF, readFailure)},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &inputFileReader{Reader: errorReader{err: test.err}, source: fileInputSource{option: "--data"}}
			_, err := reader.Read(make([]byte, 1))
			require.ErrorIs(t, err, io.EOF)
			if test.err == io.EOF {
				require.Same(t, io.EOF, err)
			} else {
				require.ErrorIs(t, err, readFailure)
				var input *inputFileError
				require.ErrorAs(t, err, &input)
				require.Equal(t, "--data", input.option)
			}
		})
	}
}

func TestInputFileUploadPreservesMetadataAndLateReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-private-upload.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic payload"), 0o600))
	upload, err := (fileInputSource{option: "--data"}).openUpload(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, upload.Close()) })
	require.Equal(t, filepath.Base(path), upload.Filename())
	require.True(t, strings.HasPrefix(upload.ContentType(), "text/plain"), upload.ContentType())
	require.True(t, upload.hasKnownSize())
	require.Equal(t, int64(len("synthetic payload")), upload.size)
	// Truncate the actual opened source after size inspection.
	require.NoError(t, os.Truncate(path, 0))
	_, err = io.ReadAll(upload)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	var input *inputFileError
	require.ErrorAs(t, err, &input)
	require.Equal(t, "--data", input.option)
	require.Equal(t, "read", input.operation)
}

func TestInputFileErrorsPreserveCleanupPresentation(t *testing.T) {
	readCause := &os.PathError{Op: "read", Path: "synthetic-private-input", Err: io.ErrUnexpectedEOF}
	closeCause := &os.PathError{Op: "close", Path: "synthetic-private-input", Err: os.ErrPermission}
	underlying := &recordingReadCloser{reader: errorReader{err: readCause}, closeErr: closeCause}
	reader := &inputFileReader{Reader: underlying, source: fileInputSource{option: "--data"}}
	_, readErr := reader.Read(make([]byte, 1))
	closeErr := reader.Close()
	err := errors.Join(fmt.Errorf("synthetic-private-wrapper: %w", readErr), closeErr, readErr)
	require.ErrorIs(t, err, readCause)
	require.ErrorIs(t, err, closeCause)
	require.Equal(t, int32(1), underlying.closeCount.Load())
	want := "Could not read the file for --data. Check the path and permissions.\n" +
		"Could not close the file for --data. Check the path and permissions."
	for _, format := range []string{"text", "json", "jsonl"} {
		t.Run(format, func(t *testing.T) {
			root := readableErrorTestCommand(t, "--format-error", format)
			var out bytes.Buffer
			require.NoError(t, ShowCommandError(root, err, &out))
			if format == "text" {
				require.Equal(t, want+"\n", out.String())
			} else {
				var payload struct {
					Message string `json:"message"`
				}
				require.NoError(t, json.Unmarshal(out.Bytes(), &payload))
				require.Equal(t, want, payload.Message)
			}
			require.NotContains(t, out.String(), "synthetic-private")
			writeErr := errors.New("synthetic writer failure")
			require.ErrorIs(t, ShowCommandError(root, err, errorSink{err: writeErr}), writeErr)
			out.Reset()
			require.NoError(t, ShowCommandError(root, errors.Join(err, context.Canceled), &out))
			require.Contains(t, out.String(), "Request canceled.")
			require.NotContains(t, out.String(), "Could not read")
		})
	}
}

func TestInputFileExpansionRetainsFailedOpenAndPartialCleanup(t *testing.T) {
	closeCause := errors.New("synthetic-private-close")
	opened := &recordingReadCloser{reader: strings.NewReader("synthetic payload"), closeErr: closeCause}
	source := fileInputSource{option: "--image"}
	command := &cli.Command{Flags: []cli.Flag{
		&requestflag.Flag[[]string]{Name: "image", BodyPath: "image", FileInput: true},
	}}
	_, err := embedRequestFiles(command, map[string]any{"image": []any{
		fileUpload{Reader: &inputFileReader{Reader: opened, source: source}, filename: "synthetic-private-opened.txt"},
		FilePathValue(filepath.Join(t.TempDir(), "synthetic-private-missing.txt")),
	}}, "body", EmbedIOReader, nil)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorIs(t, err, closeCause)
	require.Equal(t, int32(1), opened.closeCount.Load())
	var out bytes.Buffer
	require.NoError(t, ShowCommandError(readableErrorTestCommand(t), err, &out))
	require.Equal(t, "Could not open the file for --image. Check the path and permissions.\n"+
		"Could not close the file for --image. Check the path and permissions.\n", out.String())
}

func TestFlagOptionsRetainsFileOptionProvenance(t *testing.T) {
	t.Setenv(untrustedStdinEnv, "false")
	path := filepath.Join(t.TempDir(), "synthetic-private-missing.bin")
	command := &cli.Command{
		Name: "upload", HideHelpCommand: true, Writer: io.Discard, ErrWriter: io.Discard,
		Flags:          []cli.Flag{&requestflag.Flag[string]{Name: "data", BodyPath: "data", FileInput: true}},
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Action: func(_ context.Context, command *cli.Command) error {
			_, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, MultipartFormEncoded, true)
			return err
		},
	}
	err := command.Run(t.Context(), []string{"upload", "--data", path})
	require.ErrorIs(t, err, os.ErrNotExist)
	var input *inputFileError
	require.ErrorAs(t, err, &input)
	require.Equal(t, "--data", input.option)
	require.Equal(t, "Could not open the file for --data. Check the path and permissions.", localErrorMessage(command, err))
}

func TestInputFileCleanupKeepsPrimaryGuidanceWithoutProvenance(t *testing.T) {
	source := fileInputSource{option: "--file"}
	cleanup := source.failure("close", &os.PathError{Op: "close", Path: "synthetic-private-cleanup", Err: os.ErrPermission})
	for _, test := range []struct {
		name    string
		primary error
		want    string
	}{
		{"unknown field", &os.PathError{Op: "open", Path: "synthetic-private-unknown", Err: os.ErrNotExist}, "A local file could not be found. Check your file arguments and @file references."},
		{"stdin conflict", errors.New("stdin has already been read by another parameter; it can only be read once"), "stdin has already been read by another parameter; it can only be read once"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			failure := errors.Join(test.primary, cleanup)
			require.NoError(t, ShowCommandError(readableErrorTestCommand(t), failure, &out))
			require.Equal(t, test.want+"\nCould not close the file for --file. Check the path and permissions.\n", out.String())
			require.NotContains(t, out.String(), "synthetic-private")
			require.ErrorIs(t, failure, test.primary)
		})
	}
}

func TestInputFilePresenterKeepsPrivatePathCauses(t *testing.T) {
	path := &os.PathError{Op: "open", Path: "/synthetic-private-home/secret\x1b[31m", Err: os.ErrPermission}
	link := &os.LinkError{Op: "rename", Old: "synthetic-private-old", New: "synthetic-private-new", Err: os.ErrPermission}
	for _, cause := range []error{path, link, fmt.Errorf("synthetic-private-wrapper: %w", path), errors.Join(path, link)} {
		for _, annotated := range []bool{false, true} {
			failure := cause
			if annotated {
				failure = (fileInputSource{option: "--input"}).failure("open", cause)
			}
			for _, format := range []string{"text", "json"} {
				root := readableErrorTestCommand(t, "--format-error", format)
				var out bytes.Buffer
				require.NoError(t, ShowCommandError(root, failure, &out))
				require.NotEmpty(t, out.String())
				require.NotContains(t, out.String(), "synthetic-private")
				require.NotContains(t, out.String(), "\x1b")
				require.ErrorIs(t, failure, cause)
				if annotated {
					require.Contains(t, out.String(), "the file for --input.")
				}
			}
		}
	}
}
