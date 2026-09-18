package imageoutput

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	for input, want := range map[string]string{
		"robot.png": "robot", "robot.JPEG": "robot", "robot.JpG": "robot", "robot.webp": "robot",
		"robot": "robot", "robot.v2": "robot.v2", "robot.png.jpeg": "robot.png", "my image.PNG": "my image",
	} {
		got, err := NormalizeName(input)
		if err != nil || got != want {
			t.Errorf("NormalizeName(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{".png", ".JPEG", "../robot.png", `..\robot.png`, "NUL.png", "robot\n.png", "robot.png ", "robot\x1b.png"} {
		if _, err := NormalizeName(input); err == nil {
			t.Errorf("invalid normalized name accepted: %q", input)
		}
	}
}

func TestSaveResponseNormalizesExactlyOneExtension(t *testing.T) {
	for input, want := range map[string]string{
		"robot.PNG": "robot.png", "robot.jpeg": "robot.png", "robot.png.jpeg": "robot.png.png",
	} {
		paths, err := SaveResponse(t.Context(), imageResponse(t, imageFixtures(t)[0]), t.TempDir(), input)
		if err != nil || len(paths) != 1 || filepath.Base(paths[0]) != want {
			t.Errorf("SaveResponse name %q = %v, %v; want %q", input, paths, err, want)
		}
	}
}

func TestSaveResponseSalvagesImagesAfterMalformedItems(t *testing.T) {
	directory := t.TempDir()
	fixtures := imageFixtures(t)
	pngData := base64.StdEncoding.EncodeToString(fixtures[0])
	jpegData := base64.StdEncoding.EncodeToString(fixtures[1])
	paths, err := SaveResponse(t.Context(), encodedResponse(t, pngData, pngData+"!", "", jpegData), directory, "robot.png")
	if err == nil || len(paths) != 2 || !strings.Contains(err.Error(), "saved 2 of 4 images") {
		t.Fatalf("partial result = %v, %v", paths, err)
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) || !strings.Contains(err.Error(), "image 3 has no base64") {
		t.Fatalf("partial result lost individual causes: %v", err)
	}
	for i, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(contents, fixtures[i]) {
			t.Errorf("completed file %d differs: %v", i, err)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 2 {
		t.Fatalf("incomplete files remain: %v, %v", entries, err)
	}
}

func TestSaveResponseSalvagesAroundMalformedItemShapes(t *testing.T) {
	fixture := imageFixtures(t)[0]
	valid := map[string]string{"b64_json": base64.StdEncoding.EncodeToString(fixture)}
	raw, err := json.Marshal(map[string]any{"data": []any{valid, map[string]any{"b64_json": true}, nil, "wrong shape", valid}})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	paths, err := SaveResponse(t.Context(), raw, directory)
	if err == nil || len(paths) != 2 || !strings.Contains(err.Error(), "saved 2 of 5 images") {
		t.Fatalf("malformed item shapes prevented recovery: %v, %v", paths, err)
	}
	var typeError *json.UnmarshalTypeError
	if !errors.As(err, &typeError) {
		t.Fatalf("malformed item's cause was lost: %v", err)
	}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(contents, fixture) {
			t.Fatalf("recovered image changed: %v", err)
		}
	}
}

func TestCheckNameUsesActualFilesystemAndKeepsExistingFiles(t *testing.T) {
	directory := t.TempDir()
	existing := filepath.Join(directory, "robot.jpeg")
	if err := os.WriteFile(existing, []byte("keep existing image"), 0600); err != nil {
		t.Fatal(err)
	}
	existingDir := filepath.Join(directory, "robot-2.jpeg")
	if err := os.Mkdir(existingDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := CheckName(t.Context(), directory, "robot"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(existing)
	if err != nil || string(contents) != "keep existing image" {
		t.Fatalf("name check changed an existing file: %q, %v", contents, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 2 {
		t.Fatalf("name check left a probe behind: %v, %v", entries, err)
	}
	if info, err := os.Stat(existingDir); err != nil || !info.IsDir() {
		t.Fatalf("name check changed an existing directory: %v", err)
	}
	if err := CheckName(t.Context(), directory, strings.Repeat("x", 512)); err == nil {
		t.Fatal("filesystem's unsupported long name passed preflight")
	}
	if err := CheckName(t.Context(), filepath.Join(directory, "missing"), ""); err != nil {
		t.Fatalf("unnamed output must skip the named probe: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := CheckName(ctx, directory, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled name check = %v", err)
	}
}

func TestCheckNameDoesNotFollowOrRemoveSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating Windows symlinks can require elevated privileges")
	}
	directory := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.jpeg")
	if err := os.WriteFile(target, []byte("keep target"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "robot.jpeg")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckName(t.Context(), directory, "robot"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("name check replaced symlink: %v", err)
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "keep target" {
		t.Fatalf("name check changed symlink target: %q, %v", contents, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("name check left a probe: %v, %v", entries, err)
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
