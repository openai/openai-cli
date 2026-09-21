package custom

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSpeechEventIteratorPreservesEvents(t *testing.T) {
	const wire = ": heartbeat\r\nevent: speech.audio.done\r\nid: synthetic-event\r\ndata: {\"type\":\"speech.audio.done\",\r\ndata: \"usage\":{\"input_tokens\":9007199254740993}}\r\n\r\ndata: [DONE]\r\n\r\ndata: {\"after_done\":true}\n\n"
	iter := &speechEventIterator{reader: bufio.NewReader(strings.NewReader(wire))}
	if !iter.Next() {
		t.Fatalf("missing speech event: %v", iter.Err())
	}
	if got := iter.Current().Get("usage.input_tokens").Raw; got != "9007199254740993" {
		t.Fatalf("event payload changed: %q", got)
	}
	if iter.Next() || iter.Err() != nil {
		t.Fatalf("DONE did not stop the stream: %v", iter.Err())
	}
}

func TestSpeechEventIteratorRetainsFinalEventAtEOF(t *testing.T) {
	const event = `{"type":"speech.audio.done","usage":{"output_tokens":2}}`
	iter := &speechEventIterator{reader: bufio.NewReader(strings.NewReader("data: " + event))}
	if !iter.Next() || iter.Current().RawJSON() != event || iter.Next() || iter.Err() != nil {
		t.Fatalf("last speech event was lost or repeated: %v", iter.Err())
	}
}

func TestSpeechEventIteratorHasNoNewLineOrEventLimit(t *testing.T) {
	// The original speech download path had no SSE scanner ceiling. Exercise a
	// line larger than the SDK decoder's 32 MiB limit and preserve the full value.
	const size = (32 << 20) + 1024
	text := strings.Repeat("x", size)
	for _, multiline := range []bool{false, true} {
		wire := "data: {\"type\":\"speech.audio.delta\",\"future\":\"" + text + "\"}\n\n"
		if multiline {
			wire = "data: {\"first\":\"" + text[:size/2] + "\",\ndata: \"second\":\"" + text[size/2:] + "\"}\n\n"
		}
		iter := &speechEventIterator{reader: bufio.NewReader(strings.NewReader(wire))}
		if !iter.Next() {
			t.Fatalf("large event rejected (multiline=%v): %v", multiline, iter.Err())
		}
		value := iter.Current()
		if multiline {
			if value.Get("first").String() != text[:size/2] || value.Get("second").String() != text[size/2:] {
				t.Fatal("large multiline event changed")
			}
		} else if value.Get("future").String() != text {
			t.Fatal("large event changed")
		}
		if iter.Next() || iter.Err() != nil {
			t.Fatalf("unexpected event after EOF: %v", iter.Err())
		}
	}
}

func TestSpeechEventIteratorReportsReadAndParseErrors(t *testing.T) {
	want := errors.New("synthetic stream read failure")
	iter := &speechEventIterator{reader: bufio.NewReader(speechErrorReader{want})}
	if iter.Next() || !errors.Is(iter.Err(), want) {
		t.Fatalf("reader error lost: %v", iter.Err())
	}
	iter = &speechEventIterator{reader: bufio.NewReader(strings.NewReader("data: synthetic-private-invalid-json\n\n"))}
	if iter.Next() || iter.Err() == nil || strings.Contains(iter.Err().Error(), "synthetic-private") {
		t.Fatalf("malformed event was accepted or exposed: %v", iter.Err())
	}
}

type speechErrorReader struct{ err error }

func (r speechErrorReader) Read([]byte) (int, error) { return 0, r.err }

var _ io.Reader = speechErrorReader{}
