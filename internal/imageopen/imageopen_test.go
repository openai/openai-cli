package imageopen

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOpenValidatedImageWithoutShellParsing(t *testing.T) {
	for _, format := range []string{"png", "jpg", "jpeg", "webp", "PNG"} {
		t.Run(format, func(t *testing.T) {
			original := imageData(t, strings.ToLower(format))
			path := filepath.Join(t.TempDir(), "--a $(do-not-run); [x] 名."+format)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			err := openWith(context.Background(), path, func() error { return nil }, func(ctx context.Context, got string) error {
				calls++
				if !filepath.IsAbs(got) || got != path || ctx.Err() != nil {
					t.Fatalf("launcher did not receive the intact absolute path: %q", got)
				}
				return nil
			})
			if err != nil || calls != 1 {
				t.Fatalf("open request: calls=%d, error=%v", calls, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, original) {
				t.Fatal("opening modified the saved image")
			}
		})
	}
}

func TestOpenRejectsInvalidFilesBeforeLaunch(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		dir  bool
	}{
		{"program.exe", imageData(t, "png"), false},
		{"vector.svg", []byte("<svg/>"), false},
		{"noextension", imageData(t, "png"), false},
		{"mismatch.jpg", imageData(t, "png"), false},
		{"corrupt.png", []byte("not an image"), false},
		{"directory.png", nil, true},
		{"missing.png", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name)
			if tc.dir {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if tc.data != nil {
				if err := os.WriteFile(path, tc.data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := openWith(context.Background(), path, func() error { return nil }, func(context.Context, string) error {
				t.Fatal("invalid image reached the viewer launcher")
				return nil
			})
			if err == nil {
				t.Fatal("invalid image accepted")
			}
		})
	}
}

func TestOpenCancellationAndLauncherErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "valid.png")
	if err := os.WriteFile(path, imageData(t, "png"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Open(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("already-canceled open: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	err := openWith(ctx, path, func() error { cancel(); return nil }, func(context.Context, string) error {
		t.Fatal("canceled request reached the launcher")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation during preflight: %v", err)
	}
	want := errors.New("viewer unavailable")
	for _, failPreflight := range []bool{true, false} {
		err := openWith(context.Background(), path, func() error {
			if failPreflight {
				return want
			}
			return nil
		}, func(context.Context, string) error {
			if failPreflight {
				t.Fatal("failed preflight reached launcher")
			}
			return want
		})
		if !errors.Is(err, want) {
			t.Fatalf("lost launcher/preflight error: %v", err)
		}
	}
}

func TestViewerAvailability(t *testing.T) {
	for _, tc := range []struct {
		name, goos, display, wayland string
		found, wantError             bool
	}{
		{"mac", "darwin", "", "", true, false},
		{"mac launcher missing", "darwin", "", "", false, true},
		{"linux x11", "linux", ":0", "", true, false},
		{"linux wayland", "linux", "", "wayland-0", true, false},
		{"linux headless", "linux", "", "", true, true},
		{"linux launcher missing", "linux", ":0", "", false, true},
		{"windows native", "windows", "", "", false, false},
		{"unsupported", "plan9", "", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkAvailable(tc.goos, func(key string) string {
				return map[string]string{"DISPLAY": tc.display, "WAYLAND_DISPLAY": tc.wayland, "SSH_CONNECTION": "fake remote connection"}[key]
			}, func(command string) (string, error) {
				if tc.goos == "windows" || tc.goos == "plan9" || (tc.goos == "linux" && tc.display == "" && tc.wayland == "") {
					t.Fatal("unexpected command lookup")
				}
				if !tc.found {
					return "", os.ErrNotExist
				}
				return command, nil
			})
			if (err != nil) != tc.wantError {
				t.Fatalf("availability: %v", err)
			}
		})
	}
}

func TestImagePathErrorsEscapeControlCharacters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows rejects control characters in filenames")
	}
	_, err := validateImage(context.Background(), filepath.Join(t.TempDir(), "missing\n\x1b[31m.png"))
	if err == nil || strings.ContainsAny(err.Error(), "\n\x1b") {
		t.Fatalf("unsafe path diagnostics: %q", err)
	}
}

func TestViewerCommandUsesOnePathAndRemovesCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "--literal $(do-not-run); 名.png")
	for _, detached := range []bool{false, true} {
		command := viewerCommand(context.Background(), "/usr/bin/open", path, detached)
		if !reflect.DeepEqual(command.Args, []string{"/usr/bin/open", path}) {
			t.Fatalf("viewer path was parsed as shell code: %q", command.Args)
		}
		if command.Stdin != nil || command.Stdout != nil || command.Stderr != nil {
			t.Fatal("viewer must not share CLI streams")
		}
		for _, entry := range command.Env {
			if strings.HasPrefix(strings.ToUpper(entry), "OPENAI_") {
				t.Fatal("viewer inherited an OpenAI environment variable")
			}
		}
	}
	environment := []string{
		"PATH=/fake/bin", "DISPLAY=:0", "OPENAI_API_KEY=fake-key", "OPENAI_ADMIN_KEY=fake-admin",
		"OPENAI_MTLS_CLIENT_KEY_FILE=/fake/private-key.pem", "openai_api_key=fake-lowercase", "HOME=/fake/home",
	}
	if got := viewerEnvironment(environment); !reflect.DeepEqual(got, []string{"PATH=/fake/bin", "DISPLAY=:0", "HOME=/fake/home"}) {
		t.Fatal("viewer environment did not remove only OpenAI configuration")
	}
}

func TestDetachedLauncherResultsAndReaping(t *testing.T) {
	failure := errors.New("no default image handler")
	for _, result := range []error{nil, failure} {
		process := &fakeViewerProcess{result: make(chan error, 1), waited: make(chan struct{})}
		process.result <- result
		if err := startDetached(context.Background(), process, time.Second); !errors.Is(err, result) {
			t.Fatalf("immediate launcher result was lost: %v", err)
		}
		<-process.waited
	}
	process := &fakeViewerProcess{startError: failure}
	if err := startDetached(context.Background(), process, time.Second); !errors.Is(err, failure) {
		t.Fatalf("start failure was lost: %v", err)
	}
	process = &fakeViewerProcess{result: make(chan error, 1), waited: make(chan struct{})}
	if err := startDetached(context.Background(), process, 5*time.Millisecond); err != nil {
		t.Fatalf("long-running viewer request failed: %v", err)
	}
	select {
	case <-process.waited:
		t.Fatal("long-running viewer was interrupted")
	default:
	}
	process.result <- nil
	select {
	case <-process.waited:
	case <-time.After(time.Second):
		t.Fatal("detached viewer was not reaped")
	}
}

func TestDetachedLauncherCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	process := &fakeViewerProcess{}
	if err := startDetached(ctx, process, time.Second); !errors.Is(err, context.Canceled) || process.started {
		t.Fatalf("already-canceled request started a viewer: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	process = &fakeViewerProcess{result: make(chan error, 1), waited: make(chan struct{}), afterStart: cancel}
	if err := startDetached(ctx, process, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled launcher wait returned %v", err)
	}
	process.result <- nil
	select {
	case <-process.waited:
	case <-time.After(time.Second):
		t.Fatal("canceled wait did not reap the launched process")
	}
}

type fakeViewerProcess struct {
	startError error
	started    bool
	afterStart func()
	result     chan error
	waited     chan struct{}
}

func (p *fakeViewerProcess) Start() error {
	p.started = true
	if p.afterStart != nil {
		p.afterStart()
	}
	return p.startError
}

func (p *fakeViewerProcess) Wait() error {
	err := <-p.result
	close(p.waited)
	return err
}

func imageData(t *testing.T, format string) []byte {
	t.Helper()
	if format == "webp" {
		data, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	var data bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	var err error
	if format == "jpg" || format == "jpeg" {
		err = jpeg.Encode(&data, img, nil)
	} else {
		err = png.Encode(&data, img)
	}
	if err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
