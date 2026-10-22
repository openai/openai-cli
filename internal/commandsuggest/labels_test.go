package commandsuggest

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/urfave/cli/v3"
)

func TestLabelsCaptureTargetsAndReuseCommandIdentifiers(t *testing.T) {
	capture, sink := testCollector()
	capture.clickPrefix = testClickPrefix
	root := &cli.Command{Name: "openai"}
	SetRecorder(root, capture)
	const target = "'/tmp/CLI tools/openai' widgets inspect --help"
	for _, label := range []string{"inspect", "inspect widgets", "inspect"} {
		got := RecordLabel(root, label, target)
		if ansi.Strip(got) != label || !strings.HasPrefix(got, "\x1b]8;;"+testClickPrefix+"1\x1b\\") {
			t.Fatalf("label or identifier changed: %q", got)
		}
		if strings.Contains(got, "CLI tools") || strings.Contains(got, "--help") {
			t.Fatalf("link exposed its command target: %q", got)
		}
	}
	if got := Record(root, target); !strings.HasPrefix(got, "\x1b]8;;"+testClickPrefix+"1\x1b\\") {
		t.Fatalf("ordinary command did not reuse label identifier: %q", got)
	}
	capture.flush()
	if got := sink.String(); got != target+"\n" {
		t.Fatalf("capture stored labels or duplicates: %q", got)
	}
	if got := RecordLabel(root, "inspect", target); got != "inspect" {
		t.Fatalf("closed capture linked a label: %q", got)
	}
}

func TestLabelsRetainValidationAndTerminalGates(t *testing.T) {
	for _, tc := range []struct {
		name, label, target string
		terminal            bool
	}{
		{"redirected", "inspect", "openai widgets inspect --help", false},
		{"unsafe label", "inspect\x1b]8;;bad", "openai widgets inspect --help", true},
		{"empty label", "", "openai widgets inspect --help", true},
		{"unsafe target", "inspect", "openai widgets\ninspect", true},
		{"oversized target", "inspect", strings.Repeat("x", maxCommandBytes+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture, sink := testCollector()
			capture.clickPrefix = testClickPrefix
			capture.terminal = func(io.Writer) bool { return tc.terminal }
			root := &cli.Command{Name: "openai"}
			SetRecorder(root, capture)
			if got := RecordLabel(root, tc.label, tc.target); got != tc.label {
				t.Fatalf("rejected label changed: %q", got)
			}
			capture.flush()
			if sink.Len() != 0 {
				t.Fatalf("rejected target captured: %q", sink.String())
			}
		})
	}
}

type labelRecorder struct{ target string }

func (r *labelRecorder) Record(_ io.Writer, _, target string) { r.target = target }

func TestLabelsPreservePlainAndRecorderOnlyOutput(t *testing.T) {
	root := &cli.Command{Name: "openai"}
	const target = "openai widgets inspect --help"
	if got := RecordLabel(root, "inspect", target); got != "inspect" {
		t.Fatalf("inactive output changed: %q", got)
	}
	recorder := &labelRecorder{}
	SetRecorder(root, recorder)
	if got := RecordLabel(root, "inspect", target); got != "inspect" || recorder.target != target {
		t.Fatalf("label=%q target=%q", got, recorder.target)
	}
}
