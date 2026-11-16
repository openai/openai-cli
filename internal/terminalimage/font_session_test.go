package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/stretchr/testify/require"
)

func TestImageFontRejectsMissingSessionIdentity(t *testing.T) {
	for _, session := range []string{"", " \t\n"} {
		t.Run(session, func(t *testing.T) {
			t.Setenv("TERM_SESSION_ID", session)
			var output bytes.Buffer
			err := writeImageFont(t.Context(), &output, image.NewRGBA(image.Rect(0, 0, 1, 1)), 32)
			var fontErr *FontError
			require.ErrorAs(t, err, &fontErr)
			require.ErrorContains(t, err, "TERM_SESSION_ID")
			require.Empty(t, output.String())
		})
	}
}

func TestImageFontRejectsAnotherGalleryWithoutChangingScrollback(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "original-session")
	bridge := newTestFontBridge()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var original bytes.Buffer
	require.NoError(t, displayImageFont(t.Context(), &original, img, 32, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
	selected := bridge.status
	stateBefore, err := os.ReadFile(filepath.Join(directory, "state.json"))
	require.NoError(t, err)
	fontBefore, err := os.ReadFile(bridge.registered[0])
	require.NoError(t, err)
	images, err := os.ReadDir(filepath.Join(directory, "images"))
	require.NoError(t, err)
	require.Len(t, images, 1)
	imagePath := filepath.Join(directory, "images", images[0].Name())
	imageBefore, err := os.ReadFile(imagePath)
	require.NoError(t, err)

	// A missing/changed session hash selects a different directory for the same
	// TTY. Do not activate that gallery over the font still used by scrollback.
	for _, name := range []string{selected.FontName, "OpenAIImages-ffffffff-legacy-Regular", "OpenAI Local 01234567", "OpenAI Image Gallery 01234567"} {
		t.Run(name, func(t *testing.T) {
			bridge.status = selected
			bridge.status.FontName = name
			before := bridge.status
			services := bridge.services(t)
			calls := 0
			source := services.source
			services.source = func(ctx context.Context, font string, size int) (imagefontmac.SourceFont, error) {
				calls++
				return source(ctx, font, size)
			}
			register := services.register
			services.register = func(ctx context.Context, path string) error { calls++; return register(ctx, path) }
			preserve := services.preserve
			services.preserve = func(ctx context.Context, profile, tty, font string, status imagefontmac.ProfileStatus) error {
				calls++
				return preserve(ctx, profile, tty, font, status)
			}
			restore := services.restore
			services.restore = func(ctx context.Context, profile, tty, font string, status imagefontmac.ProfileStatus) error {
				calls++
				return restore(ctx, profile, tty, font, status)
			}
			newDirectory := filepath.Join(t.TempDir(), "changed-session")
			img.Set(0, 0, color.RGBA{B: 255, A: 255})
			var output bytes.Buffer
			err := displayImageFont(t.Context(), &output, img, 32, newDirectory, "/dev/ttys001", testFontViewport, services)
			var fontErr *FontError
			require.ErrorAs(t, err, &fontErr)
			require.ErrorContains(t, err, "session")
			require.Zero(t, calls, "do not resolve, register, replace or restore a font from another session")
			require.Equal(t, before, bridge.status)
			require.Empty(t, output.String())
			g, err := imagegallery.Open(t.Context(), newDirectory)
			require.NoError(t, err)
			require.Zero(t, g.State().ImageCount)
			require.Zero(t, g.State().UsedGlyphs)
			require.NoError(t, g.Close())
			for path, expected := range map[string][]byte{
				filepath.Join(directory, "state.json"): stateBefore,
				bridge.registered[0]:                   fontBefore,
				imagePath:                              imageBefore,
			} {
				actual, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, expected, actual)
			}
		})
	}
}

func TestImageFontAcceptsEarlierFontFromSameGallery(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	bridge := newTestFontBridge()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var output bytes.Buffer
	require.NoError(t, displayImageFont(t.Context(), &output, img, 32, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
	firstFont := bridge.status.FontName
	firstPath := bridge.registered[0]
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	require.NoError(t, displayImageFont(t.Context(), &output, img, 32, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
	bridge.status.FontName = firstFont
	bridge.status.ProfileName = "Renamed profile"
	registered := len(bridge.registered)
	img.Set(0, 0, color.RGBA{G: 255, A: 255})
	require.NoError(t, displayImageFont(t.Context(), &output, img, 32, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
	require.Equal(t, firstPath, bridge.registered[registered], "restore the selected immutable font before reading its lineage")
	require.Equal(t, "Renamed profile", bridge.status.ProfileName)
	g, err := imagegallery.Open(t.Context(), directory)
	require.NoError(t, err)
	require.Equal(t, 3, g.State().ImageCount)
	require.NoError(t, g.Close())
}

func TestImageFontRejectsMissingOrMalformedOwnedFont(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "malformed", true: "missing"}[missing], func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "gallery")
			bridge := newTestFontBridge()
			img := image.NewRGBA(image.Rect(0, 0, 16, 16))
			var output bytes.Buffer
			require.NoError(t, displayImageFont(t.Context(), &output, img, 32, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
			if missing {
				require.NoError(t, os.Remove(bridge.registered[0]))
			} else {
				bridge.status.FontName = strings.TrimSuffix(bridge.status.FontName, "Regular") + "Unknown"
			}
			selected := bridge.status
			registered := len(bridge.registered)
			output.Reset()
			err := displayImageFont(t.Context(), &output, img, 32, directory, "/dev/ttys001", testFontViewport, bridge.services(t))
			var fontErr *FontError
			require.ErrorAs(t, err, &fontErr)
			require.Equal(t, selected, bridge.status)
			require.Len(t, bridge.registered, registered)
			require.Empty(t, output.String())
		})
	}
}

func TestImageFontContinuesInheritedGalleryWithoutReplacingHistory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	bridge := newTestFontBridge()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var output bytes.Buffer
	require.NoError(t, displayImageFont(t.Context(), &output, img, 4, directory, "/dev/ttys008", testFontViewport, bridge.services(t)))
	inherited := bridge.status.FontName
	first := output.String()
	owner, err := os.ReadFile(filepath.Join(directory, ".tty"))
	require.NoError(t, err)
	fontPath := bridge.registered[0]
	originalFont, err := os.ReadFile(fontPath)
	require.NoError(t, err)
	img.Set(0, 0, color.RGBA{B: 255, A: 255})
	output.Reset()
	require.NoError(t, displayImageFontReservedSession(t.Context(), &output, img, 4, directory, "/dev/ttys002", testFontViewport, bridge.services(t), 0, inherited))
	require.NotEqual(t, first, output.String())
	currentFont, err := os.ReadFile(fontPath)
	require.NoError(t, err)
	require.Equal(t, originalFont, currentFont)
	currentOwner, err := os.ReadFile(filepath.Join(directory, ".tty"))
	require.NoError(t, err)
	require.Equal(t, owner, currentOwner)
	// A tab still using the earlier font can append after the other tab finishes.
	bridge.status.FontName = inherited
	img.Set(0, 0, color.RGBA{G: 255, A: 255})
	output.Reset()
	require.NoError(t, displayImageFontReservedSession(t.Context(), &output, img, 4, directory, "/dev/ttys003", testFontViewport, bridge.services(t), 0, inherited))
	stateBefore, err := os.ReadFile(filepath.Join(directory, "state.json"))
	require.NoError(t, err)
	// Selection changes during lookup must stop before font registration or output.
	output.Reset()
	registered := len(bridge.registered)
	err = displayImageFontReservedSession(t.Context(), &output, img, 4, directory, "/dev/ttys004", testFontViewport, bridge.services(t), 0, inherited)
	require.ErrorContains(t, err, "changed")
	require.Empty(t, output.String())
	require.Len(t, bridge.registered, registered)
	stateAfter, err := os.ReadFile(filepath.Join(directory, "state.json"))
	require.NoError(t, err)
	require.Equal(t, stateBefore, stateAfter)
}

func TestImageFontSessionReconnectPreservesSameImagePendingRetry(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0700))
	directory := filepath.Join(root, strings.Repeat("a", 32))
	bridge := newTestFontBridge()
	services := bridge.services(t)
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var output bytes.Buffer
	adopted, err := displayImageFontForSession(t.Context(), &output, img, 4, directory, "/dev/ttys008", testFontViewport, services, 0)
	require.NoError(t, err)
	require.False(t, adopted)
	original := bridge.status.FontName
	firstPath := bridge.registered[0]
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	output.Reset()
	failed := services
	sentinel := errors.New("synthetic uncertain registration")
	failed.register = func(ctx context.Context, path string) error {
		if path != firstPath {
			return sentinel
		}
		return services.register(ctx, path)
	}
	_, err = displayImageFontForSession(t.Context(), &output, img, 4, directory, "/dev/ttys008", testFontViewport, failed, 0)
	require.ErrorIs(t, err, sentinel)
	require.Empty(t, output.String())
	require.Equal(t, original, bridge.status.FontName)
	_, err = displayImageFontForSession(t.Context(), &output, img, 4, directory, "/dev/ttys008", testFontViewport, services, 0)
	require.NoError(t, err)
	require.NotEmpty(t, output.String())
	// A new tab inherits that exact font and shares allocation through the same lock.
	output.Reset()
	img.Set(0, 0, color.RGBA{B: 255, A: 255})
	adopted, err = displayImageFontForSession(t.Context(), &output, img, 4, filepath.Join(root, strings.Repeat("b", 32)), "/dev/ttys002", testFontViewport, services, 0)
	require.NoError(t, err)
	require.True(t, adopted)
	require.NotEmpty(t, output.String())
	owner, err := os.ReadFile(filepath.Join(directory, ".tty"))
	require.NoError(t, err)
	require.Equal(t, "/dev/ttys008", string(owner))
	// An inherited session must retain exact-attempt recovery after a partial registration.
	inheritedDirectory := filepath.Join(root, strings.Repeat("b", 32))
	known := map[string]bool{}
	for _, path := range bridge.registered {
		known[path] = true
	}
	failed = services
	failed.register = func(ctx context.Context, path string) error {
		if !known[path] {
			return sentinel
		}
		return services.register(ctx, path)
	}
	img.Set(0, 0, color.RGBA{G: 255, A: 255})
	output.Reset()
	_, err = displayImageFontForSession(t.Context(), &output, img, 4, inheritedDirectory, "/dev/ttys002", testFontViewport, failed, 0)
	require.ErrorIs(t, err, sentinel)
	require.Empty(t, output.String())
	pendingPath := filepath.Join(directory, ".pending.json")
	pendingBefore, err := os.ReadFile(pendingPath)
	require.NoError(t, err)
	_, err = displayImageFontForSession(t.Context(), &output, img, 8, inheritedDirectory, "/dev/ttys002", testFontViewport, services, 0)
	require.ErrorIs(t, err, imagegallery.ErrPending)
	require.Empty(t, output.String())
	pendingAfter, err := os.ReadFile(pendingPath)
	require.NoError(t, err)
	require.Equal(t, pendingBefore, pendingAfter)
	_, err = displayImageFontForSession(t.Context(), &output, img, 4, inheritedDirectory, "/dev/ttys002", testFontViewport, services, 0)
	require.NoError(t, err)
	require.NotEmpty(t, output.String())
}

func TestImageFontSessionWaitsForSharedLockAndCancels(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0700))
	directory := filepath.Join(root, strings.Repeat("a", 32))
	bridge := newTestFontBridge()
	services := bridge.services(t)
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var first bytes.Buffer
	_, err := displayImageFontForSession(t.Context(), &first, img, 4, directory, "/dev/ttys008", testFontViewport, services, 0)
	require.NoError(t, err)
	lock, err := imagegallery.Open(t.Context(), directory)
	require.NoError(t, err)
	retrying := make(chan struct{}, 1)
	done := make(chan error, 1)
	attempts := 0
	originalSnapshot := services.snapshot
	services.snapshot = func(ctx context.Context, profile, tty string) (imagefontmac.ProfileStatus, error) {
		attempts++
		if attempts == 2 {
			retrying <- struct{}{}
		}
		return originalSnapshot(ctx, profile, tty)
	}
	var output bytes.Buffer
	go func() {
		_, err := displayImageFontForSession(t.Context(), &output, img, 4, filepath.Join(root, strings.Repeat("b", 32)), "/dev/ttys002", testFontViewport, services, 0)
		done <- err
	}()
	select {
	case <-retrying:
	case <-time.After(time.Second):
		_ = lock.Close()
		t.Fatal("shared lock was not retried")
	}
	require.NoError(t, lock.Close())
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("preview did not resume")
	}
	require.NotEmpty(t, output.String())
	lock, err = imagegallery.Open(t.Context(), directory)
	require.NoError(t, err)
	defer lock.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	output.Reset()
	_, err = displayImageFontForSession(ctx, &output, img, 4, filepath.Join(root, strings.Repeat("c", 32)), "/dev/ttys003", testFontViewport, services, 0)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, output.String())
}
