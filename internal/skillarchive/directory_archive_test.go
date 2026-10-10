package skillarchive

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func writeFixture(t *testing.T, directory, name string, data []byte, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func readArchive(t *testing.T, archive *Archive) []byte {
	t.Helper()
	data, err := io.ReadAll(archive)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != archive.Size() {
		t.Fatalf("size = %d; read %d bytes", archive.Size(), len(data))
	}
	return data
}

func TestPrepareLayoutBytesMetadataAndCleanup(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "example skill")
	fixtures := map[string][]byte{
		"SKILL.md": []byte("synthetic instructions\n"), ".hidden": []byte("included"),
		"a.txt": []byte("lexical sibling"), "a/empty": {},
		"scripts/run.sh": []byte("#!/bin/sh\nexit 0\n"),
		"data/raw.bin":   {0, 0xff, 0x80, '\r', '\n', 0},
	}
	for name, data := range fixtures {
		mode := os.FileMode(0600)
		if name == "scripts/run.sh" {
			mode = 0751
		}
		writeFixture(t, directory, name, data, mode)
	}
	if err := os.Mkdir(filepath.Join(directory, "empty-directory"), 0700); err != nil {
		t.Fatal(err)
	}
	archive, err := Prepare(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = archive.Close() })
	if archive.Filename() != "example skill.zip" {
		t.Fatalf("filename = %q", archive.Filename())
	}
	info, err := os.Stat(archive.path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("temporary mode = %v", info.Mode())
	}
	stagingDirectory := filepath.Dir(archive.path)
	info, err = os.Stat(stagingDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatalf("temporary directory mode = %v", info.Mode())
	}
	data := readArchive(t, archive)
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range reader.File {
		names = append(names, entry.Name)
		if !strings.HasPrefix(entry.Name, "example skill/") {
			t.Fatalf("unexpected archive root: %q", entry.Name)
		}
		if !entry.Modified.Equal(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("timestamp %s = %v", entry.Name, entry.Modified)
		}
		if entry.FileInfo().IsDir() {
			if entry.Mode().Perm() != 0755 {
				t.Errorf("directory mode = %v", entry.Mode())
			}
			continue
		}
		name := strings.TrimPrefix(entry.Name, "example skill/")
		want, ok := fixtures[name]
		if !ok {
			t.Fatalf("unexpected file %q", entry.Name)
		}
		stream, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("bytes for %q differ: read=%v close=%v", name, readErr, closeErr)
		}
		mode := os.FileMode(0644)
		if name == "scripts/run.sh" && runtime.GOOS != "windows" {
			mode = 0755
		}
		if entry.Mode().Perm() != mode {
			t.Errorf("mode for %q = %v; want %v", name, entry.Mode(), mode)
		}
		delete(fixtures, name)
	}
	if len(fixtures) != 0 || !slices.IsSorted(names) {
		t.Fatalf("missing files = %v; ordered names = %v", fixtures, names)
	}
	for range 2 {
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(archive.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive retained after close: %v", err)
	}
	if _, err := os.Stat(stagingDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary directory retained after close: %v", err)
	}
}

func TestPrepareReproducibleAndRewindable(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "skill")
	writeFixture(t, directory, "SKILL.md", []byte("same bytes"), 0644)
	prepare := func() []byte {
		archive, err := Prepare(context.Background(), directory)
		if err != nil {
			t.Fatal(err)
		}
		defer archive.Close()
		data := readArchive(t, archive)
		if _, err := archive.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		if repeated := readArchive(t, archive); !bytes.Equal(data, repeated) {
			t.Fatal("rewound bytes differ")
		}
		return data
	}
	first := prepare()
	modified := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(filepath.Join(directory, "SKILL.md"), modified, modified); err != nil {
		t.Fatal(err)
	}
	if second := prepare(); !bytes.Equal(first, second) {
		t.Fatal("source timestamp changed archive bytes")
	}
}

func TestPrepareErrorsAndCleanup(t *testing.T) {
	for _, kind := range []string{"missing", "file", "empty", "empty-tree", "symlink", "directory-symlink"} {
		t.Run(kind, func(t *testing.T) {
			scratch := t.TempDir()
			t.Setenv("TMPDIR", scratch)
			t.Setenv("TMP", scratch)
			directory := filepath.Join(t.TempDir(), "skill")
			if kind != "missing" && kind != "file" {
				if err := os.Mkdir(directory, 0755); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "file":
				writeFixture(t, filepath.Dir(directory), "skill", []byte("not a directory"), 0644)
			case "empty-tree":
				if err := os.Mkdir(filepath.Join(directory, "nested"), 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink", "directory-symlink":
				writeFixture(t, directory, "SKILL.md", []byte("synthetic"), 0644)
				link, target := filepath.Join(directory, "link"), "SKILL.md"
				if kind == "directory-symlink" {
					link, target = directory+"-link", directory
				}
				if err := os.Symlink(target, link); err != nil {
					t.Skipf("symlink creation unavailable: %v", err)
				}
				if kind == "directory-symlink" {
					directory = link
				}
			}
			archive, err := Prepare(context.Background(), directory)
			if archive != nil || err == nil {
				if archive != nil {
					archive.Close()
				}
				t.Fatalf("archive=%v error=%v", archive, err)
			}
			var detail *Error
			if !errors.As(err, &detail) || detail.Reason == "" || strings.Contains(err.Error(), filepath.Dir(directory)) {
				t.Fatalf("unsafe or unstructured error: %v", err)
			}
			if kind == "missing" && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing original cause: %v", err)
			}
			entries, readErr := os.ReadDir(scratch)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("scratch retained: %v; read=%v", entries, readErr)
			}
		})
	}
}

type observedContext struct {
	context.Context
	onCheck func()
}

func (c observedContext) Err() error {
	c.onCheck()
	return c.Context.Err()
}

func TestPrepareCancellationDuringCopyRemovesArchive(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	t.Setenv("TMP", scratch)
	directory := filepath.Join(t.TempDir(), "skill")
	data := make([]byte, 1024*1024)
	stream := rand.NewChaCha8([32]byte{1})
	if _, err := stream.Read(data); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, directory, "data.bin", data, 0644)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checks := 0
	observed := observedContext{Context: ctx, onCheck: func() {
		checks++
		entries, err := os.ReadDir(scratch)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			info, err := os.Stat(filepath.Join(scratch, entry.Name(), temporaryArchiveName))
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() > 32*1024 {
				cancel()
			}
		}
	}}
	archive, err := Prepare(observed, directory)
	if archive != nil || !errors.Is(err, context.Canceled) {
		if archive != nil {
			archive.Close()
		}
		t.Fatalf("archive=%v error=%v checks=%d", archive, err, checks)
	}
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Fatalf("scratch retained after cancellation: %v; error=%v", entries, err)
	}
}

type writingFunc func([]byte) (int, error)

func (f writingFunc) Write(p []byte) (int, error) { return f(p) }

func TestCopyRejectsChangedFiles(t *testing.T) {
	for _, change := range []string{"shrink", "grow", "rewrite", "replace"} {
		t.Run(change, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "data")
			original := bytes.Repeat([]byte("x"), 96*1024)
			writeFixture(t, directory, "data", original, 0644)
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			before, err := file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			changed := false
			writer := writingFunc(func(p []byte) (int, error) {
				if changed {
					return len(p), nil
				}
				changed = true
				switch change {
				case "shrink":
					err = os.Truncate(path, 1)
				case "grow":
					err = os.Truncate(path, int64(len(original)+1))
				case "rewrite":
					err = os.WriteFile(path, bytes.Repeat([]byte("y"), len(original)), 0644)
					if err == nil {
						stamp := before.ModTime().Add(time.Second)
						err = os.Chtimes(path, stamp, stamp)
					}
				case "replace":
					err = os.Rename(path, path+"-old")
					if err == nil {
						err = os.WriteFile(path, original, 0644)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				return len(p), nil
			})
			pack := packer{ctx: context.Background(), buffer: make([]byte, 32*1024)}
			err = pack.copyFile(writer, file, before, "data")
			if change == "replace" {
				parent, openErr := os.Open(directory)
				if openErr != nil {
					t.Fatal(openErr)
				}
				defer parent.Close()
				err = checkNamedIdentity(parent, "data", before, "data")
			}
			var detail *Error
			if !errors.As(err, &detail) || detail.RelativePath != "data" || !strings.Contains(detail.Reason, "changed") {
				t.Fatalf("changed file accepted: %v", err)
			}
		})
	}
}

func TestArchiveClosePreservesReplacement(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	t.Setenv("TMP", scratch)
	t.Setenv("TEMP", scratch)
	directory := filepath.Join(t.TempDir(), "skill")
	writeFixture(t, directory, "SKILL.md", []byte("synthetic"), 0644)
	archive, err := Prepare(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(archive.path, archive.path+"-owned"); err != nil {
		archive.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(archive.path + "-owned") })
	if err := os.WriteFile(archive.path, []byte("replacement"), 0600); err != nil {
		archive.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(archive.path) })
	if err := archive.Close(); err == nil {
		t.Fatal("replaced temporary path accepted")
	}
	if data, err := os.ReadFile(archive.path); err != nil || string(data) != "replacement" {
		t.Fatalf("replacement removed or changed: %q, %v", data, err)
	}
}

func TestPrepareAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	archive, err := Prepare(ctx, filepath.Join(t.TempDir(), "missing"))
	if archive != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled input result: %v, %v", archive, err)
	}
}

func TestPrepareRejectsTemporaryDirectoryInsideInput(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("TMPDIR", directory)
	t.Setenv("TMP", directory)
	writeFixture(t, directory, "SKILL.md", []byte("synthetic"), 0644)
	archive, err := Prepare(context.Background(), directory)
	if archive != nil {
		archive.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "temporary directory must be outside") {
		t.Fatalf("temporary input conflict result: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "SKILL.md" {
		t.Fatalf("archive cleanup changed input: %v, %v", entries, err)
	}
}

func TestCopyPreservesWriteFailure(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "data", []byte("synthetic"), 0644)
	file, err := os.Open(filepath.Join(directory, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("synthetic disk failure")
	pack := packer{ctx: context.Background(), buffer: make([]byte, 32*1024)}
	err = pack.copyFile(writingFunc(func([]byte) (int, error) { return 0, want }), file, before, "data")
	if !errors.Is(err, want) {
		t.Fatalf("lost write failure: %v", err)
	}
}
