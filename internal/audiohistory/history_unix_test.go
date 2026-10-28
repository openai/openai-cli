//go:build unix

package audiohistory

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestOpenRejectsFIFOWithoutWaitingForWriter(t *testing.T) {
	for _, name := range []string{"audio", "manifest.json"} {
		t.Run(name, func(t *testing.T) {
			store := Store{Directory: filepath.Join(t.TempDir(), "history")}
			entry := testCapture(t, store, "synthetic audio")
			path := filepath.Join(store.Directory, entry.ID, name)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := store.Open(t.Context(), entry.ID); done <- err }()
			select {
			case err := <-done:
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("FIFO was accepted: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("history open waited for a FIFO writer")
			}
		})
	}
}

func TestPrivateHistoryRejectsExposedFilesAndRoots(t *testing.T) {
	for _, target := range []string{".", "entry", "audio", "manifest.json"} {
		t.Run(target, func(t *testing.T) {
			store := Store{Directory: filepath.Join(t.TempDir(), "history")}
			entry := testCapture(t, store, "synthetic audio")
			path := store.Directory
			if target == "entry" {
				path = filepath.Join(path, entry.ID)
			} else if target != "." {
				path = filepath.Join(path, entry.ID, target)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, info.Mode().Perm()|0040); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Open(t.Context(), entry.ID); !errors.Is(err, ErrInvalid) {
				t.Fatalf("exposed private history accepted: %v", err)
			}
		})
	}
}
