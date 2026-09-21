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

func TestReadablePipelineSelectsAndPreparesOnce(t *testing.T) {
	for _, format := range []string{"", "auto", "AUTO", "text", "TEXT"} {
		for _, kind := range []OutputKind{OutputResponse, OutputPageItem, OutputStreamEvent} {
			t.Run(format+"/"+string(kind), func(t *testing.T) {
				var out strings.Builder
				selected, normalized, projected := 0, 0, 0
				opts := ShowJSONOpts{Context: t.Context(), Operation: "(resource) responses > (method) create", OutputKind: kind, Format: format, ExplicitFormat: format != "", Stdout: &out}
				selectPipeline := func(route transformers.Route) transformers.Pipeline {
					selected++
					require.Equal(t, kind, route.OutputKind)
					return transformers.Pipeline{
						Transform: func(_ context.Context, value gjson.Result) (gjson.Result, error) {
							normalized++
							require.Equal(t, "original", value.Get("id").String())
							return gjson.Parse(`{"id":"normalized"}`), nil
						},
						Project: func(value gjson.Result) transformers.Projection {
							projected++
							require.Equal(t, "normalized", value.Get("id").String())
							return transformers.Projection{Text: transformers.ReadableValue{Text: "Prepared text", IsText: true}}
						},
					}
				}
				value := gjson.Parse(`{"id":"original"}`)
				if kind == OutputResponse {
					require.NoError(t, showJSON(value, opts, selectPipeline))
				} else {
					source := &transformTestIterator{items: []any{outputJSON{value}}}
					require.NoError(t, showJSONIterator(source, -1, opts, selectPipeline))
				}
				require.Equal(t, "Prepared text\n", out.String())
				require.Equal(t, 1, selected)
				require.Equal(t, 1, normalized)
				require.Equal(t, 1, projected)
			})
		}
	}
}

func TestPreparedIteratorKeepsJSONSeparateFromSummary(t *testing.T) {
	const raw = `{"id":"file_test","future":9007199254740993,"projection":{"text":"ordinary API data"}}`
	projected := 0
	iterator := &outputIterator[any]{
		source: &transformTestIterator{items: []any{outputJSON{gjson.Parse(raw)}}}, context: t.Context(), remaining: -1,
		pipeline: transformers.Pipeline{Project: func(value gjson.Result) transformers.Projection {
			projected++
			return transformers.Projection{Summary: gjson.Parse(`{"id":"summary"}`), Omitted: true}
		}},
	}
	require.True(t, iterator.Next())
	for range 3 {
		require.Equal(t, raw, iterator.Current().RawJSON())
		require.Equal(t, "summary", iterator.Current().Projection.Summary.Get("id").String())
	}
	require.Equal(t, 1, projected)
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
				selector := func(route transformers.Route) transformers.Pipeline {
					selected++
					require.Equal(t, operation, route.Operation)
					require.Equal(t, OutputResponse, route.OutputKind)
					return transformers.Pipeline{Transform: func(_ context.Context, value gjson.Result) (gjson.Result, error) {
						normalized++
						return value, nil
					}}
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
