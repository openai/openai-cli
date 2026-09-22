package custom

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"strings"
	"testing"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestReadableOutputSelectsAndPreparesOnce(t *testing.T) {
	for _, format := range []string{"", "auto", "AUTO", "text", "TEXT"} {
		for _, test := range []struct {
			kind                OutputKind
			resource, raw, want string
		}{
			{OutputResponse, "responses", `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"Prepared text"}]}]}`, "Prepared text\n"},
			{OutputStreamEvent, "responses", `{"type":"response.output_text.delta","delta":"Prepared text"}`, "Prepared text\n"},
			{OutputPageItem, "models", `{"id":"normalized","object":"model","created":123}`, "ID: normalized\nDetails: Use --format json for all fields.\n"},
		} {
			t.Run(format+"/"+string(test.kind), func(t *testing.T) {
				var out strings.Builder
				selected, normalized := 0, 0
				opts := ShowJSONOpts{Context: t.Context(), Operation: "(resource) " + test.resource + " > (method) create", OutputKind: test.kind, Format: format, ExplicitFormat: format != "", Stdout: &out}
				selector := func(route transformers.Route) transformers.Transformer {
					selected++
					require.Equal(t, test.kind, route.OutputKind)
					return func(_ context.Context, value gjson.Result) (gjson.Result, error) {
						normalized++
						require.Equal(t, "original", value.Get("id").String())
						return gjson.Parse(test.raw), nil
					}
				}
				value := gjson.Parse(`{"id":"original"}`)
				if test.kind == OutputResponse {
					require.NoError(t, showJSON(value, opts, selector))
				} else {
					source := &transformTestIterator{items: []any{outputJSON{value}}}
					require.NoError(t, showJSONIterator(source, -1, opts, selector))
				}
				require.Equal(t, test.want, out.String())
				require.Equal(t, 1, selected)
				require.Equal(t, 1, normalized)
			})
		}
	}
}

func TestPreparedIteratorKeepsJSONSeparateFromSummary(t *testing.T) {
	const raw = `{"id":"file_test","object":"file","filename":"original.txt","created_at":9007199254740993}`
	iterator := &outputIterator[any]{
		source: &transformTestIterator{items: []any{outputJSON{gjson.Parse(raw)}}}, context: t.Context(), remaining: -1,
		transform: transformers.Identity,
		route:     transformers.Route{Operation: "(resource) files > (method) list", OutputKind: OutputPageItem},
	}
	require.True(t, iterator.Next())
	for range 3 {
		require.Equal(t, raw, iterator.Current().RawJSON())
		require.Equal(t, "file_test", iterator.Current().View.Summary.Get("id").String())
		require.True(t, iterator.Current().View.Omitted)
		require.False(t, iterator.Current().View.Summary.Get("created_at").Exists())
	}
}

func TestImageOutputUsesSelectedRouteAndFormatGate(t *testing.T) {
	_, encoded := imageStreamTestPNG(t, color.RGBA{R: 180, A: 255})
	for _, operation := range []string{transformers.ImageGenerateOperation, transformers.ImageEditOperation, transformers.ImageVariationOperation} {
		for _, format := range []string{"auto", "text", "json", "raw"} {
			t.Run(operation+"/"+format, func(t *testing.T) {
				var out strings.Builder
				directory := t.TempDir()
				plan := &imageOutputPlan{directory: directory, filenameStem: "result", name: "result"}
				ctx := transformers.WithImageOutput(context.WithValue(t.Context(), imagePresentationKey{}, imagePresentation{plan, &out}))
				selected, normalized := 0, 0
				selector := func(route transformers.Route) transformers.Transformer {
					selected++
					require.Equal(t, operation, route.Operation)
					require.Equal(t, OutputResponse, route.OutputKind)
					return func(_ context.Context, value gjson.Result) (gjson.Result, error) {
						normalized++
						return value, nil
					}
				}
				value := gjson.Parse(fmt.Sprintf(`{"data":[{"b64_json":%q}]}`, encoded))
				opts := ShowJSONOpts{Context: ctx, Operation: operation, OutputKind: OutputResponse, Format: format, ExplicitFormat: true, Stdout: &out}
				require.NoError(t, showJSON(value, opts, selector))
				files, err := os.ReadDir(directory)
				require.NoError(t, err)
				if format == "auto" || format == "text" {
					require.Len(t, files, 1)
					require.Equal(t, 1, selected)
					require.Equal(t, 1, normalized)
					require.Contains(t, out.String(), "Saved image:")
				} else {
					require.Empty(t, files)
					require.Zero(t, selected)
					require.Zero(t, normalized)
					require.JSONEq(t, value.Raw, out.String())
				}
			})
		}
	}
}
