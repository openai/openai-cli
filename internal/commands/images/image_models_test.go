package images

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imagemodels"
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
	if err := writeImageModels(&out, report, false, "openai", &out); err != nil {
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
	if err := writeImageModels(&out, report, true, "openai", &out); err != nil {
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
				if err := writeImageModels(&out, report, all, "openai", &out); err != nil {
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
		if got := writeImageModels(imageModelsFailWriter{failure}, imageModelsReport{}, false, "openai", nil); !errors.Is(got, expected) {
			t.Fatalf("error=%v; want %v", got, expected)
		}
	}
}
