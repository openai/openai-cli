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
	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/stretchr/testify/require"
)

func TestImageFontFailedRetriesReuseImmutableFonts(t *testing.T) {
	for _, failure := range []string{"registration", "activation", "inspection", "commit", "rollback"} {
		t.Run(failure, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "gallery")
			bridge := newTestFontBridge()
			services := bridge.services(t)
			services.source = func(context.Context, string, int) (imagefontmac.SourceFont, error) {
				source := testFontSource()
				companion := testFontSource()
				companion.PostScript = "GoMonoCompanion"
				otherCompanion := testFontSource()
				otherCompanion.PostScript = "GoMonoOtherCompanion"
				source.Companions = []imagefontmac.SourceFont{companion, otherCompanion}
				return source, nil
			}
			registered := map[string]bool{}
			register := services.register
			services.register = func(ctx context.Context, path string) error {
				if registered[path] {
					return &imagefontmac.NativeError{Code: 105}
				}
				if err := register(ctx, path); err != nil {
					return err
				}
				registered[path] = true
				return nil
			}
			img := image.NewRGBA(image.Rect(0, 0, 16, 16))
			var output bytes.Buffer
			require.NoError(t, displayImageFont(t.Context(), &output, img, 4, directory, "/dev/ttys001", testFontViewport, services))
			originalText, originalStatus := output.String(), bridge.status
			statePath := filepath.Join(directory, "state.json")
			stateData, err := os.ReadFile(statePath)
			require.NoError(t, err)
			retained := map[string][]byte{}
			for path := range registered {
				retained[path], err = os.ReadFile(path)
				require.NoError(t, err)
			}
			img.Set(0, 0, color.RGBA{R: 255, A: 255})
			failed := services
			sentinel := errors.New("synthetic " + failure + " failure")
			switch failure {
			case "registration":
				failed.register = func(ctx context.Context, path string) error {
					// The original font is restored first. Permit one new companion
					// registration, then fail the second companion on every attempt.
					if !registered[path] && len(registered) == 4 {
						return sentinel
					}
					return services.register(ctx, path)
				}
			case "activation":
				failed.preserve = func(context.Context, string, string, string, imagefontmac.ProfileStatus) error { return sentinel }
			case "inspection", "rollback":
				failed.inspect = func(context.Context, string, string) (imagefontmac.ProfileStatus, error) {
					return imagefontmac.ProfileStatus{}, sentinel
				}
				if failure == "rollback" {
					failed.restore = func(context.Context, string, string, string, imagefontmac.ProfileStatus) error { return sentinel }
				}
			case "commit":
				failed.inspect = func(ctx context.Context, profile, tty string) (imagefontmac.ProfileStatus, error) {
					require.NoError(t, os.Rename(statePath, statePath+".saved"))
					require.NoError(t, os.Mkdir(statePath, 0700))
					return services.inspect(ctx, profile, tty)
				}
			}
			for attempt := 0; attempt < 4; attempt++ {
				output.Reset()
				err := displayImageFont(t.Context(), &output, img, 4, directory, "/dev/ttys001", testFontViewport, failed)
				require.Error(t, err)
				if failure != "commit" {
					require.ErrorIs(t, err, sentinel)
				} else {
					require.NoError(t, os.Remove(statePath))
					require.NoError(t, os.Rename(statePath+".saved", statePath))
				}
				require.Empty(t, output.String())
				actualState, err := os.ReadFile(statePath)
				require.NoError(t, err)
				require.Equal(t, stateData, actualState)
				files, err := os.ReadDir(filepath.Join(directory, "fonts"))
				require.NoError(t, err)
				require.Len(t, files, 6, "failed retries reuse the pending font files")
				if failure == "registration" {
					require.Len(t, registered, 4)
				} else {
					require.Len(t, registered, 6)
				}
				for _, file := range files {
					path := filepath.Join(directory, "fonts", file.Name())
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					if expected, ok := retained[path]; ok {
						require.Equal(t, expected, data, "old and pending fonts stay immutable")
					} else {
						retained[path] = data
					}
				}
				if failure != "rollback" {
					require.Equal(t, originalStatus, bridge.status)
				}
			}
			require.NoError(t, displayImageFont(t.Context(), &output, img, 4, directory, "/dev/ttys001", testFontViewport, services))
			require.NotEmpty(t, output.String())
			require.NotEqual(t, originalText, output.String())
			require.Len(t, registered, 6, "session registrations are bounded across retries")
			gallery, err := imagegallery.Open(t.Context(), directory)
			require.NoError(t, err)
			require.Equal(t, 2, gallery.State().ImageCount)
			require.NoError(t, gallery.Close())
		})
	}
}
