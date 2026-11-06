package app

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
	requestconfig "github.com/openai/openai-cli/internal/request"
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
			_, err := requestconfig.EmbedRequestFiles(command, test.body, test.area, requestconfig.EmbedText, nil)
			require.ErrorIs(t, err, os.ErrNotExist)
			var input *requestconfig.InputFileError
			if test.option == "" {
				require.False(t, errors.As(err, &input))
			} else {
				require.ErrorAs(t, err, &input)
				require.Equal(t, test.option, input.Option)
				require.Contains(t, localErrorMessage(command, err), "the file for "+test.option+".")
			}
			require.NotContains(t, localErrorMessage(command, err), "synthetic-private")
		})
	}
}

func TestInputFileReadRetainsActualPathErrorPrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-private-missing.txt")
	_, err := (requestconfig.FileInputSource{Option: "--input"}).ReadFile(path)
	var cause *os.PathError
	require.ErrorAs(t, err, &cause)
	require.Equal(t, path, cause.Path)
	require.ErrorIs(t, err, os.ErrNotExist)
	var out bytes.Buffer
	require.NoError(t, ShowCommandError(readableErrorTestCommand(t), err, &out))
	require.Contains(t, out.String(), "the file for --input. Check the path and permissions.")
	require.NotContains(t, out.String(), path)
}

func TestInputFileErrorsPreserveCleanupPresentation(t *testing.T) {
	readCause := &os.PathError{Op: "read", Path: "synthetic-private-input", Err: io.ErrUnexpectedEOF}
	closeCause := &os.PathError{Op: "close", Path: "synthetic-private-input", Err: os.ErrPermission}
	underlying := &recordingReadCloser{reader: errorReader{err: readCause}, closeErr: closeCause}
	reader := &requestconfig.InputFileReader{Reader: underlying, Source: requestconfig.FileInputSource{Option: "--data"}}
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
	source := requestconfig.FileInputSource{Option: "--image"}
	command := &cli.Command{Flags: []cli.Flag{
		&requestflag.Flag[[]string]{Name: "image", BodyPath: "image", FileInput: true},
	}}
	_, err := requestconfig.EmbedRequestFiles(command, map[string]any{"image": []any{
		requestconfig.FileUpload{Reader: &requestconfig.InputFileReader{Reader: opened, Source: source}, FileName: "synthetic-private-opened.txt"},
		requestconfig.FilePathValue(filepath.Join(t.TempDir(), "synthetic-private-missing.txt")),
	}}, "body", requestconfig.EmbedIOReader, nil)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorIs(t, err, closeCause)
	require.Equal(t, int32(1), opened.closeCount.Load())
	var out bytes.Buffer
	require.NoError(t, ShowCommandError(readableErrorTestCommand(t), err, &out))
	require.Equal(t, "Could not open the file for --image. Check the path and permissions.\n"+
		"Could not close the file for --image. Check the path and permissions.\n", out.String())
}

func TestFlagOptionsRetainsFileOptionProvenance(t *testing.T) {
	t.Setenv(requestconfig.UntrustedStdinEnv, "false")
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
	var input *requestconfig.InputFileError
	require.ErrorAs(t, err, &input)
	require.Equal(t, "--data", input.Option)
	require.Equal(t, "Could not open the file for --data. Check the path and permissions.", localErrorMessage(command, err))
}

func TestInputFileCleanupKeepsPrimaryGuidanceWithoutProvenance(t *testing.T) {
	source := requestconfig.FileInputSource{Option: "--file"}
	cleanup := source.Failure("close", &os.PathError{Op: "close", Path: "synthetic-private-cleanup", Err: os.ErrPermission})
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
				failure = (requestconfig.FileInputSource{Option: "--input"}).Failure("open", cause)
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
