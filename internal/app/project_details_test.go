package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/output/listview"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestProjectDetailsLayout(t *testing.T) {
	details := gjson.Parse(`{"name":"CLI sandbox","id":"proj_example","status":"active","created_at":"2026-08-22 17:37:08 UTC","residency":"GLOBAL"}`)
	var out bytes.Buffer
	require.NoError(t, writeProjectDetails(&out, details, 80, listview.Style{}))
	require.Equal(t, "Name:       CLI sandbox\nID:         proj_example\nStatus:     active\nCreated:    2026-08-22 17:37:08 UTC\nResidency:  GLOBAL\n", out.String())

	out.Reset()
	require.NoError(t, writeProjectDetails(&out, details, 16, listview.Style{}))
	require.Contains(t, out.String(), "Name:\nCLI sandbox\n")
	require.Contains(t, out.String(), "ID:\nproj_example\n")
}

func TestProjectDetailsEscapesTerminalData(t *testing.T) {
	details := gjson.Parse(`{"name":"CLI\nstatus","status":"active\u001b[2J","id":"proj_\u202esynthetic","external_key_id":"key\tlabel"}`)
	var out bytes.Buffer
	require.NoError(t, writeProjectDetails(&out, details, 80, listview.Style{}))
	require.Contains(t, out.String(), `CLI\nstatus`)
	require.Contains(t, out.String(), `active\u001b[2J`)
	require.Contains(t, out.String(), `proj_\u202esynthetic`)
	require.Contains(t, out.String(), `key\tlabel`)
	require.NotContains(t, out.String(), "\x1b")
}

func TestProjectDetailsColorsOnlyLabels(t *testing.T) {
	details := gjson.Parse(`{"name":"CLI sandbox","id":"proj_example","status":"active"}`)
	for _, width := range []int{16, 80} {
		var plain bytes.Buffer
		require.NoError(t, writeProjectDetails(&plain, details, width, listview.Style{}))
		for _, dark := range []bool{true, false} {
			var colored bytes.Buffer
			style := listview.NewStyle(colorprofile.TrueColor, dark)
			require.NoError(t, writeProjectDetails(&colored, details, width, style))
			require.Equal(t, plain.String(), ansi.Strip(colored.String()))
			expected := plain.String()
			for _, label := range []string{"Name:", "ID:", "Status:"} {
				rendered := style.Label(label)
				require.NotEqual(t, label, rendered)
				expected = strings.Replace(expected, label, rendered, 1)
			}
			require.Equal(t, expected, colored.String())
		}
	}
}

func TestProjectDetailsFallbackPreservesData(t *testing.T) {
	details := gjson.Parse(`{"name":"CLI sandbox","status":"active","id":"proj_example","psp_decryption_key_id":{"unexpected":123}}`)
	var out bytes.Buffer
	require.NoError(t, writeProjectDetails(&out, details, 80, listview.Style{}))
	require.Contains(t, out.String(), "Unexpected: 123")
	require.Contains(t, out.String(), "Name: CLI sandbox")
}
