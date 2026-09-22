package custom

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagepreview"
	"golang.org/x/image/font/gofont/gomono"
)

// Current-tab orchestration uses only the existing in-memory bridge. These
// tests never invoke Terminal, register a native font, or access user caches.
func currentImageFontServices(t *testing.T, bridge *fakeImageFontBridge, tty string) imageFontServices {
	t.Helper()
	services := bridge.services()
	services.snapshot = func(_ context.Context, _, selectedTTY string) (imagefontmac.ProfileStatus, error) {
		if selectedTTY != tty {
			t.Fatal("snapshot lost exact tty")
		}
		name := bridge.currentFont
		if name == "" {
			name = "GoMono"
		}
		return imagefontmac.ProfileStatus{FontName: name, FontSize: bridge.fontSize, ProfileID: 42, ProfileName: "Pro"}, nil
	}
	services.source = func(_ context.Context, _ string, size int) (imagefontmac.SourceFont, error) {
		return imageTypographyFixture(t, size), nil
	}
	services.preserve = func(_ context.Context, name, selectedTTY, font string, before imagefontmac.ProfileStatus) error {
		bridge.calls = append(bridge.calls, "switch")
		if selectedTTY != tty || !strings.HasPrefix(font, "OpenAIImages-"+strings.TrimPrefix(name, "OpenAI Images ")+"-") {
			t.Fatalf("font override lost the caller or gallery identity: %q %q %q", name, selectedTTY, font)
		}
		bridge.currentFont = font
		if before.FontSize != bridge.fontSize {
			t.Fatal("font size changed")
		}
		return nil
	}
	return services
}

func assertCurrentImageFontCalls(t *testing.T, bridge *fakeImageFontBridge) {
	t.Helper()
	valid := len(bridge.calls) >= 3 && len(bridge.calls) <= 4 && bridge.calls[len(bridge.calls)-1] == "switch"
	for _, call := range bridge.calls[:len(bridge.calls)-1] {
		valid = valid && call == "register"
	}
	if !valid {
		t.Fatalf("current-tab setup must register and override its font once: %v", bridge.calls)
	}
}

func assertNoCurrentImageFontProfile(t *testing.T, dir string) {
	t.Helper()
	profiles, err := filepath.Glob(filepath.Join(dir, "*.terminal"))
	if err != nil || len(profiles) != 0 {
		t.Fatalf("current-tab setup created an unnecessary profile: files=%v err=%v", profiles, err)
	}
}

func TestImageInlineCurrentSetupSelectsExactTabWithoutOpeningWindow(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	services := currentImageFontServices(t, bridge, "/dev/ttys007")
	var output bytes.Buffer
	if err := setupCurrentImageFont(ctx, &output, dir, "/dev/ttys007", services); err != nil {
		t.Fatal(err)
	}
	before := imageInlineState(t, dir)
	if !before.Initialized || before.ImageCount != 0 || !bridge.registered[before.FontPath] {
		t.Fatalf("fresh setup did not initialize and register its gallery: %+v", before)
	}
	assertCurrentImageFontCalls(t, bridge)
	assertNoCurrentImageFontProfile(t, dir)
	bridge.calls = nil
	services.preserve = func(_ context.Context, name, tty, font string, _ imagefontmac.ProfileStatus) error {
		bridge.calls = append(bridge.calls, "switch")
		if name != before.ProfileName || tty != "/dev/ttys007" || font != bridge.currentFont || !bridge.registered[before.FontPath] {
			t.Fatalf("override lost exact caller or registered gallery identity: %q %q %q", name, tty, font)
		}
		return nil
	}
	output.Reset()
	if err := setupCurrentImageFont(ctx, &output, dir, "/dev/ttys007", services); err != nil {
		t.Fatal(err)
	}
	assertCurrentImageFontCalls(t, bridge)
	assertNoCurrentImageFontProfile(t, dir)
	if after := imageInlineState(t, dir); after != before {
		t.Fatalf("repeated setup changed existing gallery identity: before=%+v after=%+v", before, after)
	}
	text := strings.ToLower(output.String())
	if !strings.Contains(text, "enabled in this tab") || !strings.Contains(text, "profile") || !strings.Contains(text, "kept") || strings.Contains(text, "new window") {
		t.Fatalf("setup did not explain the current tab and preserved profile: %q", output.String())
	}
}

func TestImageInlineCurrentSetupCancellationDuringOverrideIsRecoverable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	services := currentImageFontServices(t, bridge, "/dev/ttys001")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	services.preserve = func(ctx context.Context, _, _, _ string, _ imagefontmac.ProfileStatus) error {
		bridge.calls = append(bridge.calls, "switch")
		cancel()
		return ctx.Err()
	}
	var output bytes.Buffer
	err := setupCurrentImageFont(ctx, &output, dir, "/dev/ttys001", services)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	// Opening the state proves the canceled operation released its gallery
	// lock. The initialized font remains available for a normal setup retry.
	before := imageInlineState(t, dir)
	if !before.Initialized || output.Len() != 0 {
		t.Fatalf("cancellation lost recoverable state or claimed success: %q", output.String())
	}
	assertCurrentImageFontCalls(t, bridge)
	assertNoCurrentImageFontProfile(t, dir)
	bridge.calls = nil
	services = currentImageFontServices(t, bridge, "/dev/ttys001")
	if err := setupCurrentImageFont(context.Background(), &output, dir, "/dev/ttys001", services); err != nil {
		t.Fatal(err)
	}
	assertCurrentImageFontCalls(t, bridge)
	if after := imageInlineState(t, dir); after != before {
		t.Fatalf("retry discarded prepared gallery: before=%+v after=%+v", before, after)
	}
}

func TestImageInlineCurrentSetupErrorsDoNotImportOrRetry(t *testing.T) {
	for _, cause := range []error{errors.New("synthetic permission denial"), imagefontmac.ErrOtherProfile, imagefontmac.ErrProfileMissing, errors.New("synthetic conflicting identity")} {
		t.Run(cause.Error(), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "gallery")
			bridge := newFakeImageFontBridge(t)
			services := currentImageFontServices(t, bridge, "/dev/ttys001")
			services.preserve = func(context.Context, string, string, string, imagefontmac.ProfileStatus) error {
				bridge.calls = append(bridge.calls, "switch")
				return cause
			}
			var output bytes.Buffer
			err := setupCurrentImageFont(context.Background(), &output, dir, "/dev/ttys001", services)
			if !errors.Is(err, cause) {
				t.Fatalf("override error was hidden: %v", err)
			}
			assertCurrentImageFontCalls(t, bridge)
			assertNoCurrentImageFontProfile(t, dir)
			before := imageInlineState(t, dir)
			if output.Len() != 0 || !before.Initialized {
				t.Fatalf("failure lost prepared state or claimed success: %q", output.String())
			}
			bridge.calls = nil
			services = currentImageFontServices(t, bridge, "/dev/ttys001")
			if err := setupCurrentImageFont(context.Background(), &output, dir, "/dev/ttys001", services); err != nil {
				t.Fatal(err)
			}
			assertCurrentImageFontCalls(t, bridge)
			if imageInlineState(t, dir) != before {
				t.Fatal("successful retry replaced the prepared gallery")
			}
		})
	}
}

func TestImageInlineCurrentSetupRegistrationFailureDoesNotChangeTab(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	services := currentImageFontServices(t, bridge, "/dev/ttys001")
	failure := errors.New("synthetic registration failure")
	services.register = func(context.Context, string) error {
		bridge.calls = append(bridge.calls, "register")
		return failure
	}
	var output bytes.Buffer
	err := setupCurrentImageFont(context.Background(), &output, dir, "/dev/ttys001", services)
	if !errors.Is(err, failure) || !reflect.DeepEqual(bridge.calls, []string{"register"}) || output.Len() != 0 {
		t.Fatalf("registration failure changed the tab or claimed success: err=%v calls=%v output=%q", err, bridge.calls, output.String())
	}
	assertNoCurrentImageFontProfile(t, dir)
	before := imageInlineState(t, dir)
	if !before.Initialized {
		t.Fatal("registration failure did not retain recoverable gallery metadata")
	}
	// A retry must acquire the released lock and reuse/recover prepared files.
	bridge.calls = nil
	services = currentImageFontServices(t, bridge, "/dev/ttys001")
	if err := setupCurrentImageFont(context.Background(), &output, dir, "/dev/ttys001", services); err != nil {
		t.Fatal(err)
	}
	assertCurrentImageFontCalls(t, bridge)
	if after := imageInlineState(t, dir); after != before {
		t.Fatalf("retry discarded registration preparation: before=%+v after=%+v", before, after)
	}
}

func TestImageInlineCurrentSetupPreservesEarlierImageMappings(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	services := currentImageFontServices(t, bridge, "/dev/ttys001")
	var output bytes.Buffer
	if err := setupCurrentImageFont(ctx, &output, dir, "/dev/ttys001", services); err != nil {
		t.Fatal(err)
	}
	path := imageInlineFixture(t, "synthetic.png", color.NRGBA{R: 235, G: 90, B: 20, A: 255})
	output.Reset()
	if err := displayImageFont(ctx, &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 12}, services); err != nil {
		t.Fatal(err)
	}
	before := imageInlineState(t, dir)
	characters := output.String()
	fontBefore, err := os.ReadFile(before.FontPath)
	if err != nil {
		t.Fatal(err)
	}
	bridge.calls = nil
	output.Reset()
	if err := setupCurrentImageFont(ctx, &output, dir, "/dev/ttys001", services); err != nil {
		t.Fatal(err)
	}
	if imageInlineState(t, dir) != before || bridge.currentFont == before.PostScript {
		t.Fatal("repeated setup replaced the cumulative font or gallery identity")
	}
	assertCurrentImageFontCalls(t, bridge)
	assertNoCurrentImageFontProfile(t, dir)
	fontAfter, err := os.ReadFile(before.FontPath)
	if err != nil || !bytes.Equal(fontBefore, fontAfter) {
		t.Fatal("repeated setup mutated the existing font file")
	}
	output.Reset()
	if err := displayImageFont(ctx, &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 80}, services); err != nil {
		t.Fatal(err)
	}
	if output.String() != characters || imageInlineState(t, dir) != before {
		t.Fatal("repeated setup changed earlier scrollback mappings or image deduplication")
	}
}

func TestImageInlineCurrentSetupRepairsFontWithoutImport(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	services := currentImageFontServices(t, bridge, "/dev/ttys001")
	var output bytes.Buffer
	if err := setupCurrentImageFont(ctx, &output, dir, "/dev/ttys001", services); err != nil {
		t.Fatal(err)
	}
	path := imageInlineFixture(t, "synthetic.png", color.NRGBA{R: 20, G: 90, B: 235, A: 255})
	output.Reset()
	if err := displayImageFont(ctx, &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 12}, services); err != nil {
		t.Fatal(err)
	}
	characters := output.String()
	before := imageInlineState(t, dir)
	if err := os.Remove(before.FontPath); err != nil {
		t.Fatal(err)
	}
	delete(bridge.registered, before.FontPath)
	bridge.calls = nil
	output.Reset()
	if err := setupCurrentImageFont(ctx, &output, dir, "/dev/ttys001", services); err != nil {
		t.Fatal(err)
	}
	assertCurrentImageFontCalls(t, bridge)
	assertNoCurrentImageFontProfile(t, dir)
	after := imageInlineState(t, dir)
	if !after.Initialized || after.ID != before.ID || after.ImageCount != before.ImageCount || after.UsedGlyphs != before.UsedGlyphs || bridge.currentFont == after.PostScript {
		t.Fatalf("repair changed gallery identity or discarded mappings: before=%+v after=%+v", before, after)
	}
	output.Reset()
	if err := displayImageFont(ctx, &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 80}, services); err != nil {
		t.Fatal(err)
	}
	if output.String() != characters || imageInlineState(t, dir) != after {
		t.Fatal("font repair changed scrollback mappings or stopped deduplicating the image")
	}
}

func imageTypographyFixture(t *testing.T, size int) imagefontmac.SourceFont {
	t.Helper()
	tables := map[string][]byte{}
	data := gomono.TTF
	count := int(binary.BigEndian.Uint16(data[4:]))
	for i := 0; i < count; i++ {
		p := data[12+16*i:]
		offset, n := int(binary.BigEndian.Uint32(p[8:])), int(binary.BigEndian.Uint32(p[12:]))
		tables[string(p[:4])] = append([]byte(nil), data[offset:offset+n]...)
	}
	return imagefontmac.SourceFont{PostScript: "GoMono", Style: "Regular", Tables: tables, Ascent: float64(size) * .8, Descent: float64(size) * .2, Advance: float64(size) * .6, LineHeight: float64(size) * 1.2}
}
