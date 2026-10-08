package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/tidwall/gjson"
)

// This iterator deliberately does not make Close idempotent.
// The presentation boundary must own exactly one explicit cleanup call.
type deterministicCleanupIterator struct {
	events               []outputJSON
	reads, closes        int
	errorReadsAfterClose int
	sourceErr, closeErr  error
}

func (s *deterministicCleanupIterator) Next() bool          { s.reads++; return s.reads <= len(s.events) }
func (s *deterministicCleanupIterator) Current() outputJSON { return s.events[s.reads-1] }
func (s *deterministicCleanupIterator) Err() error {
	if s.closes != 0 {
		s.errorReadsAfterClose++
	}
	return s.sourceErr
}
func (s *deterministicCleanupIterator) Close() error { s.closes++; return s.closeErr }

func deterministicCleanupEvents() []outputJSON {
	return []outputJSON{
		{gjson.Parse(`{"type":"future.event","value":"first"}`)},
		{gjson.Parse(`{"type":"future.event","value":"second"}`)},
	}
}

func TestDeterministicStreamCleanupClosesEveryFormat(t *testing.T) {
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw", "yaml", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			for _, limit := range []int64{0, 1, -1} {
				stream := &deterministicCleanupIterator{events: deterministicCleanupEvents()}
				err := ShowJSONIterator(stream, limit, ShowJSONOpts{
					Context: t.Context(), OutputKind: OutputStreamEvent,
					Format: format, ExplicitFormat: true, Stdout: io.Discard, Stderr: io.Discard,
				})
				wantReads := int(limit)
				if limit < 0 {
					wantReads = len(stream.events) + 1
				}
				if err != nil || stream.reads != wantReads || stream.closes != 1 || stream.errorReadsAfterClose != 0 {
					t.Errorf("limit=%d: err=%v reads=%d closes=%d error reads after close=%d", limit, err, stream.reads, stream.closes, stream.errorReadsAfterClose)
				}
			}
		})
	}
}

func TestDeterministicStreamCleanupRetainsJoinedFailures(t *testing.T) {
	closeErr := errors.New("synthetic stream cleanup failure")
	sourceErr := errors.New("synthetic source failure")
	outputErr := errors.New("synthetic output failure")
	for _, tc := range []struct {
		name                      string
		limit                     int64
		cancelled, source, output bool
		cancelOnWrite             bool
		wantReads                 int
	}{
		{name: "zero initial source error", limit: 0, source: true},
		{name: "cancelled zero", limit: 0, cancelled: true},
		{name: "cancelled zero with source error", limit: 0, cancelled: true, source: true},
		{name: "cancelled before reading", limit: 1, cancelled: true, source: true},
		{name: "cancelled while writing", limit: 1, cancelOnWrite: true, source: true, wantReads: 1},
		{name: "sink and source failure", limit: 1, source: true, output: true, wantReads: 1},
		{name: "source failure at EOF", limit: -1, source: true, wantReads: 3},
		{name: "cleanup failure at EOF", limit: -1, wantReads: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			stream := &deterministicCleanupIterator{events: deterministicCleanupEvents(), closeErr: closeErr}
			if tc.source {
				stream.sourceErr = sourceErr
			}
			var out io.Writer = io.Discard
			if tc.output {
				out = failOutputWriter{outputErr}
			}
			if tc.cancelOnWrite {
				out = &cancelOutputWriter{cancel: cancel}
			}
			err := ShowJSONIterator(stream, tc.limit, ShowJSONOpts{
				Context: ctx, OutputKind: OutputStreamEvent, Format: "jsonl", Stdout: out,
			})
			if !errors.Is(err, closeErr) || errors.Is(err, sourceErr) != tc.source ||
				errors.Is(err, outputErr) != tc.output || errors.Is(err, context.Canceled) != (tc.cancelled || tc.cancelOnWrite) {
				t.Errorf("lost or added failure: %v", err)
			}
			if stream.reads != tc.wantReads || stream.closes != 1 || stream.errorReadsAfterClose != 0 {
				t.Errorf("reads=%d closes=%d error reads after close=%d", stream.reads, stream.closes, stream.errorReadsAfterClose)
			}
		})
	}
}

func TestDeterministicStreamCleanupPreservesListOwnership(t *testing.T) {
	for _, kind := range []OutputKind{OutputPageItem, OutputUnspecified} {
		for _, limit := range []int64{0, 1, -1} {
			stream := &deterministicCleanupIterator{events: deterministicCleanupEvents(), closeErr: errors.New("caller-owned cleanup")}
			err := ShowJSONIterator(stream, limit, ShowJSONOpts{OutputKind: kind, Format: "jsonl", Stdout: io.Discard})
			if err != nil || stream.closes != 0 {
				t.Fatalf("kind=%v limit=%d: err=%v closes=%d", kind, limit, err, stream.closes)
			}
		}
	}
}

type deterministicCleanupDecoder struct {
	events, reads, closes int
	closeErr              error
}

func (d *deterministicCleanupDecoder) Next() bool { d.reads++; return d.reads <= d.events }
func (d *deterministicCleanupDecoder) Event() ssestream.Event {
	return ssestream.Event{Data: []byte(`{"type":"future.event","value":"synthetic"}`)}
}
func (d *deterministicCleanupDecoder) Err() error   { return nil }
func (d *deterministicCleanupDecoder) Close() error { d.closes++; return d.closeErr }

func TestDeterministicStreamCleanupSDKCachedCloseFailure(t *testing.T) {
	closeErr := errors.New("synthetic decoder cleanup failure")
	for _, limit := range []int64{0, 1, -1} {
		decoder := &deterministicCleanupDecoder{events: 2, closeErr: closeErr}
		stream := ssestream.NewStream[map[string]any](decoder, nil)
		err := ShowJSONIterator(stream, limit, ShowJSONOpts{OutputKind: OutputStreamEvent, Format: "jsonl", Stdout: io.Discard})
		if !errors.Is(err, closeErr) || decoder.closes != 1 {
			t.Errorf("limit=%d: err=%v decoder closes=%d", limit, err, decoder.closes)
		}
		// SDK EOF already closes the decoder. Explicit cleanup must return the
		// cached error without a second decoder close, including after EOF.
		if !errors.Is(stream.Close(), closeErr) || !errors.Is(stream.Close(), closeErr) || decoder.closes != 1 {
			t.Errorf("limit=%d: SDK cleanup changed: closes=%d", limit, decoder.closes)
		}
		if stream.Next() {
			t.Errorf("limit=%d: stream remained readable after cleanup", limit)
		}
	}
}

func TestDeterministicStreamCleanupRepeatedLoopbackExit(t *testing.T) {
	requests := make(chan chan struct{}, 1)
	var active atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done := make(chan struct{})
		active.Add(1)
		defer func() { active.Add(-1); close(done) }()
		requests <- done
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"synthetic\"}\n\n"); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	client := openai.NewClient(option.WithAPIKey("sk-fake-cleanup-test"), option.WithBaseURL(server.URL), option.WithMaxRetries(0))
	for range 3 {
		for _, limit := range []int64{0, 1} {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{})
			var done chan struct{}
			select {
			case done = <-requests:
			case <-ctx.Done():
				stream.Close()
				cancel()
				t.Fatal("synthetic stream request did not reach the loopback server")
			}
			err := ShowJSONIterator(stream, limit, ShowJSONOpts{
				Context: ctx, Operation: responseStreamOperation, OutputKind: OutputStreamEvent, Format: "jsonl", Stdout: io.Discard,
			})
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				stream.Close()
				cancel()
				t.Fatal("early presentation exit retained the active HTTP stream")
			}
			cancel()
			if err != nil || active.Load() != 0 {
				t.Fatalf("limit=%d: err=%v active streams=%d", limit, err, active.Load())
			}
		}
	}
}

func TestDeterministicStreamCleanupKeepsSavedImageOwner(t *testing.T) {
	for _, event := range []string{
		`{"type":"image_generation.completed","b64_json":"iVBORw0KGgo="}`,
		`{"type":"image_generation.failed"}`,
	} {
		stream := &deterministicCleanupIterator{events: []outputJSON{{gjson.Parse(event)}}}
		plan := &imageOutputPlan{directory: t.TempDir(), name: "synthetic", inline: "off"}
		ctx := context.WithValue(t.Context(), imagePresentationKey{}, imagePresentation{plan: plan, writer: io.Discard})
		err := ShowJSONIterator(stream, 0, ShowJSONOpts{Context: ctx, Operation: imageGenerationOperation, OutputKind: OutputStreamEvent})
		failed := gjson.Get(event, "type").String() == "image_generation.failed"
		if (err != nil) != failed || stream.reads != 1 || stream.closes != 1 {
			t.Fatalf("saved-image owner changed: err=%v reads=%d closes=%d", err, stream.reads, stream.closes)
		}
	}
}

type deterministicSpeechBody struct {
	io.Reader
	closes int
}

func (b *deterministicSpeechBody) Close() error { b.closes++; return nil }

func TestDeterministicStreamCleanupKeepsSpeechBodyOwner(t *testing.T) {
	body := &deterministicSpeechBody{Reader: strings.NewReader("data: {\"type\":\"speech.audio.done\"}\n\ndata: [DONE]\n\n")}
	opts := ShowJSONOpts{OutputKind: OutputStreamEvent, Operation: speechStreamOperation, Format: "jsonl", ExplicitFormat: true}
	ctx := context.WithValue(t.Context(), speechOutputKey{}, opts)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://synthetic.invalid/audio/speech", nil)
	if err != nil {
		t.Fatal(err)
	}
	response := &http.Response{Request: request, StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}
	var out bytes.Buffer
	handled, _, err := writeReadableSpeech(response, &out, "")
	if !handled || err != nil || body.closes != 0 || out.String() != "{\"type\":\"speech.audio.done\"}\n" {
		t.Fatalf("speech ownership changed: handled=%v err=%v closes=%d output=%q", handled, err, body.closes, out.String())
	}
	if err := body.Close(); err != nil || body.closes != 1 {
		t.Fatalf("caller cleanup failed: %v", err)
	}
}
