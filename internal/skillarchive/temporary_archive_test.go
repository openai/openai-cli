package skillarchive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func prepareCleanupFixture(t *testing.T) (*Archive, string) {
	t.Helper()
	base := t.TempDir()
	scratch := filepath.Join(base, "scratch")
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", scratch)
	t.Setenv("TMP", scratch)
	t.Setenv("TEMP", scratch)
	directory := filepath.Join(base, "skill")
	writeFixture(t, directory, "SKILL.md", []byte("synthetic"), 0644)
	archive, err := Prepare(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = archive.Close() })
	return archive, scratch
}

func TestArchiveCleanupAnchorsTemporaryParent(t *testing.T) {
	archive, scratch := prepareCleanupFixture(t)
	moved := scratch + "-moved"
	if err := os.Rename(scratch, moved); err != nil {
		t.Fatal(err)
	}
	// Recreate the complete old pathname with an unrelated archive file.
	writeFixture(t, scratch, filepath.Join(archive.staging.name, temporaryArchiveName), []byte("foreign archive"), 0600)
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(archive.path); err != nil || string(data) != "foreign archive" {
		t.Fatalf("parent replacement changed: %q, %v", data, err)
	}
	entries, err := os.ReadDir(moved)
	if err != nil || len(entries) != 0 {
		t.Fatalf("original temporary parent retained owned entries: %v, %v", entries, err)
	}
}

func TestArchiveCleanupPreservesDirectoryReplacement(t *testing.T) {
	archive, _ := prepareCleanupFixture(t)
	originalDirectory := filepath.Dir(archive.path)
	moved := originalDirectory + "-moved"
	if err := os.Rename(originalDirectory, moved); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, originalDirectory, temporaryArchiveName, []byte("foreign archive"), 0600)
	err := archive.Close()
	if err == nil || !strings.Contains(err.Error(), "directory changed before cleanup") {
		t.Fatalf("directory replacement was not reported: %v", err)
	}
	if data, err := os.ReadFile(archive.path); err != nil || string(data) != "foreign archive" {
		t.Fatalf("replacement directory changed: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(moved, temporaryArchiveName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("anchored archive leaf survived: %v", err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("unknown relocated directory was removed: %v", err)
	}
}

func TestArchiveCleanupPreservesEmptyDirectoryReplacement(t *testing.T) {
	archive, _ := prepareCleanupFixture(t)
	originalDirectory := filepath.Dir(archive.path)
	if err := os.Rename(originalDirectory, originalDirectory+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(originalDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err == nil {
		t.Fatal("empty replacement directory was not reported")
	}
	if info, err := os.Stat(originalDirectory); err != nil || !info.IsDir() {
		t.Fatalf("empty replacement directory was removed: %v, %v", info, err)
	}
}

func TestArchiveCleanupPreservesLeafDirectoryReplacement(t *testing.T) {
	archive, _ := prepareCleanupFixture(t)
	if err := os.Rename(archive.path, archive.path+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(archive.path, 0700); err != nil {
		t.Fatal(err)
	}
	err := archive.Close()
	if err == nil || !strings.Contains(err.Error(), "archive changed before cleanup") {
		t.Fatalf("leaf directory replacement was not reported: %v", err)
	}
	if info, err := os.Stat(archive.path); err != nil || !info.IsDir() {
		t.Fatalf("replacement leaf directory was removed: %v, %v", info, err)
	}
}

func TestArchiveCleanupPreservesDirectorySymlinkReplacement(t *testing.T) {
	archive, scratch := prepareCleanupFixture(t)
	originalDirectory := filepath.Dir(archive.path)
	moved := originalDirectory + "-moved"
	foreign := filepath.Join(scratch, "foreign")
	writeFixture(t, foreign, temporaryArchiveName, []byte("foreign archive"), 0600)
	if err := os.Rename(originalDirectory, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, originalDirectory); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := archive.Close(); err == nil {
		t.Fatal("directory symlink replacement was not reported")
	}
	if info, err := os.Lstat(originalDirectory); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("replacement symlink was removed: %v, %v", info, err)
	}
	if data, err := os.ReadFile(filepath.Join(foreign, temporaryArchiveName)); err != nil || string(data) != "foreign archive" {
		t.Fatalf("symlink target changed: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(moved, temporaryArchiveName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("anchored archive leaf survived: %v", err)
	}
}

func TestArchiveCleanupPreservesUnexpectedEntry(t *testing.T) {
	archive, _ := prepareCleanupFixture(t)
	foreign := filepath.Join(filepath.Dir(archive.path), "foreign")
	if err := os.WriteFile(foreign, []byte("foreign content"), 0600); err != nil {
		t.Fatal(err)
	}
	err := archive.Close()
	if err == nil || !strings.Contains(err.Error(), "cannot remove temporary skill directory") {
		t.Fatalf("unexpected entry was not reported: %v", err)
	}
	if data, err := os.ReadFile(foreign); err != nil || string(data) != "foreign content" {
		t.Fatalf("unexpected entry changed: %q, %v", data, err)
	}
	if _, err := os.Stat(archive.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned archive retained: %v", err)
	}
}

func TestPrepareRejectsTemporaryDirectoryNestedInsideInput(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "SKILL.md", []byte("synthetic"), 0644)
	scratch := filepath.Join(directory, "scratch")
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", scratch)
	t.Setenv("TMP", scratch)
	t.Setenv("TEMP", scratch)
	archive, err := Prepare(context.Background(), directory)
	if archive != nil {
		archive.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "temporary directory must be outside") {
		t.Fatalf("nested temporary input conflict result: %v", err)
	}
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Fatalf("private temporary directory survived: %v, %v", entries, err)
	}
}
