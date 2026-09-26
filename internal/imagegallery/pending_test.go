package imagegallery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Leave durable state exactly as a process exit would, without Close cleanup.
func abandonPendingGallery(t *testing.T, g *Gallery) {
	t.Helper()
	require.NoError(t, unlockFile(g.lock))
	require.NoError(t, g.lock.Close())
	g.closed = true
}

func TestPendingRecoversBeforeRegistration(t *testing.T) {
	for _, window := range []string{"record only", "before link", "after link", "cancelled"} {
		t.Run(window, func(t *testing.T) {
			g := initialized(t)
			img := fixture(color.NRGBA{R: 200, A: 255})
			revision, err := g.Prepare(t.Context(), img, 4)
			require.NoError(t, err)
			name := revision.state.Images[0].Hash + ".png"
			final := filepath.Join(g.directory, "images", name)
			stage := filepath.Join(g.directory, ".pending", name)
			switch window {
			case "record only":
				require.NoError(t, os.Remove(final))
				require.NoError(t, os.Remove(stage))
				require.NoError(t, os.Remove(filepath.Dir(stage)))
			case "before link":
				require.NoError(t, os.Remove(final))
			case "cancelled":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				_, err := g.FontForTypography(ctx, revision, typographySource(t))
				require.ErrorIs(t, err, context.Canceled)
				require.NoError(t, g.Close(), "cleanup must survive request cancellation")
			}
			if !g.closed {
				abandonPendingGallery(t, g)
			}
			reopened, err := Open(t.Context(), g.directory)
			require.NoError(t, err)
			defer reopened.Close()
			require.NoFileExists(t, final)
			require.NoFileExists(t, filepath.Join(g.directory, ".pending.json"))
			_, err = reopened.Prepare(t.Context(), fixture(color.NRGBA{B: 200, A: 255}), 4)
			require.NoError(t, err, "a different request is safe before registration")
		})
	}
}

func TestPendingReplacesOnlyBeforeRegistration(t *testing.T) {
	for _, change := range []string{"image", "typography"} {
		t.Run(change, func(t *testing.T) {
			g := initialized(t)
			defer g.Close()
			img := fixture(color.NRGBA{R: 200, A: 255})
			source := typographySource(t)
			revision, err := g.Prepare(t.Context(), img, 4)
			require.NoError(t, err)
			first := revision
			display, err := g.FontForTypography(t.Context(), revision, source)
			require.NoError(t, err)
			for i := range 3 {
				oldPath := display.FontPath
				if change == "image" {
					img = fixture(color.NRGBA{B: uint8(100 + i), A: 255})
					revision, err = g.Prepare(t.Context(), img, 4)
					require.NoError(t, err)
				} else {
					source.PointSize++
				}
				display, err = g.FontForTypography(t.Context(), revision, source)
				require.NoError(t, err)
				require.NoFileExists(t, oldPath)
				for _, directory := range []string{"images", "fonts"} {
					files, err := os.ReadDir(filepath.Join(g.directory, directory))
					require.NoError(t, err)
					require.Len(t, files, 1, "preparing replacements must remain bounded")
				}
			}
			if change == "image" {
				require.Error(t, g.Commit(t.Context(), first), "replacement must invalidate the old revision")
			}
			require.NoError(t, g.MarkRegistering(t.Context(), revision))
			otherSource := source
			otherSource.PointSize++
			_, err = g.FontForTypography(t.Context(), revision, otherSource)
			require.ErrorIs(t, err, ErrPending)
			_, err = g.Prepare(t.Context(), fixture(color.NRGBA{G: 100, A: 255}), 4)
			require.ErrorIs(t, err, ErrPending)
			retry, err := g.Prepare(t.Context(), img, 4)
			require.NoError(t, err)
			reused, err := g.FontForTypography(t.Context(), retry, source)
			require.NoError(t, err)
			require.Equal(t, display.FontPath, reused.FontPath)
			require.True(t, reused.Existing)
			require.NoError(t, g.Commit(t.Context(), retry))
		})
	}
}

func TestPendingDiscardProtectsPreexistingAndReplacedFiles(t *testing.T) {
	g := initialized(t)
	img := fixture(color.NRGBA{R: 200, A: 255})
	_, data, err := normalize(t.Context(), img)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	imagePath := filepath.Join(g.directory, "images", hex.EncodeToString(digest[:])+".png")
	require.NoError(t, os.WriteFile(imagePath, data, 0600))
	revision, err := g.Prepare(t.Context(), img, 4)
	require.NoError(t, err)
	display, err := g.FontForTypography(t.Context(), revision, typographySource(t))
	require.NoError(t, err)
	fontData, err := os.ReadFile(display.FontPath)
	require.NoError(t, err)
	// Replacing a pathname does not transfer ownership of its new inode.
	require.NoError(t, os.Remove(display.FontPath))
	require.NoError(t, os.WriteFile(display.FontPath, fontData, 0600))
	require.NoError(t, g.Close())
	for path, want := range map[string][]byte{imagePath: data, display.FontPath: fontData} {
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestPendingTypographySurvivesInitializationUntilCommitted(t *testing.T) {
	g := initialized(t)
	img := fixture(color.NRGBA{R: 200, A: 255})
	source := typographySource(t)
	revision, err := g.Prepare(t.Context(), img, 4)
	require.NoError(t, err)
	old, err := g.FontForTypography(t.Context(), revision, source)
	require.NoError(t, err)
	require.NoError(t, g.Commit(t.Context(), revision))
	oldBytes, err := os.ReadFile(old.FontPath)
	require.NoError(t, err)
	oldState := g.state
	source.PointSize++
	revision, err = g.Prepare(t.Context(), img, 4)
	require.NoError(t, err)
	require.True(t, revision.Existing)
	display, err := g.FontForTypography(t.Context(), revision, source)
	require.NoError(t, err)
	require.NoError(t, g.MarkRegistering(t.Context(), revision))
	abandonPendingGallery(t, g)
	g, err = Open(t.Context(), g.directory)
	require.NoError(t, err)
	defer g.Close()
	initial, err := g.Initialize(t.Context())
	require.NoError(t, err)
	require.NoError(t, g.Commit(t.Context(), initial))
	require.Equal(t, oldState, g.state)
	require.NotNil(t, g.attempt)
	revision, err = g.Prepare(t.Context(), img, 4)
	require.NoError(t, err)
	_, err = g.FontForTypography(t.Context(), revision, typographySource(t))
	require.ErrorIs(t, err, ErrPending)
	retry, err := g.FontForTypography(t.Context(), revision, source)
	require.NoError(t, err)
	require.Equal(t, display.FontPath, retry.FontPath)
	require.True(t, retry.Existing)
	require.NoError(t, g.Commit(t.Context(), revision))
	require.NotEqual(t, oldState.CompletedAttempt, g.state.CompletedAttempt)
	require.Nil(t, g.attempt)
	got, err := os.ReadFile(old.FontPath)
	require.NoError(t, err)
	require.Equal(t, oldBytes, got)
	_, err = g.Prepare(t.Context(), fixture(color.NRGBA{B: 200, A: 255}), 4)
	require.NoError(t, err)
}

func TestPendingCommitReceiptRecoversCleanupFailure(t *testing.T) {
	g := initialized(t)
	revision, err := g.Prepare(t.Context(), fixture(color.NRGBA{R: 200, A: 255}), 4)
	require.NoError(t, err)
	display, err := g.FontForTypography(t.Context(), revision, typographySource(t))
	require.NoError(t, err)
	require.NoError(t, g.MarkRegistering(t.Context(), revision))
	attempt := g.attempt.ID
	fontData, err := os.ReadFile(display.FontPath)
	require.NoError(t, err)
	// An unexpected entry forces cleanup to preserve its ownership evidence.
	blocker := filepath.Join(g.directory, ".pending", "unrecognized")
	require.NoError(t, os.WriteFile(blocker, []byte("retain"), 0600))
	require.NoError(t, g.Commit(t.Context(), revision))
	require.Equal(t, attempt, g.state.CompletedAttempt)
	require.FileExists(t, filepath.Join(g.directory, ".pending.json"))
	require.NoError(t, os.Remove(blocker))
	abandonPendingGallery(t, g)
	g, err = Open(t.Context(), g.directory)
	require.NoError(t, err)
	defer g.Close()
	require.Nil(t, g.attempt)
	require.Equal(t, 1, g.State().ImageCount)
	got, err := os.ReadFile(display.FontPath)
	require.NoError(t, err)
	require.Equal(t, fontData, got)
	_, err = g.Prepare(t.Context(), fixture(color.NRGBA{B: 200, A: 255}), 4)
	require.NoError(t, err, "a completed request must not leave a false pending block")
}

func TestPendingClosedGalleryCleanupRetainsLiveFonts(t *testing.T) {
	root, directory, _ := cleanupFixture(t, true)
	g, err := Open(t.Context(), directory)
	require.NoError(t, err)
	revision, err := g.Prepare(t.Context(), fixture(color.NRGBA{R: 200, A: 255}), 4)
	require.NoError(t, err)
	display, err := g.FontForTypography(t.Context(), revision, typographySource(t))
	require.NoError(t, err)
	require.NoError(t, g.MarkRegistering(t.Context(), revision))
	require.NoError(t, g.Close())
	live := true
	unregistered := 0
	inventory := func(context.Context) ([]string, []string, error) {
		if live {
			return []string{"/dev/ttys002"}, []string{display.PostScript}, nil
		}
		return nil, nil, nil
	}
	unregister := func(_ context.Context, path string) error {
		require.FileExists(t, path)
		unregistered++
		return nil
	}
	require.NoError(t, CleanupClosed(t.Context(), root, TerminalSession{}, inventory, unregister))
	require.Zero(t, unregistered)
	require.FileExists(t, display.FontPath)
	require.FileExists(t, filepath.Join(directory, ".pending.json"))
	live = false
	require.NoError(t, CleanupClosed(t.Context(), root, TerminalSession{}, inventory, unregister))
	require.Equal(t, 2, unregistered)
	files, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, ".lock", files[0].Name())
}

func TestPendingRejectsInvalidRecordsAndStorage(t *testing.T) {
	for _, failure := range []string{"unknown field", "trailing data", "invalid typography", "invalid revision", "record symlink", "stage symlink", "public record"} {
		t.Run(failure, func(t *testing.T) {
			if runtime.GOOS == "windows" && (strings.Contains(failure, "symlink") || failure == "public record") {
				t.Skip("Unix ownership and symlink checks")
			}
			g := initialized(t)
			revision, err := g.Prepare(t.Context(), fixture(color.NRGBA{R: 200, A: 255}), 4)
			require.NoError(t, err)
			display, err := g.FontForTypography(t.Context(), revision, typographySource(t))
			require.NoError(t, err)
			require.NoError(t, g.MarkRegistering(t.Context(), revision))
			before, err := os.ReadFile(display.FontPath)
			require.NoError(t, err)
			record := filepath.Join(g.directory, ".pending.json")
			data, err := os.ReadFile(record)
			require.NoError(t, err)
			abandonPendingGallery(t, g)
			switch failure {
			case "unknown field":
				data = append([]byte(`{"unknown":1,`), data[1:]...)
			case "trailing data":
				data = append(data, []byte(` {}`)...)
			case "invalid typography", "invalid revision":
				var value map[string]any
				require.NoError(t, json.Unmarshal(data, &value))
				value[strings.TrimPrefix(failure, "invalid ")] = "../outside"
				data, err = json.Marshal(value)
				require.NoError(t, err)
			case "record symlink":
				require.NoError(t, os.Rename(record, record+".saved"))
				require.NoError(t, os.Symlink(record+".saved", record))
			case "stage symlink":
				stage := filepath.Join(g.directory, ".pending")
				require.NoError(t, os.Rename(stage, stage+".saved"))
				require.NoError(t, os.Symlink(stage+".saved", stage))
			case "public record":
				require.NoError(t, os.Chmod(record, 0644))
			}
			if !strings.Contains(failure, "symlink") && failure != "public record" {
				require.NoError(t, os.WriteFile(record, data, 0600))
			}
			_, err = Open(t.Context(), g.directory)
			require.Error(t, err)
			after, err := os.ReadFile(display.FontPath)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}
