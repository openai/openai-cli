package imagegallery

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"golang.org/x/image/font/sfnt"
)

func TestGeometryDefaultUsesOriginalRevision(t *testing.T) {
	ctx := context.Background()
	g := initialized(t)
	existing, err := g.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, reuse := range []bool{true, false} {
		revision := existing
		if !reuse {
			path := fixture(t, t.TempDir(), "new.png", color.NRGBA{R: 180, G: 30, A: 255})
			revision, err = g.Prepare(ctx, path, 4)
			if err != nil {
				t.Fatal(err)
			}
		}
		before := g.State()
		fonts, err := g.Fonts()
		if err != nil {
			t.Fatal(err)
		}
		display, err := g.FontForGeometry(ctx, revision, 16, 32)
		if err != nil {
			t.Fatal(err)
		}
		if display != (DisplayFont{revision.FontPath, revision.PostScript, reuse}) {
			t.Fatalf("default geometry replaced the original revision: %+v", display)
		}
		currentFonts, err := g.Fonts()
		if err != nil || !reflect.DeepEqual(currentFonts, fonts) || g.State() != before {
			t.Fatalf("default geometry created artifacts or committed state: %v", err)
		}
	}
}

func TestGeometryVariantsAreImmutableAndDeterministic(t *testing.T) {
	ctx := context.Background()
	g := initialized(t)
	path := fixture(t, t.TempDir(), "red.png", color.NRGBA{R: 220, G: 30, A: 255})
	revision, err := g.Prepare(ctx, path, 4)
	if err != nil {
		t.Fatal(err)
	}
	before := g.State()
	metadata := geometryReadFile(t, filepath.Join(g.directory, "state.json"))
	base := geometryReadFile(t, revision.FontPath)
	fontPath, postScript, text := revision.FontPath, revision.PostScript, revision.Text
	first, err := g.FontForGeometry(ctx, revision, 14, 28)
	if err != nil {
		t.Fatal(err)
	}
	if first.Existing || first.FontPath == fontPath || first.PostScript == postScript {
		t.Fatalf("custom geometry needs its own fresh immutable identity: %+v", first)
	}
	encoded := geometryReadFile(t, first.FontPath)
	geometryAssertFont(t, first, revision.Text)
	if len(encoded) == 0 || bytes.Equal(encoded, base) {
		t.Fatal("custom geometry did not produce a distinct font")
	}
	fonts, err := g.Fonts()
	if err != nil {
		t.Fatal(err)
	}
	second, err := g.FontForGeometry(ctx, revision, 14, 28)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Existing || second.FontPath != first.FontPath || second.PostScript != first.PostScript {
		t.Fatalf("repeated geometry did not reuse its identity: first=%+v second=%+v", first, second)
	}
	currentFonts, err := g.Fonts()
	if err != nil || !reflect.DeepEqual(currentFonts, fonts) {
		t.Fatalf("repeated geometry added files: %v", err)
	}
	if g.State() != before || !bytes.Equal(metadata, geometryReadFile(t, filepath.Join(g.directory, "state.json"))) {
		t.Fatal("building display variants changed committed gallery metadata")
	}
	if revision.FontPath != fontPath || revision.PostScript != postScript || revision.Text != text || !bytes.Equal(base, geometryReadFile(t, fontPath)) || !bytes.Equal(encoded, geometryReadFile(t, first.FontPath)) {
		t.Fatal("building/reusing a variant mutated the original revision or font bytes")
	}
	if err := g.Commit(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if state := g.State(); state.FontPath != fontPath || state.PostScript != postScript || state.FontPath == first.FontPath {
		t.Fatalf("commit stored display-specific geometry instead of its original revision: %+v", state)
	}
	dir := g.directory
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reused, err := reopened.Prepare(ctx, path, 32)
	if err != nil {
		t.Fatal(err)
	}
	display, err := reopened.FontForGeometry(ctx, reused, 14, 28)
	if err != nil || !display.Existing || display.FontPath != first.FontPath || display.PostScript != first.PostScript || reused.Text != text {
		t.Fatalf("reopened gallery lost deterministic variant or old characters: display=%+v err=%v", display, err)
	}
}

func TestGeometryTwoTabsKeepMappingsAcrossAppendedImages(t *testing.T) {
	ctx := context.Background()
	g := initialized(t)
	sources := t.TempDir()
	red := fixture(t, sources, "red.png", color.NRGBA{R: 240, A: 255})
	first, err := g.Prepare(ctx, red, 4)
	if err != nil {
		t.Fatal(err)
	}
	// Each tab may use a different cell geometry for the same character map.
	geometries := [][2]int{{14, 32}, {16, 36}, {14, 36}, {8, 16}, {24, 48}}
	oldFonts := make([]DisplayFont, 0, len(geometries))
	oldBytes := make([][]byte, 0, len(geometries))
	identities := map[string]bool{first.PostScript: true}
	for _, geometry := range geometries {
		display, err := g.FontForGeometry(ctx, first, geometry[0], geometry[1])
		if err != nil {
			t.Fatal(err)
		}
		if identities[display.PostScript] || display.Existing {
			t.Fatalf("different geometry reused a font identity: %+v", display)
		}
		identities[display.PostScript] = true
		oldFonts = append(oldFonts, display)
		oldBytes = append(oldBytes, geometryReadFile(t, display.FontPath))
		geometryAssertFont(t, display, first.Text)
	}
	if err := g.Commit(ctx, first); err != nil {
		t.Fatal(err)
	}
	oldEntry := g.state.Images[0]
	second, err := g.Prepare(ctx, fixture(t, sources, "blue.png", color.NRGBA{B: 240, A: 255}), 4)
	if err != nil {
		t.Fatal(err)
	}
	before := g.State()
	newFonts := make([]DisplayFont, 0, len(geometries))
	for i, geometry := range geometries {
		display, err := g.FontForGeometry(ctx, second, geometry[0], geometry[1])
		if err != nil {
			t.Fatal(err)
		}
		if identities[display.PostScript] || display.FontPath == oldFonts[i].FontPath {
			t.Fatal("appending images reused a font identity already active in another tab")
		}
		identities[display.PostScript] = true
		newFonts = append(newFonts, display)
		geometryAssertFont(t, display, first.Text, second.Text)
		if g.State() != before || !bytes.Equal(oldBytes[i], geometryReadFile(t, oldFonts[i].FontPath)) {
			t.Fatal("new variant changed committed metadata or an earlier tab's font")
		}
	}
	if err := g.Commit(ctx, second); err != nil {
		t.Fatal(err)
	}
	if g.state.Images[0] != oldEntry || first.Text != textFor(g.state.Images[0]) || g.State().ImageCount != 2 {
		t.Fatal("appending geometry variants changed earlier scrollback characters")
	}
	duplicate, err := g.Prepare(ctx, red, 32)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Text != first.Text || !duplicate.Existing || duplicate.FontPath != second.FontPath {
		t.Fatal("geometry variants broke content deduplication")
	}
	for i, geometry := range geometries {
		display, err := g.FontForGeometry(ctx, duplicate, geometry[0], geometry[1])
		if err != nil || !display.Existing || display.FontPath != newFonts[i].FontPath || display.PostScript != newFonts[i].PostScript {
			t.Fatalf("tab did not reuse its cumulative display font: %+v, %v", display, err)
		}
	}
	if err := g.Commit(ctx, duplicate); err != nil || g.State().ImageCount != 2 {
		t.Fatalf("duplicate geometry allocated new images: %v", err)
	}
}

func TestGeometryRejectsInvalidStaleForeignAndCanceledRevisions(t *testing.T) {
	ctx := context.Background()
	g := galleryWithImage(t)
	revision, err := g.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := g.State()
	fonts, _ := g.Fonts()
	for _, geometry := range [][2]int{{0, 0}, {7, 32}, {25, 32}, {16, 15}, {16, 49}, {-1, 32}} {
		if _, err := g.FontForGeometry(ctx, revision, geometry[0], geometry[1]); err == nil {
			t.Fatalf("accepted unsupported geometry: %v", geometry)
		}
	}
	if _, err := g.FontForGeometry(ctx, nil, 16, 32); err == nil {
		t.Fatal("accepted nil revision")
	}
	other := initialized(t)
	foreign, err := other.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.FontForGeometry(ctx, foreign, 14, 28); err == nil {
		t.Fatal("accepted another gallery's revision")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := g.FontForGeometry(canceled, revision, 14, 28); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	current, err := g.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.FontForGeometry(ctx, revision, 16, 32); err == nil {
		t.Fatal("accepted superseded revision even for default geometry")
	}
	if err := g.Commit(ctx, current); err != nil {
		t.Fatal(err)
	}
	if _, err := g.FontForGeometry(ctx, current, 14, 28); err == nil {
		t.Fatal("accepted a revision after commit consumed it")
	}
	afterFonts, err := g.Fonts()
	if err != nil || !reflect.DeepEqual(fonts, afterFonts) || g.State() != before {
		t.Fatalf("invalid geometry request changed files or metadata: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.FontForGeometry(ctx, current, 14, 28); err == nil {
		t.Fatal("accepted geometry after the gallery was closed")
	}
}

func TestGeometryVariantsParticipateInOwnedReset(t *testing.T) {
	ctx := context.Background()
	g := galleryWithImage(t)
	source := fixture(t, t.TempDir(), "uncommitted.png", color.NRGBA{G: 200, A: 255})
	original := geometryReadFile(t, source)
	revision, err := g.Prepare(ctx, source, 4)
	if err != nil {
		t.Fatal(err)
	}
	display, err := g.FontForGeometry(ctx, revision, 14, 28)
	if err != nil {
		t.Fatal(err)
	}
	fonts, err := g.Fonts()
	if err != nil || !slices.Contains(fonts, display.FontPath) || !slices.Contains(fonts, revision.FontPath) {
		t.Fatalf("reset discovery omitted uncommitted display/base fonts: %v %v", fonts, err)
	}
	unrelated := filepath.Join(g.directory, "fonts", "keep.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := g.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	for _, path := range fonts {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reset kept owned variant %q: %v", path, err)
		}
	}
	if g.State().Initialized || string(geometryReadFile(t, unrelated)) != "keep" || !bytes.Equal(original, geometryReadFile(t, source)) {
		t.Fatal("reset retained metadata or changed unrelated/source files")
	}
}

func TestGeometryRejectsUnsafeExistingVariants(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges and Unix permission bits differ on Windows")
	}
	for _, mode := range []string{"symlink", "nonprivate"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			g := galleryWithImage(t)
			revision, err := g.Initialize(ctx)
			if err != nil {
				t.Fatal(err)
			}
			display, err := g.FontForGeometry(ctx, revision, 14, 28)
			if err != nil {
				t.Fatal(err)
			}
			before := g.State()
			outside := filepath.Join(t.TempDir(), "outside.ttf")
			if err := os.WriteFile(outside, []byte("unrelated font"), 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "symlink" {
				if err := os.Remove(display.FontPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, display.FontPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Chmod(display.FontPath, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := g.FontForGeometry(ctx, revision, 14, 28); err == nil {
				t.Fatal("reused an unsafe cached display font")
			}
			if _, err := g.Fonts(); err == nil {
				t.Fatal("reset discovery trusted an unsafe display font")
			}
			if err := g.Clear(ctx); err == nil {
				t.Fatal("reset accepted unsafe display storage")
			}
			if g.State() != before || string(geometryReadFile(t, outside)) != "unrelated font" {
				t.Fatal("unsafe variant handling changed state or followed the external target")
			}
		})
	}
}

func geometryReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func geometryAssertFont(t *testing.T, display DisplayFont, texts ...string) {
	t.Helper()
	font, err := sfnt.Parse(geometryReadFile(t, display.FontPath))
	if err != nil {
		t.Fatalf("display variant is not a valid font: %v", err)
	}
	var buffer sfnt.Buffer
	name, err := font.Name(&buffer, sfnt.NameIDPostScript)
	if err != nil || name != display.PostScript {
		t.Fatalf("display identity differs from embedded font name: %q, %v", name, err)
	}
	for _, text := range texts {
		for _, character := range text {
			if character < '\ue000' || character > '\uf8ff' {
				continue
			}
			glyph, err := font.GlyphIndex(&buffer, character)
			if err != nil || glyph == 0 {
				t.Fatalf("display font lost image character U+%04X: %v", character, err)
			}
		}
	}
}
