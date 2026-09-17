package cmd

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/openai/openai-cli/internal/imagepreview"
	"github.com/urfave/cli/v3"
)

// These workflow checks use synthetic files and an in-memory font bridge.
// They never access Terminal, register native fonts, or call an API.
func TestImageInlineSetupAndRepairHaveNoProfileCreationOptions(t *testing.T) {
	var commands []*cli.Command
	for _, resource := range Command.Commands {
		if resource.Name != "images" {
			continue
		}
		for _, group := range resource.Commands {
			if group.Name != "inline" {
				continue
			}
			for _, command := range group.Commands {
				if command.Name == "setup" || command.Name == "repair" {
					commands = append(commands, command)
				}
			}
		}
	}
	if len(commands) != 2 {
		t.Fatalf("expected setup and repair, found %d", len(commands))
	}
	for _, command := range commands {
		for _, argument := range []string{"--new-window", "--no-open", "--help"} {
			t.Run(command.Name+argument, func(t *testing.T) {
				var output bytes.Buffer
				called := false
				// Use the registered command's flags/help, with a harmless action
				// so a regression cannot reach native Terminal APIs during tests.
				probe := &cli.Command{Name: command.Name, Usage: command.Usage, Description: command.Description, Flags: command.Flags, Writer: &output, ErrWriter: &output,
					Action: func(context.Context, *cli.Command) error { called = true; return nil }}
				err := probe.Run(context.Background(), []string{command.Name, argument})
				if called {
					t.Fatal("profile option reached the command action")
				}
				if argument == "--help" {
					if err != nil {
						t.Fatal(err)
					}
					for _, obsolete := range []string{"--new-window", "--no-open", "Open this profile", "NEW window"} {
						if strings.Contains(output.String(), obsolete) {
							t.Fatalf("help advertises removed profile workflow: %q", output.String())
						}
					}
				} else if err == nil {
					t.Fatalf("removed profile option %s was accepted", argument)
				}
			})
		}
	}
}

func TestImageInlineSetupRepairsMissingFontAndKeepsImageMapping(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	var output bytes.Buffer
	if err := prepareImageInlineTestGallery(ctx, dir, bridge); err != nil {
		t.Fatal(err)
	}
	path := imageInlineFixture(t, "original.png", color.NRGBA{230, 120, 20, 255})
	output.Reset()
	if err := displayImageFont(ctx, &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 12}, bridge.services()); err != nil {
		t.Fatal(err)
	}
	oldText := output.String()
	before := imageInlineState(t, dir)
	if err := os.Remove(before.FontPath); err != nil {
		t.Fatal(err)
	}
	bridge.calls = nil
	output.Reset()
	if err := prepareImageInlineTestGallery(ctx, dir, bridge); err != nil {
		t.Fatal(err)
	}
	after := imageInlineState(t, dir)
	if after.ID != before.ID || after.ProfileName != before.ProfileName || after.ImageCount != before.ImageCount || after.UsedGlyphs != before.UsedGlyphs || after.MaxColumns != before.MaxColumns {
		t.Fatalf("repair changed the gallery's image mapping: before=%+v after=%+v", before, after)
	}
	if after.FontPath == before.FontPath || after.PostScript == before.PostScript || after.Revision != before.Revision+1 {
		t.Fatalf("repair did not create a fresh font identity: before=%+v after=%+v", before, after)
	}
	if !reflect.DeepEqual(bridge.calls, []string{"register"}) {
		t.Fatalf("repair accessed an unrelated native operation: %v", bridge.calls)
	}
	output.Reset()
	if err := displayImageFont(ctx, &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 80}, bridge.services()); err != nil {
		t.Fatal(err)
	}
	if output.String() != oldText || imageInlineState(t, dir) != after {
		t.Fatal("repair changed old scrollback characters or repeated-image deduplication")
	}
}

func TestImageInlineResetRecoversMissingArtifactsAndKeepsOriginal(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	var output bytes.Buffer
	if err := prepareImageInlineTestGallery(ctx, dir, bridge); err != nil {
		t.Fatal(err)
	}
	path := imageInlineFixture(t, "original.png", color.NRGBA{20, 120, 230, 255})
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := displayImageFont(ctx, &output, dir, path, "/dev/ttys001", imagepreview.Size{Columns: 12}, bridge.services()); err != nil {
		t.Fatal(err)
	}
	before := imageInlineState(t, dir)
	thumbs, err := filepath.Glob(filepath.Join(dir, "images", "*.png"))
	if err != nil || len(thumbs) != 1 {
		t.Fatalf("missing synthetic thumbnail: %v %v", thumbs, err)
	}
	for _, missing := range []string{before.FontPath, thumbs[0]} {
		if err := os.Remove(missing); err != nil {
			t.Fatal(err)
		}
	}
	bridge.calls = nil
	output.Reset()
	if err := resetImageFontCache(ctx, &output, dir, bridge.services()); err != nil {
		t.Fatal(err)
	}
	if len(bridge.calls) < 2 || bridge.calls[0] != "unused" {
		t.Fatalf("reset removed fonts before checking live tabs: %v", bridge.calls)
	}
	if bridge.registered[before.FontPath] || imageInlineState(t, dir).Initialized {
		t.Fatal("reset retained the missing font registration or gallery state")
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, original) {
		t.Fatal("reset altered the original image")
	}
	for _, phrase := range []string{"Original images", "scrollback", "images inline setup"} {
		if !strings.Contains(output.String(), phrase) {
			t.Fatalf("reset omitted consequence or recovery: %q", output.String())
		}
	}
}

func TestImageInlineResetRefusesActiveProfile(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	var output bytes.Buffer
	if err := prepareImageInlineTestGallery(ctx, dir, bridge); err != nil {
		t.Fatal(err)
	}
	before := imageInlineState(t, dir)
	bridge.unusedErr = errors.New("synthetic profile is still open")
	bridge.calls = nil
	output.Reset()
	err := resetImageFontCache(ctx, &output, dir, bridge.services())
	if !errors.Is(err, bridge.unusedErr) || !reflect.DeepEqual(bridge.calls, []string{"unused"}) || output.Len() != 0 || imageInlineState(t, dir) != before {
		t.Fatalf("unsafe reset: calls=%v error=%v output=%q", bridge.calls, err, output.String())
	}
}

func TestImageInlineStatusExplainsCapacityAndCleanup(t *testing.T) {
	state := imagegallery.State{ProfileName: "OpenAI Images 0123abcd", ImageCount: 2, UsedGlyphs: 1024}
	usage := imagegallery.Usage{Bytes: 2 * 1024 * 1024}
	var output bytes.Buffer
	if err := printImageFontStatus(&output, state, usage, "/private/synthetic-cache", false); err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"Cached images: 2", "about 10 more square previews", "other shapes vary", "2.0 MiB"} {
		if !strings.Contains(output.String(), phrase) {
			t.Fatalf("status omitted useful capacity: %q", output.String())
		}
	}
	if strings.Contains(output.String(), "Preview cells:") || strings.Contains(output.String(), "/private/synthetic-cache") {
		t.Fatalf("default status exposed implementation details: %q", output.String())
	}
	output.Reset()
	state.UsedGlyphs = 6000
	usage.MissingFiles = 2
	if err := printImageFontStatus(&output, state, usage, "/private/synthetic-cache", true); err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"about 0 more", "images inline repair", "images inline reset", "old image scrollback", "saved originals", "Preview cells: 6000 / 6400", "/private/synthetic-cache"} {
		if !strings.Contains(output.String(), phrase) {
			t.Fatalf("detailed status omitted recovery or capacity: %q", output.String())
		}
	}
}

func TestImageInlineWidthChecksKnownAndUnknownWindowSizes(t *testing.T) {
	for _, tt := range []struct {
		name            string
		cached, columns int
		wantError       bool
	}{
		{"unknown width", 0, 0, true},
		{"empty too narrow", 0, 8, true},
		{"empty minimum", 0, 9, false},
		{"cached exact width wraps", 32, 32, true},
		{"cached minimum", 32, 33, false},
		{"cached wider", 32, 80, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := checkImageFontWidth(imagegallery.State{MaxColumns: tt.cached}, imagepreview.Size{Columns: tt.columns})
			if (err != nil) != tt.wantError {
				t.Fatalf("width check returned %v", err)
			}
		})
	}
}

func TestImageInlineVisualCheckReusesSampleAndRemovesTemporaryFile(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "gallery")
	bridge := newFakeImageFontBridge(t)
	var output bytes.Buffer
	if err := prepareImageInlineTestGallery(ctx, dir, bridge); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	bridge.calls = nil
	if err := runImageInlineTest(ctx, &output, dir, "/dev/ttys001", imagepreview.Size{Columns: 80}, bridge.services()); err != nil {
		t.Fatal(err)
	}
	first := imageInlineState(t, dir)
	if first.ImageCount != 1 || first.UsedGlyphs != 256 || !containsImageGlyphs(output.String()) {
		t.Fatalf("visual check did not show its bounded sample: state=%+v", first)
	}
	if !strings.Contains(strings.Join(bridge.calls, ","), "check") {
		t.Fatalf("visual check did not check the target profile: %v", bridge.calls)
	}
	output.Reset()
	if err := runImageInlineTest(ctx, &output, dir, "/dev/ttys001", imagepreview.Size{Columns: 80}, bridge.services()); err != nil {
		t.Fatal(err)
	}
	if after := imageInlineState(t, dir); after != first || !containsImageGlyphs(output.String()) {
		t.Fatalf("repeated test consumed another image entry: before=%+v after=%+v", first, after)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".visual-check-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("visual check retained temporary source images: %v %v", leftovers, err)
	}
}

func TestImageInlineVisualCheckFailurePreservesCache(t *testing.T) {
	for _, stage := range []string{"check", "unknown width", "activate"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			dir := filepath.Join(t.TempDir(), "gallery")
			bridge := newFakeImageFontBridge(t)
			var output bytes.Buffer
			if err := prepareImageInlineTestGallery(ctx, dir, bridge); err != nil {
				t.Fatal(err)
			}
			before := imageInlineState(t, dir)
			failure := errors.New("synthetic preview check failure")
			size := imagepreview.Size{Columns: 80}
			switch stage {
			case "check":
				bridge.checkErr = failure
			case "unknown width":
				size.Columns = 0
			case "activate":
				bridge.activateErr = failure
			}
			output.Reset()
			err := runImageInlineTest(ctx, &output, dir, "/dev/ttys001", size, bridge.services())
			if err == nil || stage != "unknown width" && !errors.Is(err, failure) || containsImageGlyphs(output.String()) || imageInlineState(t, dir) != before {
				t.Fatalf("failed visual check published glyphs or cache state: error=%v output=%q", err, output.String())
			}
			leftovers, globErr := filepath.Glob(filepath.Join(dir, ".visual-check-*"))
			if globErr != nil || len(leftovers) != 0 {
				t.Fatalf("failed check retained temporary source images: %v %v", leftovers, globErr)
			}
		})
	}
}
