package terminalimage

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/font/sfnt"
)

func fontCacheFiles(t *testing.T, directory string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	require.NoError(t, filepath.WalkDir(directory, func(path string, item os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !item.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[strings.TrimPrefix(path, directory)] = data
		}
		return nil
	}))
	return files
}

func TestSetupImageFontAddsNoSampleAndIsRepeatable(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	bridge := newTestFontBridge()
	before := bridge.status
	var out bytes.Buffer
	require.NoError(t, setupImageFont(t.Context(), &out, directory, "/dev/ttys001", false, testFontViewport, bridge.services(t)))
	require.Contains(t, out.String(), "GoMono at 12 pt and 0 cached images")
	require.Contains(t, out.String(), "Use --inline on")
	require.Contains(t, out.String(), "Automatic preview preferences are unchanged")
	require.NotContains(t, out.String(), string(rune(0xf0000)))
	cache, err := imagegallery.Inspect(t.Context(), directory, "/dev/ttys001", bridge.status.FontName)
	require.NoError(t, err)
	require.True(t, cache.Initialized)
	require.Zero(t, cache.ImageCount)
	require.Zero(t, cache.UsedGlyphs)
	require.Zero(t, cache.Revision)
	files := fontCacheFiles(t, directory)
	for _, repair := range []bool{false, true} {
		require.NoError(t, setupImageFont(t.Context(), io.Discard, directory, "/dev/ttys001", repair, testFontViewport, bridge.services(t)))
		require.Equal(t, files, fontCacheFiles(t, directory), "repeated setup/repair must reuse exact files and slots")
	}
	require.Equal(t, before.FontSize, bridge.status.FontSize)
	require.Equal(t, before.ProfileID, bridge.status.ProfileID)
	require.Equal(t, before.ProfileName, bridge.status.ProfileName)
}

func TestRepairImageFontRetainsImagesFontsAndOriginal(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	original := filepath.Join(t.TempDir(), "saved-original.png")
	require.NoError(t, os.WriteFile(original, []byte("synthetic untouched saved original"), 0600))
	bridge := newTestFontBridge()
	var output bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var chars []rune
	for _, c := range []color.RGBA{{R: 255, A: 255}, {B: 255, A: 255}} {
		img.Set(0, 0, c)
		output.Reset()
		require.NoError(t, displayImageFont(t.Context(), &output, img, 8, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
		chars = append(chars, []rune(output.String())[0])
	}
	before, err := imagegallery.Inspect(t.Context(), directory, "/dev/ttys001", bridge.status.FontName)
	require.NoError(t, err)
	oldFiles := fontCacheFiles(t, directory)
	bridge.status.FontName, bridge.status.FontSize = "GoMono", 13
	require.NoError(t, setupImageFont(t.Context(), io.Discard, directory, "/dev/ttys001", true, testFontViewport, bridge.services(t)))
	after, err := imagegallery.Inspect(t.Context(), directory, "/dev/ttys001", bridge.status.FontName)
	require.NoError(t, err)
	require.Equal(t, before.State, after.State)
	require.NotEqual(t, before.FontPath, after.FontPath)
	for name, data := range oldFiles {
		if name != "/state.json" {
			require.Equal(t, data, fontCacheFiles(t, directory)[name], name)
		}
	}
	data, err := os.ReadFile(after.FontPath)
	require.NoError(t, err)
	font, err := sfnt.Parse(data)
	require.NoError(t, err)
	for _, char := range append(chars, 'A') {
		glyph, err := font.GlyphIndex(nil, char)
		require.NoError(t, err)
		require.NotZero(t, glyph)
	}
	require.Equal(t, 13.0, bridge.status.FontSize)
	require.Equal(t, "Synthetic profile", bridge.status.ProfileName)
	data, err = os.ReadFile(original)
	require.NoError(t, err)
	require.Equal(t, "synthetic untouched saved original", string(data))
}

func TestRepairRestoresRegistrationBeforeResolvingSource(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	bridge := newTestFontBridge()
	require.NoError(t, setupImageFont(t.Context(), io.Discard, directory, "/dev/ttys001", false, testFontViewport, bridge.services(t)))
	selected := bridge.registered[len(bridge.registered)-1]
	bridge.registered = nil
	services := bridge.services(t)
	services.source = func(context.Context, string, int) (imagefontmac.SourceFont, error) {
		require.Equal(t, []string{selected}, bridge.registered, "restore registration after logout before lineage lookup")
		return testFontSource(), nil
	}
	require.NoError(t, setupImageFont(t.Context(), io.Discard, directory, "/dev/ttys001", true, testFontViewport, services))
}

func TestRepairMissingSelectedFontRequiresOriginalFace(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	bridge := newTestFontBridge()
	require.NoError(t, setupImageFont(t.Context(), io.Discard, directory, "/dev/ttys001", false, testFontViewport, bridge.services(t)))
	path := bridge.registered[len(bridge.registered)-1]
	old, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	files := fontCacheFiles(t, directory)
	err = setupImageFont(t.Context(), io.Discard, directory, "/dev/ttys001", true, testFontViewport, bridge.services(t))
	require.ErrorContains(t, err, "select your original font and size")
	require.Equal(t, files, fontCacheFiles(t, directory))
	bridge.status.FontName = "GoMono"
	require.NoError(t, setupImageFont(t.Context(), io.Discard, directory, "/dev/ttys001", true, testFontViewport, bridge.services(t)))
	rebuilt, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, old, rebuilt)
}

func TestRepairMissingCacheDoesNotCreateOrMutate(t *testing.T) {
	for _, damage := range []string{"missing", "metadata", "thumbnail", "foreign font"} {
		t.Run(damage, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "gallery")
			bridge := newTestFontBridge()
			if damage != "missing" {
				require.NoError(t, displayImageFont(t.Context(), io.Discard, image.NewRGBA(image.Rect(0, 0, 4, 4)), 4, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
				switch damage {
				case "metadata":
					require.NoError(t, os.WriteFile(filepath.Join(directory, "state.json"), []byte("{invalid"), 0600))
				case "thumbnail":
					files, err := filepath.Glob(filepath.Join(directory, "images", "*.png"))
					require.NoError(t, err)
					require.Len(t, files, 1)
					require.NoError(t, os.Remove(files[0]))
				case "foreign font":
					bridge.status.FontName = "OpenAIImages-aaaaaaaa-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-Regular"
				}
			}
			files, selected := fontCacheFiles(t, directory), bridge.status
			calls := len(bridge.registered)
			var out bytes.Buffer
			require.Error(t, setupImageFont(t.Context(), &out, directory, "/dev/ttys001", true, testFontViewport, bridge.services(t)))
			require.Empty(t, out.String())
			require.Equal(t, files, fontCacheFiles(t, directory))
			require.Equal(t, selected, bridge.status)
			require.Len(t, bridge.registered, calls)
		})
	}
}

func TestSetupCancellationRollsBackActivationAndRetainsRegisteredFont(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	bridge := newTestFontBridge()
	original := bridge.status
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	services := bridge.services(t)
	preserve := services.preserve
	restore := services.restore
	restored := false
	services.preserve = func(ctx context.Context, p, tty, font string, expected imagefontmac.ProfileStatus) error {
		require.NoError(t, preserve(ctx, p, tty, font, expected))
		cancel()
		return ctx.Err()
	}
	services.restore = func(ctx context.Context, p, tty, font string, expected imagefontmac.ProfileStatus) error {
		require.NoError(t, ctx.Err())
		restored = true
		return restore(ctx, p, tty, font, expected)
	}
	var out bytes.Buffer
	require.ErrorIs(t, setupImageFont(ctx, &out, directory, "/dev/ttys001", false, testFontViewport, services), context.Canceled)
	require.True(t, restored)
	require.Equal(t, original, bridge.status)
	require.Empty(t, out.String())
	require.NotEmpty(t, bridge.registered)
	for _, path := range bridge.registered {
		require.FileExists(t, path)
	}
	require.FileExists(t, filepath.Join(directory, ".pending.json"))
	require.NoError(t, setupImageFont(t.Context(), io.Discard, directory, "/dev/ttys001", true, testFontViewport, bridge.services(t)))
}

func TestSetupFailureAndNarrowRepairLeaveSettingsIntact(t *testing.T) {
	for _, failure := range []string{"permission", "fractional size", "source", "narrow", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "gallery")
			bridge := newTestFontBridge()
			require.NoError(t, displayImageFont(t.Context(), io.Discard, image.NewRGBA(image.Rect(0, 0, 4, 4)), 16, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
			services := bridge.services(t)
			viewport := testFontViewport
			ctx := t.Context()
			switch failure {
			case "permission":
				bridge.deny = true
			case "fractional size":
				bridge.status.FontSize = 12.5
			case "source":
				services.source = func(context.Context, string, int) (imagefontmac.SourceFont, error) {
					return imagefontmac.SourceFont{}, errors.New("unavailable text face")
				}
			case "narrow":
				viewport = func() fontViewport { return fontViewport{Columns: 8, Rows: 24, PixelWidth: 56, PixelHeight: 336} }
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			original := bridge.status
			var out bytes.Buffer
			require.Error(t, setupImageFont(ctx, &out, directory, "/dev/ttys001", true, viewport, services))
			require.Empty(t, out.String())
			require.Equal(t, original, bridge.status)
		})
	}
}

func TestSetupReportWriterFailureDoesNotUndoVerifiedActivation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	bridge := newTestFontBridge()
	writeErr := errors.New("closed output")
	err := setupImageFont(t.Context(), writerFunc(func([]byte) (int, error) { return 0, writeErr }), directory, "/dev/ttys001", false, testFontViewport, bridge.services(t))
	require.ErrorIs(t, err, writeErr)
	require.True(t, isImageFont(bridge.status.FontName))
	cache, err := imagegallery.Inspect(t.Context(), directory, "/dev/ttys001", bridge.status.FontName)
	require.NoError(t, err)
	require.FileExists(t, cache.FontPath)
}

func TestFontCommandsRejectUnsupportedOutputAndRespectCancellation(t *testing.T) {
	for _, run := range []func(context.Context, io.Writer) error{SetupFont, RepairFont, FontStatus} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var out bytes.Buffer
		require.ErrorIs(t, run(ctx, &out), context.Canceled)
		require.Empty(t, out.String())
	}
	for _, remote := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "TMUX", "STY", "ZELLIJ", "CI"} {
		t.Run(remote, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", "Apple_Terminal")
			t.Setenv(remote, "synthetic")
			require.ErrorContains(t, SetupFont(t.Context(), io.Discard), "local Apple Terminal")
			var out bytes.Buffer
			require.NoError(t, FontStatus(t.Context(), &out))
			require.Contains(t, out.String(), "unavailable here")
		})
	}
	t.Setenv("TERM_PROGRAM", "unsupported")
	require.ErrorContains(t, RepairFont(t.Context(), io.Discard), "local Apple Terminal")
}

func TestSetupMissingMetadataNeverReassignsRetainedGlyphs(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(map[bool]string{false: "setup", true: "repair"}[repair], func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "gallery")
			bridge := newTestFontBridge()
			require.NoError(t, displayImageFont(t.Context(), io.Discard, image.NewRGBA(image.Rect(0, 0, 4, 4)), 4, directory, "/dev/ttys001", testFontViewport, bridge.services(t)))
			require.NoError(t, os.Remove(filepath.Join(directory, "state.json")))
			bridge.status.FontName = "GoMono" // Manual text-font change cannot prove old scrollback is gone.
			original, files, calls := bridge.status, fontCacheFiles(t, directory), len(bridge.registered)
			var out bytes.Buffer
			err := setupImageFont(t.Context(), &out, directory, "/dev/ttys001", repair, testFontViewport, bridge.services(t))
			require.ErrorContains(t, err, "metadata is missing")
			require.Empty(t, out.String())
			require.Equal(t, original, bridge.status)
			require.Equal(t, files, fontCacheFiles(t, directory))
			require.Len(t, bridge.registered, calls)
		})
	}
}

func TestFontSessionRejectsMissingIdentityAndRedirectedOutput(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	for _, name := range []string{"CI", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "TMUX", "STY", "ZELLIJ"} {
		t.Setenv(name, "")
	}
	if !imagefontmac.Supported() {
		t.Skip("Apple Terminal session identification is macOS-only")
	}
	file, err := os.CreateTemp(t.TempDir(), "redirected-output")
	require.NoError(t, err)
	defer file.Close()
	t.Setenv("TERM_SESSION_ID", "")
	require.ErrorContains(t, SetupFont(t.Context(), file), "TERM_SESSION_ID")
	t.Setenv("TERM_SESSION_ID", "synthetic-session")
	require.ErrorContains(t, SetupFont(t.Context(), file), "without redirecting output")
	require.ErrorContains(t, FontStatus(t.Context(), file), "without redirecting output")
	var out bytes.Buffer
	require.ErrorContains(t, SetupFont(t.Context(), &out), "local Apple Terminal")
}
