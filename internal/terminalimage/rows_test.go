package terminalimage

import (
	"context"
	"image"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareRowsNativeAndBlocksStayIndependent(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "terminal")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	img := image.NewRGBA(image.Rect(7, 9, 39, 41))
	for _, protocol := range []string{"kitty", "iterm", "iterm-auto", "iterm-konsole", "blocks"} {
		rows, err := PrepareRows(t.Context(), out, img, protocol, 8)
		require.NoError(t, err, protocol)
		require.Len(t, rows, 4, protocol)
		for _, row := range rows {
			require.NotContains(t, row, "\n")
			if protocol == "kitty" {
				require.Contains(t, row, "r=1")
				require.Contains(t, row, "C=1")
			}
			if strings.HasPrefix(protocol, "iterm") {
				require.Contains(t, row, "height=1")
			}
		}
	}
	info, err := out.Stat()
	require.NoError(t, err)
	require.Zero(t, info.Size(), "preparing rows must not write image bytes")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = PrepareRows(ctx, out, img, "kitty", 8)
	require.ErrorIs(t, err, context.Canceled)
}
