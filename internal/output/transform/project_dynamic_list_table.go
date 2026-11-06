package transform

import (
	"context"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
)

// Arbitrary lists can present their actual fields when each record fits the
// same set of columns. Preserve every scalar, including unfamiliar fields.
// The renderer requests labeled output when the full values cannot fit.
func projectDynamicListTable(ctx context.Context, operation string, items []gjson.Result) (ListTableProjection, error) {
	if operation == "(resource) models > (method) list" || len(items) == 0 {
		return ListTableProjection{}, nil
	}
	table := ListTableProjection{FullValues: true}
	var keys []string
	route := Route{Operation: operation, OutputKind: OutputPageItem}
	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return ListTableProjection{}, err
		}
		if item.IsObject() != items[0].IsObject() || !gjson.Valid(item.Raw) {
			return ListTableProjection{}, ctx.Err()
		}
		fields, err := dynamicListFields(ctx, item, route)
		if err != nil || len(fields) == 0 {
			return ListTableProjection{}, err
		}
		if i == 0 {
			for key := range fields {
				keys = append(keys, key)
			}
			// Put identifiers and names first. Stable ordering avoids depending
			// on JSON property order or map iteration.
			slices.SortFunc(keys, func(left, right string) int {
				if order := listColumnRank(left) - listColumnRank(right); order != 0 {
					return order
				}
				return strings.Compare(left, right)
			})
			seen := make(map[string]bool, len(keys))
			for _, key := range keys {
				label := strings.ToUpper(strings.ReplaceAll(key, "_", " "))
				if seen[label] || strings.TrimSpace(label) == "" {
					return ListTableProjection{}, nil
				}
				seen[label] = true
				table.Headers = append(table.Headers, label)
			}
		}
		if len(fields) != len(keys) {
			return ListTableProjection{}, nil
		}
		row := make([]string, len(keys))
		for col, key := range keys {
			value, ok := fields[key]
			if !ok {
				return ListTableProjection{}, nil
			}
			row[col] = value
		}
		table.Rows = append(table.Rows, row)
	}
	if err := ctx.Err(); err != nil {
		return ListTableProjection{}, err
	}
	return table, nil
}

func listColumnRank(key string) int {
	switch {
	case key == "id":
		return 0
	case key == "name" || key == "filename" || key == "title":
		return 1
	case strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_name"):
		return 2
	case key == "status" || key == "type" || key == "role":
		return 3
	default:
		return 4
	}
}

func dynamicListFields(ctx context.Context, item gjson.Result, route Route) (map[string]string, error) {
	if !item.IsObject() {
		if item.Type == gjson.JSON {
			return nil, nil
		}
		return map[string]string{"value": listScalarText(item)}, nil
	}
	// Apply the same approved field omissions as labeled readable output.
	// Unknown fields leave the original record available for projection.
	summary, _, err := SummarizeResource(ctx, item, route)
	if err != nil {
		return nil, err
	}
	if summary.Exists() {
		item = summary
	}
	fields := make(map[string]string)
	valid := true
	item.ForEach(func(k, v gjson.Result) bool {
		key := k.Str
		if _, duplicate := fields[key]; duplicate || (v.Type != gjson.Null && listDetailField(key)) {
			valid = false
			return false
		}
		if summary.Exists() && (key == "file_counts" || key == "usage") {
			var label, total string
			label, total, valid, err = listCountTotal(ctx, key, v)
			if valid {
				fields[strings.ToLower(label)] = total
			}
			return valid && err == nil
		}
		if v.Type == gjson.JSON {
			valid = false
			return false
		}
		fields[key] = listScalarText(v)
		return ctx.Err() == nil
	})
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, ctx.Err()
	}
	return fields, ctx.Err()
}

func listScalarText(value gjson.Result) string {
	if value.Type == gjson.String {
		return value.Str
	}
	return value.Raw // Preserve large integers, decimals, booleans and null.
}

// Keep diagnostics and credential-bearing fields together in labeled output.
func listDetailField(key string) bool {
	for _, word := range strings.Split(strings.ToLower(key), "_") {
		switch word {
		case "error", "errors", "warning", "warnings", "action", "actions", "incomplete",
			"key", "keys", "secret", "password", "credentials", "credential", "token",
			"cookie", "authorization", "value", "url", "pem", "certificate", "content", "instructions", "prompt":
			return true
		}
	}
	return false
}

// These nested summaries have a single useful total. Any failed, cancelled,
// pending, or unfamiliar counter keeps the complete summary in labeled output.
func listCountTotal(ctx context.Context, key string, counts gjson.Result) (string, string, bool, error) {
	fields := []string{"prompt_tokens", "completion_tokens"}
	total, label := "total_tokens", "TOKENS"
	if key == "file_counts" {
		fields = []string{"in_progress", "completed", "failed", "cancelled"}
		total, label = "total", "FILES"
	}
	values, err := knownResourceFields(ctx, counts, fields, []string{total})
	if err != nil || values == nil || !counts.IsObject() {
		return "", "", false, err
	}
	remaining, ok := listTableUnsignedInteger(values[total])
	if !ok {
		return "", "", false, nil
	}
	for _, field := range fields {
		count, ok := listTableUnsignedInteger(values[field])
		if !ok || count > remaining || key == "file_counts" && field != "completed" && count != 0 {
			return "", "", false, nil
		}
		remaining -= count
	}
	return label, values[total].Raw, remaining == 0, nil
}
