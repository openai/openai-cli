package imageoutput

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestSaveResponseFormatsAndUniqueNames(t *testing.T) {
	directory := t.TempDir()
	fixtures := imageFixtures(t)
	var allPaths []string
	for repeat := 0; repeat < 2; repeat++ {
		paths, err := SaveResponse(context.Background(), imageResponse(t, fixtures...), directory)
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) != len(fixtures) {
			t.Fatalf("saved %d images; want %d", len(paths), len(fixtures))
		}
		for i, path := range paths {
			if filepath.Dir(path) != directory || filepath.Ext(path) != []string{".png", ".jpeg", ".webp"}[i] {
				t.Fatalf("wrong output path: %q", path)
			}
			contents, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(contents, fixtures[i]) {
				t.Fatalf("image %d contents differ: %v", i, err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Fatalf("image permissions = %o; want 600", info.Mode().Perm())
			}
		}
		allPaths = append(allPaths, paths...)
	}
	unique := make(map[string]bool)
	for _, path := range allPaths {
		if unique[path] {
			t.Fatalf("reused image filename: %s", path)
		}
		unique[path] = true
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != len(allPaths) {
		t.Fatalf("expected all images to remain after second save; found %d: %v", len(entries), err)
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

func TestSaveResponseReadableDefaultName(t *testing.T) {
	for _, name := range [][]string{nil, {""}} {
		directory := t.TempDir()
		before := time.Now().Add(-time.Second)
		paths, err := SaveResponse(context.Background(), imageResponse(t, imageFixtures(t)[0]), directory, name...)
		if err != nil {
			t.Fatal(err)
		}
		stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(paths[0]), "image-"), ".png")
		parsed, err := time.ParseInLocation("2006-01-02-150405", stamp, time.Local)
		if err != nil || parsed.Before(before) || parsed.After(time.Now()) {
			t.Fatalf("default filename does not contain current local date/time: %q, %v", paths[0], err)
		}
	}
}

func TestSaveResponseNamedCollisions(t *testing.T) {
	directory := t.TempDir()
	kept := filepath.Join(directory, "orange-robot.png")
	if err := os.WriteFile(kept, []byte("existing image"), 0600); err != nil {
		t.Fatal(err)
	}
	// A conflicting directory must also be left intact.
	if err := os.Mkdir(filepath.Join(directory, "orange-robot-2.png"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture := imageFixtures(t)[0]
	paths, err := SaveResponse(context.Background(), imageResponse(t, fixture, fixture), directory, "orange-robot")
	if err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		if want := fmt.Sprintf("orange-robot-%d.png", i+3); filepath.Base(path) != want {
			t.Fatalf("filename = %q; want %q", path, want)
		}
	}
	contents, err := os.ReadFile(kept)
	if err != nil || string(contents) != "existing image" {
		t.Fatalf("existing image changed: %q, %v", contents, err)
	}
	if info, err := os.Stat(filepath.Join(directory, "orange-robot-2.png")); err != nil || !info.IsDir() {
		t.Fatalf("existing directory changed: %v", err)
	}
}

func TestSaveResponseNamedCollisionDoesNotFollowSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating Windows symlinks can require elevated privileges")
	}
	directory := t.TempDir()
	target := filepath.Join(t.TempDir(), "untouched.png")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "robot.png")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	paths, err := SaveResponse(context.Background(), imageResponse(t, imageFixtures(t)[0]), directory, "robot")
	if err != nil || len(paths) != 1 || filepath.Base(paths[0]) != "robot-2.png" {
		t.Fatalf("symlink collision = %v, %v", paths, err)
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "untouched" {
		t.Fatalf("symlink target changed: %q, %v", contents, err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink changed: %v", err)
	}
}

func TestSaveResponseConcurrentNames(t *testing.T) {
	directory := t.TempDir()
	fixture := imageFixtures(t)[0]
	raw := imageResponse(t, fixture)
	const count = 16
	type result struct {
		paths []string
		err   error
	}
	results := make(chan result, count)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			paths, err := SaveResponse(context.Background(), raw, directory, "robot")
			results <- result{paths, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	names := make(map[string]bool)
	for result := range results {
		if result.err != nil || len(result.paths) != 1 {
			t.Fatalf("concurrent save = %v, %v", result.paths, result.err)
		}
		path := result.paths[0]
		if names[filepath.Base(path)] {
			t.Fatalf("concurrent save reused %q", path)
		}
		names[filepath.Base(path)] = true
		contents, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(contents, fixture) {
			t.Fatalf("concurrent output corrupted: %v", err)
		}
	}
	for sequence := 1; sequence <= count; sequence++ {
		name := "robot.png"
		if sequence > 1 {
			name = fmt.Sprintf("robot-%d.png", sequence)
		}
		if !names[name] {
			t.Fatalf("missing expected sequential filename %q", name)
		}
	}
}

func TestSaveResponseNamedPartialSuccessAndValidation(t *testing.T) {
	directory := t.TempDir()
	kept := filepath.Join(directory, "robot.png")
	if err := os.WriteFile(kept, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	valid := base64.StdEncoding.EncodeToString(imageFixtures(t)[0])
	if paths, err := SaveResponse(context.Background(), encodedResponse(t, valid, valid+"!"), directory, "robot"); err == nil || len(paths) != 1 || filepath.Base(paths[0]) != "robot-2.png" {
		t.Fatalf("invalid batch = %v, %v", paths, err)
	}
	for _, names := range [][]string{{"../outside"}, {"one", "two"}} {
		if paths, err := SaveResponse(context.Background(), encodedResponse(t, valid), directory, names...); err == nil || len(paths) != 0 {
			t.Fatalf("invalid filename = %v, %v", paths, err)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 2 {
		t.Fatalf("cleanup must keep the completed image and the preexisting image: %v, %v", entries, err)
	}
	contents, err := os.ReadFile(kept)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("cleanup changed existing image: %q, %v", contents, err)
	}
	paths, err := SaveResponse(context.Background(), encodedResponse(t, valid), directory, "robot")
	if err != nil || len(paths) != 1 || filepath.Base(paths[0]) != "robot-3.png" {
		t.Fatalf("cleaned filename was not available for retry: %v, %v", paths, err)
	}
}

func TestSaveResponseRejectsInvalidDataAndCleansUp(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(imageFixtures(t)[0])
	// A valid signature followed by enough decoded data to exercise streaming,
	// then invalid base64 after bytes have already been written.
	large := append(append([]byte(nil), imageFixtures(t)[0]...), bytes.Repeat([]byte{1}, 64*1024)...)
	invalidLate := base64.StdEncoding.EncodeToString(large) + "!"
	cases := map[string][]byte{
		"empty body":        nil,
		"invalid JSON":      []byte(`{"data":`),
		"empty response":    []byte(`{}`),
		"empty data":        []byte(`{"data":[]}`),
		"wrong data type":   []byte(`{"data":{}}`),
		"URL response":      []byte(`{"data":[{"url":"https://example.invalid/image.png"}]}`),
		"empty base64":      []byte(`{"data":[{"b64_json":""}]}`),
		"invalid base64":    encodedResponse(t, "not base64!"),
		"unsupported bytes": imageResponse(t, []byte("not an image")),
		"truncated header":  imageResponse(t, []byte("\x89PNG")),
		"bad second image":  encodedResponse(t, valid, invalidLate),
		"missing second":    encodedResponse(t, valid, ""),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			kept := filepath.Join(directory, "gpt-image-existing.png")
			if err := os.WriteFile(kept, []byte("keep existing file"), 0600); err != nil {
				t.Fatal(err)
			}
			paths, err := SaveResponse(context.Background(), raw, directory)
			wantSaved := 0
			if name == "bad second image" || name == "missing second" {
				wantSaved = 1
			}
			if err == nil || len(paths) != wantSaved {
				t.Fatalf("invalid response saved: %v, %v", paths, err)
			}
			entries, readErr := os.ReadDir(directory)
			if readErr != nil || len(entries) != 1+wantSaved {
				t.Fatalf("incomplete output was not cleaned up or completed output was lost: %v, %v", entries, readErr)
			}
			for _, path := range paths {
				contents, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(contents, imageFixtures(t)[0]) {
					t.Fatalf("completed image changed: %q, %v", contents, readErr)
				}
			}
			contents, readErr := os.ReadFile(kept)
			if readErr != nil || string(contents) != "keep existing file" {
				t.Fatalf("existing file was changed: %q, %v", contents, readErr)
			}
		})
	}
}

func TestSaveResponseCancellation(t *testing.T) {
	raw := imageResponse(t, imageFixtures(t)[0])
	t.Run("before saving", func(t *testing.T) {
		directory := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		paths, err := SaveResponse(ctx, raw, directory)
		if !errors.Is(err, context.Canceled) || len(paths) != 0 {
			t.Fatalf("cancellation = %v, %v", paths, err)
		}
		assertEmptyDirectory(t, directory)
	})
	t.Run("after partial write", func(t *testing.T) {
		directory := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Cancel while saving the second image, after the first has completed.
		checking := checkingContext{Context: ctx, check: func() {
			entries, err := os.ReadDir(directory)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) == 2 {
				cancel()
			}
		}}
		fixture := imageFixtures(t)[0]
		paths, err := SaveResponse(checking, imageResponse(t, fixture, fixture), directory)
		if !errors.Is(err, context.Canceled) || len(paths) != 1 {
			t.Fatalf("partial-write cancellation = %v, %v", paths, err)
		}
		contents, readErr := os.ReadFile(paths[0])
		if readErr != nil || !bytes.Equal(contents, fixture) {
			t.Fatalf("cancellation lost the completed image: %v", readErr)
		}
		entries, readErr := os.ReadDir(directory)
		if readErr != nil || len(entries) != 1 {
			t.Fatalf("cancellation left incomplete files: %v, %v", entries, readErr)
		}
	})
}

type checkingContext struct {
	context.Context
	check func()
}

func (c checkingContext) Err() error {
	c.check()
	return c.Context.Err()
}

func assertEmptyDirectory(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("expected empty directory, got %v, %v", entries, err)
	}
}

func imageFixtures(t *testing.T) [][]byte {
	t.Helper()
	pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	pixel.Set(0, 0, color.RGBA{R: 255, A: 255})
	var pngBytes, jpegBytes bytes.Buffer
	if err := png.Encode(&pngBytes, pixel); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegBytes, pixel, nil); err != nil {
		t.Fatal(err)
	}
	// A synthetic 1x1 WebP image, with no third-party encoder dependency.
	webpBytes, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	return [][]byte{pngBytes.Bytes(), jpegBytes.Bytes(), webpBytes}
}

func imageResponse(t *testing.T, images ...[]byte) []byte {
	t.Helper()
	encoded := make([]string, len(images))
	for i, image := range images {
		encoded[i] = base64.StdEncoding.EncodeToString(image)
	}
	return encodedResponse(t, encoded...)
}

func encodedResponse(t *testing.T, images ...string) []byte {
	t.Helper()
	data := make([]map[string]string, len(images))
	for i, encoded := range images {
		data[i] = map[string]string{"b64_json": encoded}
	}
	raw, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
