package audiohistory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestClearCancellationRetainsRetryMetadata(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "history")}
	entry := testCapture(t, store, "synthetic interrupted audio")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := store.clear(ctx, func(root *os.Root, name string) error {
		if name == "audio" {
			cancel()
			return ctx.Err()
		}
		return root.Remove(name)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("clear lost cancellation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Directory, clearPrefix+entry.ID)); err != nil {
		t.Fatalf("interrupted clear lost retry metadata: %v", err)
	}
	if err := store.Clear(t.Context()); err != nil {
		t.Fatalf("clear did not resume after cancellation: %v", err)
	}
	children, err := os.ReadDir(store.Directory)
	if err != nil || len(children) != 0 {
		t.Fatalf("canceled clear left artifacts after retry: children=%d err=%v", len(children), err)
	}
}

func TestClearRetriesEveryRemovalFailure(t *testing.T) {
	for _, boundary := range []string{"manifest", "audio", "directory", "marker"} {
		t.Run(boundary, func(t *testing.T) {
			store := Store{Directory: filepath.Join(t.TempDir(), "history")}
			entry := testCapture(t, store, "synthetic retained audio")
			target := map[string]string{"manifest": "manifest.json", "audio": "audio",
				"directory": entry.ID, "marker": clearPrefix + entry.ID}[boundary]
			failure := errors.New("synthetic removal failure")
			injected := 0
			err := store.clear(t.Context(), func(root *os.Root, name string) error {
				if name == target {
					injected++
					return failure
				}
				return root.Remove(name)
			})
			if !errors.Is(err, failure) || injected == 0 {
				t.Fatalf("removal failure was not preserved: attempts=%d err=%v", injected, err)
			}
			marker := filepath.Join(store.Directory, clearPrefix+entry.ID)
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("failed deletion lost its retry metadata: %v", err)
			}
			if boundary == "manifest" || boundary == "audio" {
				data, err := os.ReadFile(filepath.Join(store.Directory, entry.ID, "audio"))
				if err != nil || string(data) != "synthetic retained audio" {
					t.Fatalf("failed removal changed retained audio: %v", err)
				}
			}
			entries, err := store.List(t.Context())
			expected := 0
			if boundary == "manifest" {
				expected = 1
			}
			if err != nil || len(entries) != expected {
				t.Fatalf("partial deletion advertised missing audio: entries=%d err=%v", len(entries), err)
			}
			// Use a new Store and the public method: retry cannot depend on
			// in-memory ownership retained by the failed operation.
			if err := (Store{Directory: store.Directory}).Clear(t.Context()); err != nil {
				t.Fatalf("retry failed: %v", err)
			}
			children, err := os.ReadDir(store.Directory)
			if err != nil || len(children) != 0 {
				t.Fatalf("retry stranded private artifacts: children=%d err=%v", len(children), err)
			}
		})
	}
}

func TestClearRetryPreservesChangedFiles(t *testing.T) {
	for _, change := range []string{"audio", "marker", "unexpected child", "symlink"} {
		t.Run(change, func(t *testing.T) {
			if change == "symlink" && runtime.GOOS == "windows" {
				t.Skip("symlink creation requires separate Windows privileges")
			}
			store := Store{Directory: filepath.Join(t.TempDir(), "history")}
			entry := testCapture(t, store, "original audio")
			failure := errors.New("synthetic audio removal failure")
			err := store.clear(t.Context(), func(root *os.Root, name string) error {
				if name == "audio" {
					return failure
				}
				return root.Remove(name)
			})
			if !errors.Is(err, failure) {
				t.Fatal(err)
			}
			audio := filepath.Join(store.Directory, entry.ID, "audio")
			marker := filepath.Join(store.Directory, clearPrefix+entry.ID)
			protected := audio
			expected := "original audio"
			switch change {
			case "audio":
				expected = "replaced audio"
				if err := os.WriteFile(audio, []byte(expected), 0600); err != nil {
					t.Fatal(err)
				}
			case "marker":
				protected, expected = marker, `{"version":99}`
				if err := os.WriteFile(marker, []byte(expected), 0600); err != nil {
					t.Fatal(err)
				}
			case "unexpected child":
				protected, expected = filepath.Join(store.Directory, entry.ID, "unrelated"), "keep unrelated bytes"
				if err := os.WriteFile(protected, []byte(expected), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				protected, expected = filepath.Join(t.TempDir(), "outside"), "keep outside bytes"
				if err := os.WriteFile(protected, []byte(expected), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(audio); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(protected, audio); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Clear(t.Context()); err == nil {
				t.Fatal("retry accepted changed ownership or unexpected files")
			}
			data, err := os.ReadFile(protected)
			if err != nil || string(data) != expected {
				t.Fatalf("retry removed or changed unverified bytes: %v", err)
			}
			if _, err := os.Lstat(marker); err != nil {
				t.Fatalf("failed retry removed its marker: %v", err)
			}
		})
	}
}

func TestClearRejectsUnownedMarker(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "history")}
	entry := testCapture(t, store, "original audio")
	manifestPath := filepath.Join(store.Directory, entry.ID, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	// Matching names and JSON are insufficient while the original manifest
	// exists: the deletion marker must be the exact same owned file.
	marker := filepath.Join(store.Directory, clearPrefix+entry.ID)
	if err := os.WriteFile(marker, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(t.Context()); !errors.Is(err, ErrChanged) {
		t.Fatalf("clear accepted an unowned manifest copy: %v", err)
	}
	for _, path := range []string{manifestPath, marker, filepath.Join(store.Directory, entry.ID, "audio")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("clear removed an unverified artifact: %v", err)
		}
	}
}

func TestConcurrentClearFinishesRetainedDeletion(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "history")}
	testCapture(t, store, "synthetic concurrent audio")
	removing, resume := make(chan struct{}), make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- store.clear(t.Context(), func(root *os.Root, name string) error {
			if name == "audio" {
				close(removing)
				select {
				case <-resume:
				case <-t.Context().Done():
					return t.Context().Err()
				}
			}
			return root.Remove(name)
		})
	}()
	defer close(resume)
	select {
	case <-removing:
	case err := <-done:
		t.Fatalf("clear stopped before the audio boundary: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("clear did not reach the audio boundary")
	}
	if err := store.Clear(t.Context()); err != nil {
		t.Fatalf("concurrent clear did not resume the retained deletion: %v", err)
	}
	resume <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("original clear did not accept completed removal: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("original clear did not finish")
	}
	children, err := os.ReadDir(store.Directory)
	if err != nil || len(children) != 0 {
		t.Fatalf("concurrent clear left artifacts: children=%d err=%v", len(children), err)
	}
}
