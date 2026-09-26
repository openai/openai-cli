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
	path, err := g.LookupFontPS(t.Context(), state.PostScript)
	if err != nil || path != state.FontPath {
		t.Fatalf("current font lookup: path=%q error=%v", path, err)
	}
	token := "abcdef0123456789abcdef0123456789"
	variant := filepath.Join(g.directory, "fonts", "revision-"+token+".ttf")
	if err := writeNew(variant, []byte("synthetic immutable font")); err != nil {
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
	if err := os.Symlink(g.State().FontPath, path); err != nil {
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
	if _, err := g.LookupFontPS(ctx, g.State().PostScript); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	name := g.State().PostScript
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.LookupFontPS(t.Context(), name); err == nil {
		t.Fatal("closed gallery still resolves font files")
	}
}
