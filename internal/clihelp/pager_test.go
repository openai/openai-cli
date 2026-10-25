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
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if !m.viewport.AtBottom() || !strings.Contains(m.View().Content, "line 099") {
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
