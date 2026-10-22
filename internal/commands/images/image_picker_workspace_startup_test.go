package images

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"
)

func TestImagePickerWorkspaceStartupSeparatesOnlyUnframedTitles(t *testing.T) {
	for _, test := range []struct {
		name          string
		width, height int
		separator     bool
	}{
		{"framed", 96, 40, false},
		{"first framed width", 50, 40, false},
		{"narrow", 49, 40, true},
		{"short", 96, 19, true},
		{"first framed height", 96, 20, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := os.CreateTemp(t.TempDir(), "screen")
			require.NoError(t, err)
			t.Cleanup(func() { out.Close() })
			m := pickerForTest(t)
			m.width, m.height, m.workspace = test.width, test.height, true
			w := &imagePickerWorkspace{out: out}
			require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
			// Crossing a frame boundary must repaint the existing logical line.
			m.width, m.height = 49, 19
			require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
			m.width, m.height = 96, 40
			require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
			_, err = out.Seek(0, io.SeekStart)
			require.NoError(t, err)
			raw, err := io.ReadAll(out)
			require.NoError(t, err)
			require.Equal(t, test.separator, strings.HasPrefix(string(raw), "\r\n"))
			newlines := 0
			if test.separator {
				newlines = 1
			}
			require.Equal(t, newlines, strings.Count(string(raw), "\n"), "resize must not add startup spacing")
		})
	}
}
