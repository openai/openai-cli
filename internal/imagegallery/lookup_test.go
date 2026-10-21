package imagegallery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLookupFontPSFindsCurrentAndImmutableVariant(t *testing.T) {
	g := initialized(t)
	state := g.State()
	revision, err := g.Initialize(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	display, err := g.FontForTypography(t.Context(), revision, typographySource(t))
	if err != nil {
		t.Fatal(err)
	}
	path, err := g.LookupFontPS(t.Context(), display.PostScript)
	if err != nil || path != display.FontPath {
		t.Fatalf("current font lookup: path=%q error=%v", path, err)
	}
	token := "abcdef0123456789abcdef0123456789"
	variant := filepath.Join(g.directory, "fonts", "revision-"+token+".ttf")
	if err := writeNew(t.Context(), variant, []byte("synthetic immutable font")); err != nil {
		t.Fatal(err)
	}
	before := g.State()
	for _, style := range []string{"Regular", "Bold", "Italic", "BoldItalic"} {
		name := "OpenAIImages-" + state.ID[:8] + "-" + token + "-" + style
		got, err := g.LookupFontPS(t.Context(), name)
		if err != nil || got != variant {
			t.Fatalf("variant lookup: path=%q error=%v", got, err)
		}
	}
	if g.State() != before {
		t.Fatal("font lookup modified gallery metadata")
	}
}

func TestLookupFontPSRejectsUnownedAndMalformedNames(t *testing.T) {
	g := initialized(t)
	for _, name := range []string{"Menlo-Regular", "Courier", "OpenAIImages-ffffffff-abcdef0123456789abcdef0123456789-Regular"} {
		path, err := g.LookupFontPS(t.Context(), name)
		if err != nil || path != "" {
			t.Fatalf("unowned font should not resolve: path=%q error=%v", path, err)
		}
	}
	prefix := "OpenAIImages-" + g.State().ID[:8] + "-"
	for _, suffix := range []string{"../outside-Regular", "ABCDEF0123456789ABCDEF0123456789-Regular", "abcdef0123456789abcdef0123456789-Unknown", "abcdef0123456789abcdef0123456789-Regular-extra", "abcdef0123456789abcdef0123456789-Regular\n\x1b", ""} {
		path, err := g.LookupFontPS(t.Context(), prefix+suffix)
		if err == nil || path != "" || strings.ContainsAny(err.Error(), "\n\x1b") {
			t.Fatalf("malformed identity accepted or echoed: path=%q error=%v", path, err)
		}
	}
}

func TestLookupFontPSMissingOrUnsafeFiles(t *testing.T) {
	g := initialized(t)
	token := "abcdef0123456789abcdef0123456789"
	name := "OpenAIImages-" + g.State().ID[:8] + "-" + token + "-Regular"
	path := filepath.Join(g.directory, "fonts", "revision-"+token+".ttf")
	_, err := g.LookupFontPS(t.Context(), name)
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "select your original font and size") {
		t.Fatalf("missing font did not explain recovery: %v", err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := g.LookupFontPS(t.Context(), name); err == nil {
		t.Fatal("directory accepted as a font")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return // Symlinks require privileges on some Windows configurations.
	}
	if err := os.Symlink(filepath.Join(g.directory, "state.json"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := g.LookupFontPS(t.Context(), name); err == nil {
		t.Fatal("symlink accepted as a font")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.LookupFontPS(t.Context(), name); err == nil {
		t.Fatal("non-private font accepted")
	}
}

func TestLookupFontPSHonorsGalleryLifetime(t *testing.T) {
	g := initialized(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := g.LookupFontPS(ctx, g.state.PostScript); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	name := g.state.PostScript
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.LookupFontPS(t.Context(), name); err == nil {
		t.Fatal("closed gallery still resolves font files")
	}
}

func TestFindFontGalleryMatchesExactFontAndPreservesOwnership(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, strings.Repeat("a", 32))
	g, err := Open(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := g.Initialize(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	display, err := g.FontForTypography(t.Context(), revision, typographySource(t))
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Commit(t.Context(), revision); err != nil {
		t.Fatal(err)
	}
	if err = g.BindTTY(t.Context(), "/dev/ttys008"); err != nil {
		t.Fatal(err)
	}
	if err = g.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(directory, ".tty"))
	if err != nil {
		t.Fatal(err)
	}
	found, err := FindFontGallery(t.Context(), root, display.PostScript)
	if err != nil || found != directory {
		t.Fatalf("found=%q err=%v", found, err)
	}
	after, _ := os.ReadFile(filepath.Join(directory, ".tty"))
	if string(before) != string(after) {
		t.Fatal("ownership changed")
	}
	found, err = FindFontGallery(t.Context(), root, "OpenAI Local "+strings.Repeat("a", 8))
	if err != nil || found != "" {
		t.Fatal("accepted alias")
	}
	g, err = Open(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	_, err = FindFontGallery(t.Context(), root, display.PostScript)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("expected contention, got %v", err)
	}
	_ = g.Close()
	duplicate := filepath.Join(root, strings.Repeat("b", 32))
	err = filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		target := filepath.Join(duplicate, relative)
		if info.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = FindFontGallery(t.Context(), root, display.PostScript); err == nil {
		t.Fatal("accepted ambiguous galleries")
	}
}

func TestFindFontGallerySupportsLegacyRootWithoutBinding(t *testing.T) {
	g := initialized(t)
	root := g.directory
	revision, err := g.Initialize(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	display, err := g.FontForTypography(t.Context(), revision, typographySource(t))
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Commit(t.Context(), revision); err != nil {
		t.Fatal(err)
	}
	_ = g.Close()
	found, err := FindFontGallery(t.Context(), root, display.PostScript)
	if err != nil || found != root {
		t.Fatalf("legacy lookup %q %v", found, err)
	}
	if _, err = os.Lstat(filepath.Join(root, ".tty")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy owner changed")
	}
	if err = os.Remove(display.FontPath); err != nil {
		t.Fatal(err)
	}
	if _, err = FindFontGallery(t.Context(), root, display.PostScript); err == nil {
		t.Fatal("accepted missing font")
	}
}
