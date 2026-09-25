package transformers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestReadableSummariesUseGeneratedRoutesAndKnownSlots(t *testing.T) {
	const media = "aW1hZ2U="
	const summary = "(8 base64 characters; use --format json for full value)"
	for _, test := range []struct {
		resource, method string
		kind             OutputKind
		input, path      string
	}{
		{"images", "generate", OutputResponse, `{"data":[{"b64_json":"aW1hZ2U="}]}`, "data.0.b64_json"},
		{"images", "edit", OutputResponse, `{"data":[{"b64_json":"aW1hZ2U="}]}`, "data.0.b64_json"},
		{"images", "create_variation", OutputResponse, `{"data":[{"b64_json":"aW1hZ2U="}]}`, "data.0.b64_json"},
		{"images", "generate", OutputStreamEvent, `{"type":"image_generation.partial_image","b64_json":"aW1hZ2U="}`, "b64_json"},
		{"images", "generate", OutputStreamEvent, `{"type":"image_generation.completed","b64_json":"aW1hZ2U="}`, "b64_json"},
		{"images", "edit", OutputStreamEvent, `{"type":"image_edit.partial_image","b64_json":"aW1hZ2U="}`, "b64_json"},
		{"images", "edit", OutputStreamEvent, `{"type":"image_edit.completed","b64_json":"aW1hZ2U="}`, "b64_json"},
		{"chat.completions", "create", OutputResponse, `{"choices":[{"message":{"audio":{"data":"aW1hZ2U=","transcript":"full transcript"}}}]}`, "choices.0.message.audio.data"},
		{"chat.completions", "retrieve", OutputResponse, `{"choices":[{"message":{"audio":{"data":"aW1hZ2U="}}}]}`, "choices.0.message.audio.data"},
		{"chat.completions", "update", OutputResponse, `{"choices":[{"message":{"audio":{"data":"aW1hZ2U="}}}]}`, "choices.0.message.audio.data"},
		{"chat.completions", "list", OutputPageItem, `{"choices":[{"message":{"audio":{"data":"aW1hZ2U="}}}]}`, "choices.0.message.audio.data"},
		{"chat.completions", "create", OutputStreamEvent, `{"choices":[{"delta":{"audio":{"data":"aW1hZ2U="}}}]}`, "choices.0.delta.audio.data"},
		{"chat.completions.messages", "list", OutputPageItem, `{"audio":{"data":"aW1hZ2U="}}`, "audio.data"},
		{"responses", "create", OutputResponse, `{"output":[{"type":"image_generation_call","result":"aW1hZ2U="}]}`, "output.0.result"},
		{"responses", "retrieve", OutputResponse, `{"output":[{"type":"image_generation_call","result":"aW1hZ2U="}]}`, "output.0.result"},
		{"responses", "cancel", OutputResponse, `{"output":[{"type":"image_generation_call","result":"aW1hZ2U="}]}`, "output.0.result"},
		{"responses", "compact", OutputResponse, `{"output":[{"type":"image_generation_call","result":"aW1hZ2U="}]}`, "output.0.result"},
		{"responses", "create", OutputStreamEvent, `{"type":"response.audio.delta","delta":"aW1hZ2U="}`, "delta"},
		{"responses", "retrieve", OutputStreamEvent, `{"type":"response.image_generation_call.partial_image","partial_image_b64":"aW1hZ2U="}`, "partial_image_b64"},
		{"responses", "create", OutputStreamEvent, `{"type":"response.output_item.done","item":{"type":"image_generation_call","result":"aW1hZ2U="}}`, "item.result"},
		{"responses", "create", OutputStreamEvent, `{"type":"response.completed","response":{"output":[{"type":"image_generation_call","result":"aW1hZ2U="}]}}`, "response.output.0.result"},
		{"responses.input_items", "list", OutputPageItem, `{"type":"image_generation_call","result":"aW1hZ2U="}`, "result"},
		{"conversations.items", "create", OutputResponse, `{"data":[{"type":"image_generation_call","result":"aW1hZ2U="}]}`, "data.0.result"},
		{"conversations.items", "retrieve", OutputResponse, `{"type":"image_generation_call","result":"aW1hZ2U="}`, "result"},
		{"conversations.items", "list", OutputPageItem, `{"type":"image_generation_call","result":"aW1hZ2U="}`, "result"},
	} {
		resources := []string{test.resource}
		if strings.HasPrefix(test.resource, "responses") {
			resources = append(resources, "beta."+test.resource)
		}
		for _, resource := range resources {
			t.Run(resource+"/"+test.method+"/"+string(test.kind)+"/"+test.path, func(t *testing.T) {
				route := Route{fmt.Sprintf("(resource) %s > (method) %s", resource, test.method), test.kind}
				value := gjson.Parse(test.input)
				got, err := Select(route)(t.Context(), value)
				require.NoError(t, err)
				require.Equal(t, summary, got.Get(test.path).String())
				require.Equal(t, strings.Replace(test.input, `"`+media+`"`, `"`+summary+`"`, 1), got.Raw)
			})
		}
	}
}

func TestReadableSummariesPreserveUserDataAndUnknownFields(t *testing.T) {
	// Every example is valid user data. The old recursive renderer summarized
	// both metadata strings and a numeric vector inside a tool's JSON schema.
	const extra = `"metadata":{"b64_json":"test","type":"response.audio.delta","delta":"note"},"tools":[{"type":"function","parameters":{"const":{"embedding":[1,2],"audio":{"data":"note"},"type":"image_generation_call","result":"test"}}}],"future":{"data":[{"b64_json":"test","embedding":[1,2]}],"type":"response.audio.delta","delta":"note"},"b64_json":"test","embedding":[1,2],"literal.key":9007199254740993`
	for _, test := range []struct {
		route Route
		known string
	}{
		{Route{"(resource) images > (method) generate", OutputResponse}, `"data":[{"b64_json":"aW1hZ2U="}]`},
		{Route{"(resource) embeddings > (method) create", OutputResponse}, `"data":[{"embedding":[0.1,2,3e-5]}]`},
		{Route{"(resource) responses > (method) create", OutputResponse}, `"output":[{"type":"image_generation_call","result":"aW1hZ2U="}]`},
		{Route{"(resource) chat.completions > (method) create", OutputResponse}, `"choices":[{"message":{"audio":{"data":"aW1hZ2U="}}}]`},
		{Route{"(resource) responses > (method) create", OutputStreamEvent}, `"type":"response.audio.delta","delta":"aW1hZ2U="`},
	} {
		t.Run(test.route.Operation+"/"+string(test.route.OutputKind), func(t *testing.T) {
			input := "{ " + extra + ", " + test.known + " }"
			got, err := Select(test.route)(t.Context(), gjson.Parse(input))
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(got.Raw, "{ "+extra+", "), got.Raw)
			require.Contains(t, got.Raw, "use --format json")
			require.True(t, json.Valid([]byte(got.Raw)))
		})
	}
}

func TestReadableSummariesPreserveUnknownShapesAndInvalidMedia(t *testing.T) {
	for _, test := range []struct {
		route Route
		input string
	}{
		{Route{"(resource) images > (method) generate", OutputPageItem}, `{"b64_json":"test"}`},
		{Route{"(resource) unknown > (method) create", OutputResponse}, `{"data":[{"b64_json":"test","embedding":[1,2]}]}`},
		{Route{"(resource) images > (method) generate", OutputResponse}, `{"data":{"b64_json":"test"}}`},
		{Route{"(resource) images > (method) generate", OutputResponse}, `{"data":[{"b64_json":"unexpected readable prose"},{"b64_json":""},{"b64_json":[1,2]}]}`},
		{Route{"(resource) images > (method) generate", OutputStreamEvent}, `{"type":"future","b64_json":"test"}`},
		{Route{"(resource) responses > (method) create", OutputResponse}, `{"output":[{"type":"future","result":"test"}]}`},
		{Route{"(resource) responses > (method) create", OutputStreamEvent}, `{"type":"future","response":{"output":[{"type":"image_generation_call","result":"test"}]},"delta":"test"}`},
		{Route{"(resource) embeddings > (method) create", OutputResponse}, `{"data":[{"embedding":[]},{"embedding":[1,"text",2]},{"embedding":"test"}]}`},
	} {
		t.Run(test.route.Operation+"/"+test.input, func(t *testing.T) {
			value := gjson.Parse(test.input)
			got, err := Select(test.route)(t.Context(), value)
			require.NoError(t, err)
			require.Equal(t, value, got, "unchanged JSON must retain its raw bytes")
		})
	}
}

func TestReadableSummariesMultipleValuesAndSubdocument(t *testing.T) {
	const raw = `{ "data" : [{"embedding":[1e100,9007199254740993]},{"embedding":[0.1,2,3]},{"embedding":[1,"text"]}], "preserved" : 9007199254740993 }`
	const want = `{ "data" : [{"embedding":"(2 numbers; use --format json for full vector)"},{"embedding":"(3 numbers; use --format json for full vector)"},{"embedding":[1,"text"]}], "preserved" : 9007199254740993 }`
	transform := Select(Route{"(resource) embeddings > (method) create", OutputResponse})
	for _, value := range []gjson.Result{gjson.Parse(raw), gjson.Get(`{"wrapper":`+raw+`}`, "wrapper")} {
		got, err := transform(t.Context(), value)
		require.NoError(t, err)
		require.Equal(t, want, got.Raw)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := transform(ctx, gjson.Parse(raw))
	require.ErrorIs(t, err, context.Canceled)
}

func TestReadableSummariesCancelDuringScan(t *testing.T) {
	for _, test := range []struct {
		name, resource, method, input string
	}{
		{"base64", "images", "generate", `{"data":[{"b64_json":` + strconv.Quote(strings.Repeat("QUJD", 32768)) + `}]}`},
		// The decoder can consume many source chunks while filtering newlines
		// within a single Read, so cancellation must reach its input reader.
		{"base64 newlines", "images", "generate", `{"data":[{"b64_json":` + strconv.Quote(strings.Repeat("\r\n", 65536)+"YQ==") + `}]}`},
		{"vector", "embeddings", "create", `{"data":[{"embedding":[` + strings.Repeat("0.25,", 32768) + `1]}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			// The entrypoint checks context before selecting and before scanning
			// the field. Cancel only once the scan has polled several more times.
			controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: 8}
			transform := Select(Route{fmt.Sprintf("(resource) %s > (method) %s", test.resource, test.method), OutputResponse})
			got, err := transform(controlled, gjson.Parse(test.input))
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, gjson.Result{}, got, "cancellation must not return partially transformed output")
		})
	}
}

// Cancel at a repeatable point during CPU work without timers or scheduling races.
type cancelSummaryContext struct {
	context.Context
	cancel context.CancelFunc
	after  int32
	checks atomic.Int32
}

func (ctx *cancelSummaryContext) Err() error {
	if ctx.checks.Add(1) == ctx.after {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestReadableSummariesPreserveBase64Validation(t *testing.T) {
	for _, test := range []struct {
		name, value string
		valid       bool
	}{
		{"padded", "YQ==", true},
		{"unpadded full block", "YWJj", true},
		{"newlines", "\r\nYQ==\r\n", true},
		{"only newlines", "\r\n", true},
		{"empty", "", false},
		{"space", "Y Q==", false},
		{"missing padding", "YQ", false},
		{"extra padding", "YQ===", false},
		{"trailing text", "YQ==!", false},
		{"invalid after many chunks", strings.Repeat("QUJD", 32768) + "!", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := `{"data":[{"b64_json":` + strconv.Quote(test.value) + `}]}`
			transform := Select(Route{"(resource) images > (method) generate", OutputResponse})
			got, err := transform(t.Context(), gjson.Parse(input))
			require.NoError(t, err)
			if test.valid {
				require.Equal(t, fmt.Sprintf("(%d base64 characters; use --format json for full value)", len(test.value)), got.Get("data.0.b64_json").String())
			} else {
				require.Equal(t, input, got.Raw)
			}
		})
	}
}
