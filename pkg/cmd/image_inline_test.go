package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/openai/openai-cli/internal/imagepreview"
)

type fakeImageFontBridge struct {
	t           *testing.T
	registered  map[string]bool
	calls       []string
	checkErr    error
	activateErr error
	inspectErr  error
	unusedErr   error
	currentFont string
	fontSize    float64
	onActivate  func(string)
}

func newFakeImageFontBridge(t *testing.T) *fakeImageFontBridge {
	return &fakeImageFontBridge{t: t, registered: map[string]bool{}, fontSize: 16}
}

func (f *fakeImageFontBridge) services() imageFontServices {
	return imageFontServices{
		register: func(_ context.Context, path string) error {
			f.calls = append(f.calls, "register")
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				f.t.Fatalf("registration preceded font file preparation: %v", err)
			}
			if f.registered[path] {
				return &imagefontmac.NativeError{Operation: "register", Code: 105}
			}
			f.registered[path] = true
			return nil
		},
		unregister: func(_ context.Context, path string) error {
			f.calls = append(f.calls, "unregister")
			delete(f.registered, path)
			return nil
		},
		check: func(_ context.Context, name, tty string) error {
			f.calls = append(f.calls, "check")
			if !strings.HasPrefix(name, "OpenAI Images ") || tty != "/dev/ttys001" {
				f.t.Fatal("preview checked the wrong profile or tty")
			}
			return f.checkErr
		},
		activate: func(_ context.Context, name, tty, font string) error {
			f.calls = append(f.calls, "activate")
			if !strings.HasPrefix(font, "OpenAIImages-"+strings.TrimPrefix(name, "OpenAI Images ")+"-") || tty != "/dev/ttys001" {
				f.t.Fatal("preview activated a font from a different gallery")
			}
			if f.onActivate != nil {
				f.onActivate(font)
			}
			if f.activateErr == nil {
				f.currentFont = font
			}
			return f.activateErr
		},
		inspect: func(_ context.Context, name, tty string) (imagefontmac.ProfileStatus, error) {
			f.calls = append(f.calls, "inspect")
			if !strings.HasPrefix(name, "OpenAI Images ") || tty != "/dev/ttys001" {
				f.t.Fatal("inspection used the wrong profile or tty")
			}
			return imagefontmac.ProfileStatus{FontName: f.currentFont, FontSize: f.fontSize, ProfileID: 42, ProfileName: "Pro"}, f.inspectErr
		},
		unused: func(_ context.Context, name string) error {
			f.calls = append(f.calls, "unused")
			if !strings.HasPrefix(name, "OpenAI Images ") {
				f.t.Fatal("reset checked an unowned profile")
			}
			return f.unusedErr
		},
	}
}

// Legacy-renderer fixtures need an initialized gallery and a simulated selected
// font, not a saved or imported Terminal profile.
func prepareImageInlineTestGallery(ctx context.Context, dir string, bridge *fakeImageFontBridge) error {
	gallery, err := prepareImageFontGallery(ctx, dir, bridge.services())
	if err != nil {
		return err
	}
	bridge.currentFont = gallery.State().PostScript
	return gallery.Close()
}

func imageInlineState(t *testing.T, dir string) imagegallery.State {
	t.Helper()
	gallery, err := imagegallery.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer gallery.Close()
	return gallery.State()
}

func imageInlineFixture(t *testing.T, name string, shade color.NRGBA) string {
	t.Helper()
	data := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			data.SetNRGBA(x, y, shade)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, data); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImageInlineGalleryPreparationIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	ctx := context.Background()
	if err := prepareImageInlineTestGallery(ctx, dir, bridge); err != nil {
		t.Fatal(err)
	}
	before := imageInlineState(t, dir)
	if !before.Initialized || before.ImageCount != 0 || !reflect.DeepEqual(bridge.calls, []string{"register"}) {
		t.Fatalf("state=%+v calls=%v", before, bridge.calls)
	}
	assertNoCurrentImageFontProfile(t, dir)
	bridge.calls = nil
	if err := prepareImageInlineTestGallery(ctx, dir, bridge); err != nil {
		t.Fatal(err)
	}
	if after := imageInlineState(t, dir); after != before {
		t.Fatalf("setup replaced an existing gallery: before=%+v after=%+v", before, after)
	}
	if !reflect.DeepEqual(bridge.calls, []string{"register"}) {
		t.Fatalf("gallery preparation accessed unrelated native operations: %v", bridge.calls)
	}
	assertNoCurrentImageFontProfile(t, dir)
}

func TestImageInlineGalleryRegistrationFailureRemainsRecoverable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	failure := errors.New("synthetic registration failure")
	services := bridge.services()
	services.register = func(context.Context, string) error { return failure }
	if gallery, err := prepareImageFontGallery(context.Background(), dir, services); !errors.Is(err, failure) || gallery != nil {
		t.Fatalf("failed registration returned gallery=%v error=%v", gallery, err)
	}
	before := imageInlineState(t, dir)
	if !before.Initialized {
		t.Fatal("registration failure lost retryable gallery state")
	}
	if err := prepareImageInlineTestGallery(context.Background(), dir, bridge); err != nil {
		t.Fatal(err)
	}
	if after := imageInlineState(t, dir); after != before {
		t.Fatal("retry replaced the prepared gallery")
	}
	assertNoCurrentImageFontProfile(t, dir)
}

func TestImageInlinePreviewRetainsImagesAndDeduplicates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	ctx := context.Background()
	if err := prepareImageInlineTestGallery(ctx, dir, bridge); err != nil {
		t.Fatal(err)
	}
	red := imageInlineFixture(t, "red.png", color.NRGBA{255, 40, 20, 255})
	blue := imageInlineFixture(t, "blue.png", color.NRGBA{20, 40, 255, 255})
	var output bytes.Buffer
	stateBytes := func() []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, "state.json"))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	before := stateBytes()
	bridge.onActivate = func(string) {
		if output.Len() != 0 || !bytes.Equal(stateBytes(), before) {
			t.Fatal("private glyphs or state were published before font activation")
		}
	}
	bridge.calls = nil
	if err := displayImageFont(ctx, &output, dir, red, "/dev/ttys001", imagepreview.Size{Columns: 12, Rows: 30}, bridge.services()); err != nil {
		t.Fatal(err)
	}
	firstText := output.String()
	first := imageInlineState(t, dir)
	oldFont, err := os.ReadFile(first.FontPath)
	if err != nil {
		t.Fatal(err)
	}
	if first.ImageCount != 1 || !containsImageGlyphs(firstText) || !reflect.DeepEqual(bridge.calls, []string{"register", "check", "register", "activate"}) {
		t.Fatalf("state=%+v calls=%v glyphs=%v", first, bridge.calls, containsImageGlyphs(firstText))
	}
	before = stateBytes()
	output.Reset()
	if err := displayImageFont(ctx, &output, dir, blue, "/dev/ttys001", imagepreview.Size{Columns: 12, Rows: 30}, bridge.services()); err != nil {
		t.Fatal(err)
	}
	secondText := output.String()
	second := imageInlineState(t, dir)
	if second.ImageCount != 2 || second.FontPath == first.FontPath || second.PostScript == first.PostScript || second.UsedGlyphs != 2*first.UsedGlyphs {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	for _, r := range secondText {
		if r != '\n' && strings.ContainsRune(firstText, r) {
			t.Fatal("second image reused a character from the first image")
		}
	}
	if data, err := os.ReadFile(first.FontPath); err != nil || !bytes.Equal(data, oldFont) {
		t.Fatal("earlier immutable font was removed or modified")
	}
	before = stateBytes()
	output.Reset()
	if err := displayImageFont(ctx, &output, dir, red, "/dev/ttys001", imagepreview.Size{Columns: 80, Rows: 30}, bridge.services()); err != nil {
		t.Fatal(err)
	}
	if output.String() != firstText || imageInlineState(t, dir) != second {
		t.Fatal("repeated image changed old glyphs or consumed another revision")
	}
}

func TestImageInlinePreviewFailuresNeverPublish(t *testing.T) {
	for _, stage := range []string{"check", "other profile", "activate", "new registration"} {
		t.Run(stage, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "gallery")
			bridge := newFakeImageFontBridge(t)
			if err := prepareImageInlineTestGallery(context.Background(), dir, bridge); err != nil {
				t.Fatal(err)
			}
			before := imageInlineState(t, dir)
			failure := errors.New("synthetic failure")
			if stage == "other profile" {
				failure = imagefontmac.ErrOtherProfile
			}
			if stage == "check" {
				bridge.checkErr = failure
			} else if stage == "other profile" {
				bridge.checkErr = fmt.Errorf("native check: %w", failure)
			} else if stage == "activate" {
				bridge.activateErr = failure
			}
			services := bridge.services()
			if stage == "new registration" {
				register := services.register
				services.register = func(ctx context.Context, path string) error {
					if path != before.FontPath {
						return failure
					}
					return register(ctx, path)
				}
			}
			var output bytes.Buffer
			path := imageInlineFixture(t, "image.png", color.NRGBA{100, 30, 255, 255})
			err := displayImageFont(context.Background(), &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 12}, services)
			if !errors.Is(err, failure) || output.Len() != 0 || imageInlineState(t, dir) != before {
				t.Fatalf("failure published state or output: error=%v output=%q", err, output.String())
			}
		})
	}
}

func TestImageInlinePreviewRequiresEnoughWidth(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	var output bytes.Buffer
	ctx := context.Background()
	if err := prepareImageInlineTestGallery(ctx, dir, bridge); err != nil {
		t.Fatal(err)
	}
	path := imageInlineFixture(t, "image.png", color.NRGBA{150, 30, 10, 255})
	before := imageInlineState(t, dir)
	output.Reset()
	err := displayImageFont(ctx, &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 8}, bridge.services())
	if err == nil || !strings.Contains(err.Error(), "at least 9 columns") || output.Len() != 0 || imageInlineState(t, dir) != before {
		t.Fatal("narrow terminal emitted or committed an image")
	}
	if err := displayImageFont(ctx, &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 40}, bridge.services()); err != nil {
		t.Fatal(err)
	}
	before = imageInlineState(t, dir)
	output.Reset()
	err = displayImageFont(ctx, &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 20}, bridge.services())
	if err == nil || !strings.Contains(err.Error(), "at least 33 columns") || output.Len() != 0 || imageInlineState(t, dir) != before {
		t.Fatal("cached image was reflowed into a narrower terminal")
	}
}

func TestImageInlineRegistrationCollisionPolicy(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, code := range []int{105, 104, 202} {
			failure := fmt.Errorf("wrapped: %w", &imagefontmac.NativeError{Operation: "register", Code: code})
			err := registerImageFont(context.Background(), imageFontServices{register: func(context.Context, string) error { return failure }}, "font.ttf", existing)
			if (err == nil) != (existing && code == 105) {
				t.Fatalf("existing=%v code=%d error=%v", existing, code, err)
			}
		}
	}
	plain := errors.New("CoreText 105")
	if err := registerImageFont(context.Background(), imageFontServices{register: func(context.Context, string) error { return plain }}, "font.ttf", true); !errors.Is(err, plain) {
		t.Fatal("non-native registration errors were suppressed")
	}
}

func TestImageInlineEnvironmentPolicy(t *testing.T) {
	for _, tt := range []struct {
		name, goos, key, value string
		want                   bool
	}{
		{"local", "darwin", "", "", true},
		{"linux", "linux", "", "", false},
		{"other terminal", "darwin", "TERM_PROGRAM", "iTerm.app", false},
		{"ssh connection", "darwin", "SSH_CONNECTION", "remote", false},
		{"ssh client", "darwin", "SSH_CLIENT", "remote", false},
		{"ssh tty", "darwin", "SSH_TTY", "remote", false},
		{"tmux", "darwin", "TMUX", "socket", false},
		{"screen", "darwin", "STY", "session", false},
		{"zellij", "darwin", "ZELLIJ", "session", false},
		{"ci", "darwin", "CI", "true", false},
		{"ci false", "darwin", "CI", "false", true},
		{"ci zero", "darwin", "CI", "0", true},
		{"dumb", "darwin", "TERM", "dumb", false},
		{"screen term", "darwin", "TERM", "screen-256color", false},
		{"tmux term", "darwin", "TERM", "tmux-256color", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM": "xterm-256color"}
			env[tt.key] = tt.value
			if got := localAppleImageTerminal(tt.goos, func(key string) string { return env[key] }); got != tt.want {
				t.Fatalf("got=%v want=%v", got, tt.want)
			}
		})
	}
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "TMUX", "STY", "ZELLIJ", "CI"} {
		t.Setenv(key, "")
	}
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("TERM", "xterm-256color")
	var output bytes.Buffer
	if handled, err := tryImageFontPreview(context.Background(), &output, "unused.png", imagepreview.Size{}); handled || err != nil || output.Len() != 0 {
		t.Fatal("nonterminal output reached the native gallery workflow")
	}
	if err := preflightImageFont(context.Background(), &output); err != nil || output.Len() != 0 {
		t.Fatal("generation preflight touched native setup for nonterminal output")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if handled, err := tryImageFontPreview(context.Background(), writer, "unused.png", imagepreview.Size{}); handled || err != nil {
		t.Fatal("pipe output reached the native gallery workflow")
	}
	if err := preflightImageFont(context.Background(), writer); err != nil {
		t.Fatal("generation preflight touched native setup for a pipe")
	}
}

func containsImageGlyphs(text string) bool {
	for _, r := range text {
		if r >= '\ue000' && r <= '\uf8ff' {
			return true
		}
	}
	return false
}
