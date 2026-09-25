package imagemodels

import (
	"slices"
	"testing"
)

func TestCatalog(t *testing.T) {
	aliases := Catalog(false)
	all := Catalog(true)
	want := []Entry{
		{ID: "gpt-image-2.5-sunburst"},
		{ID: "gpt-image-2.5-flare"},
		{ID: "gpt-image-2"},
		{ID: "gpt-image-1.5"},
		{ID: "gpt-image-1"},
		{ID: "gpt-image-1-mini"},
		{ID: "chatgpt-image-latest"},
		{ID: "dall-e-3"},
		{ID: "dall-e-2"},
		{ID: "gpt-image-2.5-sunburst-2026-09-08", Snapshot: true},
		{ID: "gpt-image-2.5-flare-2026-09-08", Snapshot: true},
		{ID: "gpt-image-2-2026-04-21", Snapshot: true},
	}
	if !slices.Equal(all, want) || !slices.Equal(aliases, want[:9]) {
		t.Fatalf("unexpected catalogs: aliases=%+v; all=%+v", aliases, all)
	}
	aliases[0].ID = "mutated"
	if Catalog(false)[0].ID == "mutated" {
		t.Fatal("caller modified the shared catalog")
	}
}
