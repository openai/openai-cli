package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/stretchr/testify/require"
)

func TestImageFontDifferentFailuresDoNotAccumulateArtifacts(t *testing.T) {
	for _, failure := range []string{"geometry", "encoding", "registration", "activation", "inspection", "commit", "uncertain rollback"} {
		t.Run(failure, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "gallery")
			bridge := newTestFontBridge()
			services := bridge.services(t)
			var output bytes.Buffer
			img := image.NewRGBA(image.Rect(0, 0, 16, 16))
			require.NoError(t, displayImageFont(t.Context(), &output, img, 4, directory, "/dev/ttys001", testFontViewport, services))
			original := bridge.status
			originalFont := bridge.registered[0]
			fontData, err := os.ReadFile(originalFont)
			require.NoError(t, err)
			statePath := filepath.Join(directory, "state.json")
			stateData, err := os.ReadFile(statePath)
			require.NoError(t, err)
			sentinel := errors.New("synthetic failure")
			failed := services
			switch failure {
			case "geometry", "encoding":
				failed.source = func(context.Context, string, int) (imagefontmac.SourceFont, error) {
					source, companion := testFontSource(), testFontSource()
					companion.PostScript = "SyntheticCompanion"
					if failure == "geometry" {
						companion.Ascent = -1
					} else {
						companion.Tables["maxp"] = nil
					}
					source.Companions = []imagefontmac.SourceFont{companion}
					return source, nil
				}
			case "registration":
				failed.register = func(ctx context.Context, path string) error {
					if path == originalFont {
						return services.register(ctx, path)
					}
					// A failed reply does not prove CoreText rejected the font.
					require.NoError(t, services.register(ctx, path))
					return sentinel
				}
			case "activation":
				failed.preserve = func(context.Context, string, string, string, imagefontmac.ProfileStatus) error { return sentinel }
			case "inspection", "uncertain rollback":
				failed.inspect = func(context.Context, string, string) (imagefontmac.ProfileStatus, error) {
					return imagefontmac.ProfileStatus{}, sentinel
				}
				if failure == "uncertain rollback" {
					failed.restore = func(context.Context, string, string, string, imagefontmac.ProfileStatus) error { return sentinel }
				}
			case "commit":
				failed.inspect = func(ctx context.Context, profile, tty string) (imagefontmac.ProfileStatus, error) {
					require.NoError(t, os.Rename(statePath, statePath+".saved"))
					require.NoError(t, os.Mkdir(statePath, 0700))
					return services.inspect(ctx, profile, tty)
				}
			}
			for attempt := 0; attempt < 6; attempt++ {
				img.Set(0, 0, color.RGBA{R: byte(attempt/2 + 1), A: 255})
				if attempt%2 == 1 {
					bridge.status.FontSize = 14
				} else {
					bridge.status.FontSize = 12
				}
				output.Reset()
				err := displayImageFont(t.Context(), &output, img, 4, directory, "/dev/ttys001", testFontViewport, failed)
				require.Error(t, err)
				require.Empty(t, output.String())
				if info, err := os.Stat(statePath); err == nil && info.IsDir() {
					require.NoError(t, os.Remove(statePath))
					require.NoError(t, os.Rename(statePath+".saved", statePath))
				}
				for _, directoryName := range []string{"images", "fonts"} {
					files, err := os.ReadDir(filepath.Join(directory, directoryName))
					require.NoError(t, err)
					limit := 2
					if failure == "geometry" || failure == "encoding" {
						limit = 1
					}
					require.LessOrEqual(t, len(files), limit, "different failed attempts must not grow the gallery")
				}
				actual, err := os.ReadFile(originalFont)
				require.NoError(t, err)
				require.Equal(t, fontData, actual)
				actual, err = os.ReadFile(statePath)
				require.NoError(t, err)
				require.Equal(t, stateData, actual)
			}
			// The original pending request can still complete; before registration,
			// a different supported request is immediately usable too.
			bridge.status = original
			img.Set(0, 0, color.RGBA{R: 1, A: 255})
			require.NoError(t, displayImageFont(t.Context(), &output, img, 4, directory, "/dev/ttys001", testFontViewport, services))
			img.Set(0, 0, color.RGBA{B: 255, A: 255})
			require.NoError(t, displayImageFont(t.Context(), &output, img, 4, directory, "/dev/ttys001", testFontViewport, services))
		})
	}
}
