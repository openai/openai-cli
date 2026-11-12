package vectorstores

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

type screenWriteProbe struct{ fail bool }

func (p *screenWriteProbe) WriteContext(_ context.Context, frame []byte) (int, error) {
	if p.fail {
		return 0, io.ErrClosedPipe
	}
	return len(frame), nil
}

func TestScreenCleanupTracksAcknowledgedTransitions(t *testing.T) {
	for _, tc := range []struct {
		name, data             string
		initial, fail, restore bool
	}{
		{"inline", ansi.RequestModeAltScreenSaveCursor + "\rtext", false, false, false},
		{"enter", ansi.SetModeAltScreenSaveCursor, false, false, true},
		{"partial enter", ansi.SetModeAltScreenSaveCursor, false, true, true},
		{"leave", ansi.ResetModeAltScreenSaveCursor, true, false, false},
		{"partial leave", ansi.ResetModeAltScreenSaveCursor, true, true, true},
		{"enter then leave", ansi.SetModeAltScreenSaveCursor + ansi.ResetModeAltScreenSaveCursor, false, false, false},
		{"leave then enter", ansi.ResetModeAltScreenSaveCursor + ansi.SetModeAltScreenSaveCursor, true, false, true},
		{"partial transitions", ansi.SetModeAltScreenSaveCursor + ansi.ResetModeAltScreenSaveCursor, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := workspaceScreenOutput{FrameOutput: &screenWriteProbe{fail: tc.fail}, mayBeAlternate: tc.initial}
			_, err := out.WriteContext(t.Context(), []byte(tc.data))
			require.Equal(t, tc.fail, err != nil)
			require.Equal(t, tc.restore, out.mayBeAlternate)
			p := workspaceInline{modesActive: true, started: true}
			got := p.close(out.mayBeAlternate)
			require.Equal(t, tc.restore, strings.Contains(got, ansi.ResetModeAltScreenSaveCursor))
			if tc.restore {
				require.Less(t, strings.Index(got, ansi.ResetModeAltScreenSaveCursor), strings.Index(got, ansi.EraseScreenBelow))
			}
			require.Empty(t, p.close(out.mayBeAlternate))
		})
	}
}

// Simulate the painter draining its queued frame immediately when it receives a control.
type screenSwitchProbe struct{ pending, atSwitch, control string }

func (p *screenSwitchProbe) Frame(frame string) { p.pending = frame }
func (p *screenSwitchProbe) Control(control string) {
	p.atSwitch, p.pending, p.control = p.pending, "", control
}

func TestScreenSwitchDropsPreviousBuffersQueuedFrame(t *testing.T) {
	probe := &screenSwitchProbe{}
	m := modelForTest(t, modelService{})
	p := workspaceInline{painter: probe, model: m}
	p.paintFrame("compact", 60)
	require.Empty(t, probe.control)
	require.Contains(t, probe.pending, "compact")
	m.fullScreen = true
	p.paintFrame("expanded", 60)
	require.Empty(t, probe.atSwitch)
	require.Equal(t, ansi.SetModeAltScreenSaveCursor, probe.control)
	require.True(t, strings.HasPrefix(probe.pending, ansi.CursorHomePosition))
	m.fullScreen = false
	p.paintFrame("compact", 60)
	require.Empty(t, probe.atSwitch)
	require.Equal(t, ansi.ResetModeAltScreenSaveCursor, probe.control)
	require.NotContains(t, probe.pending, ansi.CursorHomePosition)
}

func TestScreenRespectsCallersAlternateBuffer(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.fullScreen = true
	probe := &screenSwitchProbe{}
	p := workspaceInline{model: m, painter: probe, modesActive: true}
	p.paintFrame("expanded", 60) // The startup report can arrive after the first toggle.
	require.Equal(t, ansi.SetModeAltScreenSaveCursor, probe.control)
	p.Update(tea.ModeReportMsg{Mode: ansi.ModeAltScreenSaveCursor, Value: ansi.ModeSet})
	require.False(t, m.fullScreen)
	require.True(t, p.alternateUnavailable)
	require.Contains(t, m.note, "Full screen is unavailable")
	p.Update(tea.ModeReportMsg{Mode: ansi.ModeAltScreenSaveCursor, Value: ansi.ModeReset})
	p.paintFrame("compact", 60)
	require.NotEqual(t, ansi.ResetModeAltScreenSaveCursor, probe.control)
	require.NotContains(t, probe.pending, ansi.CursorHomePosition)
	require.NotContains(t, p.close(true), ansi.ResetModeAltScreenSaveCursor)
}

func TestExpandedStartupWaitsForAlternateScreenReport(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.fullScreen = true
	probe := &screenSwitchProbe{}
	p := workspaceInline{model: m, painter: probe, modesActive: true}
	p.Update(tea.ModeReportMsg{Mode: ansi.ModeAutoWrap, Value: ansi.ModeSet})
	require.False(t, p.started)
	require.Empty(t, probe.control)

	p.Update(tea.ModeReportMsg{Mode: ansi.ModeAltScreenSaveCursor, Value: ansi.ModeSet})
	require.False(t, m.fullScreen)
	require.True(t, p.alternateUnavailable)
	require.Empty(t, probe.control)
	require.Empty(t, probe.pending)
}
