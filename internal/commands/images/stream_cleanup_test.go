package images_test

import (
	"io"
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/tidwall/gjson"
)

func TestDeterministicStreamCleanupKeepsSavedImageOwner(t *testing.T) {
	for _, event := range []string{
		`{"type":"image_generation.completed","b64_json":"iVBORw0KGgo="}`,
		`{"type":"image_generation.failed"}`,
	} {
		stream := &deterministicCleanupIterator{events: []images.TestOutputJSON{{Result: gjson.Parse(event)}}}
		plan := images.NewOutputPlanForTest(t.TempDir(), "synthetic", "off")
		ctx := images.WithPresentationForTest(t.Context(), plan, io.Discard)
		err := custom.ShowJSONIterator(stream, 0, custom.ShowJSONOpts{Context: ctx, Operation: imageGenerationOperation, OutputKind: OutputStreamEvent})
		failed := gjson.Get(event, "type").String() == "image_generation.failed"
		if (err != nil) != failed || stream.reads != 1 || stream.closes != 1 {
			t.Fatalf("saved-image owner changed: err=%v reads=%d closes=%d", err, stream.reads, stream.closes)
		}
	}
}

type deterministicCleanupIterator struct {
	events               []images.TestOutputJSON
	reads, closes        int
	errorReadsAfterClose int
	sourceErr, closeErr  error
}

func (s *deterministicCleanupIterator) Next() bool                     { s.reads++; return s.reads <= len(s.events) }
func (s *deterministicCleanupIterator) Current() images.TestOutputJSON { return s.events[s.reads-1] }
func (s *deterministicCleanupIterator) Err() error {
	if s.closes != 0 {
		s.errorReadsAfterClose++
	}
	return s.sourceErr
}
func (s *deterministicCleanupIterator) Close() error { s.closes++; return s.closeErr }
