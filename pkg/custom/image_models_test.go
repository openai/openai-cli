package custom

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imagemodels"
	"github.com/urfave/cli/v3"
)

func imageModelTestRow(id string, status imagemodels.Status, failure imagemodels.Failure) imageModelRow {
	return imageModelRow{Result: imagemodels.Result{Entry: imagemodels.Entry{ID: id}, Status: status, Failure: failure}, Default: id == defaultSavedImageModel}
}

func TestImageModelsPresentationPartialAndHidden(t *testing.T) {
	report := imageModelsReport{Source: "live", DefaultModel: defaultSavedImageModel, Models: []imageModelRow{
		imageModelTestRow(defaultSavedImageModel, imagemodels.StatusVisible, ""),
		imageModelTestRow("gpt-image-2.5-flare", imagemodels.StatusUnknown, imagemodels.FailureTimeout),
		imageModelTestRow("gpt-image-1", imagemodels.StatusNotVisible, ""),
		imageModelTestRow("dall-e-2", imagemodels.StatusRetired, ""),
	}}
	var out strings.Builder
	if err := writeImageModels(&out, report, false, "./openai"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{defaultSavedImageModel, "default", "gpt-image-2.5-flare", "Could not check (timeout)", "2 retired or not visible", "./openai images models --all", "./openai images generate", "generation permissions can differ"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in output: %s", want, out.String())
		}
	}
	for _, unwanted := range []string{"gpt-image-1", "dall-e-2", "No known", "{", "\x1b"} {
		if strings.Contains(out.String(), unwanted) {
			t.Errorf("unexpected %q in output: %s", unwanted, out.String())
		}
	}
	out.Reset()
	if err := writeImageModels(&out, report, true, "./openai"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "gpt-image-1") || !strings.Contains(out.String(), "dall-e-2") {
		t.Fatalf("--all omitted checked models: %s", out.String())
	}
}

func TestImageModelsPresentationNeverTreatsTimeoutAsUnavailable(t *testing.T) {
	report := imageModelsReport{Source: "live", Models: []imageModelRow{
		imageModelTestRow(defaultSavedImageModel, imagemodels.StatusUnknown, imagemodels.FailureServer),
	}}
	var out strings.Builder
	if err := writeImageModels(&out, report, false, "./openai"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Could not check") || strings.Contains(out.String(), "No known") || strings.Contains(out.String(), "Choose a model") {
		t.Fatalf("timeout misrepresented visibility: %s", out.String())
	}
	message := imageModelsFailureMessage([]imagemodels.Result{report.Models[0].Result}, "./openai")
	if !strings.Contains(message, "API could not complete") || !strings.Contains(message, "./openai images models --offline") {
		t.Fatalf("no safe next step after failure: %s", message)
	}
}

func TestImageModelsPresentationRecommendsOnlyEligibleDefault(t *testing.T) {
	for _, test := range []struct {
		status      imagemodels.Status
		wantDefault bool
	}{
		{imagemodels.StatusVisible, true},
		{imagemodels.StatusNotChecked, true},
		{imagemodels.StatusRetired, false},
		{imagemodels.StatusNotVisible, false},
		{imagemodels.StatusUnknown, false},
	} {
		t.Run(string(test.status), func(t *testing.T) {
			report := imageModelsReport{Source: "live", DefaultModel: defaultSavedImageModel, Models: []imageModelRow{
				imageModelTestRow(defaultSavedImageModel, test.status, ""),
				imageModelTestRow("gpt-image-1", imagemodels.StatusVisible, ""),
			}}
			if test.status == imagemodels.StatusNotChecked {
				report.Source = "offline"
				report.Models[1].Status = imagemodels.StatusNotChecked
			}
			for _, all := range []bool{false, true} {
				var out strings.Builder
				if err := writeImageModels(&out, report, all, "./openai"); err != nil {
					t.Fatal(err)
				}
				for _, guidance := range []string{"Or use the default:", "Leaving out --model uses"} {
					if strings.Contains(out.String(), guidance) != test.wantDefault {
						t.Errorf("default guidance %q with all=%v, want %v: %s", guidance, all, test.wantDefault, out.String())
					}
				}
				if !test.wantDefault && !strings.Contains(out.String(), `./openai images generate --prompt "A tiny orange robot" --model gpt-image-1`) {
					t.Errorf("visible alternate not recommended with all=%v: %s", all, out.String())
				}
			}
		})
	}
}

func TestImageModelsPresentationOfflineAndEmpty(t *testing.T) {
	report := imageModelsReport{Source: "offline", Models: []imageModelRow{
		imageModelTestRow(defaultSavedImageModel, imagemodels.StatusNotChecked, ""),
	}}
	var out strings.Builder
	if err := writeImageModels(&out, report, false, "./openai"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "access not checked (offline)") || !strings.Contains(out.String(), "Not checked") || strings.Contains(out.String(), "Visible") {
		t.Fatalf("offline catalog claimed access: %s", out.String())
	}
	report.Source = "live"
	report.Models[0].Status = imagemodels.StatusNotVisible
	out.Reset()
	if err := writeImageModels(&out, report, false, "./openai"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No known, active image models were visible") || strings.Contains(out.String(), "Choose a model") {
		t.Fatalf("empty catalog needs an accurate explanation: %s", out.String())
	}
}

type imageModelsFailWriter struct{ err error }

func (w imageModelsFailWriter) Write([]byte) (int, error) { return 0, w.err }

func TestImageModelsPresentationPreservesWriteFailure(t *testing.T) {
	want := errors.New("synthetic write failure")
	if got := writeImageModels(imageModelsFailWriter{want}, imageModelsReport{}, false, "./openai"); !errors.Is(got, want) {
		t.Fatalf("write failure = %v, want %v", got, want)
	}
}

func TestImageModelsDataUsesConfiguredWriter(t *testing.T) {
	report := imageModelsReport{Source: "offline", DefaultModel: defaultSavedImageModel, Models: []imageModelRow{
		imageModelTestRow(defaultSavedImageModel, imagemodels.StatusNotChecked, ""),
	}}
	for _, format := range []string{"auto", "text", "JSON", "jsonl", "raw", "yaml"} {
		t.Run(format, func(t *testing.T) {
			var out strings.Builder
			command := &cli.Command{Name: "openai", Writer: &out, Flags: []cli.Flag{
				&cli.StringFlag{Name: "format", Value: "auto"},
			}, Action: func(_ context.Context, command *cli.Command) error { return writeImageModelsData(command, report) }}
			if err := command.Run(context.Background(), []string{"openai", "--format", format}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), defaultSavedImageModel) || !strings.Contains(out.String(), "not_checked") {
				t.Fatalf("report did not reach configured writer: %q", out.String())
			}
			if format == "auto" || format == "text" {
				if json.Valid([]byte(out.String())) || !strings.Contains(out.String(), "Source: offline") || !strings.Contains(out.String(), "Default model: "+defaultSavedImageModel) {
					t.Fatalf("expected readable model report: %q", out.String())
				}
			} else if format != "yaml" && !json.Valid([]byte(out.String())) {
				t.Fatalf("invalid JSON report: %q", out.String())
			}
		})
	}
	var out strings.Builder
	command := &cli.Command{Name: "openai", Writer: &out, Flags: []cli.Flag{
		&cli.StringFlag{Name: "format", Value: "auto"},
		&cli.StringFlag{Name: "transform"},
		&cli.BoolFlag{Name: "raw-output"},
	}, Action: func(_ context.Context, command *cli.Command) error { return writeImageModelsData(command, report) }}
	if err := command.Run(context.Background(), []string{"openai", "--format", "yaml", "--transform", "models.0.id", "--raw-output"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != defaultSavedImageModel+"\n" {
		t.Fatalf("raw exact model ID = %q", out.String())
	}
}
