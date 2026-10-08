package transformers

import (
	"context"
	"strconv"

	"github.com/tidwall/gjson"
)

// ProjectListTable selects columns from one loaded API page. It preserves IDs
// and leaves terminal escaping to the renderer. Unsupported or unfamiliar
// records require full-page fallback, so no partial table hides their details.
// Cancellation returns an error instead of a partial table or fallback.
func ProjectListTable(ctx context.Context, operation string, items []gjson.Result) (headers []string, rows [][]string, supported bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}
	var object string
	var fields []string
	switch operation {
	case "(resource) files > (method) list":
		object, fields = "file", []string{"id", "filename", "purpose", "bytes", "status"}
		headers = []string{"ID", "FILENAME", "PURPOSE", "SIZE", "STATUS"}
	case "(resource) batches > (method) list":
		object, fields = "batch", []string{"id", "status"}
		headers = []string{"ID", "STATUS"}
	case "(resource) admin.organization.projects > (method) list":
		object, fields = "organization.project", []string{"id", "name", "status"}
		headers = []string{"ID", "NAME", "STATUS"}
	default:
		return nil, nil, false, nil
	}
	rows = make([][]string, 0, len(items))
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, nil, false, err
		}
		if !item.IsObject() || item.Get("object").Str != object || item.Get("id").Type != gjson.String || item.Get("id").Str == "" {
			return nil, nil, false, nil
		}
		var summary gjson.Result
		if object == "organization.project" {
			summary, _, err = summarizeResourceFields(ctx, item, fields,
				[]string{"object", "created_at", "archived_at", "external_key_id", "residency"})
		} else {
			// Reuse the existing resource allowlists and duplicate-key checks.
			summary, _, err = SummarizeResource(ctx, item, Route{operation, OutputPageItem})
		}
		if err != nil {
			return nil, nil, false, err
		}
		errors := item.Get("errors")
		if !summary.Exists() || !listTableEmptyString(item.Get("status_details")) || resourceFieldPresent(errors) ||
			errors.Exists() && errors.Type != gjson.Null && !errors.IsObject() || !listTableEmptyString(item.Get("error_file_id")) {
			return nil, nil, false, nil
		}
		countsSupported, err := listTableCountsWithoutFailures(ctx, item.Get("request_counts"))
		if err != nil || !countsSupported {
			return nil, nil, false, err
		}
		row := make([]string, len(fields))
		for i, key := range fields {
			if err := ctx.Err(); err != nil {
				return nil, nil, false, err
			}
			value := item.Get(key)
			if object == "organization.project" && key != "id" &&
				(!value.Exists() || value.Type == gjson.Null || value.Type == gjson.String && value.Str == "") {
				row[i] = "-"
				continue
			}
			if key == "bytes" {
				bytes, ok := listTableUnsignedInteger(value)
				if !ok {
					return nil, nil, false, nil
				}
				row[i] = listTableSize(bytes)
			} else {
				if value.Type != gjson.String || value.Str == "" {
					return nil, nil, false, nil
				}
				row[i] = value.Str
			}
		}
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}
	return headers, rows, true, nil
}

func listTableEmptyString(value gjson.Result) bool {
	return !value.Exists() || value.Type == gjson.Null || value.Type == gjson.String && value.Str == ""
}

// Preserve error details and failed request counts in the existing full view.
// Unfamiliar nested counts also require fallback, even when failed is zero.
func listTableCountsWithoutFailures(ctx context.Context, counts gjson.Result) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !counts.Exists() || counts.Type == gjson.Null {
		return true, nil
	}
	if !counts.IsObject() {
		return false, nil
	}
	checked, _, err := summarizeResourceFields(ctx, counts, []string{"total", "completed", "failed"}, nil)
	if err != nil || !checked.Exists() {
		return false, err
	}
	for _, key := range []string{"total", "completed", "failed"} {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		number, ok := listTableUnsignedInteger(counts.Get(key))
		if !ok || key == "failed" && number != 0 {
			return false, nil
		}
	}
	return true, nil
}

func listTableUnsignedInteger(value gjson.Result) (uint64, bool) {
	if value.Type != gjson.Number {
		return 0, false
	}
	number, err := strconv.ParseUint(value.Raw, 10, 64)
	return number, err == nil
}

// Use integer arithmetic to avoid float rounding of large API byte counts.
func listTableSize(bytes uint64) string {
	units := [...]string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	divisor, unit := uint64(1), 0
	for unit < len(units)-1 && bytes/divisor >= 1024 {
		divisor *= 1024
		unit++
	}
	whole := bytes / divisor
	decimal := (bytes%divisor*10 + divisor/2) / divisor
	if decimal == 10 {
		whole++
		decimal = 0
	}
	result := strconv.FormatUint(whole, 10)
	if decimal != 0 {
		result += "." + strconv.FormatUint(decimal, 10)
	}
	return result + " " + units[unit]
}
