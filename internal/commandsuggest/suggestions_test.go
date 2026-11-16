package commandsuggest

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/urfave/cli/v3"
)

type bufferSink struct {
	bytes.Buffer
	closes int
}

func (b *bufferSink) Close() error { b.closes++; return nil }

func testCollector() (*collector, *bufferSink) {
	sink := &bufferSink{}
	return &collector{shell: "zsh", sink: sink, terminal: func(io.Writer) bool { return true }, seen: make(map[string]struct{})}, sink
}

func TestRecordsOnlyExplicitCommandsForCurrentShell(t *testing.T) {
	capture, sink := testCollector()
	root := &cli.Command{Name: "openai", Writer: io.Discard}
	SetRecorder(root, capture)
	text := "openai models list"
	if got := RecordFor(root, "bash", text); got != text {
		t.Fatalf("display changed: %q", got)
	}
	RecordFor(root, "zsh", text)
	Record(root, text)
	RecordTo(root, io.Discard, "openai help setup")
	capture.flush()
	capture.flush()
	Record(root, "openai files list")
	if got, want := sink.String(), "openai models list\nopenai help setup\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if sink.closes != 1 {
		t.Fatalf("closed %d times", sink.closes)
	}
}

func TestRejectsControlsAndOversizedCommands(t *testing.T) {
	capture, sink := testCollector()
	for _, text := range []string{"", "  ", "openai\nmodels list", "openai\rmodels list", "openai\x1b[31m", "openai\tmodels list", "openai\x00", "openai\u202Emodels", "openai\u2028models", "openai\u2029models", "openai\xff", strings.Repeat("x", maxCommandBytes+1)} {
		capture.Record(io.Discard, "", text)
	}
	valid := "openai responses create --input 'café; $(literal)'"
	capture.Record(io.Discard, "", valid)
	capture.flush()
	if got := sink.String(); got != valid+"\n" {
		t.Fatalf("got %q", got)
	}
}

func TestCaptureLimitsDoNotAlterDisplay(t *testing.T) {
	capture, sink := testCollector()
	root := &cli.Command{Name: "openai"}
	SetRecorder(root, capture)
	for i := 0; i < maxCommands+1; i++ {
		text := fmt.Sprintf("openai models retrieve model-%d", i)
		if got := Record(root, text); got != text {
			t.Fatal("display changed")
		}
	}
	capture.flush()
	if got := strings.Count(sink.String(), "\n"); got != maxCommands {
		t.Fatalf("got %d commands", got)
	}
}

func TestCaptureByteBudgetIncludesDelimiters(t *testing.T) {
	capture, sink := testCollector()
	for i := 0; i < maxCommands; i++ {
		text := fmt.Sprintf("%03d", i) + strings.Repeat("x", maxCommandBytes-3)
		capture.Record(io.Discard, "", text)
	}
	// The last full-sized command exceeds the transport budget. A shorter one
	// can use the remaining space, including its newline delimiter.
	remaining := maxCaptureBytes - (maxCommands-1)*(maxCommandBytes+1)
	capture.Record(io.Discard, "", strings.Repeat("y", remaining-1))
	capture.flush()
	if sink.Len() != maxCaptureBytes {
		t.Fatalf("got %d bytes, want %d", sink.Len(), maxCaptureBytes)
	}
	if got := strings.Count(sink.String(), "\n"); got != maxCommands {
		t.Fatalf("got %d commands", got)
	}
}

func TestRedirectedOutputIsNotCaptured(t *testing.T) {
	capture, sink := testCollector()
	capture.terminal = isTerminal
	capture.Record(&bytes.Buffer{}, "", "openai models list")
	capture.flush()
	if sink.Len() != 0 {
		t.Fatal("captured redirected output")
	}
}

func TestConcurrentCaptureDeduplicates(t *testing.T) {
	capture, sink := testCollector()
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Go(func() { capture.Record(io.Discard, "", "openai models list") })
	}
	workers.Wait()
	capture.flush()
	if sink.String() != "openai models list\n" {
		t.Fatalf("got %q", sink.String())
	}
}

type failingSink struct{ closed bool }

func (s *failingSink) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (s *failingSink) Close() error              { s.closed = true; return io.ErrClosedPipe }

func TestOptionalCaptureFailurePreservesText(t *testing.T) {
	capture, _ := testCollector()
	sink := &failingSink{}
	capture.sink = sink
	root := &cli.Command{Name: "openai"}
	SetRecorder(root, capture)
	if got := Record(root, "openai models list"); got != "openai models list" {
		t.Fatal("display changed")
	}
	capture.flush()
	if !sink.closed {
		t.Fatal("sink was not closed")
	}
}
