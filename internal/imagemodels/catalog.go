// Package imagemodels maintains the known image model catalog and checks model
// metadata visibility without downloading the full account model catalog.
package imagemodels

import "github.com/openai/openai-go/v3"

// Entry is a model name supported by the SDK's ImageModel enum. The catalog is
// maintained alongside SDK updates; it is not an exhaustive account inventory.
type Entry struct {
	ID       string `json:"id"`
	Snapshot bool   `json:"snapshot"`
}

// Catalog returns exact SDK model names in a stable order. A new slice is
// returned so callers cannot accidentally change subsequent checks.
func Catalog(includeSnapshots bool) []Entry {
	entries := []Entry{
		{ID: openai.ImageModelGPTImage2_5Sunburst},
		{ID: openai.ImageModelGPTImage2_5Flare},
		{ID: openai.ImageModelGPTImage2},
		{ID: openai.ImageModelGPTImage1_5},
		{ID: openai.ImageModelGPTImage1},
		{ID: openai.ImageModelGPTImage1Mini},
		{ID: openai.ImageModelChatgptImageLatest},
		{ID: openai.ImageModelDallE3},
		{ID: openai.ImageModelDallE2},
	}
	if includeSnapshots {
		entries = append(entries,
			Entry{ID: openai.ImageModelGPTImage2_5Sunburst2026_09_08, Snapshot: true},
			Entry{ID: openai.ImageModelGPTImage2_5Flare2026_09_08, Snapshot: true},
			Entry{ID: openai.ImageModelGPTImage2_2026_04_21, Snapshot: true},
		)
	}
	return entries
}
