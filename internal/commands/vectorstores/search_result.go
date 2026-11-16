package vectorstores

import (
	"strings"

	"github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
)

// ValidateSearchResult checks the fields used to display a human-readable match.
func ValidateSearchResult(result openai.VectorStoreSearchResponse) error {
	// The SDK also accepts numbers and booleans for some scalar fields.
	// Inspect their wire types before presenting them as text or relevance scores.
	source := strings.HasPrefix(result.JSON.FileID.Raw(), `"`) && result.FileID != "" && strings.HasPrefix(result.JSON.Filename.Raw(), `"`)
	score := gjson.Parse(result.JSON.Score.Raw()).Type == gjson.Number && result.Score >= 0 && result.Score <= 1
	if !source || !score || !result.JSON.Content.Valid() {
		return &commands.LocalError{Message: "The API returned an incomplete search result."}
	}
	for _, chunk := range result.Content {
		if chunk.Type != "text" || !strings.HasPrefix(chunk.JSON.Text.Raw(), `"`) {
			return &commands.LocalError{Message: "The API returned an incomplete search passage."}
		}
	}
	return nil
}
