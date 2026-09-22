package imagegallery

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func cleanupFixture(t *testing.T, bind bool) (string, string, State) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0700))
	directory := filepath.Join(root, strings.Repeat("a", 32))
	gallery, err := Open(t.Context(), directory)
	require.NoError(t, err)
	initial, err := gallery.Initialize(t.Context())
	require.NoError(t, err)
	require.NoError(t, gallery.Commit(t.Context(), initial))
	revision, err := gallery.PrepareImage(t.Context(), image.NewRGBA(image.Rect(0, 0, 2, 2)), 1)
	require.NoError(t, err)
	require.NoError(t, gallery.Commit(t.Context(), revision))
	if bind {
		require.NoError(t, gallery.BindTTY(t.Context(), "/dev/ttys001"))
	}
	state := gallery.State()
	require.NoError(t, gallery.Close())
	return root, directory, state
}

func TestCleanupClosedReclaimsAllArtifactsAfterUnregistering(t *testing.T) {
	root, directory, _ := cleanupFixture(t, true)
	fonts, err := os.ReadDir(filepath.Join(directory, "fonts"))
	require.NoError(t, err)
	var unregistered []string
	inventory := func(ctx context.Context) ([]string, []string, error) {
		// Collection occurs under the same lock used by renderers.
		_, err := Open(ctx, directory)
		require.ErrorIs(t, err, ErrBusy)
		return []string{"/dev/ttys002"}, []string{"GoMono"}, nil
	}
	unregister := func(_ context.Context, path string) error {
		require.FileExists(t, path)
		require.FileExists(t, filepath.Join(directory, "state.json"))
		unregistered = append(unregistered, filepath.Base(path))
		return nil
	}
	require.NoError(t, CleanupClosed(t.Context(), root, TerminalSession{}, inventory, unregister))
	require.Len(t, unregistered, len(fonts))
	remaining, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	require.Equal(t, ".lock", remaining[0].Name())
	require.NoError(t, CleanupClosed(t.Context(), root, TerminalSession{}, func(context.Context) ([]string, []string, error) {
		t.Fatal("tombstones must not invoke the native inventory")
		return nil, nil, nil
	}, unregister))
}

func TestCleanupPreservesLiveAndUnownedGalleries(t *testing.T) {
	for _, reason := range []string{"owner", "font used by another tab", "unowned", "busy"} {
		t.Run(reason, func(t *testing.T) {
			root, directory, state := cleanupFixture(t, reason != "unowned")
			if reason == "busy" {
				gallery, err := Open(t.Context(), directory)
				require.NoError(t, err)
				defer gallery.Close()
			}
			require.NoError(t, CleanupClosed(t.Context(), root, TerminalSession{}, func(context.Context) ([]string, []string, error) {
				if reason == "unowned" || reason == "busy" {
					t.Fatal("unsafe gallery must be skipped before native calls")
				}
				if reason == "owner" {
					return []string{"/dev/ttys001"}, []string{"GoMono"}, nil
				}
				return []string{"/dev/ttys002"}, []string{state.PostScript}, nil
			}, func(context.Context, string) error {
				t.Fatal("must retain registered fonts")
				return nil
			}))
			require.FileExists(t, state.FontPath)
			require.DirExists(t, filepath.Join(directory, "images"))
		})
	}
}

func TestCleanupDistinguishesReusedTTYFromCurrentSession(t *testing.T) {
	for _, mode := range []string{"reused tty", "old font still selected", "current session"} {
		t.Run(mode, func(t *testing.T) {
			root, directory, state := cleanupFixture(t, true)
			current := TerminalSession{Directory: filepath.Join(root, strings.Repeat("b", 32)), TTY: "/dev/ttys001"}
			if mode == "current session" {
				current.Directory = directory
			}
			unregistered := 0
			require.NoError(t, CleanupClosed(t.Context(), root, current, func(context.Context) ([]string, []string, error) {
				if mode == "current session" {
					t.Fatal("current session must be retained before native calls")
				}
				font := "GoMono"
				if mode == "old font still selected" {
					font = state.PostScript
				}
				return []string{current.TTY}, []string{font}, nil
			}, func(context.Context, string) error { unregistered++; return nil }))
			if mode == "reused tty" {
				require.Positive(t, unregistered)
				require.NoFileExists(t, state.FontPath)
			} else {
				require.Zero(t, unregistered)
				require.FileExists(t, state.FontPath)
			}
		})
	}
}

func TestCleanupFailuresRetainFilesForRetry(t *testing.T) {
	for _, reason := range []string{"inventory", "invalid tty", "invalid font", "unregister", "cancel"} {
		t.Run(reason, func(t *testing.T) {
			root, directory, state := cleanupFixture(t, true)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			syntheticErr := errors.New("synthetic failure")
			err := CleanupClosed(ctx, root, TerminalSession{}, func(context.Context) ([]string, []string, error) {
				switch reason {
				case "inventory":
					return nil, nil, syntheticErr
				case "invalid tty":
					return []string{"/dev/ttys002", "invalid"}, []string{"GoMono"}, nil
				case "invalid font":
					return []string{"/dev/ttys002"}, []string{"GoMono", "\x1b"}, nil
				default:
					return nil, nil, nil
				}
			}, func(context.Context, string) error {
				if reason == "cancel" {
					cancel()
					return nil
				}
				return syntheticErr
			})
			require.Error(t, err)
			require.FileExists(t, state.FontPath)
			require.FileExists(t, filepath.Join(directory, "state.json"))
			require.NoError(t, CleanupClosed(t.Context(), root, TerminalSession{}, func(context.Context) ([]string, []string, error) {
				return nil, nil, nil
			}, func(context.Context, string) error { return nil }))
			require.NoFileExists(t, state.FontPath)
		})
	}
}

func TestCleanupRejectsSymlinks(t *testing.T) {
	root, directory, state := cleanupFixture(t, true)
	external := filepath.Join(t.TempDir(), "external.ttf")
	require.NoError(t, os.WriteFile(external, []byte("synthetic unrelated font"), 0600))
	link := filepath.Join(directory, "fonts", "revision-"+strings.Repeat("b", 32)+".ttf")
	require.NoError(t, os.Symlink(external, link))
	require.NoError(t, CleanupClosed(t.Context(), root, TerminalSession{}, func(context.Context) ([]string, []string, error) {
		return nil, nil, nil
	}, func(context.Context, string) error {
		t.Fatal("symlinked gallery must retain all registrations")
		return nil
	}))
	require.FileExists(t, external)
	require.FileExists(t, state.FontPath)
	rootLink := filepath.Join(t.TempDir(), "root-link")
	require.NoError(t, os.Symlink(root, rootLink))
	require.Error(t, CleanupClosed(t.Context(), rootLink, TerminalSession{}, nil, nil))
}

func TestBindTTYRejectsReassignment(t *testing.T) {
	_, directory, _ := cleanupFixture(t, true)
	gallery, err := Open(t.Context(), directory)
	require.NoError(t, err)
	defer gallery.Close()
	require.NoError(t, gallery.BindTTY(t.Context(), "/dev/ttys001"))
	require.Error(t, gallery.BindTTY(t.Context(), "/dev/ttys002"))
	require.Error(t, gallery.BindTTY(t.Context(), "invalid"))
}

func TestCleanupSkipsUninitializedOwner(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	gallery, err := Open(t.Context(), filepath.Join(root, strings.Repeat("a", 32)))
	require.NoError(t, err)
	require.NoError(t, gallery.BindTTY(t.Context(), "/dev/ttys001"))
	require.NoError(t, gallery.Close())
	require.NoError(t, CleanupClosed(t.Context(), root, TerminalSession{}, func(context.Context) ([]string, []string, error) {
		t.Fatal("uninitialized ownership is not enough to unregister fonts")
		return nil, nil, nil
	}, nil))
}

func TestCleanupUnregistersMissingRepairedFontURLs(t *testing.T) {
	root, directory, before := cleanupFixture(t, true)
	require.NoError(t, os.Remove(before.FontPath))
	gallery, err := OpenForRepair(t.Context(), directory)
	require.NoError(t, err)
	repaired, err := gallery.Repair(t.Context())
	require.NoError(t, err)
	require.NoError(t, gallery.Commit(t.Context(), repaired))
	require.NoError(t, gallery.Close())
	var unregistered []string
	require.NoError(t, CleanupClosed(t.Context(), root, TerminalSession{}, func(context.Context) ([]string, []string, error) {
		return nil, nil, nil
	}, func(_ context.Context, path string) error {
		unregistered = append(unregistered, path)
		return nil
	}))
	require.Contains(t, unregistered, before.FontPath)
	require.Contains(t, unregistered, repaired.FontPath)
	require.NoFileExists(t, filepath.Join(directory, "state.json"))
}

func TestCleanupPreservesUnrelatedImageFiles(t *testing.T) {
	root, directory, state := cleanupFixture(t, true)
	unrelated := filepath.Join(directory, "images", "notes.txt")
	require.NoError(t, os.WriteFile(unrelated, []byte("keep"), 0600))
	require.NoError(t, CleanupClosed(t.Context(), root, TerminalSession{}, func(context.Context) ([]string, []string, error) {
		return nil, nil, nil
	}, func(context.Context, string) error {
		t.Fatal("unrelated files must prevent automatic cleanup")
		return nil
	}))
	require.FileExists(t, unrelated)
	require.FileExists(t, state.FontPath)
}
