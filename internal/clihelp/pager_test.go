package clihelp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

func TestHelpPagerNavigationAndResize(t *testing.T) {
	var content strings.Builder
	for i := range 100 {
		fmt.Fprintf(&content, "line %03d with readable help\n", i)
	}
	m := newHelpPager(content.String(), 80, 12, func(int) string { return content.String() })
	if !m.viewport.AtTop() || !strings.Contains(m.View().Content, "line 000") || strings.Contains(m.View().Content, "line 099") {
		t.Fatal("help did not start with the first page")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.viewport.AtTop() {
		t.Fatal("Page Down did not scroll")
	}
	_, done := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if done == nil || !m.finished || !m.viewport.AtBottom() || !strings.Contains(m.View().Content, "line 099") {
		t.Fatal("End did not reach the complete reference")
	}
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	for _, line := range strings.Split(m.View().Content, "\n") {
		if ansi.StringWidth(line) > 20 {
			t.Fatalf("resized view overflows: %q", line)
		}
	}
	if rows := strings.Count(m.View().Content, "\n") + 1; rows > 5 {
		t.Fatalf("resized view has %d rows", rows)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	if !m.viewport.AtTop() || !strings.Contains(m.View().Content, "line 000") {
		t.Fatal("Home did not return to the top")
	}
	m.Update(tea.WindowSizeMsg{Width: 1, Height: 1})
	if strings.Contains(m.View().Content, "\n") || ansi.StringWidth(m.View().Content) > 1 {
		t.Fatal("one-cell terminal overflowed")
	}
	_, quit := m.Update(tea.KeyPressMsg{Code: 'q'})
	if quit == nil || m.interrupted {
		t.Fatal("q must quit successfully")
	}
	_, quit = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if quit == nil || !m.interrupted {
		t.Fatal("Ctrl+C must record cancellation")
	}
}

func TestHelpPagerFinishesOnDownwardNavigation(t *testing.T) {
	const content = "one\ntwo\nthree\nfour\nfive\n"
	for _, scroll := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyPgDown},
		tea.MouseWheelMsg{Button: tea.MouseWheelDown},
	} {
		m := newHelpPager(content, 80, 4, func(int) string { return content })
		m.viewport.SetYOffset(1)
		_, done := m.Update(scroll)
		if done == nil || !m.finished || !m.viewport.AtBottom() {
			t.Fatalf("scroll %v failed to leave the completed reference", scroll)
		}
	}
}

func TestHelpPagerFinishesWhenResizeExposesTheEnd(t *testing.T) {
	const content = "one\ntwo\nthree\nfour\nfive\nsix\n"
	for _, tc := range []struct {
		name           string
		offset, height int
		finish         bool
	}{
		{"top still overflows", 0, 5, false},
		{"whole reference fits", 0, 6, true},
		{"scrolled but end is hidden", 1, 5, false},
		{"scroll then expose the last line", 2, 5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newHelpPager(content, 80, 3, func(int) string { return content })
			m.viewport.SetYOffset(tc.offset)
			_, done := m.Update(tea.WindowSizeMsg{Width: 80, Height: tc.height})
			if (done != nil) != tc.finish || m.finished != tc.finish {
				t.Fatalf("resize finished=%t, want %t", m.finished, tc.finish)
			}
		})
	}
}

func TestHelpPagerPreservesNonterminalCallback(t *testing.T) {
	var out bytes.Buffer
	root := &cli.Command{Reader: strings.NewReader("unconsumed"), Writer: &out}
	failure := errors.New("synthetic callback failure")
	for _, args := range [][]string{{"openai"}, {"openai", "--help"}, {"openai", "models"}} {
		out.Reset()
		err := RunWithPager(t.Context(), root, args, func() error {
			if root.Writer != &out {
				t.Fatal("changed a nonterminal writer")
			}
			fmt.Fprint(root.Writer, "complete help\n")
			return failure
		})
		if err != failure || out.String() != "complete help\n" {
			t.Fatalf("changed callback output or error: %q, %v", out.String(), err)
		}
	}
	if remaining, _ := io.ReadAll(root.Reader); string(remaining) != "unconsumed" {
		t.Fatal("pager consumed redirected stdin")
	}
}

type helpShortWriter struct{}

func (helpShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestHelpPagerOutputFailureCancelsWithoutClosingTerminal(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "borrowed-output")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out := &helpPagerOutput{BorrowedWriter: terminal.BorrowedWriter{Writer: helpShortWriter{}, File: file}, cancel: cancel}
	if _, err := out.Write([]byte("frame")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write disappeared: %v", err)
	}
	if ctx.Err() == nil || !errors.Is(out.failure(), io.ErrShortWrite) {
		t.Fatal("output failure did not stop input")
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("terminal remains owned by caller"); err != nil {
		t.Fatal(err)
	}
}
