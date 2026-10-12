package transformers

import (
	"context"
	"regexp"
	"sort"

	"github.com/tidwall/gjson"
)

// ModelSelection filters exact IDs, sorts them, and then limits the result.
// Negative limits retain every match. Configured filters must both match.
type ModelSelection struct {
	ExactID    *string
	Pattern    *regexp.Regexp
	Descending bool
	Limit      int64
}

// SelectModels retains original records and stable ordering for equal IDs.
// Ambiguous identities make the whole selection unsupported, including records
// that would otherwise be filtered out. Metadata does not affect selection.
func SelectModels(ctx context.Context, items []gjson.Result, selection ModelSelection) ([]gjson.Result, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	type model struct {
		id   string
		item gjson.Result
	}
	selected := make([]model, 0, len(items))
	for _, item := range items {
		id, supported, err := modelIdentity(ctx, item)
		if err != nil || !supported {
			return nil, false, err
		}
		if selection.ExactID != nil && id != *selection.ExactID ||
			selection.Pattern != nil && !selection.Pattern.MatchString(id) {
			continue
		}
		selected = append(selected, model{id, item})
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selection.Descending {
			return selected[i].id > selected[j].id
		}
		return selected[i].id < selected[j].id
	})
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if selection.Limit >= 0 && selection.Limit < int64(len(selected)) {
		selected = selected[:int(selection.Limit)]
	}
	result := make([]gjson.Result, 0, len(selected))
	for _, model := range selected {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		result = append(result, model.item)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return result, true, nil
}
