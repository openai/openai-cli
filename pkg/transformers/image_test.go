package transformers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestImageResponsePreservesEnvelopeAndItemValidation(t *testing.T) {
	// Keep all metadata, numeric precision, formatting and malformed individual
	// items. The image saver can still recover the other items in the response.
	value := gjson.Parse(`{ "created": 9007199254740993, "data": [{"b64_json":"synthetic-image"}, null], "usage": {"tokens": 7}, "future": true }`)
	result, err := ImageResponse(context.Background(), value)
	require.NoError(t, err)
	require.Equal(t, value, result)
}

func TestImageStreamEventNormalizesKnownEvents(t *testing.T) {
	for _, kind := range []string{"image_generation.completed", "image_generation.partial_image"} {
		t.Run(kind, func(t *testing.T) {
			value := gjson.Parse(`{"type":"` + kind + `","b64_json":"synthetic\\image\"bytes","partial_image_index":2,"created_at":9007199254740993,"usage":{"total_tokens":12},"future":[true,null,{"key":"value"}]}`)
			result, err := ImageStreamEvent(context.Background(), value)
			require.NoError(t, err)
			require.True(t, gjson.Valid(result.Raw))
			require.False(t, result.Get("b64_json").Exists())
			require.Len(t, result.Get("data").Array(), 1)
			require.Equal(t, value.Get("b64_json").Str, result.Get("data.0.b64_json").Str)
			for _, field := range []string{"type", "partial_image_index", "created_at", "usage", "future"} {
				require.Equal(t, value.Get(field).Raw, result.Get(field).Raw, field)
			}
			// The caller's original response must remain reusable.
			require.True(t, value.Get("b64_json").Exists())
			require.False(t, value.Get("data").Exists())
		})
	}
}

func TestImageStreamEventUnknownEventsPreserveRawBytes(t *testing.T) {
	for _, raw := range []string{
		`{ "type" : "image_generation.future_event", "b64_json" : "synthetic", "unknown" : 9007199254740993 }`,
		`{ "type": "error", "error": { "message": "synthetic error" } }`,
		`{ "event": "unrecognized" }`,
	} {
		value := gjson.Parse(raw)
		result, err := ImageStreamEvent(context.Background(), value)
		require.NoError(t, err)
		require.Equal(t, value, result)
	}
}

func TestImageStreamEventErrorsDoNotIncludeResponseContents(t *testing.T) {
	for _, raw := range []string{
		`{"type":"image_generation.completed","prompt":"synthetic-private-prompt"}`,
		`{"type":"image_generation.completed","b64_json":"","prompt":"synthetic-private-prompt"}`,
		`{"type":"image_generation.completed","b64_json":{"secret":"synthetic-private-image"}}`,
		`{"type":"image_generation.partial_image","b64_json":"synthetic-private-image","invalid":synthetic-private-value}`,
	} {
		result, err := ImageStreamEvent(context.Background(), gjson.Parse(raw))
		require.Error(t, err)
		require.Empty(t, result.Raw)
		require.NotContains(t, err.Error(), "synthetic-private")
		require.Contains(t, err.Error(), "image stream event")
	}
}

func TestImageTransformersRespectCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	value := gjson.Parse(`{"type":"image_generation.completed","b64_json":"synthetic"}`)
	for _, transform := range []Transformer{
		ImageResponse,
		ImageStreamEvent,
		Select(Route{Operation: ImageGenerateOperation, OutputKind: OutputResponse}),
		Select(Route{Operation: ImageGenerateOperation, OutputKind: OutputStreamEvent}),
	} {
		for _, input := range []context.Context{ctx, WithImageOutput(ctx)} {
			result, err := transform(input, value)
			require.ErrorIs(t, err, context.Canceled)
			require.Empty(t, result.Raw)
		}
	}
}

func TestImageSelectionRequiresExplicitInvocationOptIn(t *testing.T) {
	value := gjson.Parse(`{ "type" : "image_generation.completed", "b64_json" : "synthetic" }`)
	route := Route{Operation: ImageGenerateOperation, OutputKind: OutputStreamEvent}
	transform := Select(route)
	plain := context.WithValue(context.Background(), struct{ unrelated string }{}, true)
	result, err := transform(plain, value)
	require.NoError(t, err)
	require.Equal(t, value, result)

	result, err = transform(WithImageOutput(plain), value)
	require.NoError(t, err)
	require.Equal(t, "synthetic", result.Get("data.0.b64_json").String())
	require.False(t, result.Get("b64_json").Exists())

	// The transform and parent context remain safe to reuse for API output.
	result, err = transform(plain, value)
	require.NoError(t, err)
	require.Equal(t, value, result)
}

func TestImageSelectionDoesNotAffectOtherRoutes(t *testing.T) {
	value := gjson.Parse(`{ "type" : "image_generation.completed", "b64_json" : "synthetic" }`)
	for _, route := range []Route{
		{Operation: ImageGenerateOperation, OutputKind: OutputUnspecified},
		{Operation: ImageGenerateOperation, OutputKind: OutputPageItem},
		{Operation: ImageGenerateOperation, OutputKind: "future"},
		{Operation: "images.generate", OutputKind: OutputStreamEvent},
		{Operation: "(resource) images > (method) edit", OutputKind: OutputStreamEvent},
		{Operation: "(resource) responses > (method) create", OutputKind: OutputStreamEvent},
		{OutputKind: OutputStreamEvent},
	} {
		result, err := Select(route)(WithImageOutput(context.Background()), value)
		require.NoError(t, err)
		require.Equal(t, value, result, route)
	}
}
