package imageoutput

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveDirectory(t *testing.T) {
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	want := filepath.Join(home, "Downloads", "gpt-images")
	got, err := ResolveDirectory("")
	if err != nil || got != want {
		t.Fatalf("default directory = %q, %v; want %q", got, err, want)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("default directory was not created: %v", err)
	}
	for _, requested := range []string{"~/Downloads/gpt-images", got} {
		if resolved, err := ResolveDirectory(requested); err != nil || resolved != want {
			t.Fatalf("ResolveDirectory(%q) = %q, %v", requested, resolved, err)
		}
	}
	assertEmptyDirectory(t, want)
	if got, err := ResolveDirectory("~"); err != nil || got != home {
		t.Fatalf("home directory = %q, %v", got, err)
	}
	if got, err := ResolveDirectory("."); err != nil || !filepath.IsAbs(got) {
		t.Fatalf("relative directory did not resolve to an absolute path: %q, %v", got, err)
	}
	missing := filepath.Join(home, "missing")
	if _, err := ResolveDirectory(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing explicit directory: %v", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explicit missing directory was created: %v", err)
	}
	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDirectory(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("explicit file path error = %v", err)
	}
}

func TestRemoveProbeKeepsReplacementFile(t *testing.T) {
	directory := t.TempDir()
	probe, err := createImageFile(context.Background(), directory, "robot", ".jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(probe.Name(), filepath.Join(directory, "moved.jpeg")); err != nil {
		probe.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(probe.Name(), []byte("keep replacement"), 0600); err != nil {
		probe.Close()
		t.Fatal(err)
	}
	if err := removeProbe(probe); err == nil || !strings.Contains(err.Error(), "path changed") {
		t.Fatalf("replacement probe cleanup = %v", err)
	}
	contents, err := os.ReadFile(probe.Name())
	if err != nil || string(contents) != "keep replacement" {
		t.Fatalf("replacement probe file was removed or changed: %q, %v", contents, err)
	}
}

func TestResolveDirectoryRejectsReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix permission checks for a non-root user")
	}
	directory := t.TempDir()
	kept := filepath.Join(directory, "existing.png")
	if err := os.WriteFile(kept, []byte("keep existing file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(directory, 0700); err != nil {
			t.Error(err)
		}
	})
	if _, err := ResolveDirectory(directory); err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("read-only directory error = %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(kept) {
		t.Fatalf("write probe changed the directory: %v, %v", entries, err)
	}
	contents, err := os.ReadFile(kept)
	if err != nil || string(contents) != "keep existing file" {
		t.Fatalf("write probe changed the existing file: %q, %v", contents, err)
	}
}

func TestDirectoryErrorsEscapePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows rejects control characters in filenames")
	}
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing\n\x1b[2J")
	_, err := ResolveDirectory(missing)
	if !errors.Is(err, os.ErrNotExist) || strings.ContainsAny(err.Error(), "\n\x1b") || !strings.Contains(err.Error(), `\x1b`) {
		t.Fatalf("missing-directory error is not escaped or lost its cause: %q", err)
	}
	file := filepath.Join(directory, "file\n\x1b[2J")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDirectory(file); err == nil || strings.ContainsAny(err.Error(), "\n\x1b") || !strings.Contains(err.Error(), `\x1b`) {
		t.Fatalf("file-instead-of-folder error is not escaped: %q", err)
	}
}
