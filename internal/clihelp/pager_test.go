package clihelp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/urfave/cli/v3"
)

func TestHelpPagerNavigationAndResize(t *testing.T) {
	var content strings.Builder
	for i := range 100 {
		fmt.Fprintf(&content, "line %03d with readable help\n", i)
	}
	m := newHelpPager(content.String(), 80, 12)
	if !m.viewport.AtTop() || !strings.Contains(m.View().Content, "line 000") || strings.Contains(m.View().Content, "line 099") {
		t.Fatal("help did not start with the first page")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.viewport.AtTop() {
		t.Fatal("Page Down did not scroll")
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
	small := newHelpPager(content.String(), 80, 12)
	small.Update(tea.WindowSizeMsg{Width: 1, Height: 1})
	if strings.Contains(small.View().Content, "\n") || ansi.StringWidth(small.View().Content) > 1 {
		t.Fatal("one-cell terminal overflowed")
	}
	_, done := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if done == nil || !m.finished || !m.viewport.AtBottom() {
		t.Fatal("End did not reach the complete reference")
	}

}

func TestHelpPagerAppendsOnlyNewRowsOnForwardScroll(t *testing.T) {
	const full = "first\nsecond\nthird\nfourth\nfifth\nsixth\n\n"
	m := newHelpPager(full, 80, 4)
	var out strings.Builder
	m.output = &out
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 4})
	if !strings.Contains(out.String(), "first\r\nsecond\r\nthird\r\n") || strings.Contains(out.String(), "fourth") {
		t.Fatalf("unexpected first page: %q", out.String())
	}
	out.Reset()
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if !strings.Contains(out.String(), "fourth\r\n") || strings.Contains(out.String(), "third") {
		t.Fatalf("Down should append only the next row: %q", out.String())
	}
	out.Reset()
	m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	if !strings.Contains(out.String(), "\x1b[?1049h") || !strings.Contains(out.String(), "first") {
		t.Fatal("Home did not open the previously read rows")
	}
	out.Reset()
	_, done := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if done == nil || !strings.Contains(out.String(), "\x1b[?1049l") || !strings.Contains(out.String(), "fifth\r\nsixth\r\n\r\n") || strings.Contains(out.String(), "fourth") {
		t.Fatalf("EOF must append only the unread tail: %q", out.String())
	}
	out.Reset()
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if out.Len() > 0 {
		t.Fatal("buffered wheel input emitted more text after EOF")
	}
}

func TestHelpPagerLimitsAnUnbrokenLineToTheFirstPage(t *testing.T) {
	// Direct executable paths can span several terminal heights.
	m := newHelpPager("abcdefghijklmnop\nEND\n", 4, 3)
	var out strings.Builder
	m.output = &out
	m.Update(tea.WindowSizeMsg{Width: 4, Height: 3})
	if !strings.Contains(out.String(), "abcd\r\nefgh\r\n") || strings.Contains(out.String(), "ijkl") {
		t.Fatalf("initial page overflowed: %q", out.String())
	}
	out.Reset()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if !strings.Contains(out.String(), "ijkl\r\nmnop\r\nEND") || strings.Contains(out.String(), "abcd") {
		t.Fatalf("unexpected final rows: %q", out.String())
	}
}

func TestHelpPagerFinishesOnDownwardNavigation(t *testing.T) {
	const content = "one\ntwo\nthree\nfour\nfive\n"
	for _, scroll := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyPgDown},
		tea.MouseWheelMsg{Button: tea.MouseWheelDown},
	} {
		m := newHelpPager(content, 80, 4)
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
			m := newHelpPager(content, 80, 3)
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

func TestHelpPagerStopsOnPartialTerminalWrite(t *testing.T) {
	m := newHelpPager("one\ntwo\nthree\n", 80, 2)
	m.output = clioutput.Writer{Context: t.Context(), Sink: helpShortWriter{}}
	_, done := m.Update(tea.WindowSizeMsg{Width: 80, Height: 2})
	if done == nil || !errors.Is(m.writeErr, io.ErrShortWrite) {
		t.Fatalf("short write did not stop the pager: %v", m.writeErr)
	}
}
