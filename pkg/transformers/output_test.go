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
