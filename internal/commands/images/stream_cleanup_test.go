package images

import (
	"context"
	"io"
	"testing"

	"github.com/tidwall/gjson"
)

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

func CheckTestDeterministicStreamCleanupKeepsSavedImageOwner(assembly AssemblyForTest, t *testing.T) {
	for _, event := range []string{
		`{"type":"image_generation.completed","b64_json":"iVBORw0KGgo="}`,
		`{"type":"image_generation.failed"}`,
	} {
		stream := &deterministicCleanupIterator{events: []outputJSON{{Result: gjson.Parse(event)}}}
		plan := &imageOutputPlan{directory: t.TempDir(), name: "synthetic", inline: "off"}
		ctx := context.WithValue(t.Context(), imagePresentationKey{}, imagePresentation{plan: plan, writer: io.Discard})
		err := assembly.ShowJSONIterator(stream, 0, PresentationForTest{Context: ctx, Operation: imageGenerationOperation, OutputKind: OutputStreamEvent})
		failed := gjson.Get(event, "type").String() == "image_generation.failed"
		if (err != nil) != failed || stream.reads != 1 || stream.closes != 1 {
			t.Fatalf("saved-image owner changed: err=%v reads=%d closes=%d", err, stream.reads, stream.closes)
		}
	}
}
