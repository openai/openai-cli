package transformers

import (
	"context"
	"slices"
	"sort"

	"github.com/tidwall/gjson"
)

// ProjectModelNames selects and sorts exact IDs from already-selected model
// records. Metadata does not affect selection. Ambiguous identities require
// full rendering; terminal escaping belongs to the renderer.
func ProjectModelNames(ctx context.Context, items []gjson.Result, descending bool) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		name, supported, err := modelIdentity(ctx, item)
		if err != nil || !supported {
			return nil, false, err
		}
		names = append(names, name)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	sort.Strings(names)
	if descending {
		slices.Reverse(names)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return names, true, nil
}

func modelIdentity(ctx context.Context, item gjson.Result) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	valid := item.IsObject() && gjson.Valid(item.Raw)
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if !valid {
		return "", false, nil
	}
	var name string
	var ids, objects int
	item.ForEach(func(key, value gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		switch key.Str {
		case "id":
			ids++
			valid = ids == 1 && value.Type == gjson.String && value.Str != ""
			name = value.Str
		case "object":
			objects++
			valid = objects == 1 && value.Type == gjson.String && value.Str == "model"
		}
		return valid
	})
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return name, valid && ids == 1 && objects == 1, nil
}
