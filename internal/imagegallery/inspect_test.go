package imagegallery

import (
	"context"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type inspectedFile struct {
	Data     string
	Mode     os.FileMode
	Modified time.Time
}

func inspectionFiles(t *testing.T, directory string) map[string]inspectedFile {
	t.Helper()
	result := map[string]inspectedFile{}
	require.NoError(t, filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := inspectedFile{Mode: info.Mode(), Modified: info.ModTime()}
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			item.Data = string(data)
		}
		result[path] = item
		return nil
	}))
	return result
}

func TestInspectAbsentCacheNeverCreatesFiles(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing directory", true: "empty directory"}[existing], func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "gallery")
			if existing {
				require.NoError(t, os.Mkdir(directory, 0700))
			}
			before := inspectionFiles(t, directory)
			got, err := Inspect(t.Context(), directory, "/dev/ttys001", "")
			require.NoError(t, err)
			require.False(t, got.Initialized)
			require.Equal(t, before, inspectionFiles(t, directory))
		})
	}
}

func TestInspectLeavesPendingAttemptsAndMetadataUntouched(t *testing.T) {
	for _, registered := range []bool{false, true} {
		t.Run(map[bool]string{false: "unregistered", true: "registration uncertain"}[registered], func(t *testing.T) {
			g := initialized(t)
			require.NoError(t, g.BindTTY(t.Context(), "/dev/ttys001"))
			revision, err := g.Prepare(t.Context(), fixture(color.NRGBA{R: 255, A: 255}), 4)
			require.NoError(t, err)
			font, err := g.FontForTypography(t.Context(), revision, typographySource(t))
			require.NoError(t, err)
			if registered {
				require.NoError(t, g.MarkRegistering(t.Context(), revision))
			}
			expected := g.State()
			abandonPendingGallery(t, g)
			before := inspectionFiles(t, g.directory)
			got, err := Inspect(t.Context(), g.directory, "/dev/ttys001", font.PostScript)
			require.NoError(t, err)
			require.Equal(t, expected, got.State)
			require.Equal(t, font.FontPath, got.FontPath)
			require.Equal(t, before, inspectionFiles(t, g.directory))
			require.FileExists(t, filepath.Join(g.directory, ".pending.json"))
		})
	}
}

func TestInspectRejectsBusyAndInvalidCachesWithoutMutation(t *testing.T) {
	for _, damage := range []string{"busy", "foreign tty", "unknown field", "trailing data", "missing lock", "missing font", "symlink directory", "public directory"} {
		t.Run(damage, func(t *testing.T) {
			if damage == "public directory" && runtime.GOOS == "windows" {
				t.Skip("Unix permissions")
			}
			g := initialized(t)
			require.NoError(t, g.BindTTY(t.Context(), "/dev/ttys001"))
			dir := g.directory
			revision, err := g.Initialize(t.Context())
			require.NoError(t, err)
			font, err := g.FontForTypography(t.Context(), revision, typographySource(t))
			require.NoError(t, err)
			require.NoError(t, g.Commit(t.Context(), revision))
			if damage != "busy" {
				require.NoError(t, g.Close())
			} else {
				defer g.Close()
			}
			tty := "/dev/ttys001"
			switch damage {
			case "foreign tty":
				tty = "/dev/ttys002"
			case "unknown field":
				require.NoError(t, os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"extra":true}`), 0600))
			case "trailing data":
				data, err := os.ReadFile(filepath.Join(dir, "state.json"))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "state.json"), append(data, []byte(` {}`)...), 0600))
			case "missing lock":
				require.NoError(t, os.Remove(filepath.Join(dir, ".lock")))
			case "missing font":
				require.NoError(t, os.Remove(font.FontPath))
			case "symlink directory":
				old := filepath.Join(dir, "images")
				moved := filepath.Join(dir, "moved-images")
				require.NoError(t, os.Rename(old, moved))
				require.NoError(t, os.Symlink(moved, old))
			case "public directory":
				require.NoError(t, os.Chmod(filepath.Join(dir, "fonts"), 0755))
			}
			before := inspectionFiles(t, dir)
			_, err = Inspect(t.Context(), dir, tty, font.PostScript)
			require.Error(t, err)
			if damage == "busy" {
				require.ErrorIs(t, err, ErrBusy)
			}
			require.Equal(t, before, inspectionFiles(t, dir))
		})
	}
}

func TestInspectCanceledDoesNotOpenCache(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Inspect(ctx, filepath.Join(t.TempDir(), "absent"), "/dev/ttys001", "")
	require.ErrorIs(t, err, context.Canceled)
}

func TestInspectMissingIdentityDoesNotHideRetainedArtifacts(t *testing.T) {
	for _, artifact := range []string{"fonts/retained.ttf", "images/retained.png", ".pending/retained.png", ".pending.json"} {
		t.Run(artifact, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "gallery")
			path := filepath.Join(directory, artifact)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			require.NoError(t, os.WriteFile(path, []byte("retained"), 0600))
			before := inspectionFiles(t, directory)
			_, err := Inspect(t.Context(), directory, "/dev/ttys001", "")
			require.ErrorContains(t, err, "metadata is missing")
			require.Equal(t, before, inspectionFiles(t, directory))
		})
	}
}
