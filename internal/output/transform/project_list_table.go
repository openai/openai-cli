package transform

import (
	"context"
	"slices"
	"strconv"

	"github.com/tidwall/gjson"
)

// ListTableProjection describes one loaded screen. Empty headers request labeled output.
type ListTableProjection struct {
	Headers []string
	Rows    [][]string
	// ProtectedColumn identifies the value to keep complete in compact rows.
	// FullValues projections preserve every column and ignore this index.
	ProtectedColumn int
	FullValues      bool
}

// ProjectListTable selects columns from one loaded API page. It preserves IDs
// and leaves terminal escaping to the renderer. Unsupported or unfamiliar
// records require full-page fallback, so no partial table hides their details.
// Cancellation returns an error instead of a partial table or fallback.
func ProjectListTable(ctx context.Context, operation string, items []gjson.Result) (ListTableProjection, error) {
	if !IsResourceListOperation(operation) {
		return ListTableProjection{}, ctx.Err()
	}
	return ProjectRecordTable(ctx, operation, items)
}

// ProjectRecordTable projects a list whose command or action established its
// identity. Handwritten lists can supply records without generated operation metadata.
func ProjectRecordTable(ctx context.Context, operation string, items []gjson.Result) (ListTableProjection, error) {
	if err := ctx.Err(); err != nil {
		return ListTableProjection{}, err
	}
	var object string
	var headers, fields []string
	switch operation {
	case "(resource) files > (method) list":
		object, fields = "file", []string{"id", "filename", "purpose", "bytes", "status"}
		headers = []string{"ID", "FILENAME", "PURPOSE", "SIZE", "STATUS"}
	case "(resource) batches > (method) list":
		object, fields = "batch", []string{"id", "status"}
		headers = []string{"ID", "STATUS"}
	case "(resource) admin.organization.projects > (method) list":
		object, fields = "organization.project", []string{"name", "status", "id"}
		headers = []string{"NAME", "STATUS", "ID"}
	default:
		return projectCompactListTable(ctx, operation, items)
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return ListTableProjection{}, err
		}
		if !item.IsObject() || item.Get("object").Str != object || item.Get("id").Type != gjson.String || item.Get("id").Str == "" {
			return ListTableProjection{}, nil
		}
		var err error
		var summary gjson.Result
		if object == "organization.project" {
			// An unconfigured decryption key has no list detail. Keep other values in labeled output.
			if item.Get("psp_decryption_key_id").Type != gjson.Null {
				return ListTableProjection{}, nil
			}
			summary, _, err = summarizeResourceFields(ctx, item, fields,
				[]string{"object", "created_at", "archived_at", "external_key_id", "residency", "psp_decryption_key_id"})
		} else {
			// Reuse the existing resource allowlists and duplicate-key checks.
			summary, _, err = SummarizeResource(ctx, item, Route{operation, OutputPageItem})
		}
		if err != nil {
			return ListTableProjection{}, err
		}
		errors := item.Get("errors")
		if !summary.Exists() || !listTableEmptyString(item.Get("status_details")) || resourceFieldPresent(errors) ||
			errors.Exists() && errors.Type != gjson.Null && !errors.IsObject() || !listTableEmptyString(item.Get("error_file_id")) {
			return ListTableProjection{}, nil
		}
		countsSupported, err := listTableCountsWithoutFailures(ctx, item.Get("request_counts"))
		if err != nil || !countsSupported {
			return ListTableProjection{}, err
		}
		row := make([]string, len(fields))
		for i, key := range fields {
			if err := ctx.Err(); err != nil {
				return ListTableProjection{}, err
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
					return ListTableProjection{}, nil
				}
				row[i] = listTableSize(bytes)
			} else {
				if value.Type != gjson.String || value.Str == "" {
					return ListTableProjection{}, nil
				}
				row[i] = value.Str
			}
		}
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return ListTableProjection{}, err
	}
	return ListTableProjection{Headers: headers, Rows: rows, ProtectedColumn: slices.Index(fields, "id")}, nil
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
