//go:build unix

package custom

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStoredCompletionExportSaveResolvesParentBeforeDotDot(t *testing.T) {
	directory := t.TempDir()
	actual := filepath.Join(directory, "actual")
	if err := os.MkdirAll(filepath.Join(actual, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("actual", "nested"), filepath.Join(directory, "link")); err != nil {
		t.Fatal(err)
	}
	// filepath.Join would erase the path semantics that this caller supplies.
	path := directory + "/link/../export.jsonl"
	const payload = "{\"id\":\"chatcmpl_demo\"}\n"
	if err := saveStoredCompletionExport(t.Context(), path, func(out io.Writer) error {
		_, err := io.WriteString(out, payload)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(actual, "export.jsonl"))
	if err != nil || string(data) != payload {
		t.Fatalf("resolved destination: data=%q, error=%v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(directory, "export.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("export used the lexically cleaned path: %v", err)
	}
	assertStoredExportStageCleanup(t, actual)
}

func TestStoredCompletionExportSaveRejectsExistingFIFO(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "export.jsonl")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		finished <- saveStoredCompletionExport(t.Context(), path, func(io.Writer) error {
			return errors.New("export unexpectedly started for an existing FIFO")
		})
	}()
	select {
	case err = <-finished:
		if !errors.Is(err, os.ErrExist) {
			t.Fatalf("existing FIFO rejection: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("export blocked on an existing FIFO without a reader")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("export changed the existing FIFO: %v", err)
	}
	assertStoredExportStageCleanup(t, directory)
}

func TestStoredCompletionExportSavePreservesConcurrentSpecialDestination(t *testing.T) {
	for _, kind := range []string{"symlink", "FIFO", "directory"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "export.jsonl")
			target := filepath.Join(directory, "target.jsonl")
			const original = "unrelated target\n"
			if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			var replacement os.FileInfo
			err := saveStoredCompletionExport(t.Context(), path, func(out io.Writer) error {
				if _, err := io.WriteString(out, "{\"id\":\"chatcmpl_demo\"}\n"); err != nil {
					return err
				}
				var err error
				switch kind {
				case "symlink":
					err = os.Symlink(target, path)
				case "FIFO":
					err = syscall.Mkfifo(path, 0o600)
				case "directory":
					err = os.Mkdir(path, 0o700)
				}
				if err == nil {
					replacement, err = os.Lstat(path)
				}
				return err
			})
			if !errors.Is(err, os.ErrExist) || replacement == nil {
				t.Fatalf("concurrent destination rejection: %v", err)
			}
			current, err := os.Lstat(path)
			if err != nil || !os.SameFile(replacement, current) {
				t.Fatalf("export changed the concurrent destination: %v", err)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != original {
				t.Fatalf("export changed the symlink target: data=%q, error=%v", data, err)
			}
			assertStoredExportStageCleanup(t, directory)
		})
	}
}

func TestStoredCompletionExportSavePreservesReplacedStageFIFO(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "export.jsonl")
	moved := filepath.Join(directory, "moved-owned-stage")
	const payload = "{\"id\":\"chatcmpl_demo\"}\n"
	var stagePath string
	var replacement os.FileInfo
	finished := make(chan error, 1)
	go func() {
		finished <- saveStoredCompletionExport(t.Context(), path, func(out io.Writer) error {
			if _, err := io.WriteString(out, payload); err != nil {
				return err
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".openai-download-") {
					stagePath = filepath.Join(directory, entry.Name())
					break
				}
			}
			if stagePath == "" {
				return errors.New("export did not create a staging file")
			}
			if err := os.Rename(stagePath, moved); err != nil {
				return err
			}
			if err := syscall.Mkfifo(stagePath, 0o600); err != nil {
				return err
			}
			replacement, err = os.Lstat(stagePath)
			return err
		})
	}()
	select {
	case err := <-finished:
		if !errors.Is(err, errDownloadDestinationChanged) || replacement == nil {
			t.Fatalf("replaced staging FIFO rejection: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("export blocked on a replaced staging FIFO without a reader")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("export published a destination from the replaced stage: %v", err)
	}
	current, err := os.Lstat(stagePath)
	if err != nil || !os.SameFile(replacement, current) || current.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("export removed or changed the replacement FIFO: %v", err)
	}
	data, err := os.ReadFile(moved)
	if err != nil || string(data) != payload {
		t.Fatalf("export changed the moved stage: data=%q, error=%v", data, err)
	}
}
