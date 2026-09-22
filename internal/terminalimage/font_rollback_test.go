package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/stretchr/testify/require"
)

func TestImageFontRestoresOriginalAfterActivationFailure(t *testing.T) {
	for _, failure := range []string{"cancelled activation", "lost activation reply", "inspection", "geometry", "width", "commit", "cancelled commit"} {
		t.Run(failure, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "gallery")
			bridge := newTestFontBridge()
			original := bridge.status
			services := bridge.services(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if failure == "cancelled activation" || failure == "lost activation reply" {
				preserve := services.preserve
				services.preserve = func(ctx context.Context, profile, tty, font string, captured imagefontmac.ProfileStatus) error {
					require.NoError(t, preserve(ctx, profile, tty, font, captured))
					if failure == "cancelled activation" {
						cancel()
						return ctx.Err()
					}
					return errors.New("synthetic lost activation reply")
				}
			}
			if failure == "inspection" {
				services.inspect = func(context.Context, string, string) (imagefontmac.ProfileStatus, error) {
					return imagefontmac.ProfileStatus{}, errors.New("synthetic inspection failure")
				}
			}
			restores := 0
			restore := services.restore
			services.restore = func(ctx context.Context, profile, tty, font string, captured imagefontmac.ProfileStatus) error {
				restores++
				require.NoError(t, ctx.Err(), "rollback must survive caller cancellation")
				_, bounded := ctx.Deadline()
				require.True(t, bounded, "rollback must have a bounded lifetime")
				require.Equal(t, original, captured)
				return restore(ctx, profile, tty, font, captured)
			}
			calls := 0
			viewport := func() fontViewport {
				calls++
				size := testFontViewport()
				if calls == 2 {
					switch failure {
					case "geometry":
						size.PixelHeight += 24
					case "width":
						size.Columns, size.PixelWidth = 20, 140
					case "commit":
						require.NoError(t, os.Rename(filepath.Join(directory, "state.json"), filepath.Join(directory, "state.saved")))
						require.NoError(t, os.Mkdir(filepath.Join(directory, "state.json"), 0700))
					case "cancelled commit":
						cancel()
					}
				}
				return size
			}
			var output bytes.Buffer
			err := displayNativeImageFont(ctx, &output, image.NewRGBA(image.Rect(0, 0, 16, 16)), 32, directory, "/dev/ttys001", viewport, services)
			require.Error(t, err)
			var fontErr *FontError
			require.ErrorAs(t, err, &fontErr)
			require.Empty(t, output.String())
			require.Equal(t, 1, restores)
			require.Equal(t, original, bridge.status)
			if failure == "cancelled activation" || failure == "cancelled commit" {
				require.ErrorIs(t, err, context.Canceled)
			}
			if failure == "commit" {
				require.NoError(t, os.Remove(filepath.Join(directory, "state.json")))
				require.NoError(t, os.Rename(filepath.Join(directory, "state.saved"), filepath.Join(directory, "state.json")))
			}
			gallery, err := imagegallery.Open(t.Context(), directory)
			require.NoError(t, err)
			require.Zero(t, gallery.State().ImageCount)
			require.NoError(t, gallery.Close())
		})
	}
}

func TestImageFontRollbackPreservesConcurrentSettings(t *testing.T) {
	for _, change := range []string{"font", "size", "profile"} {
		t.Run(change, func(t *testing.T) {
			bridge := newTestFontBridge()
			services := bridge.services(t)
			var changed imagefontmac.ProfileStatus
			services.inspect = func(context.Context, string, string) (imagefontmac.ProfileStatus, error) {
				switch change {
				case "font":
					bridge.status.FontName = "Courier"
				case "size":
					bridge.status.FontSize++
				case "profile":
					bridge.status.ProfileID++
				}
				changed = bridge.status
				return bridge.status, nil
			}
			var output bytes.Buffer
			err := displayNativeImageFont(t.Context(), &output, image.NewRGBA(image.Rect(0, 0, 16, 16)), 32, filepath.Join(t.TempDir(), "gallery"), "/dev/ttys001", testFontViewport, services)
			require.Error(t, err)
			require.Empty(t, output.String())
			require.Equal(t, changed, bridge.status)
		})
	}
}

func TestImageFontReportsRollbackFailure(t *testing.T) {
	bridge := newTestFontBridge()
	services := bridge.services(t)
	inspectErr := errors.New("synthetic inspection failure")
	restoreErr := errors.New("synthetic rollback failure")
	services.inspect = func(context.Context, string, string) (imagefontmac.ProfileStatus, error) {
		return imagefontmac.ProfileStatus{}, inspectErr
	}
	services.restore = func(context.Context, string, string, string, imagefontmac.ProfileStatus) error { return restoreErr }
	var output bytes.Buffer
	err := displayNativeImageFont(t.Context(), &output, image.NewRGBA(image.Rect(0, 0, 16, 16)), 32, filepath.Join(t.TempDir(), "gallery"), "/dev/ttys001", testFontViewport, services)
	require.ErrorIs(t, err, inspectErr)
	require.ErrorIs(t, err, restoreErr)
	require.Empty(t, output.String())
}
