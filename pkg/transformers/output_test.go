package transformers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDefaultTransformersPreserveResponses(t *testing.T) {
	for _, operation := range []string{"", "images.generate", "responses.list", ImageGenerateOperation} {
		for _, kind := range []OutputKind{OutputUnspecified, OutputResponse, OutputPageItem, OutputStreamEvent} {
			value := gjson.Parse("{ \"id\" : \"synthetic\", \"data\" : [1, 2] }")
			result, err := Select(Route{Operation: operation, OutputKind: kind})(context.Background(), value)
			require.NoError(t, err)
			require.Equal(t, value, result)
		}
	}
}

func TestImageDefaultTransformerRoutes(t *testing.T) {
	value := gjson.Parse(`{"type":"image_generation.completed","b64_json":"synthetic","future":9007199254740993}`)
	for _, operation := range []string{ImageGenerateOperation, ImageEditOperation, ImageVariationOperation} {
		t.Run(operation, func(t *testing.T) {
			result, err := Select(Route{Operation: operation, OutputKind: OutputStreamEvent})(WithImageOutput(t.Context()), value)
			require.NoError(t, err)
			require.Equal(t, "synthetic", result.Get("data.0.b64_json").Str)
			require.Equal(t, "9007199254740993", result.Get("future").Raw)
			for _, kind := range []OutputKind{OutputPageItem, OutputUnspecified, "future"} {
				original, err := Select(Route{Operation: operation, OutputKind: kind})(WithImageOutput(t.Context()), value)
				require.NoError(t, err)
				require.Equal(t, value, original)
			}
		})
	}
}
