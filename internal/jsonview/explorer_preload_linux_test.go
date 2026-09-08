package jsonview

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/charmbracelet/x/term"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

type preloadIterator struct {
	next int
	err  error
}

func (it *preloadIterator) Next() bool   { it.next++; return true }
func (it *preloadIterator) Current() int { return it.next }
func (it *preloadIterator) Err() error   { return it.err }

func TestExploreJSONStreamPreloadUsesTerminalRows(t *testing.T) {
	terminal, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("pseudo-terminal unavailable: %v", err)
	}
	t.Cleanup(func() { require.NoError(t, terminal.Close()) })
	require.True(t, term.IsTerminal(terminal.Fd()))

	for _, size := range []unix.Winsize{{Col: 40, Row: 24}, {Col: 80, Row: 24}, {Col: 40, Row: 36}} {
		t.Run(fmt.Sprintf("%dx%d", size.Col, size.Row), func(t *testing.T) {
			require.NoError(t, unix.IoctlSetWinsize(int(terminal.Fd()), unix.TIOCSWINSZ, &size))
			checkExplorerPreload(t, terminal, int(size.Row))
		})
	}
}

func TestExploreJSONStreamPreloadWithoutTerminal(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, output.Close()) })
	require.False(t, term.IsTerminal(output.Fd()))
	checkExplorerPreload(t, output, 20)
}

func checkExplorerPreload(t *testing.T, output *os.File, want int) {
	t.Helper()
	previous := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = previous }()

	// Stop at the existing post-preload error check, before starting the UI.
	stop := errors.New("preload complete")
	iter := &preloadIterator{err: stop}
	require.ErrorIs(t, ExploreJSONStream("test", iter), stop)
	require.Equal(t, want, iter.next)
}
