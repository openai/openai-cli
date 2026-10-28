package request

import (
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

func TestInputFileErrorsUseBodyRootOption(t *testing.T) {
	command := &cli.Command{Flags: []cli.Flag{
		&requestflag.Flag[any]{Name: "request", BodyRoot: true},
	}}
	_, err := EmbedRequestFiles(command, []any{"@" + filepath.Join(t.TempDir(), "missing.txt")}, "body", EmbedText, nil)
	var input *InputFileError
	require.ErrorAs(t, err, &input)
	require.Equal(t, "--request", input.Option)
}

func TestInputFileExpansionPreservesNonFileValues(t *testing.T) {
	command := &cli.Command{Flags: []cli.Flag{
		&requestflag.Flag[any]{Name: "input", BodyPath: "input"},
	}}
	missing := "@" + filepath.Join(t.TempDir(), "synthetic-private-missing.txt")
	for _, style := range []FileEmbedStyle{EmbedText, EmbedIOReader} {
		body := map[string]any{"input": []any{UntrustedStdinValue(missing), "\\" + missing, nil, false, 0}}
		embedded, err := EmbedRequestFiles(command, body, "body", style, nil)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"input": []any{missing, missing, nil, false, 0}}, embedded)
	}
}

func TestInputFileFailurePreservesJoinedCausesAndExitStatus(t *testing.T) {
	path := &os.PathError{Op: "open", Path: "synthetic-private-path", Err: os.ErrNotExist}
	link := &os.LinkError{Op: "rename", Old: "synthetic-private-old", New: "synthetic-private-new", Err: os.ErrPermission}
	exit := cli.Exit("synthetic-private-exit", 19)
	cause := errors.Join(path, link, exit)
	err := (FileInputSource{Option: "--file"}).Failure("open", cause)
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
	require.Same(t, cause, (FileInputSource{}).Failure("open", cause))
	require.NoError(t, (FileInputSource{Option: "--file"}).Failure("open", nil))
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
			reader := &InputFileReader{Reader: errorReader{err: test.err}, Source: FileInputSource{Option: "--data"}}
			_, err := reader.Read(make([]byte, 1))
			require.ErrorIs(t, err, io.EOF)
			if test.err == io.EOF {
				require.Same(t, io.EOF, err)
			} else {
				require.ErrorIs(t, err, readFailure)
				var input *InputFileError
				require.ErrorAs(t, err, &input)
				require.Equal(t, "--data", input.Option)
			}
		})
	}
}

func TestInputFileUploadPreservesMetadataAndLateReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-private-upload.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic payload"), 0o600))
	upload, err := (FileInputSource{Option: "--data"}).OpenUpload(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, upload.Close()) })
	require.Equal(t, filepath.Base(path), upload.Filename())
	require.True(t, strings.HasPrefix(upload.ContentType(), "text/plain"), upload.ContentType())
	require.True(t, upload.KnownSize)
	require.Equal(t, int64(len("synthetic payload")), upload.Size)
	// Truncate the actual opened source after size inspection.
	require.NoError(t, os.Truncate(path, 0))
	_, err = io.ReadAll(upload)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	var input *InputFileError
	require.ErrorAs(t, err, &input)
	require.Equal(t, "--data", input.Option)
	require.Equal(t, "read", input.Operation)
}
