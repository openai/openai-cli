package terminalimage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/sfnt"
)

func testFontSource() imagefontmac.SourceFont {
	tables := map[string][]byte{}
	for i := 0; i < int(binary.BigEndian.Uint16(gomono.TTF[4:6])); i++ {
		record := gomono.TTF[12+16*i : 28+16*i]
		offset, length := binary.BigEndian.Uint32(record[8:12]), binary.BigEndian.Uint32(record[12:16])
		tables[string(record[:4])] = gomono.TTF[offset : offset+length]
	}
	return imagefontmac.SourceFont{PostScript: "GoMono", Tables: tables,
		Ascent: 10, Descent: 3, Advance: 7, LineHeight: 14}
}

type testFontBridge struct {
	status     imagefontmac.ProfileStatus
	registered []string
	deny       bool
	change     bool
}

func (bridge *testFontBridge) services(t *testing.T) fontServices {
	t.Helper()
	return fontServices{
		snapshot: func(context.Context, string, string) (imagefontmac.ProfileStatus, error) {
			return bridge.status, nil
		},
		source: func(context.Context, string, int) (imagefontmac.SourceFont, error) { return testFontSource(), nil },
		register: func(_ context.Context, path string) error {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			_, err = sfnt.Parse(data)
			require.NoError(t, err)
			bridge.registered = append(bridge.registered, path)
			return nil
		},
		preserve: func(_ context.Context, _, _, font string, expected imagefontmac.ProfileStatus) error {
			require.Equal(t, bridge.status, expected)
			if bridge.deny {
				return errors.New("synthetic Automation denial")
			}
			bridge.status.FontName = font
			return nil
		},
		restore: func(_ context.Context, _, _, font string, original imagefontmac.ProfileStatus) error {
			expected := original
			expected.FontName = font
			if bridge.status == expected {
				bridge.status.FontName = original.FontName
			}
			return nil
		},
		inspect: func(context.Context, string, string) (imagefontmac.ProfileStatus, error) {
			if bridge.change {
				bridge.status.FontSize++
			}
			return bridge.status, nil
		},
	}
}

func newTestFontBridge() *testFontBridge {
	return &testFontBridge{status: imagefontmac.ProfileStatus{FontName: "GoMono", FontSize: 12, ProfileID: 1, ProfileName: "Synthetic profile"}}
}

func testFontViewport() fontViewport {
	return fontViewport{Columns: 80, Rows: 24, PixelWidth: 560, PixelHeight: 336}
}

func TestImageFontRetainsScrollbackAndTextSettings(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	bridge := newTestFontBridge()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var first, second bytes.Buffer
	require.NoError(t, displayImageFont(t.Context(), &first, img, 32, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
	require.NotEmpty(t, first.String())
	firstRune := []rune(first.String())[0]
	require.GreaterOrEqual(t, firstRune, rune(0xf0000))
	img.Set(0, 0, color.RGBA{B: 255, A: 255})
	require.NoError(t, displayImageFont(t.Context(), &second, img, 32, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
	require.NotEqual(t, first.String(), second.String())
	require.Equal(t, 12.0, bridge.status.FontSize)
	require.Equal(t, "Synthetic profile", bridge.status.ProfileName)
	require.Equal(t, 1, bridge.status.ProfileID)
	data, err := os.ReadFile(bridge.registered[len(bridge.registered)-1])
	require.NoError(t, err)
	font, err := sfnt.Parse(data)
	require.NoError(t, err)
	for _, char := range []rune{'A', firstRune, []rune(second.String())[0]} {
		glyph, err := font.GlyphIndex(nil, char)
		require.NoError(t, err)
		require.NotZero(t, glyph, "current font must retain text and both images")
	}
}

func TestImageFontFailureDoesNotPrintOrCommitImage(t *testing.T) {
	for _, reason := range []string{"denied", "changed"} {
		t.Run(reason, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "gallery")
			bridge := newTestFontBridge()
			bridge.deny, bridge.change = reason == "denied", reason == "changed"
			var output bytes.Buffer
			img := image.NewRGBA(image.Rect(0, 0, 16, 16))
			err := displayImageFont(t.Context(), &output, img, 32, directory, "/dev/ttys001", testFontViewport, bridge.services(t))
			require.Error(t, err)
			var fontErr *FontError
			require.ErrorAs(t, err, &fontErr)
			require.Empty(t, output.String())
			gallery, err := imagegallery.Open(t.Context(), directory)
			require.NoError(t, err)
			require.Zero(t, gallery.State().ImageCount)
			require.NoError(t, gallery.Close())
			bridge.deny, bridge.change = false, false
			bridge.status.FontSize = 12
			require.NoError(t, displayImageFont(t.Context(), &output, img, 32, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
		})
	}
}

func TestImageFontOutputFailureDoesNotFallBack(t *testing.T) {
	bridge := newTestFontBridge()
	sinkErr := errors.New("synthetic output failure")
	err := displayImageFont(t.Context(), writerFunc(func([]byte) (int, error) {
		return 0, sinkErr
	}), image.NewRGBA(image.Rect(0, 0, 16, 16)), 32, filepath.Join(t.TempDir(), "gallery"), "/dev/ttys001", testFontViewport, bridge.services(t))
	require.ErrorIs(t, err, sinkErr)
	var fontErr *FontError
	require.False(t, errors.As(err, &fontErr))
}

func TestImageFontRedisplaysForNarrowerWindow(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	bridge := newTestFontBridge()
	var output bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	require.NoError(t, displayImageFont(t.Context(), &output, img, 32, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
	selected := bridge.status.FontName
	fonts, err := os.ReadDir(filepath.Join(directory, "fonts"))
	require.NoError(t, err)
	originals := map[string][]byte{}
	for _, font := range fonts {
		data, err := os.ReadFile(filepath.Join(directory, "fonts", font.Name()))
		require.NoError(t, err)
		originals[font.Name()] = data
	}
	output.Reset()
	err = displayImageFont(t.Context(), &output, img, 32, directory, "/dev/ttys001", func() fontViewport {
		return fontViewport{Columns: 20, Rows: 24, PixelWidth: 140, PixelHeight: 336}
	}, bridge.services(t))
	require.NoError(t, err)
	require.NotEqual(t, selected, bridge.status.FontName)
	for _, row := range strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n") {
		require.Len(t, []rune(row), 19)
	}
	for name, before := range originals {
		after, err := os.ReadFile(filepath.Join(directory, "fonts", name))
		require.NoError(t, err)
		require.Equal(t, before, after)
	}
	selected = bridge.status.FontName
	output.Reset()
	err = displayImageFont(t.Context(), &output, img, 32, directory, "/dev/ttys001", func() fontViewport {
		return fontViewport{Columns: 1, Rows: 24, PixelWidth: 7, PixelHeight: 336}
	}, bridge.services(t))
	require.Error(t, err)
	require.Empty(t, output.String())
	require.Equal(t, selected, bridge.status.FontName)
}

func TestImageFontRemoteSessionsStayUnsupported(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	for _, name := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "TMUX", "STY", "ZELLIJ", "CI"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "synthetic")
			require.False(t, FontSupported())
		})
	}
	require.Error(t, Write(t.Context(), &strings.Builder{}, image.NewRGBA(image.Rect(0, 0, 1, 1)), "font", 32))
}
