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
			require.Equal(t, 8, RowColumns(row), "prepared rows must report their actual encoded cell width")
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

func TestPreparedRowColumns(t *testing.T) {
	for _, test := range []struct {
		row  string
		want int
	}{
		{"plain", 5},
		{"\x1b]1337;File=inline=1;width=64;height=1:AA==\a", 64},
		{"\x1b_Ga=T,c=64,r=1;AA==\x1b\\", 64},
		{"\x1b_Gc=32,a=T;AA==\x1b\\", 32},
		{"\x1b]1337;File=inline=1;width=999999999999999999999:AA==\a", -1},
		{"\x1b]1337;File=inline=1;width=auto:AA==\a", -1},
		{"\x1b_Ga=T,r=1;AA==\x1b\\", -1},
	} {
		require.Equal(t, test.want, RowColumns(test.row))
	}
}
