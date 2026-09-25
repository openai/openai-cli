package imageoutput

import (
	"context"
	"errors"
	"fmt"
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

func TestValidateName(t *testing.T) {
	for _, name := range []string{"orange-robot", "my image", "café", "机器人", "image_2", ".preview", "robot.v2", "COM10", "lpt0"} {
		t.Run("valid "+name, func(t *testing.T) {
			if err := ValidateName(name); err != nil {
				t.Fatalf("valid name rejected: %v", err)
			}
		})
	}
	for _, name := range []string{"", " ", ".", "..", "../robot", `..\robot`, "/robot", `C:\robot`, "robot\n", "robot\x00", "robot\x7f", "robot\u0085", "robot<", "robot>", "robot:", `robot"`, "robot|", "robot?", "robot*", "robot.", "robot ", "nul", "Con", "NUL.txt", "con .txt", "PRN", "AUX", "COM1", "LPT9", "COM¹", "LPT²", "COM³", "CONIN$", "CONOUT$", string([]byte{0xff})} {
		t.Run(fmt.Sprintf("invalid %q", name), func(t *testing.T) {
			if err := ValidateName(name); err == nil {
				t.Fatal("invalid name accepted")
			}
		})
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

func TestCheckNameCancellationRemovesProbe(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	checks := 0
	checking := checkingContext{Context: ctx, check: func() {
		checks++
		if checks == 3 {
			cancel()
		}
	}}
	if err := CheckName(checking, directory, "robot"); !errors.Is(err, context.Canceled) {
		t.Fatalf("name check cancellation = %v", err)
	}
	assertEmptyDirectory(t, directory)
}
