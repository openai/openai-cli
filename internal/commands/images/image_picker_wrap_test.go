package images

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestImagePickerWrapRestorationFollowsCursorCleanup(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		t.Run(map[bool]string{false: "inline", true: "workspace"}[workspace], func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "wrap-output")
			require.NoError(t, err)
			defer f.Close()
			_, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := &imagePickerInline{output: &imagePickerOutput{File: f, cancel: cancel}, wrapKnown: true, restoreWrap: true}
			if workspace {
				p.workspace = &imagePickerWorkspace{out: f, context: context.Background(), width: 80}
			}
			p.start()
			p.start()
			if workspace {
				// A workspace anchor also saves the temporarily enabled wrap mode.
				_, err := f.WriteString(ansi.SaveCursor)
				require.NoError(t, err)
				p.workspace.started = true
				require.NoError(t, p.workspace.close())
			} else {
				p.paint(tea.NewView("Create image\nPrompt"), 80, 24)
				p.paint(tea.NewView("Create image\nPrompt"), 60, 24)
			}
			p.close()
			before, err := os.ReadFile(f.Name())
			require.NoError(t, err)
			p.close()
			if workspace {
				require.NoError(t, p.workspace.close())
			}
			after, err := os.ReadFile(f.Name())
			require.NoError(t, err)
			require.Equal(t, before, after, "cleanup must not overwrite the inherited mode snapshot")
			output := string(after)
			require.Equal(t, 1, strings.Count(output, ansi.SetModeAutoWrap))
			require.Equal(t, 1, strings.Count(output, ansi.ResetModeAutoWrap))
			require.True(t, strings.HasSuffix(output, ansi.ResetModeAutoWrap), "restore after the final DECRC")
			require.NoError(t, p.output.Err())
		})
	}
}

func TestImagePickerEarlyExitDoesNotChangeWrapping(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("KITTY_WINDOW_ID", "")
	f, err := os.CreateTemp(t.TempDir(), "early-wrap-output")
	require.NoError(t, err)
	defer f.Close()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &imagePickerInline{model: pickerForTest(t), output: &imagePickerOutput{File: f, cancel: cancel}}
	p.Init()
	p.close()
	output, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	for _, sequence := range []string{ansi.RequestModeAutoWrap, ansi.SetModeAutoWrap, ansi.ResetModeAutoWrap, "\x1b[?7s", "\x1b[?7r"} {
		require.NotContains(t, string(output), sequence)
	}
}

func TestImagePickerWrapProbeTimeoutKeepsInputAndMode(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("KITTY_WINDOW_ID", "")
	f, err := os.CreateTemp(t.TempDir(), "wrap-timeout-output")
	require.NoError(t, err)
	defer f.Close()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := pickerForTest(t)
	m.settings.prompt, m.draft, m.cursor = "", nil, 0
	p := &imagePickerInline{model: m, output: &imagePickerOutput{File: f, cancel: cancel}}
	require.NotNil(t, p.Init())
	require.NotNil(t, p.wrapProbe)
	p.Update(tea.PasteMsg{Content: "synthetic pending prompt"})
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "synthetic pending prompt", m.settings.prompt)
	require.Empty(t, m.result.Args)
	_, cmd := p.Update(imagePickerWrapTimeout{p.wrapProbe.revision})
	require.NotNil(t, cmd)
	require.ErrorContains(t, p.err, "--prompt")
	p.close()
	output, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	require.Contains(t, string(output), ansi.RequestCursorPositionReport+ansi.RestoreCursor)
	require.NotContains(t, string(output), ansi.RequestModeAutoWrap)
	require.NotContains(t, string(output), ansi.SetModeAutoWrap)
	require.NotContains(t, string(output), ansi.ResetModeAutoWrap)
}

func TestImagePickerWrapProbeWaitsForUsableSize(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("KITTY_WINDOW_ID", "")
	f, err := os.CreateTemp(t.TempDir(), "wrap-size-output")
	require.NoError(t, err)
	defer f.Close()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := pickerForTest(t)
	m.width, m.height = 2, 1
	p := &imagePickerInline{model: m, output: &imagePickerOutput{File: f, cancel: cancel}}
	require.Nil(t, p.Init())
	require.Nil(t, p.wrapProbe)
	_, cmd := p.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	require.NotNil(t, cmd)
	require.Equal(t, &imagePickerWrapProbe{width: 80, height: 24, revision: 1}, p.wrapProbe)
	_, cmd = p.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	require.Nil(t, cmd)
	require.NoError(t, p.err)
	require.True(t, p.wrapProbe.invalid)
	_, cmd = p.Update(imagePickerWrapTimeout{revision: 0})
	require.Nil(t, cmd, "an old probe timer must not abort a replacement")
	p.close()
	output, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	require.NotContains(t, string(output), ansi.SetModeAutoWrap)
}
