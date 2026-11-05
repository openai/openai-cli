package terminalimage

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/imagefont"
	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/stretchr/testify/require"
)

func filledLegacyFontGallery(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "gallery")
	g, err := imagegallery.Open(t.Context(), directory)
	require.NoError(t, err)
	initial, err := g.Initialize(t.Context())
	require.NoError(t, err)
	require.NoError(t, g.Commit(t.Context(), initial))
	for i := range 12 {
		img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
		img.SetNRGBA(0, 0, color.NRGBA{R: uint8(i), A: 255})
		revision, err := g.Prepare(t.Context(), img, 32)
		require.NoError(t, err)
		require.NoError(t, g.Commit(t.Context(), revision))
	}
	require.NoError(t, g.Close())
	return directory
}

func TestNativeFontMigratesThirteenthPreviewAfterInspectionFailure(t *testing.T) {
	directory := filledLegacyFontGallery(t)
	bridge := newTestFontBridge()
	services := bridge.services(t)
	inspect := services.inspect
	services.inspect = func(context.Context, string, string) (imagefontmac.ProfileStatus, error) {
		return bridge.status, errors.New("synthetic inspection failure")
	}
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	img.SetNRGBA(0, 0, color.NRGBA{R: 13, A: 255})
	var out bytes.Buffer
	err := displayImageFont(t.Context(), &out, img, 32, directory, "/dev/ttys001", testFontViewport, services)
	require.ErrorContains(t, err, "synthetic inspection failure")
	require.Empty(t, out.String())
	var state struct {
		Version int
		Images  []json.RawMessage
	}
	readState := func() {
		data, err := os.ReadFile(filepath.Join(directory, "state.json"))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &state))
	}
	readState()
	require.Equal(t, 1, state.Version)
	require.Len(t, state.Images, 12)
	paths := append([]string(nil), bridge.registered...)
	require.NotEmpty(t, paths)
	services.inspect = inspect
	require.NoError(t, displayImageFont(t.Context(), &out, img, 32, directory, "/dev/ttys001", testFontViewport, services))
	require.NotEmpty(t, out.String())
	require.Equal(t, paths, bridge.registered[len(paths):], "retry must reuse the registered immutable fonts")
	readState()
	require.Equal(t, 2, state.Version)
	require.Len(t, state.Images, 13)
}

func TestNativeFontUsesCompanionCapacityWithoutProgressReservation(t *testing.T) {
	directory := filledLegacyFontGallery(t)
	bridge := newTestFontBridge()
	services := bridge.services(t)
	source := testFontSource()
	companion := testFontSource()
	companion.Tables["maxp"] = bytes.Clone(companion.Tables["maxp"])
	binary.BigEndian.PutUint16(companion.Tables["maxp"][4:6], 65535)
	source.Companions = []imagefontmac.SourceFont{companion}
	services.source = func(context.Context, string, int) (imagefontmac.SourceFont, error) { return source, nil }
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	img.SetNRGBA(0, 0, color.NRGBA{R: 13, A: 255})
	var out bytes.Buffer
	err := displayImageFont(t.Context(), &out, img, 32, directory, "/dev/ttys001", testFontViewport, services)
	require.ErrorIs(t, err, imagegallery.ErrFull)
	var safe *FontError
	require.ErrorAs(t, err, &safe)
	require.Empty(t, out.String())
	require.Empty(t, bridge.registered)
}

func TestNativeFontClassifiesMatchingReplayBeyondActualCapacity(t *testing.T) {
	directory := filledLegacyFontGallery(t)
	bridge := newTestFontBridge()
	services := bridge.services(t)
	source := testFontSource()
	source.Tables["maxp"] = bytes.Clone(source.Tables["maxp"])
	binary.BigEndian.PutUint16(source.Tables["maxp"][4:6], 65535)
	services.source = func(context.Context, string, int) (imagefontmac.SourceFont, error) { return source, nil }
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	img.SetNRGBA(0, 0, color.NRGBA{R: 11, A: 255})
	var out bytes.Buffer
	err := displayImageFont(t.Context(), &out, img, 32, directory, "/dev/ttys001", testFontViewport, services)
	require.ErrorIs(t, err, imagegallery.ErrFull)
	var safe *FontError
	require.ErrorAs(t, err, &safe)
	require.Empty(t, out.String())
	require.Empty(t, bridge.registered)
}

func TestNativeFontReplaysExistingGlyphsWhenOnlyReservationExceedsCapacity(t *testing.T) {
	directory := filledLegacyFontGallery(t)
	bridge := newTestFontBridge()
	geometry, err := preservedGeometry(testFontViewport(), int(bridge.status.FontSize), testFontSource())
	require.NoError(t, err)
	capacity, err := imagefont.PreservedGlyphCapacity(geometry)
	require.NoError(t, err)
	reserve := capacity - 12*512 + 1
	require.Positive(t, reserve)
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	img.SetNRGBA(0, 0, color.NRGBA{R: 11, A: 255})
	var out bytes.Buffer
	require.NoError(t, displayImageFontReserved(t.Context(), &out, img, 32, directory, "/dev/ttys001", testFontViewport, bridge.services(t), reserve))
	require.NotEmpty(t, out.String())
}
