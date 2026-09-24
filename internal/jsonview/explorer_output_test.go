package jsonview

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExplorerRoutesDisplayAndSelectedValueToOutput(t *testing.T) {
	var output bytes.Buffer
	err := runOutputExplorer(t, &output)
	require.NoError(t, err)
	require.Contains(t, output.String(), "synthetic explorer")
	require.True(t, strings.HasSuffix(output.String(), "\nselected value\n"), "selected value must follow the display on the same output")
}

func TestExplorerReturnsSelectedValueWriteFailure(t *testing.T) {
	failure := errors.New("synthetic output failure")
	for _, test := range []struct {
		name   string
		writer io.Writer
		want   error
	}{
		{name: "write error", writer: explorerFailingWriter{err: failure}, want: failure},
		{name: "short write", writer: explorerFailingWriter{}, want: io.ErrShortWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := runOutputExplorer(t, test.writer, tea.WithoutRenderer())
			require.ErrorIs(t, err, test.want)
		})
	}
}

func TestExplorerReturnsDisplayWriteFailure(t *testing.T) {
	failure := errors.New("synthetic display failure")
	for _, test := range []struct {
		name   string
		writer io.Writer
		want   error
	}{
		{name: "write error", writer: explorerFailingWriter{err: failure}, want: failure},
		{name: "short write", writer: explorerFailingWriter{}, want: io.ErrShortWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Quitting without printing proves the failure came from UI output.
			err := runOutputExplorer(t, test.writer, tea.WithInput(strings.NewReader("q")))
			require.ErrorIs(t, err, test.want)
		})
	}
}

func TestExplorerOutputPreservesTerminalFile(t *testing.T) {
	tracked := &explorerOutput{writer: os.Stdout}
	file, ok := tracked.terminalOutput().(term.File)
	require.True(t, ok, "Bubble Tea requires term.File for terminal detection")
	require.Equal(t, os.Stdout.Fd(), file.Fd())

	tracked = &explorerOutput{writer: io.Discard}
	_, ok = tracked.terminalOutput().(term.File)
	require.False(t, ok, "ordinary writers must not acquire a file descriptor")
}

func TestExplorerOutputPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runOutputExplorer(t, io.Discard, tea.WithContext(ctx), tea.WithoutRenderer())
	require.ErrorIs(t, err, context.Canceled)
}

func runOutputExplorer(t *testing.T, output io.Writer, options ...tea.ProgramOption) error {
	t.Helper()
	view, err := newView("", gjson.Parse(`["selected value"]`), false)
	require.NoError(t, err)
	viewer := &JSONViewer{stack: []JSONView{view}, root: "synthetic explorer", help: help.New()}
	viewer.resize(80, 24)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	programOptions := []tea.ProgramOption{tea.WithContext(ctx), tea.WithInput(strings.NewReader("p")), tea.WithWindowSize(80, 24)}
	programOptions = append(programOptions, options...)
	return runExplorerWithOutput(viewer, output, programOptions...)
}

type explorerFailingWriter struct{ err error }

func (w explorerFailingWriter) Write(p []byte) (int, error) {
	return len(p) - 1, w.err
}
