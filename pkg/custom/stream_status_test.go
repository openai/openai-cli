package custom

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStreamFailureSurvivesAllFormatsAndExtraction(t *testing.T) {
	const event = `{"event":"thread.run.failed","data":{"id":"run_test","status":"failed"}}`
	for _, format := range []string{"auto", "text", "json", "JSON", "jsonl", "raw", "yaml", "pretty", "explore"} {
		for _, transform := range []string{"", "data.id"} {
			t.Run(format+"/"+transform, func(t *testing.T) {
				file := outputFile(t)
				iter := &transformTestIterator{items: []any{outputJSON{gjson.Parse(event)}}}
				err := ShowJSONIterator(iter, -1, ShowJSONOpts{Stdout: file, Stderr: io.Discard, Format: format, ExplicitFormat: true, Transform: transform, OutputKind: OutputStreamEvent})
				require.ErrorContains(t, err, "streamed response failed")
				require.Contains(t, readOutput(t, file), "run_test", "failure must remain visible before returning its status")
			})
		}
	}
}

func TestFailedResourceListDoesNotFailRequest(t *testing.T) {
	iter := &transformTestIterator{items: []any{outputJSON{gjson.Parse(`{"id":"batch_test","status":"failed"}`)}}}
	var out strings.Builder
	require.NoError(t, ShowJSONIterator(iter, -1, ShowJSONOpts{Stdout: &out, OutputKind: OutputPageItem}))
	require.Contains(t, out.String(), "failed")
}
