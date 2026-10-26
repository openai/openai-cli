package listview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestListStylePreservesTableDataAndLayout(t *testing.T) {
	items := []gjson.Result{gjson.Parse(`{"object":"file","id":"file_synthetic","filename":"東京-👩🏽‍💻-\u001b[31m.jsonl","purpose":"batch","bytes":1024,"status":"processed","created_at":1}`)}
	for _, width := range []int{1, 40, 80, 110} {
		plain, supported, err := RenderTable(t.Context(), "(resource) files > (method) list", items, width, true, Style{})
		require.NoError(t, err)
		for _, dark := range []bool{false, true} {
			for _, profile := range []colorprofile.Profile{colorprofile.NoTTY, colorprofile.ASCII, colorprofile.ANSI, colorprofile.ANSI256, colorprofile.TrueColor} {
				styled, gotSupported, err := RenderTable(t.Context(), "(resource) files > (method) list", items, width, true, NewStyle(profile, dark))
				require.NoError(t, err)
				require.Equal(t, supported, gotSupported)
				require.Equal(t, plain, ansi.Strip(styled))
				if !supported || profile <= colorprofile.ASCII {
					require.Equal(t, plain, styled)
					continue
				}
				lines := strings.Split(styled, "\n")
				require.Contains(t, lines[0], "\x1b[")
				require.Equal(t, strings.Split(plain, "\n")[1], lines[1], "data rows must remain undecorated")
				require.NotContains(t, styled, "\x1b[1m", "headers use normal weight")
			}
		}
	}
}

func TestListStyleUsesHelpPalette(t *testing.T) {
	for _, test := range []struct {
		dark          bool
		accent, muted string
	}{
		{true, "138;168;255", "164;172;194"},
		{false, "49;89;188", "101;112;135"},
	} {
		style := NewStyle(colorprofile.TrueColor, test.dark)
		require.Equal(t, "\x1b[38;2;"+test.accent+"mID\x1b[m\nfile_synthetic\n", style.Table("ID\nfile_synthetic\n"))
		require.Contains(t, style.Note("Full data: --format json."), "\x1b[38;2;"+test.muted+"m")
	}
}

func TestListStylePreservesNavigationAndNarrowHints(t *testing.T) {
	for _, width := range []int{1, 5, 20, 40, 80} {
		model := New(Options{}, nil, func() {}, width, 24)
		model.more = true
		plain := model.View().Content
		model.opts.Style = NewStyle(colorprofile.TrueColor, true)
		styled := model.View().Content
		require.Equal(t, plain, ansi.Strip(styled))
		model.opts.Style = Style{}
		model.loading = true
		plain = model.View().Content
		model.opts.Style = NewStyle(colorprofile.ANSI256, false)
		require.Equal(t, plain, ansi.Strip(model.View().Content))
	}
}
