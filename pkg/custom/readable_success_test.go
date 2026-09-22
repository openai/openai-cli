package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestReadableBodylessActionConfirmsOnlySuccess(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		failure error
		want    string
	}{
		{name: "default", want: "Deleted response \"resp_synthetic\".\n"},
		{name: "explicit text", args: []string{"--format", "text"}, want: "Deleted response \"resp_synthetic\".\n"},
		{name: "JSON", args: []string{"--format", "json"}},
		{name: "raw", args: []string{"--format", "raw"}},
		{name: "transformed", args: []string{"--transform", "id"}},
		{name: "raw output", args: []string{"--raw-output"}},
		{name: "failure", failure: errors.New("synthetic failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			root := readableSuccessCommand(&out, func(context.Context, *cli.Command) error { return test.failure })
			args := append([]string{"openai"}, test.args...)
			err := root.Run(t.Context(), append(args, "responses", "delete", "--response-id", "resp_synthetic"))
			if test.failure == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.failure)
			}
			require.Equal(t, test.want, out.String())
		})
	}
}

func TestReadableBodylessActionPreservesFailuresAndExitCodes(t *testing.T) {
	failure := cli.Exit("synthetic failure", 7)
	root := readableSuccessCommand(io.Discard, func(context.Context, *cli.Command) error { return failure })
	err := root.Run(t.Context(), []string{"openai", "responses", "delete"})
	var wrapped *readableCommandError
	require.ErrorAs(t, err, &wrapped)
	require.Equal(t, "delete", wrapped.command.Name)
	var exitErr cli.ExitCoder
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 7, exitErr.ExitCode())
	require.ErrorIs(t, err, failure)
}

func TestReadableBodylessActionPropagatesWriteFailure(t *testing.T) {
	failure := errors.New("synthetic write failure")
	root := readableSuccessCommand(readableSuccessFailureWriter{failure}, func(context.Context, *cli.Command) error { return nil })
	require.ErrorIs(t, root.Run(t.Context(), []string{"openai", "responses", "delete"}), failure)
}

func TestReadableBodylessActionEscapesID(t *testing.T) {
	var out bytes.Buffer
	root := readableSuccessCommand(&out, func(context.Context, *cli.Command) error { return nil })
	require.NoError(t, root.Run(t.Context(), []string{"openai", "responses", "delete", "--response-id", "resp_\n\x1b]52;c;synthetic\a\u202e"}))
	require.NotContains(t, out.String(), "\x1b")
	require.NotContains(t, out.String(), "\u202e")
	require.Equal(t, 1, bytes.Count(out.Bytes(), []byte("\n")))
}

type readableSuccessFailureWriter struct{ err error }

func (w readableSuccessFailureWriter) Write([]byte) (int, error) { return 0, w.err }

func readableSuccessCommand(writer io.Writer, action cli.ActionFunc) *cli.Command {
	root := &cli.Command{
		Name: "openai", Writer: writer, ErrWriter: io.Discard,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "transform"}, &cli.BoolFlag{Name: "raw-output"},
		},
		Commands: []*cli.Command{{Name: "responses", Category: "API RESOURCE", Commands: []*cli.Command{{
			Name: "delete", Action: action, Flags: []cli.Flag{&cli.StringFlag{Name: "response-id"}},
		}}}},
	}
	configureReadableGuidance(root)
	return root
}
