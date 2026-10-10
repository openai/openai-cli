//go:build unix

package skillarchive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPrepareRejectsFIFO(t *testing.T) {
	directory := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(directory, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	archive, err := Prepare(context.Background(), directory)
	if archive != nil {
		archive.Close()
	}
	var detail *Error
	if !errors.As(err, &detail) || detail.RelativePath != "pipe" || !strings.Contains(detail.Reason, "special") {
		t.Fatalf("FIFO accepted: %v", err)
	}
}

func TestOpenedIdentityRejectsFIFOAndSymlinkReplacement(t *testing.T) {
	for _, replacement := range []string{"fifo", "symlink", "directory"} {
		t.Run(replacement, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "data", []byte("original"), 0644)
			before, err := os.Lstat(filepath.Join(directory, "data"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(directory, "data"), filepath.Join(directory, "original")); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "data")
			switch replacement {
			case "fifo":
				err = unix.Mkfifo(path, 0600)
			case "symlink":
				err = os.Symlink("original", path)
			case "directory":
				err = os.Mkdir(path, 0755)
			}
			if err != nil {
				t.Fatal(err)
			}
			parent, err := os.Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			result := make(chan error, 1)
			go func() { result <- checkNamedIdentity(parent, "data", before, "data") }()
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("replaced file accepted")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("opening replacement blocked")
			}
		})
	}
}

func TestPrepareUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file read permissions")
	}
	directory := t.TempDir()
	writeFixture(t, directory, "private", []byte("synthetic"), 0000)
	archive, err := Prepare(context.Background(), directory)
	if archive != nil {
		archive.Close()
	}
	var detail *Error
	if !errors.Is(err, os.ErrPermission) || !errors.As(err, &detail) || detail.RelativePath != "private" {
		t.Fatalf("unreadable input result: %v", err)
	}
}

func TestPrepareRejectsBackslashMember(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, `..\outside`, []byte("synthetic"), 0644)
	archive, err := Prepare(context.Background(), directory)
	if archive != nil {
		archive.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "backslashes") {
		t.Fatalf("ambiguous ZIP path accepted: %v", err)
	}
}
