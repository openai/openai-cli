package transformers

import (
	"context"
	"strconv"

	"github.com/tidwall/gjson"
)

// ProjectListTable selects columns from one loaded API page. It preserves IDs
// and leaves terminal escaping to the renderer. Unsupported or unfamiliar
// records require full-page fallback, so no partial table hides their details.
func ProjectListTable(operation string, items []gjson.Result) (headers []string, rows [][]string, supported bool) {
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
		return nil, nil, false
	}
	rows = make([][]string, 0, len(items))
	for _, item := range items {
		if !item.IsObject() || item.Get("object").Str != object || item.Get("id").Type != gjson.String || item.Get("id").Str == "" {
			return nil, nil, false
		}
		var summary gjson.Result
		if object == "organization.project" {
			summary, _, _ = summarizeResourceFields(context.Background(), item, fields,
				[]string{"object", "created_at", "archived_at", "external_key_id", "residency"})
		} else {
			// Reuse the existing resource allowlists and duplicate-key checks.
			summary, _, _ = SummarizeResource(context.Background(), item, Route{operation, OutputPageItem})
		}
		errors := item.Get("errors")
		if !summary.Exists() || !listTableEmptyString(item.Get("status_details")) || resourceFieldPresent(errors) ||
			errors.Exists() && errors.Type != gjson.Null && !errors.IsObject() || !listTableEmptyString(item.Get("error_file_id")) ||
			!listTableCountsWithoutFailures(item.Get("request_counts")) {
			return nil, nil, false
		}
		row := make([]string, len(fields))
		for i, key := range fields {
			value := item.Get(key)
			if object == "organization.project" && key != "id" &&
				(!value.Exists() || value.Type == gjson.Null || value.Type == gjson.String && value.Str == "") {
				row[i] = "-"
				continue
			}
			if key == "bytes" {
				bytes, ok := listTableUnsignedInteger(value)
				if !ok {
					return nil, nil, false
				}
				row[i] = listTableSize(bytes)
			} else {
				if value.Type != gjson.String || value.Str == "" {
					return nil, nil, false
				}
				row[i] = value.Str
			}
		}
		rows = append(rows, row)
	}
	return headers, rows, true
}

func listTableEmptyString(value gjson.Result) bool {
	return !value.Exists() || value.Type == gjson.Null || value.Type == gjson.String && value.Str == ""
}

// Preserve error details and failed request counts in the existing full view.
// Unfamiliar nested counts also require fallback, even when failed is zero.
func listTableCountsWithoutFailures(counts gjson.Result) bool {
	if !counts.Exists() || counts.Type == gjson.Null {
		return true
	}
	if !counts.IsObject() {
		return false
	}
	checked, _, _ := summarizeResourceFields(context.Background(), counts, []string{"total", "completed", "failed"}, nil)
	if !checked.Exists() {
		return false
	}
	for _, key := range []string{"total", "completed", "failed"} {
		number, ok := listTableUnsignedInteger(counts.Get(key))
		if !ok || key == "failed" && number != 0 {
			return false
		}
	}
	return true
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
