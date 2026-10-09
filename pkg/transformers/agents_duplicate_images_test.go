package transformers

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAgentsDuplicateImagesPreserveExactSourceSpans(t *testing.T) {
	const template = `{"id":null,"type":"message","\u0074ype":"message","role":"user","role":"user","future":{"image_url":"data:image/png;base64,RlVUVVJF"},"content":[{"type":"input_text","text":"Before image data:image/png;base64,VEVYVA=="},{"type":"input_image","type":"input_image","image_url":"FIRST","image_\u0075rl":"SECOND","image_url":"data:image/png;base64,invalid!","image_url":"https://synthetic.invalid/image.png","image_url":null,"future":9007199254740993}],"\u0063ontent":[{"type":"input_image","image_url":"THIRD"},{"type":"future_image","image_url":"data:image/png;base64,VU5LTk9XTg=="},{"type":"input_text","text":"After image"}]}`
	source := strings.NewReplacer("FIRST", "data:image/png;base64,QUJDRA==", "SECOND", "data:image/gif;base64,QUJDRA%3D%3D", "THIRD", "data:image/webp;base64,QUJD").Replace(template)
	want := strings.NewReplacer("FIRST", "(image/png; 8 base64 characters)", "SECOND", "(image/gif; 8 base64 characters)", "THIRD", "(image/webp; 4 base64 characters)").Replace(template)
	for _, value := range []gjson.Result{gjson.Parse(source), gjson.Get(`{"wrapper":`+source+`}`, "wrapper")} {
		summary, hidden, err := SummarizeResource(t.Context(), value, agentsItemsListRoute)
		require.NoError(t, err)
		require.True(t, hidden)
		require.Equal(t, want, summary.Raw)
		require.Equal(t, source, value.Raw)
	}
}

func TestAgentsDuplicateImagesKeepDiscriminatorAmbiguityLocal(t *testing.T) {
	const image = `{"type":"input_image","image_url":"data:image/png;base64,QUJDRA=="}`
	for _, tc := range []struct{ name, value string }{
		{"item type", `{"type":"message","type":"function_call_output","role":"user","content":[` + image + `]}`},
		{"item role", `{"type":"message","role":"user","\u0072ole":"assistant","content":[` + image + `]}`},
		{"case ambiguous role", `{"type":"message","role":"user","Role":"user","content":[` + image + `]}`},
		{"nonstring type", `{"type":"message","type":null,"role":"user","content":[` + image + `]}`},
		{"part type", `{"type":"message","role":"user","content":[{"type":"input_image","type":"input_text","image_url":"data:image/png;base64,QUJDRA=="}]}`},
		{"screenshot type", `{"type":"computer_use_call","output":{"type":"computer_screenshot","type":"future_screenshot","image_url":"data:image/jpeg;base64,QUJDRA=="}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := gjson.Parse(tc.value)
			summary, hidden, err := SummarizeResource(t.Context(), value, agentsItemsListRoute)
			require.NoError(t, err)
			require.False(t, hidden)
			require.Equal(t, tc.value, summary.Raw)
			// A separate known item remains eligible beside the unchanged object.
			event := `{"type":"agent.session.turn.item.added","item":` + tc.value + `,"item":{"type":"function_call_output","output":[` + image + `]}}`
			want := strings.TrimSuffix(event, image+`]}}`) + strings.Replace(image, "data:image/png;base64,QUJDRA==", "(image/png; 8 base64 characters)", 1) + `]}}`
			var projector AgentsStreamProjector
			projected, ok, err := projector.Project(t.Context(), gjson.Parse(event), agentsEventsRoute)
			require.NoError(t, err)
			require.True(t, ok)
			require.True(t, projector.HasOmissions())
			require.Equal(t, want, projected.Details.Raw)
		})
	}
}

func TestAgentsDuplicateImagesPreserveRepeatedStreamContainers(t *testing.T) {
	const template = `{"type":"agent.session.turn.item.added","\u0074ype":"agent.session.turn.item.added","event_id":"ev_duplicates","session_id":"sess_test","turn_id":"turn_test","item":null,"item":{"id":"assistant","type":"message","role":"assistant","content":[{"type":"output_text","text":"Before media"}]},"it\u0065m":{"type":"message","role":"user","content":[{"type":"input_image","image_url":"FIRST"}]},"item":{"type":"function_call_output","output":[{"type":"input_image","image_url":"SECOND"}]},"item":{"id":"computer","type":"computer_use_call","output":null,"output":{"type":"computer_screenshot","\u0074ype":"computer_screenshot","image_url":"THIRD","image_url":"FOURTH"}},"future":{"integer":9007199254740993,"text":"After media"}}`
	source := strings.NewReplacer("FIRST", "data:image/png;base64,QUJDRA==", "SECOND", "data:image/webp;base64,QUJD", "THIRD", "data:image/jpeg;base64,QUJDRA==", "FOURTH", "data:image/jpeg;base64,QUJD").Replace(template)
	want := strings.NewReplacer("FIRST", "(image/png; 8 base64 characters)", "SECOND", "(image/webp; 4 base64 characters)", "THIRD", "(JPEG screenshot; 8 base64 characters)", "FOURTH", "(JPEG screenshot; 4 base64 characters)").Replace(template)
	for _, value := range []gjson.Result{gjson.Parse(source), gjson.Get(`{"wrapper":`+source+`}`, "wrapper")} {
		var projector AgentsStreamProjector
		event, projected, err := projector.Project(t.Context(), value, agentsEventsRoute)
		require.NoError(t, err)
		require.True(t, projected)
		require.True(t, projector.HasOmissions())
		require.Equal(t, want, event.Details.Raw)
		require.Equal(t, source, value.Raw)
	}
	conflict := strings.Replace(source, `"\u0074ype":"agent.session.turn.item.added"`, `"\u0074ype":"future.event"`, 1)
	var projector AgentsStreamProjector
	event, projected, err := projector.Project(t.Context(), gjson.Parse(conflict), agentsEventsRoute)
	require.NoError(t, err)
	require.True(t, projected)
	require.False(t, projector.HasOmissions())
	require.Equal(t, conflict, event.Details.Raw)
}

func TestAgentsDuplicateImagesCancelDuringLaterImage(t *testing.T) {
	source := `{"type":"function_call_output","output":[{"type":"input_image","image_url":"data:image/png;base64,QUJD","image_url":"data:image/png;base64,` + strings.Repeat("%51%55%4a%44", 1<<18) + `"}]}`
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: 64}
	value := gjson.Parse(source)
	summary, hidden, err := SummarizeResource(controlled, value, agentsItemsListRoute)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, hidden)
	require.False(t, summary.Exists())
	require.Equal(t, source, value.Raw)
}

func TestAgentsDuplicateImagesAvoidPerImagePayloadCopies(t *testing.T) {
	const count = 64
	part := `{"type":"input_image","image_url":"data:image/png;base64,` + strings.Repeat("QUJD", 256) + `","image_url":"data:image/webp;base64,QUJD"}`
	source := `{"type":"function_call_output","future_padding":"` + strings.Repeat("x", 1<<20) + `","output":[` + strings.TrimSuffix(strings.Repeat(part+",", count), ",") + `]}`
	value := gjson.Parse(source)
	result := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			summary, hidden, err := SummarizeResource(context.Background(), value, agentsItemsListRoute)
			if err != nil || !hidden || strings.Count(summary.Raw, "base64 characters)") != count*2 {
				b.Fatal("duplicate image summary failed")
			}
		}
	})
	// One full copy per image would copy at least 128 MiB of padding.
	require.Less(t, result.AllocedBytesPerOp(), int64(len(source)*8))
	t.Logf("source=%d bytes, images=%d, allocated=%d bytes/op", len(source), count*2, result.AllocedBytesPerOp())
}
