package transformers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPipelinePreservesNativeAudioText(t *testing.T) {
	for _, operation := range []string{
		"(resource) audio.transcriptions > (method) create",
		"(resource) audio.translations > (method) create",
	} {
		for _, text := range []string{"", "One line.", "Two lines.\nSecond line.\n\n", "WEBVTT\r\n\r\n00:00.000 --> 00:01.000\r\nHello\r\n", "Text with \x1b[31mcontrol\x1b[0m and \u202e directional marks."} {
			t.Run(operation+"/"+text, func(t *testing.T) {
				encoded, err := json.Marshal(text)
				require.NoError(t, err)
				value := gjson.ParseBytes(encoded)
				output, err := SelectPipeline(Route{Operation: operation, OutputKind: OutputResponse}).Prepare(t.Context(), value)
				require.NoError(t, err)
				require.Equal(t, value, output.Value)
				require.Equal(t, Projection{Text: ReadableValue{Text: text, IsText: true}}, output.Projection,
					"the pure projection must not trim, escape, or format native text")
			})
		}
	}
}

func TestPipelineNativeAudioProjectionRequiresExactStringResponseRoute(t *testing.T) {
	value := gjson.Parse(`"Original text."`)
	for _, route := range []Route{
		{Operation: "(resource) audio.transcriptions > (method) create", OutputKind: OutputPageItem},
		{Operation: "(resource) audio.transcriptions > (method) create", OutputKind: OutputStreamEvent},
		{Operation: "(resource) audio.transcriptions > (method) create", OutputKind: OutputUnspecified},
		{Operation: "(resource) audio.transcriptions > (method) future", OutputKind: OutputResponse},
		{Operation: "(resource) audio.translations > (method) create_more", OutputKind: OutputResponse},
		{Operation: "(resource) audio.speech > (method) create", OutputKind: OutputResponse},
		{Operation: "(resource) responses > (method) create", OutputKind: OutputResponse},
		{Operation: "audio.transcriptions.create", OutputKind: OutputResponse},
		{OutputKind: OutputResponse},
	} {
		output, err := SelectPipeline(route).Prepare(t.Context(), value)
		require.NoError(t, err)
		require.Equal(t, Output{Value: value}, output, route)
	}
	for _, raw := range []string{"null", "123", "true", `["Original text."]`} {
		value := gjson.Parse(raw)
		output, err := SelectPipeline(Route{Operation: "(resource) audio.transcriptions > (method) create", OutputKind: OutputResponse}).Prepare(t.Context(), value)
		require.NoError(t, err)
		require.Equal(t, Output{Value: value}, output)
	}
}

func TestPipelineKeepsJSONSeparateFromReadableViews(t *testing.T) {
	for _, test := range []struct {
		name, resource, raw, text string
		kind                      OutputKind
		summary                   bool
	}{
		{
			name: "response text", resource: "responses", kind: OutputResponse,
			raw:  `{ "object":"response", "output":[{"type":"message","content":[{"type":"output_text","text":"Original answer."}]}], "usage":{"total_tokens":7} }`,
			text: "Original answer.",
		},
		{
			name: "resource summary", resource: "models", kind: OutputResponse,
			raw:     `{ "id":"model_exact:001", "object":"model", "owned_by":"system", "created":9007199254740993 }`,
			summary: true,
		},
		{
			name: "page item summary", resource: "files", kind: OutputPageItem,
			raw:     `{"id":"file_example","object":"file","filename":"example.jsonl","bytes":9007199254740993,"created_at":123,"purpose":"fine-tune"}`,
			summary: true,
		},
		{
			name: "unknown resource field", resource: "files", kind: OutputResponse,
			raw: `{"id":"file_example","object":"file","filename":"example.jsonl","future":{"output":"New server information."}}`,
		},
		{
			name: "API fields cannot impersonate typed output", resource: "models", kind: OutputResponse,
			raw: `{"id":"model_example","object":"model","Value":"synthetic payload","Projection":{"Text":{"IsText":true,"Text":"Do not use this as presentation metadata."}}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := gjson.Parse(test.raw)
			route := Route{Operation: "(resource) " + test.resource + " > (method) retrieve", OutputKind: test.kind}
			output, err := SelectPipeline(route).Prepare(t.Context(), value)
			require.NoError(t, err)
			require.Equal(t, value, output.Value, "readable projection must not replace API data")
			require.Equal(t, test.text, output.Projection.Text.Text)
			require.Equal(t, test.summary, output.Projection.Summary.Exists())
			require.Equal(t, test.summary, output.Projection.Omitted)
			if test.summary {
				require.Equal(t, value.Get("id").Raw, output.Projection.Summary.Get("id").Raw)
				require.False(t, output.Projection.Summary.Get("details").Exists(), "pure projections must not embed a CLI hint")
			}
		})
	}
}

func TestPipelineRetainsStreamIdentityAndSnapshots(t *testing.T) {
	const raw = `{"type":"response.completed","sequence_number":4,"response":{"object":"response","status":"completed","output":[{"id":"msg_one","type":"message","content":[{"type":"output_text","text":"First."}]},{"id":"msg_two","type":"message","content":[{"type":"output_text","text":"Second."}]}],"usage":{"total_tokens":9007199254740993}}}`
	value := gjson.Parse(raw)
	pipeline := SelectPipeline(Route{Operation: "(resource) responses > (method) create", OutputKind: OutputStreamEvent})
	output, err := pipeline.Prepare(t.Context(), value)
	require.NoError(t, err)
	require.Equal(t, value, output.Value)
	require.True(t, output.Projection.Text.Snapshot)
	require.True(t, output.Projection.Text.Final)
	require.Equal(t, []ReadablePart{{Key: "response:0:0", Text: "First."}, {Key: "response:1:0", Text: "Second."}}, output.Projection.Text.Parts)
	require.Equal(t, "9007199254740993", output.Projection.Text.Details.Get("usage.total_tokens").Raw)
	require.False(t, output.Projection.Summary.Exists())

	unknown := gjson.Parse(`{ "type":"response.future_event", "future":9007199254740993 }`)
	output, err = pipeline.Prepare(t.Context(), unknown)
	require.NoError(t, err)
	require.Equal(t, Output{Value: unknown}, output)
}

func TestPipelineRegistryAndCompatibilityHookSelectSameImageTransform(t *testing.T) {
	const raw = `{"type":"image_edit.completed","b64_json":"synthetic","future":9007199254740993}`
	value := gjson.Parse(raw)
	for _, operation := range []string{ImageGenerateOperation, ImageEditOperation, ImageVariationOperation} {
		t.Run(operation, func(t *testing.T) {
			route := Route{Operation: operation, OutputKind: OutputStreamEvent}
			pipeline := SelectPipeline(route)
			require.NotNil(t, pipeline.Transform)
			require.NotNil(t, pipeline.Project)
			ctx := WithImageOutput(t.Context())
			output, err := pipeline.Prepare(ctx, value)
			require.NoError(t, err)
			legacy, err := Select(route)(ctx, value)
			require.NoError(t, err)
			require.Equal(t, legacy, output.Value)
			require.Equal(t, "synthetic", output.Value.Get("data.0.b64_json").Str)
			require.Equal(t, "9007199254740993", output.Value.Get("future").Raw)
			require.Equal(t, raw, value.Raw)
		})
	}
}

func TestPipelineIdentitySupportsExplicitDataModes(t *testing.T) {
	value := gjson.Parse(`{ "type":"image_generation.completed", "b64_json":"synthetic", "future":9007199254740993 }`)
	for _, pipeline := range []Pipeline{
		{},
		{Transform: Identity},
		SelectPipeline(Route{Operation: ImageGenerateOperation, OutputKind: OutputUnspecified}),
		SelectPipeline(Route{Operation: ImageGenerateOperation, OutputKind: "future"}),
	} {
		output, err := pipeline.Prepare(WithImageOutput(t.Context()), value)
		require.NoError(t, err)
		require.Equal(t, Output{Value: value}, output)
	}
}

func TestPipelineRunsEachStageOnceInOrder(t *testing.T) {
	original := gjson.Parse(`{"original":true}`)
	transformed := gjson.Parse(`{"transformed":true}`)
	var stages []string
	pipeline := Pipeline{
		Transform: func(ctx context.Context, value gjson.Result) (gjson.Result, error) {
			require.Equal(t, original, value)
			stages = append(stages, "transform")
			return transformed, nil
		},
		Project: func(value gjson.Result) Projection {
			require.Equal(t, transformed, value)
			stages = append(stages, "project")
			return Projection{Text: ReadableValue{Text: "Readable.", IsText: true}}
		},
	}
	output, err := pipeline.Prepare(t.Context(), original)
	require.NoError(t, err)
	require.Equal(t, []string{"transform", "project"}, stages)
	require.Equal(t, transformed, output.Value)
	require.Equal(t, "Readable.", output.Projection.Text.Text)
}

func TestPipelineDoesNotPublishPartialOutputOnErrorOrCancellation(t *testing.T) {
	for _, stage := range []string{"before", "transform", "project", "transform error"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			transforms, projections := 0, 0
			want := context.Canceled
			pipeline := Pipeline{
				Transform: func(context.Context, gjson.Result) (gjson.Result, error) {
					transforms++
					if stage == "transform" {
						cancel()
					}
					if stage == "transform error" {
						return gjson.Result{}, want
					}
					return gjson.Parse(`{"changed":true}`), nil
				},
				Project: func(gjson.Result) Projection {
					projections++
					cancel()
					return Projection{Text: ReadableValue{Text: "Do not publish.", IsText: true}}
				},
			}
			if stage == "before" {
				cancel()
			}
			if stage == "transform error" {
				want = errors.New("synthetic transformation failure")
			}
			output, err := pipeline.Prepare(ctx, gjson.Parse(`{"original":true}`))
			require.ErrorIs(t, err, want)
			require.Equal(t, Output{}, output)
			switch stage {
			case "before":
				require.Equal(t, 0, transforms)
				require.Equal(t, 0, projections)
			case "project":
				require.Equal(t, 1, transforms)
				require.Equal(t, 1, projections)
			default:
				require.Equal(t, 1, transforms)
				require.Equal(t, 0, projections)
			}
		})
	}
}
