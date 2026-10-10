package transformers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// ErrInvalidKeyInventoryResponse identifies rejected inventory data without
// retaining response bytes, which can contain credentials.
var ErrInvalidKeyInventoryResponse = errors.New("invalid JSON inventory response")

// KeyInventoryResource recognizes only read-only inventory boundaries. Create
// responses can carry one-time secrets and must never use this projection.
func KeyInventoryResource(route Route) string {
	operation, ok := strings.CutPrefix(route.Operation, "(resource) ")
	resource, method, found := strings.Cut(operation, " > (method) ")
	if !ok || !found || !(method == "list" && route.OutputKind == OutputPageItem ||
		method == "retrieve" && route.OutputKind == OutputResponse) {
		return ""
	}
	switch resource {
	case "admin.organization.admin_api_keys", "admin.organization.projects.api_keys", "admin.organization.projects.service_accounts":
		return resource
	}
	return ""
}

// ProjectKeyInventory orders inventory metadata without dropping null, empty,
// duplicate, or unfamiliar fields. Machine output formats bypass this function.
// A value field is a one-time secret, not inventory metadata.
func ProjectKeyInventory(ctx context.Context, value gjson.Result, route Route) (gjson.Result, bool, error) {
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, false, err
	}
	if KeyInventoryResource(route) == "" {
		return value, false, ctx.Err()
	}
	valid := gjson.Valid(value.Raw)
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, false, err
	}
	if !valid {
		// GJSON can read partial objects. Reject them before a fallback can
		// print an incomplete record containing a one-time secret slot.
		return gjson.Result{}, false, ErrInvalidKeyInventoryResponse
	}
	if !value.IsObject() {
		return gjson.Result{}, false, fmt.Errorf("%w: expected object", ErrInvalidKeyInventoryResponse)
	}
	type field struct{ key, encodedKey, raw string }
	var fields []field
	value.ForEach(func(key, child gjson.Result) bool {
		fields = append(fields, field{key.Str, key.Raw, child.Raw})
		return ctx.Err() == nil
	})
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, false, err
	}
	var out strings.Builder
	out.WriteByte('{')
	written := 0
	redacted := false
	write := func(f field) {
		if written > 0 {
			out.WriteByte(',')
		}
		out.WriteString(f.encodedKey)
		out.WriteByte(':')
		if f.key == "value" {
			out.WriteString(`"[redacted]"`)
			redacted = true
		} else if f.key == "api_key" && gjson.Parse(f.raw).IsObject() {
			// A service-account create response nests its secret here. Keep
			// unexpected inventory metadata, but never print that secret slot.
			out.WriteByte('{')
			first := true
			gjson.Parse(f.raw).ForEach(func(key, child gjson.Result) bool {
				if !first {
					out.WriteByte(',')
				}
				first = false
				out.WriteString(key.Raw)
				out.WriteByte(':')
				if key.Str == "value" {
					out.WriteString(`"[redacted]"`)
					redacted = true
				} else {
					out.WriteString(child.Raw)
				}
				return ctx.Err() == nil
			})
			out.WriteByte('}')
		} else {
			out.WriteString(f.raw)
		}
		written++
	}
	order := []string{"id", "name", "owner", "owner_project_access", "role", "expires_at", "last_used_at", "created_at", "redacted_value"}
	for _, key := range order {
		for i, f := range fields {
			if err := ctx.Err(); err != nil {
				return gjson.Result{}, false, err
			}
			if f.key == key {
				write(f)
				fields[i].raw = ""
			}
		}
	}
	for _, f := range fields {
		if err := ctx.Err(); err != nil {
			return gjson.Result{}, false, err
		}
		if f.raw != "" {
			write(f)
		}
	}
	out.WriteByte('}')
	return gjson.Parse(out.String()), redacted, ctx.Err()
}
