package readable

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestStreamWriterWritesDeltasImmediately(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	w := NewStreamWriter(&out)
	for _, event := range []StreamEvent{{}, {Parts: []StreamPart{{Key: "answer"}}}} {
		if err := w.Write(event); err != nil {
			t.Fatal(err)
		}
	}
	if w.HasOutput() || out.Len() != 0 {
		t.Fatal("progress-only events produced output")
	}
	for _, step := range []struct{ delta, want string }{
		{"Hello", "Hello"},
		{" 世界", "Hello 世界"},
		{"\nnext line", "Hello 世界\nnext line"},
		{"", "Hello 世界\nnext line"},
	} {
		if err := w.Write(StreamEvent{Parts: []StreamPart{{Key: "answer", Text: step.delta}}}); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != step.want {
			t.Fatalf("delta was not emitted immediately: got %q, want %q", got, step.want)
		}
		if !w.HasOutput() {
			t.Fatal("visible text was not recorded as output")
		}
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Hello 世界\nnext line\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStreamWriterSnapshotsDeduplicateOriginalBytes(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	w := NewStreamWriter(&out)
	for _, step := range []struct {
		part StreamPart
		want string
	}{
		{StreamPart{Key: "answer", Text: "世\x1b[2J"}, "世" + `\u001b[2J`},
		{StreamPart{Key: "answer", Text: "界"}, "世" + `\u001b[2J` + "界"},
		{StreamPart{Key: "answer", Text: "世\x1b[2J界", Snapshot: true}, "世" + `\u001b[2J` + "界"},
		{StreamPart{Key: "answer", Text: "世\x1b[2J界!", Snapshot: true}, "世" + `\u001b[2J` + "界!"},
		{StreamPart{Key: "answer", Text: "世\x1b[2J界!", Snapshot: true}, "世" + `\u001b[2J` + "界!"},
		{StreamPart{Key: "answer", Text: "\n"}, "世" + `\u001b[2J` + "界!\n"},
		{StreamPart{Key: "answer", Text: "世\x1b[2J界!\n", Snapshot: true}, "世" + `\u001b[2J` + "界!\n"},
	} {
		if err := w.Write(StreamEvent{Parts: []StreamPart{step.part}}); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != step.want {
			t.Fatalf("snapshot duplicated or lost text: got %q, want %q", got, step.want)
		}
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "世"+`\u001b[2J`+"界!\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStreamWriterRevisedSnapshotsRemainVisible(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, original, revised, label, want string }{
		{"same length", "old", "new", "", "old\n\nUpdated text:\nnew!\n"},
		{"shorter", "original", "short", "Choice 2", "Choice 2:\noriginal\n\nUpdated Choice 2:\nshort!\n"},
		{"longer with different prefix", "old", "replacement", "", "old\n\nUpdated text:\nreplacement!\n"},
		{"empty revision", "old", "", "", "old\n\nUpdated text:\n!\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out strings.Builder
			w := NewStreamWriter(&out)
			for _, part := range []StreamPart{
				{Key: "answer", Text: test.original, Label: test.label},
				{Key: "answer", Text: test.revised, Label: test.label, Snapshot: true},
				{Key: "answer", Text: test.revised, Label: test.label, Snapshot: true},
				{Key: "answer", Text: "!", Label: test.label},
				{Key: "answer", Text: test.revised + "!", Label: test.label, Snapshot: true},
			} {
				if err := w.Write(StreamEvent{Parts: []StreamPart{part}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Finish(); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestStreamWriterSeparatesInterleavedIdentities(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	w := NewStreamWriter(&out)
	for _, event := range []StreamEvent{
		{Parts: []StreamPart{{Key: "a", Text: "same", Label: "Choice 1"}, {Key: "b", Text: "same", Label: "Choice 2"}}},
		// A duplicate snapshot for another identity must neither repeat it nor
		// interrupt the currently active identity's next delta.
		{Parts: []StreamPart{{Key: "a", Text: "same", Label: "Choice 1", Snapshot: true}}},
		{Parts: []StreamPart{{Key: "b", Text: " B", Label: "Choice 2"}}},
		{Parts: []StreamPart{{Key: "a", Text: "same A", Label: "Choice 1", Snapshot: true}}},
		{Parts: []StreamPart{{Key: "b", Text: "same B", Label: "Choice 2", Snapshot: true}}},
	} {
		if err := w.Write(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	want := "Choice 1:\nsame\n\nChoice 2:\nsame B\n\nChoice 1:\n A\n"
	if got := out.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStreamWriterSnapshotsWithoutDeltasAndRepeatedDeltas(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	w := NewStreamWriter(&out)
	for _, part := range []StreamPart{
		{Key: "a", Text: "first", Snapshot: true},
		{Key: "a", Text: "first", Snapshot: true},
		{Key: "b", Text: "ha"},
		{Key: "b", Text: "ha"},
		{Key: "b", Text: "haha", Snapshot: true},
	} {
		if err := w.Write(StreamEvent{Parts: []StreamPart{part}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "first\n\nText:\nhaha\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStreamWriterRetainsStructuredDetailsBetweenText(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	w := NewStreamWriter(&out)
	for _, event := range []StreamEvent{
		{
			Parts:   []StreamPart{{Key: "answer", Text: "Partial"}},
			Details: gjson.Parse(`{"usage":{"total_tokens":9007199254740993},"tool_calls":[{"id":"call_fake","arguments":"{}"}],"future":{"kept":false}}`),
		},
		{Parts: []StreamPart{{Key: "answer", Text: "Partial answer", Snapshot: true}}},
		{Details: gjson.Parse(`{"refusal":"synthetic refusal","incomplete_details":{"reason":"max_output_tokens"}}`)},
	} {
		if err := w.Write(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	want := "Partial\n\nUsage:\n  Total tokens: 9007199254740993\n" +
		"Tool calls:\n  1.\n    ID: call_fake\n    Arguments: {}\nFuture:\n  Kept: false\n" +
		"\n answer\n\nRefusal: synthetic refusal\nIncomplete details:\n  Reason: max_output_tokens\n"
	if got := out.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStreamWriterRetainsScalarAndEmptyDetails(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ input, want string }{
		{`null`, "(null)\n"}, {`false`, "false\n"}, {`0`, "0\n"},
		{`""`, "(empty string)\n"}, {`{}`, "(empty object)\n"}, {`[]`, "(empty list)\n"},
	} {
		t.Run(test.input, func(t *testing.T) {
			var out strings.Builder
			w := NewStreamWriter(&out)
			if err := w.Write(StreamEvent{Details: gjson.Parse(test.input)}); err != nil {
				t.Fatal(err)
			}
			if !w.HasOutput() || out.String() != test.want {
				t.Fatalf("got %q with HasOutput=%v, want %q", out.String(), w.HasOutput(), test.want)
			}
		})
	}
}

func TestStreamWriterEscapesLabelsTextAndDetails(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	w := NewStreamWriter(&out)
	for _, event := range []StreamEvent{
		{Parts: []StreamPart{{Key: "answer", Label: "Choice\x1b[2J\u202e", Text: "safe\x1b"}}},
		{Parts: []StreamPart{{Key: "answer", Text: "]52;c;synthetic\a\r\u009b2J\u2066\u2069\u2028\n\tend"}}},
		{Details: gjson.Parse(`{"note":"safe\u001b[2J\u202e"}`)},
	} {
		if err := w.Write(event); err != nil {
			t.Fatal(err)
		}
	}
	want := `Choice\u001b[2J\u202e:` + "\n" +
		`safe\u001b]52;c;synthetic\u0007\r\u009b2J\u2066\u2069\u2028` + "\n\tend\n\n" +
		`Note: safe\u001b[2J\u202e` + "\n"
	if got := out.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStreamWriterFinishPreservesExistingNewlines(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ input, want string }{
		{"", ""}, {"text", "text\n"}, {"text\n", "text\n"}, {"text\n\n", "text\n\n"},
	} {
		t.Run(test.input, func(t *testing.T) {
			var out strings.Builder
			w := NewStreamWriter(&out)
			if err := w.Write(StreamEvent{Parts: []StreamPart{{Key: "answer", Text: test.input}}}); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := w.Finish(); err != nil {
					t.Fatal(err)
				}
			}
			if got := out.String(); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestStreamWriterPreservesLargeText(t *testing.T) {
	// This intentionally exceeds common line-buffer limits. Size cannot turn
	// ordinary text into an omission, and snapshots must not repeat it.
	large := strings.Repeat("synthetic 世界 ", 1<<17)
	var out strings.Builder
	w := NewStreamWriter(&out)
	for _, part := range []StreamPart{
		{Key: "answer", Text: large},
		{Key: "answer", Text: large, Snapshot: true},
		{Key: "answer", Text: large + "complete", Snapshot: true},
	} {
		if err := w.Write(StreamEvent{Parts: []StreamPart{part}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	if want := large + "complete\n"; out.String() != want {
		t.Fatalf("large text changed: got %d bytes, want %d", out.Len(), len(want))
	}
}

func TestStreamWriterPropagatesFailuresWithoutFurtherWrites(t *testing.T) {
	t.Parallel()
	for _, stage := range []struct {
		name   string
		events []StreamEvent
		failAt int
		finish bool
	}{
		{"label", []StreamEvent{{Parts: []StreamPart{{Key: "a", Text: "answer", Label: "Choice"}}}}, 1, false},
		{"text", []StreamEvent{{Parts: []StreamPart{{Key: "a", Text: "answer", Label: "Choice"}}}}, 2, false},
		{"finish", []StreamEvent{{Parts: []StreamPart{{Key: "a", Text: "answer"}}}}, 2, true},
		{"separator", []StreamEvent{{Parts: []StreamPart{{Key: "a", Text: "answer\n"}, {Key: "b", Text: "another"}}}}, 2, false},
		{"before details", []StreamEvent{{Parts: []StreamPart{{Key: "a", Text: "answer"}}, Details: gjson.Parse(`{"kept":true}`)}}, 2, false},
		{"inside details", []StreamEvent{{Details: gjson.Parse(`{"first":"retained","second":"never written"}`)}}, 3, false},
	} {
		for _, failure := range []struct {
			name string
			err  error
			want error
		}{{"closed pipe", io.ErrClosedPipe, io.ErrClosedPipe}, {"short write", nil, io.ErrShortWrite}} {
			t.Run(stage.name+"/"+failure.name, func(t *testing.T) {
				out := &streamFailingWriter{failAt: stage.failAt, fail: failure.err}
				w := NewStreamWriter(out)
				var err error
				for _, event := range stage.events {
					if err = w.Write(event); err != nil {
						break
					}
				}
				if err == nil && stage.finish {
					err = w.Finish()
				}
				if !errors.Is(err, failure.want) {
					t.Fatalf("got %v, want %v", err, failure.want)
				}
				if out.calls != stage.failAt {
					t.Fatalf("received %d writes, failure was on write %d", out.calls, stage.failAt)
				}
			})
		}
	}
}

type streamFailingWriter struct {
	failAt int
	fail   error
	calls  int
}

func (w *streamFailingWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.calls++
	if w.calls == w.failAt {
		return len(p) / 2, w.fail
	}
	return len(p), nil
}
