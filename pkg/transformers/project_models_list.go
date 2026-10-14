package transformers

import (
	"context"
	"sort"

	"github.com/tidwall/gjson"
)

// ModelListRow preserves an ID and its owner from the same API record.
// Owner is nil when the field is absent, empty, malformed, or ambiguous.
type ModelListRow struct {
	ID    string
	Owner *string
}

// ProjectModelsList selects ID/owner pairs from already-selected records.
// Equal IDs retain their input order. Other metadata does not affect selection.
// Ambiguous identities require full rendering; the renderer escapes controls.
func ProjectModelsList(ctx context.Context, items []gjson.Result, descending bool) ([]ModelListRow, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	rows := make([]ModelListRow, 0, len(items))
	for _, item := range items {
		name, supported, err := modelIdentity(ctx, item)
		if err != nil || !supported {
			return nil, false, err
		}
		row := ModelListRow{ID: name}
		owners := 0
		item.ForEach(func(key, value gjson.Result) bool {
			if ctx.Err() != nil {
				return false
			}
			if key.Str == "owned_by" {
				owners++
				if owners == 1 && value.Type == gjson.String && value.Str != "" {
					owner := value.Str
					row.Owner = &owner
				} else {
					row.Owner = nil
				}
			}
			return true
		})
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if descending {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].ID < rows[j].ID
	})
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return rows, true, nil
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
