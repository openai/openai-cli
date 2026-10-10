package transformers

import (
	"context"

	"github.com/tidwall/gjson"
)

func projectExternalStorage(ctx context.Context, value gjson.Result) (gjson.Result, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if value.Get("object").Str != "organization.external_storage" {
		return value, nil
	}
	status := value.Get("status")
	if status.Type != gjson.String {
		return value, nil
	}
	var note string
	switch status.Str {
	case "pending":
		note = "Validation is not complete. Retrieve this storage to inspect its status."
	case "validated":
		note = "Validation recorded a successful check. It does not establish continuous storage health."
	case "unhealthy":
		note = "Storage needs attention. Check returned details and cloud permissions before an administrator validates again."
	default:
		return value, nil
	}
	return relabelDataControl(ctx, value, "status", "validation_status", status.Raw, "validation_note", note)
}
