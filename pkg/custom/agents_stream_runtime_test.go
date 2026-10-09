package custom

import (
	"errors"
	"io"
	"testing"

	"github.com/tidwall/gjson"
)

// A limited observer owns its stream even when Next never reaches EOF.
type agentsOwnedStream struct {
	values            []gjson.Result
	reads, closes     int
	current           gjson.Result
	readErr, closeErr error
}

func (s *agentsOwnedStream) Next() bool {
	s.reads++
	if len(s.values) == 0 {
		return false
	}
	s.current, s.values = s.values[0], s.values[1:]
	return true
}
func (s *agentsOwnedStream) Current() outputJSON { return outputJSON{s.current} }
func (s *agentsOwnedStream) Err() error          { return s.readErr }
func (s *agentsOwnedStream) Close() error        { s.closes++; return s.closeErr }

func TestAgentsStreamRuntimeClosesLimitedObservation(t *testing.T) {
	for _, limit := range []int64{0, 1} {
		for _, format := range []string{"text", "json", "jsonl", "raw"} {
			stream := &agentsOwnedStream{values: []gjson.Result{
				gjson.Parse(`{"type":"agent.session.idle","session_id":"sess_fake"}`),
				gjson.Parse(`{"type":"agent.session.idle","session_id":"sess_fake"}`),
			}}
			err := ShowJSONIterator(stream, limit, ShowJSONOpts{
				Operation:  "(resource) beta.agents.sessions.events > (method) stream",
				OutputKind: OutputStreamEvent, Format: format, ExplicitFormat: true, Stdout: io.Discard,
			})
			if err != nil || stream.reads != int(limit) || stream.closes != 1 {
				t.Fatalf("limit=%d format=%s: err=%v reads=%d closes=%d", limit, format, err, stream.reads, stream.closes)
			}
		}
	}
}

func TestAgentsStreamRuntimePreservesCloseAndOutputErrors(t *testing.T) {
	closeErr, outputErr := errors.New("synthetic close failure"), errors.New("synthetic output failure")
	stream := &agentsOwnedStream{closeErr: closeErr, values: []gjson.Result{gjson.Parse(`{"type":"future.event"}`)}}
	err := ShowJSONIterator(stream, 1, ShowJSONOpts{
		Operation:  "(resource) beta.agents.sessions.events > (method) stream",
		OutputKind: OutputStreamEvent, Format: "jsonl", Stdout: failOutputWriter{outputErr},
	})
	if !errors.Is(err, closeErr) || !errors.Is(err, outputErr) || stream.closes != 1 {
		t.Fatalf("err=%v closes=%d", err, stream.closes)
	}
}

func TestAgentsStreamRuntimeDoesNotCloseListOwner(t *testing.T) {
	stream := &agentsOwnedStream{}
	if err := ShowJSONIterator(stream, 0, ShowJSONOpts{OutputKind: OutputPageItem, Stdout: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if stream.closes != 0 {
		t.Fatal("list owner changed")
	}
}
