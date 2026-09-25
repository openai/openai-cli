package custom

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imagemodels"
	"github.com/urfave/cli/v3"
)

func imageModelTestRow(id string, status imagemodels.Status, failure imagemodels.Failure) imagemodels.Result {
	return imagemodels.Result{Entry: imagemodels.Entry{ID: id}, Status: status, Failure: failure}
}

func TestImageModelsPresentationPartialAndHidden(t *testing.T) {
	report := imageModelsReport{Source: "live", Models: []imagemodels.Result{
		imageModelTestRow("gpt-image-2.5-sunburst", imagemodels.StatusVisible, ""),
		imageModelTestRow("gpt-image-2.5-flare", imagemodels.StatusUnknown, imagemodels.FailureTimeout),
		imageModelTestRow("gpt-image-1", imagemodels.StatusNotVisible, ""),
		imageModelTestRow("dall-e-2", imagemodels.StatusRetired, ""),
	}}
	var out strings.Builder
	if err := writeImageModels(&out, report, false, "openai"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare", "Could not check (timeout)", "2 retired or not visible", "openai images models --all", `openai images generate --prompt "A tiny orange robot" --model gpt-image-2.5-sunburst`, "generation permissions can differ"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q: %s", want, out.String())
		}
	}
	for _, unwanted := range []string{"gpt-image-1", "dall-e-2", "default", "No known", "{", "\x1b"} {
		if strings.Contains(out.String(), unwanted) {
			t.Errorf("unexpected %q: %s", unwanted, out.String())
		}
	}
	out.Reset()
	if err := writeImageModels(&out, report, true, "openai"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "gpt-image-1") || !strings.Contains(out.String(), "dall-e-2") {
		t.Fatalf("--all lost results: %s", out.String())
	}
}

func TestImageModelsSuggestsOnlyVisibleOrUncheckedNames(t *testing.T) {
	for _, status := range []imagemodels.Status{imagemodels.StatusVisible, imagemodels.StatusNotChecked, imagemodels.StatusRetired, imagemodels.StatusNotVisible, imagemodels.StatusUnknown} {
		t.Run(string(status), func(t *testing.T) {
			report := imageModelsReport{Source: "live", Models: []imagemodels.Result{imageModelTestRow("gpt-image-1", status, imagemodels.FailureTimeout)}}
			if status == imagemodels.StatusNotChecked {
				report.Source = "offline"
			}
			for _, all := range []bool{false, true} {
				var out strings.Builder
				if err := writeImageModels(&out, report, all, "openai"); err != nil {
					t.Fatal(err)
				}
				eligible := status == imagemodels.StatusVisible || status == imagemodels.StatusNotChecked
				if strings.Contains(out.String(), "Choose a model") != eligible {
					t.Errorf("misleading suggestion: %s", out.String())
				}
				if strings.Contains(out.String(), "default") {
					t.Errorf("claimed an unshipped default: %s", out.String())
				}
				if status == imagemodels.StatusUnknown && (strings.Contains(out.String(), "No known") || !strings.Contains(out.String(), "Could not check (timeout)")) {
					t.Errorf("failed check claimed unavailable: %s", out.String())
				}
				if status == imagemodels.StatusNotChecked && (!strings.Contains(out.String(), "access not checked (offline)") || strings.Contains(out.String(), "Visible")) {
					t.Errorf("offline claimed visibility: %s", out.String())
				}
			}
		})
	}
}

func TestImageModelsStatusAndFailureGuidance(t *testing.T) {
	for _, tc := range []struct {
		row  imagemodels.Result
		want string
	}{
		{imagemodels.Result{Status: imagemodels.StatusVisible, ShutdownDate: "2099-01-01"}, "Visible (retires 2099-01-01)"},
		{imagemodels.Result{Status: imagemodels.StatusRetired, ShutdownDate: "2000-01-01"}, "Retired 2000-01-01"},
		{imagemodels.Result{Status: imagemodels.StatusUnknown, Failure: imagemodels.FailureAuthentication}, "Could not check (authentication)"},
		{imagemodels.Result{Status: imagemodels.StatusUnknown, Failure: imagemodels.FailureForbidden}, "Could not check (access denied)"},
		{imagemodels.Result{Status: imagemodels.StatusUnknown, Failure: imagemodels.FailureRateLimit}, "Could not check (rate limit)"},
	} {
		if got := imageModelStatusText(tc.row); got != tc.want {
			t.Errorf("status=%q; want %q", got, tc.want)
		}
	}
	for failure, want := range map[imagemodels.Failure]string{
		imagemodels.FailureAuthentication: "did not accept authentication", imagemodels.FailureForbidden: "denied access",
		imagemodels.FailureRateLimit: "rate-limited", imagemodels.FailureTimeout: "timed out", imagemodels.FailureServer: "API could not complete",
		imagemodels.FailureNetwork: "Could not reach", imagemodels.FailureInvalidResponse: "unexpected model response", imagemodels.FailureCanceled: "could not be completed",
	} {
		message := imageModelsFailureMessage([]imagemodels.Result{{Status: imagemodels.StatusUnknown, Failure: failure}}, "openai")
		if !strings.Contains(message, want) || !strings.Contains(message, "openai images models --offline") {
			t.Errorf("failure %s: %s", failure, message)
		}
	}
}

type imageModelsFailWriter struct{ err error }

func (w imageModelsFailWriter) Write([]byte) (int, error) { return 0, w.err }

func TestImageModelsWriteErrors(t *testing.T) {
	want := errors.New("synthetic write failure")
	for _, failure := range []error{want, nil} {
		expected := failure
		if expected == nil {
			expected = io.ErrShortWrite
		}
		if got := writeImageModels(imageModelsFailWriter{failure}, imageModelsReport{}, false, "openai"); !errors.Is(got, expected) {
			t.Fatalf("error=%v; want %v", got, expected)
		}
	}
}

func TestImageModelsDataFormatsAndExtraction(t *testing.T) {
	report := imageModelsReport{Source: "offline", Models: []imagemodels.Result{imageModelTestRow("gpt-image-2.5-sunburst", imagemodels.StatusNotChecked, "")}}
	for _, format := range []string{"auto", "text", "JSON", "jsonl", "raw", "yaml", "explore"} {
		t.Run(format, func(t *testing.T) {
			var out, stderr strings.Builder
			command := imageModelsOutputCommand(&out, &stderr, report)
			if err := command.Run(context.Background(), []string{"openai", "--format", format}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "gpt-image-2.5-sunburst") || !strings.Contains(out.String(), "not_checked") || strings.Contains(out.String(), "default") {
				t.Fatalf("incorrect report: %q", out.String())
			}
			if format == "auto" || format == "text" {
				if json.Valid([]byte(out.String())) || !strings.Contains(out.String(), "Source: offline") {
					t.Fatalf("expected text: %q", out.String())
				}
			} else if format != "yaml" && !json.Valid([]byte(out.String())) {
				t.Fatalf("invalid JSON: %q", out.String())
			}
			if format == "explore" && !strings.Contains(stderr.String(), "falling back to 'json'") {
				t.Fatalf("missing existing explore warning: %q", stderr.String())
			}
		})
	}
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw", "yaml"} {
		for _, raw := range []bool{false, true} {
			var out, stderr strings.Builder
			command := imageModelsOutputCommand(&out, &stderr, report)
			args := []string{"openai", "--format", format, "--transform", "models.0.id"}
			if raw {
				args = append(args, "--raw-output")
			}
			if err := command.Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			want := "gpt-image-2.5-sunburst\n"
			if !raw && (format == "auto" || format == "json" || format == "jsonl" || format == "raw") {
				want = "\"gpt-image-2.5-sunburst\"\n"
			}
			if out.String() != want {
				t.Errorf("format=%s raw=%v got %q want %q", format, raw, out.String(), want)
			}
		}
	}
	var stderr strings.Builder
	want := errors.New("synthetic data output failure")
	if err := imageModelsOutputCommand(imageModelsFailWriter{want}, &stderr, report).Run(context.Background(), []string{"openai", "--format", "json"}); !errors.Is(err, want) {
		t.Errorf("lost output failure: %v", err)
	}
}

func imageModelsOutputCommand(out, stderr io.Writer, report imageModelsReport) *cli.Command {
	return &cli.Command{Name: "openai", Writer: out, ErrWriter: stderr, Flags: []cli.Flag{
		&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "transform"}, &cli.BoolFlag{Name: "raw-output"},
	}, Action: func(_ context.Context, command *cli.Command) error { return writeImageModelsData(command, report) }}
}
