package commandsuggest

import (
	"io"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestInvalidateDiscardsCaptureAndStopsLaterLinks(t *testing.T) {
	capture, sink := testCollector()
	capture.clickPrefix = "openai-insert://Ab01234567/0123456789abcdef0123456789abcdef/"
	root := &cli.Command{Name: "openai"}
	SetRecorder(root, capture)
	if got := RecordTo(root, io.Discard, "openai models list"); !strings.Contains(got, "\x1b]8;") {
		t.Fatal("initial suggestion did not render a link")
	}
	Invalidate(root)
	Invalidate(root)
	if got := RecordTo(root, io.Discard, "openai help setup"); got != "openai help setup" {
		t.Fatalf("failed capture still generated a link: %q", got)
	}
	capture.flush()
	capture.flush()
	if sink.Len() != 0 || sink.closes != 1 {
		t.Fatalf("invalid capture published %q; closes=%d", sink.String(), sink.closes)
	}
}

func TestInvalidateWithoutCaptureIsSafe(t *testing.T) {
	Invalidate(nil)
	Invalidate(&cli.Command{Name: "openai"})
}
