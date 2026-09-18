package imagegallery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func galleryWithImage(t *testing.T) *Gallery {
	t.Helper()
	g := initialized(t)
	path := fixture(t, t.TempDir(), "local.png", color.NRGBA{190, 40, 10, 255})
	revision, err := g.Prepare(context.Background(), path, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Commit(context.Background(), revision); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestRepairMissingFontKeepsScrollbackMapping(t *testing.T) {
	ctx := context.Background()
	g := galleryWithImage(t)
	before := g.State()
	entries := append([]entry(nil), g.state.Images...)
	statePath := filepath.Join(g.directory, "state.json")
	metadata, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(before.FontPath); err != nil {
		t.Fatal(err)
	}
	if err = g.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(ctx, g.directory); !errors.Is(err, ErrNeedsRepair) {
		if opened != nil {
			opened.Close()
		}
		t.Fatalf("strict open accepted missing font: %v", err)
	}
	recovery, err := OpenForRepair(ctx, g.directory)
	if err != nil {
		t.Fatal(err)
	}
	defer recovery.Close()
	revision, err := recovery.Repair(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if revision.FontPath == before.FontPath || revision.PostScript == before.PostScript || revision.ProfileName != before.ProfileName {
		t.Fatal("repair reused a font identity or changed the profile")
	}
	if recovery.State() != before {
		t.Fatal("repair changed committed state before activation")
	}
	afterPrepare, _ := os.ReadFile(statePath)
	if !bytes.Equal(metadata, afterPrepare) {
		t.Fatal("repair changed metadata before activation")
	}
	if err = recovery.Commit(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recovery.state.Images, entries) || recovery.State().MaxColumns != 4 || recovery.State().Revision != before.Revision+1 {
		t.Fatalf("repair changed image geometry or character allocation: %+v", recovery.State())
	}
	if err = recovery.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, g.directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.State().FontPath != revision.FontPath {
		t.Fatal("repair did not persist the new font")
	}
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}
	reset, err := OpenForReset(ctx, g.directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reset.Close()
	fonts, err := reset.Fonts()
	if err != nil || !contains(fonts, before.FontPath) {
		t.Fatalf("reset forgot the repaired missing font's original registration URL: %v", err)
	}
	usage, err := reset.Usage()
	if err != nil || usage.MissingFiles != 0 {
		t.Fatalf("retired missing font was counted as damage: %+v %v", usage, err)
	}
	if err = reset.Clear(ctx); err != nil {
		t.Fatalf("reset could not remove a retired missing URL: %v", err)
	}
}

func TestRetiredFontValidationAndDeduplication(t *testing.T) {
	g := initialized(t)
	retired := "revision-00000000000000000000000000000001.ttf"
	g.state.RetiredFonts = []string{retired}
	path := filepath.Join(g.directory, "fonts", retired)
	if err := os.WriteFile(path, []byte("restored font bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	fonts, err := g.Fonts()
	if err != nil || len(fonts) != 2 {
		t.Fatalf("restored retired font counted twice: %v %v", fonts, err)
	}
	usage, err := g.Usage()
	if err != nil || usage.FontCount != 2 || usage.MissingFiles != 0 {
		t.Fatalf("restored retired font usage: %+v %v", usage, err)
	}
	for _, names := range [][]string{{"../outside.ttf"}, {g.state.Font}, {retired, retired}} {
		g.state.RetiredFonts = names
		if err := g.validate(openReset); err == nil {
			t.Fatalf("accepted invalid retired font identities: %v", names)
		}
	}
	g.state.RetiredFonts = make([]string, maxRetiredFonts)
	for i := range g.state.RetiredFonts {
		g.state.RetiredFonts[i] = fmt.Sprintf("revision-%032x.ttf", i+1)
	}
	if err = g.validate(openReset); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(g.State().FontPath); err != nil {
		t.Fatal(err)
	}
	if _, err = g.Repair(context.Background()); !errors.Is(err, ErrNeedsReset) {
		t.Fatalf("repair exceeded retired registration limit: %v", err)
	}
	g.state.RetiredFonts = append(g.state.RetiredFonts, fmt.Sprintf("revision-%032x.ttf", maxRetiredFonts+1))
	if err = g.validate(openReset); err == nil {
		t.Fatal("accepted unbounded retired font metadata")
	}
}

func TestResetMissingArtifactsKeepsIdentityAndUnrelatedFiles(t *testing.T) {
	ctx := context.Background()
	g := galleryWithImage(t)
	before := g.State()
	imagePath := filepath.Join(g.directory, "images", g.state.Images[0].Hash+".png")
	for _, path := range []string{before.FontPath, imagePath} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(g.directory, "images", "keep.txt")
	if err := os.WriteFile(unrelated, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := OpenForRepair(ctx, g.directory); !errors.Is(err, ErrNeedsReset) {
		if opened != nil {
			opened.Close()
		}
		t.Fatalf("repair accepted missing thumbnail: %v", err)
	}
	recovery, err := OpenForReset(ctx, g.directory)
	if err != nil {
		t.Fatal(err)
	}
	defer recovery.Close()
	if recovery.State() != before {
		t.Fatal("reset recovery lost the profile identity")
	}
	fonts, err := recovery.Fonts()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(fonts, before.FontPath) {
		t.Fatal("missing font URL omitted from unregister list")
	}
	usage, err := recovery.Usage()
	if err != nil || usage.MissingFiles != 2 || usage.FontCount != 1 || usage.ImageCount != 0 || usage.Bytes < 1 {
		t.Fatalf("damaged usage: %+v %v", usage, err)
	}
	if err = recovery.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if recovery.State().Initialized {
		t.Fatal("reset retained metadata")
	}
	if _, err = os.Stat(filepath.Join(g.directory, "state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state file was not removed: %v", err)
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "unrelated" {
		t.Fatal("reset touched unrelated files")
	}
	usage, err = recovery.Usage()
	if err != nil || usage != (Usage{}) {
		t.Fatalf("cleared usage: %+v %v", usage, err)
	}
}

func TestInterruptedResetRetainsIdentityForRetry(t *testing.T) {
	g := galleryWithImage(t)
	before := g.State()
	fonts, err := g.Fonts()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate interruption as soon as cleanup removes its first font. This
	// specifically exercises a partially deleted cache, not an early cancel.
	ctx := &cancelAfterRemoval{Context: context.Background(), path: fonts[0], done: make(chan struct{})}
	if err = g.Clear(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cleanup did not stop after partial deletion: %v", err)
	}
	if g.State() != before {
		t.Fatal("interrupted reset forgot the profile identity")
	}
	if _, err = os.Stat(filepath.Join(g.directory, "state.json")); err != nil {
		t.Fatal("interrupted reset removed identity metadata")
	}
	if err = g.Close(); err != nil {
		t.Fatal(err)
	}
	recovery, err := OpenForReset(context.Background(), g.directory)
	if err != nil {
		t.Fatalf("could not resume interrupted reset: %v", err)
	}
	defer recovery.Close()
	if recovery.State() != before {
		t.Fatal("reopened partial reset changed profile identity")
	}
	if err = recovery.Clear(context.Background()); err != nil {
		t.Fatalf("reset retry failed: %v", err)
	}
}

type cancelAfterRemoval struct {
	context.Context
	path      string
	done      chan struct{}
	cancelled bool
}

func (c *cancelAfterRemoval) Done() <-chan struct{} { return c.done }
func (c *cancelAfterRemoval) Err() error {
	if !c.cancelled {
		if _, err := os.Stat(c.path); errors.Is(err, os.ErrNotExist) {
			c.cancelled = true
			close(c.done)
		}
	}
	if c.cancelled {
		return context.Canceled
	}
	return nil
}

func TestRepairRejectsCorruptThumbnailWithoutChangingState(t *testing.T) {
	g := galleryWithImage(t)
	before := g.State()
	path := filepath.Join(g.directory, "images", g.state.Images[0].Hash+".png")
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	fontsBefore, err := g.Fonts()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Repair(context.Background()); !errors.Is(err, ErrNeedsReset) {
		t.Fatalf("repair accepted corrupt thumbnail: %v", err)
	}
	fontsAfter, _ := g.Fonts()
	if g.State() != before || !reflect.DeepEqual(fontsBefore, fontsAfter) {
		t.Fatal("failed repair mutated the gallery")
	}
}

func TestRecoveryRefusesMissingOrCorruptIdentity(t *testing.T) {
	for _, bad := range []string{"missing", "corrupt", "invalid identity", "invalid allocation"} {
		t.Run(bad, func(t *testing.T) {
			g := galleryWithImage(t)
			fontPath := g.State().FontPath
			metadata := g.state
			if err := g.Close(); err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(g.directory, "state.json")
			var data []byte
			switch bad {
			case "missing":
				if err := os.Remove(statePath); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				data = []byte("{broken")
			case "invalid identity":
				metadata.PostScript = "SomeoneElse-Regular"
				data, _ = json.Marshal(metadata)
			case "invalid allocation":
				metadata.Images[0].Start++
				data, _ = json.Marshal(metadata)
			}
			if data != nil {
				if err := os.WriteFile(statePath, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, open := range []func(context.Context, string) (*Gallery, error){OpenForReset, OpenForRepair} {
				if recovery, err := open(context.Background(), g.directory); err == nil {
					recovery.Close()
					t.Fatal("recovery accepted unknown profile identity")
				}
				if _, err := os.Stat(fontPath); err != nil {
					t.Fatal("failed recovery deleted a font")
				}
			}
		})
	}
}

func TestRecoveryRejectsSymlinkAndNonprivateArtifacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink and permission behavior differs on Windows")
	}
	for _, target := range []string{"state", "font", "image", "fonts directory", "images directory"} {
		t.Run(target, func(t *testing.T) {
			g := galleryWithImage(t)
			path := map[string]string{
				"state":            filepath.Join(g.directory, "state.json"),
				"font":             g.State().FontPath,
				"image":            filepath.Join(g.directory, "images", g.state.Images[0].Hash+".png"),
				"fonts directory":  filepath.Join(g.directory, "fonts"),
				"images directory": filepath.Join(g.directory, "images"),
			}[target]
			if err := g.Close(); err != nil {
				t.Fatal(err)
			}
			moved := filepath.Join(t.TempDir(), "original")
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, path); err != nil {
				t.Fatal(err)
			}
			for _, open := range []func(context.Context, string) (*Gallery, error){OpenForReset, OpenForRepair} {
				if recovered, err := open(context.Background(), g.directory); err == nil || !strings.Contains(err.Error(), "symbolic links") {
					if recovered != nil {
						recovered.Close()
					}
					t.Fatalf("accepted a symlink: %v", err)
				}
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(moved, path); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0755); err != nil {
				t.Fatal(err)
			}
			for _, open := range []func(context.Context, string) (*Gallery, error){OpenForReset, OpenForRepair} {
				if recovered, err := open(context.Background(), g.directory); err == nil || !strings.Contains(err.Error(), "private") {
					if recovered != nil {
						recovered.Close()
					}
					t.Fatalf("accepted nonprivate storage: %v", err)
				}
			}
		})
	}
}

func TestRecoveryLockAndEmptyCache(t *testing.T) {
	ctx := context.Background()
	g := initialized(t)
	for _, open := range []func(context.Context, string) (*Gallery, error){OpenForReset, OpenForRepair} {
		if _, err := open(ctx, g.directory); !errors.Is(err, ErrBusy) {
			t.Fatalf("recovery bypassed lock: %v", err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := g.Repair(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := g.Clear(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !g.State().Initialized {
		t.Fatal("cancelled reset changed metadata")
	}
	empty, err := OpenForReset(ctx, filepath.Join(t.TempDir(), "empty"))
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	if err = empty.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = empty.Repair(ctx); err == nil {
		t.Fatal("repaired a gallery without an identity")
	}
}

func TestUsageIncludesOnlyOwnedArtifacts(t *testing.T) {
	g := galleryWithImage(t)
	unknown := filepath.Join(g.directory, "images", "unrelated.bin")
	if err := os.WriteFile(unknown, make([]byte, 100000), 0600); err != nil {
		t.Fatal(err)
	}
	fonts, _ := g.Fonts()
	images, _ := g.ownedFiles("images", isImageName)
	var want int64
	for _, path := range append(fonts, images...) {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want += info.Size()
	}
	usage, err := g.Usage()
	if err != nil || usage.Bytes != want || usage.FontCount != 2 || usage.ImageCount != 1 || usage.MissingFiles != 0 {
		t.Fatalf("usage %+v, expected %d bytes: %v", usage, want, err)
	}
}

func contains(paths []string, path string) bool {
	for _, item := range paths {
		if item == path {
			return true
		}
	}
	return false
}
