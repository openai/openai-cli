package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/stretchr/testify/require"
)

func TestFontSessionDirectoryPreservesLegacyOnlyForSelectedFont(t *testing.T) {
	root := filepath.Join(t.TempDir(), "image-terminal")
	expected := sessionImageFontDirectory(root, "session-a", "/dev/ttys001")
	calls := 0
	check := func(context.Context, string, string) error { calls++; return nil }
	directory, err := resolveImageFontDirectory(t.Context(), root, "session-a", "/dev/ttys001", check)
	require.NoError(t, err)
	require.Equal(t, expected, directory)
	require.Zero(t, calls, "fresh setup must not call Terminal before consent")

	bridge := newFakeImageFontBridge(t)
	gallery, err := prepareImageFontGallery(t.Context(), root, bridge.services())
	require.NoError(t, err)
	legacy := gallery.State()
	require.NoError(t, gallery.Close())
	check = func(_ context.Context, profile, tty string) error {
		require.Equal(t, legacy.ProfileName, profile)
		require.Equal(t, "/dev/ttys001", tty)
		return nil
	}
	directory, err = resolveImageFontDirectory(t.Context(), root, "session-a", "/dev/ttys001", check)
	require.NoError(t, err)
	require.Equal(t, root, directory, "existing image scrollback keeps its glyph map")
	check = func(context.Context, string, string) error { return imagefontmac.ErrOtherProfile }
	directory, err = resolveImageFontDirectory(t.Context(), root, "session-a", "/dev/ttys001", check)
	require.NoError(t, err)
	require.Equal(t, expected, directory)
	require.NotEqual(t, expected, sessionImageFontDirectory(root, "session-a", "/dev/ttys002"))
	require.NotEqual(t, expected, sessionImageFontDirectory(root, "session-b", "/dev/ttys001"))
	denial := errors.New("synthetic automation denial")
	_, err = resolveImageFontDirectory(t.Context(), root, "session-a", "/dev/ttys001", func(context.Context, string, string) error { return denial })
	require.ErrorIs(t, err, denial)
}

func TestResetIncludesClosedTabCachesAndRetainsUnrelatedFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "image-terminal")
	bridge := newFakeImageFontBridge(t)
	directories := []string{root, sessionImageFontDirectory(root, "one", "/dev/ttys001"), sessionImageFontDirectory(root, "two", "/dev/ttys002")}
	for _, directory := range directories {
		gallery, err := prepareImageFontGallery(t.Context(), directory, bridge.services())
		require.NoError(t, err)
		require.NoError(t, gallery.Close())
	}
	unrelated := filepath.Join(root, "keep.txt")
	require.NoError(t, os.WriteFile(unrelated, []byte("synthetic unrelated file"), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "unrelated-directory"), 0700))
	tombstone := sessionImageFontDirectory(root, "closed", "/dev/ttys003")
	require.NoError(t, os.Mkdir(tombstone, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(tombstone, ".lock"), nil, 0600))
	for _, name := range []string{"fonts", "images"} {
		require.NoError(t, os.Mkdir(filepath.Join(tombstone, name), 0700))
	}
	unrelatedSession := sessionImageFontDirectory(root, "unrelated", "/dev/ttys004")
	require.NoError(t, os.Mkdir(unrelatedSession, 0700))
	unrelatedNote := filepath.Join(unrelatedSession, "keep.txt")
	require.NoError(t, os.WriteFile(unrelatedNote, []byte("synthetic unrelated file"), 0600))
	var output bytes.Buffer
	require.NoError(t, printImageFontCacheStatus(t.Context(), &output, root, true))
	require.Contains(t, output.String(), "Cached image galleries: 3")
	output.Reset()
	bridge.unusedErr = errors.New("synthetic active tab")
	require.ErrorIs(t, resetImageFontCaches(t.Context(), &output, root, bridge.services()), bridge.unusedErr)
	for _, directory := range directories {
		require.FileExists(t, filepath.Join(directory, "state.json"), "failed preflight must leave every gallery intact")
	}
	bridge.unusedErr = nil
	require.NoError(t, resetImageFontCaches(t.Context(), &output, root, bridge.services()))
	require.Contains(t, output.String(), "Cleared cached previews")
	for _, directory := range directories {
		require.NoFileExists(t, filepath.Join(directory, "state.json"))
	}
	require.FileExists(t, unrelated)
	require.DirExists(t, tombstone)
	require.FileExists(t, filepath.Join(tombstone, ".lock"))
	require.FileExists(t, unrelatedNote)
}

func TestResetReportsMissingGalleryStateWithoutRemovingArtifacts(t *testing.T) {
	for _, location := range []string{"legacy", "session"} {
		t.Run(location, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "image-terminal")
			directory := root
			if location == "session" {
				directory = sessionImageFontDirectory(root, "lost-state", "/dev/ttys001")
			}
			bridge := newFakeImageFontBridge(t)
			gallery, err := prepareImageFontGallery(t.Context(), directory, bridge.services())
			require.NoError(t, err)
			font := gallery.State().FontPath
			require.NoError(t, gallery.Close())
			original, err := os.ReadFile(font)
			require.NoError(t, err)
			require.NoError(t, os.Remove(filepath.Join(directory, "state.json")))
			bridge.calls = nil

			var output bytes.Buffer
			err = printImageFontCacheStatus(t.Context(), &output, root, true)
			require.ErrorContains(t, err, "state.json is missing")
			require.NotContains(t, output.String(), "Not set up")
			output.Reset()
			err = resetImageFontCaches(t.Context(), &output, root, bridge.services())
			require.ErrorContains(t, err, "state.json is missing")
			require.ErrorContains(t, err, "cached files were kept")
			require.Empty(t, output.String(), "a damaged cache must not report a successful reset")
			require.Empty(t, bridge.calls, "missing identity must not trigger native font changes")
			preserved, err := os.ReadFile(font)
			require.NoError(t, err)
			require.Equal(t, original, preserved)
		})
	}
}

func TestBindSessionDoesNotBindSharedLegacyGallery(t *testing.T) {
	root := filepath.Join(t.TempDir(), "image-terminal")
	for _, directory := range []string{root, sessionImageFontDirectory(root, "one", "/dev/ttys001")} {
		gallery, err := imagegallery.Open(t.Context(), directory)
		require.NoError(t, err)
		require.NoError(t, bindImageFontSession(t.Context(), gallery, directory, "/dev/ttys001"))
		err = bindImageFontSession(t.Context(), gallery, directory, "/dev/ttys002")
		if directory == root {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
		require.NoError(t, gallery.Close())
	}
}

func TestUnrelatedBrokenLegacyCacheDoesNotBlockNewOrCurrentTabs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "image-terminal")
	require.NoError(t, os.MkdirAll(root, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "state.json"), []byte("synthetic corrupt state"), 0600))
	directory := sessionImageFontDirectory(root, "one", "/dev/ttys001")
	selected := "GoMono"
	snapshot := func(context.Context, string, string) (imagefontmac.ProfileStatus, error) {
		return imagefontmac.ProfileStatus{FontName: selected, FontSize: 12}, nil
	}
	check := func(context.Context, string, string) error { return nil }
	got, err := resolveImageFontDirectory(t.Context(), root, "one", "/dev/ttys001", check, snapshot)
	require.NoError(t, err)
	require.Equal(t, directory, got)
	bridge := newFakeImageFontBridge(t)
	gallery, err := prepareImageFontGallery(t.Context(), directory, bridge.services())
	require.NoError(t, err)
	selected = gallery.State().PostScript
	require.NoError(t, gallery.Close())
	got, err = resolveImageFontDirectory(t.Context(), root, "one", "/dev/ttys001", check, snapshot)
	require.NoError(t, err)
	require.Equal(t, directory, got)
	check = func(context.Context, string, string) error { return imagefontmac.ErrOtherProfile }
	_, err = resolveImageFontDirectory(t.Context(), root, "one", "/dev/ttys001", check, snapshot)
	require.Error(t, err, "a selected legacy image font must not silently lose its gallery")
}

func TestSavedImagePreviewRestoresFontAfterLostActivationReply(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	bridge := newTestFontBridge()
	services := bridge.services(t)
	var output bytes.Buffer
	require.NoError(t, setupCurrentImageFont(t.Context(), &output, directory, "/dev/ttys001", services))
	before := bridge.status
	output.Reset()
	preserve := services.preserve
	failure := errors.New("synthetic lost activation reply")
	services.preserve = func(ctx context.Context, profile, tty, font string, original imagefontmac.ProfileStatus) error {
		require.NoError(t, preserve(ctx, profile, tty, font, original))
		return failure
	}
	path := imageInlineFixture(t, "sample.png", color.NRGBA{R: 255, A: 255})
	err := displayImageFont(t.Context(), &output, directory, path, "/dev/ttys001", testFontViewport(), services)
	require.ErrorIs(t, err, failure)
	require.Empty(t, output.String())
	require.Equal(t, before, bridge.status)
	require.Zero(t, imageInlineState(t, directory).ImageCount)
}

func TestSelectedLegacyFontIgnoresUnrelatedBrokenOrBusySessionCache(t *testing.T) {
	for _, condition := range []string{"broken", "busy"} {
		t.Run(condition, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "image-terminal")
			directory := sessionImageFontDirectory(root, "one", "/dev/ttys001")
			bridge := newFakeImageFontBridge(t)
			legacy, err := prepareImageFontGallery(t.Context(), root, bridge.services())
			require.NoError(t, err)
			state := legacy.State()
			require.NoError(t, legacy.Close())
			current, err := prepareImageFontGallery(t.Context(), directory, bridge.services())
			require.NoError(t, err)
			if condition == "broken" {
				require.NoError(t, current.Close())
				require.NoError(t, os.WriteFile(filepath.Join(directory, "state.json"), []byte("synthetic invalid state"), 0600))
			} else {
				t.Cleanup(func() { require.NoError(t, current.Close()) })
			}
			snapshot := func(context.Context, string, string) (imagefontmac.ProfileStatus, error) {
				return imagefontmac.ProfileStatus{FontName: state.PostScript, FontSize: 12}, nil
			}
			check := func(_ context.Context, profile, tty string) error {
				require.Equal(t, state.ProfileName, profile)
				require.Equal(t, "/dev/ttys001", tty)
				return nil
			}
			got, err := resolveImageFontDirectory(t.Context(), root, "one", "/dev/ttys001", check, snapshot)
			require.NoError(t, err)
			require.Equal(t, root, got)
			// If neither healthy candidate owns the selected font, preserve the
			// metadata failure rather than silently choosing a different glyph map.
			check = func(context.Context, string, string) error { return imagefontmac.ErrOtherProfile }
			got, err = resolveImageFontDirectory(t.Context(), root, "one", "/dev/ttys001", check, snapshot)
			require.Error(t, err)
			require.Empty(t, got)
			if condition == "busy" {
				require.ErrorIs(t, err, imagegallery.ErrBusy)
			}
		})
	}
}
