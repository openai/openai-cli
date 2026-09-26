package imagegallery

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/imagefont"
)

func fixture(shade color.NRGBA) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetNRGBA(x, y, shade)
		}
	}
	return img
}
func initialized(t *testing.T) *Gallery {
	t.Helper()
	g, err := Open(context.Background(), filepath.Join(t.TempDir(), "gallery"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	revision, err := g.Initialize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if g.State().Initialized {
		t.Fatal("initialization committed before activation")
	}
	if err = g.Commit(context.Background(), revision); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGalleryImmutableRevisionsAndDedup(t *testing.T) {
	ctx := context.Background()
	g := initialized(t)
	red := fixture(color.NRGBA{240, 40, 20, 255})
	first, err := g.Prepare(ctx, red, 4)
	if err != nil {
		t.Fatal(err)
	}
	if g.State().ImageCount != 0 {
		t.Fatal("prepare changed committed state")
	}
	if err = g.Commit(ctx, first); err != nil {
		t.Fatal(err)
	}
	oldFont, err := os.ReadFile(first.FontPath)
	if err != nil {
		t.Fatal(err)
	}
	oldEntry := g.state.Images[0]
	second, err := g.Prepare(ctx, fixture(color.NRGBA{20, 40, 240, 255}), 4)
	if err != nil {
		t.Fatal(err)
	}
	if first.PostScript == second.PostScript || first.FontPath == second.FontPath {
		t.Fatal("font identity reused")
	}
	if err = g.Commit(ctx, second); err != nil {
		t.Fatal(err)
	}
	if g.state.Images[0] != oldEntry || first.Text != textFor(g.state.Images[0]) {
		t.Fatal("old scrollback mapping changed")
	}
	if g.state.Images[1].Start != oldEntry.Start+rune(oldEntry.Columns*oldEntry.Rows) {
		t.Fatal("image characters overlap")
	}
	saved, _ := os.ReadFile(first.FontPath)
	if !bytes.Equal(saved, oldFont) {
		t.Fatal("old font changed")
	}
	duplicate, err := g.Prepare(ctx, red, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.Existing || duplicate.Text != first.Text || duplicate.Columns != first.Columns || duplicate.FontPath != second.FontPath {
		t.Fatal("duplicate consumed new glyphs or lost old geometry")
	}
	if err = g.Commit(ctx, duplicate); err != nil {
		t.Fatal(err)
	}
	if g.State().ImageCount != 2 || g.State().UsedGlyphs != 16 {
		t.Fatalf("unexpected state: %+v", g.State())
	}

	directory := g.directory
	if err = g.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.State().ImageCount != 2 {
		t.Fatal("state did not survive reopen")
	}
	for _, path := range []string{directory, filepath.Join(directory, "fonts"), filepath.Join(directory, "images"), filepath.Join(directory, "state.json"), first.FontPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			t.Fatalf("nonprivate storage: %s", path)
		}
	}
}

func TestGalleryFailedActivationRetainsArtifactsWithoutCommit(t *testing.T) {
	g := initialized(t)
	before := g.State()
	path := fixture(color.NRGBA{200, 0, 0, 255})
	abandoned, err := g.Prepare(context.Background(), path, 4)
	if err != nil {
		t.Fatal(err)
	}
	if g.State() != before {
		t.Fatal("failed activation changed state")
	}
	files, err := os.ReadDir(filepath.Join(g.directory, "fonts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatal("uncommitted font missing from cleanup list")
	}
	replacement, err := g.Prepare(context.Background(), path, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Commit(context.Background(), abandoned); err == nil {
		t.Fatal("stale revision committed")
	}
	if err = g.Commit(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(abandoned.FontPath); err != nil {
		t.Fatal("potentially registered abandoned font removed")
	}
}

func TestGalleryLockCorruptionAndCancellation(t *testing.T) {
	ctx := context.Background()
	g := initialized(t)
	if _, err := Open(ctx, g.directory); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent lock: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := g.Prepare(cancelled, nil, 32); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	directory := g.directory
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(directory, "state.json")
	for _, bad := range []string{`{"version":99}`, `{"version":1,"font":"../../outside"}`, `{} {}`} {
		if err := os.WriteFile(statePath, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if invalid, err := Open(ctx, directory); err == nil {
			invalid.Close()
			t.Fatal("accepted corrupt state")
		}
		lock, err := acquireLock(filepath.Join(directory, ".lock"))
		if err != nil {
			t.Fatalf("failed open leaked lock: %v", err)
		}
		_ = unlockFile(lock)
		_ = lock.Close()
	}
}

func TestGalleryRejectsSymlinkStorage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege differs on Windows")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if g, err := Open(context.Background(), link); err == nil {
		g.Close()
		t.Fatal("accepted symlink gallery")
	}
	g := initialized(t)
	font := g.State().FontPath
	if err := os.Remove(font); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(directory, "outside")
	if err := os.WriteFile(outside, []byte("do not remove"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, font); err != nil {
		t.Fatal(err)
	}
	if err := g.validate(); err == nil {
		t.Fatal("followed symlink font")
	}

	if data, _ := os.ReadFile(outside); string(data) != "do not remove" {
		t.Fatal("modified external file")
	}
}

func TestGalleryFull(t *testing.T) {
	g := initialized(t)
	source := fixture(color.NRGBA{255, 0, 0, 255})
	// Exercise the allocation boundary without generating a 6,400-glyph fixture.
	g.state.Images = []entry{{Columns: 64, Rows: 32}, {Columns: 64, Rows: 32}, {Columns: 64, Rows: 32}, {Columns: 64, Rows: 4}}
	if _, err := g.Prepare(context.Background(), source, 4); !errors.Is(err, ErrFull) {
		t.Fatalf("capacity error: %v", err)
	}
	if g.State().UsedGlyphs != imagefont.MaxGlyphs {
		t.Fatal("capacity mutated")
	}
}

func TestGalleryRejectsCorruptCacheAndAllocations(t *testing.T) {
	g := initialized(t)
	source := fixture(color.NRGBA{255, 0, 0, 255})
	first, err := g.Prepare(context.Background(), source, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Commit(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(g.directory, "images", g.state.Images[0].Hash+".png")
	if err = os.WriteFile(cached, []byte("not a PNG"), 0600); err != nil {
		t.Fatal(err)
	}
	other := fixture(color.NRGBA{0, 0, 255, 255})
	if _, err = g.Prepare(context.Background(), other, 4); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("accepted corrupt cache: %v", err)
	}
	g.state.Images[0].Start++
	data, _ := json.Marshal(g.state)
	directory := g.directory
	if err = g.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(context.Background(), directory); err == nil {
		opened.Close()
		t.Fatal("accepted changed allocation")
	}
}

func TestGalleryLockReleasedAfterProcessExit(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	command := exec.Command(os.Args[0], "-test.run=^TestGalleryLockHelperProcess$")
	command.Env = append(os.Environ(), "IMAGE_GALLERY_LOCK_HELPER="+directory)
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	ready, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || ready != "ready\n" {
		t.Fatalf("helper did not acquire lock: %q %v", ready, err)
	}
	if g, err := Open(context.Background(), directory); !errors.Is(err, ErrBusy) {
		if g != nil {
			g.Close()
		}
		t.Fatalf("cross-process lock: %v", err)
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	g, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatalf("killed process left lock: %v", err)
	}
	defer g.Close()
}

func TestGalleryLockHelperProcess(t *testing.T) {
	directory := os.Getenv("IMAGE_GALLERY_LOCK_HELPER")
	if directory == "" {
		return
	}
	g, err := Open(context.Background(), directory)
	if err != nil {
		os.Exit(2)
	}
	defer g.Close()
	_, _ = os.Stdout.WriteString("ready\n")
	time.Sleep(time.Hour)
}
