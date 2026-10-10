package transformers

import (
	"context"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// Select only successful data-control boundaries. Generated handlers retain
// request construction; explicit formats and extraction bypass these projections.
func selectDataControlsTransformer(route Route) Transformer {
	switch route {
	case Route{"(resource) admin.organization.data_retention > (method) retrieve", OutputResponse},
		Route{"(resource) admin.organization.data_retention > (method) update", OutputResponse}:
		return projectDataRetention("organization.data_retention")
	case Route{"(resource) admin.organization.projects.data_retention > (method) retrieve", OutputResponse},
		Route{"(resource) admin.organization.projects.data_retention > (method) update", OutputResponse}:
		return projectDataRetention("project.data_retention")
	case Route{"(resource) admin.organization.external_storage > (method) create", OutputResponse},
		Route{"(resource) admin.organization.external_storage > (method) retrieve", OutputResponse},
		Route{"(resource) admin.organization.external_storage > (method) validate", OutputResponse},
		Route{"(resource) admin.organization.external_storage > (method) list", OutputPageItem}:
		return projectExternalStorage
	}
	return nil
}

func projectDataRetention(object string) Transformer {
	return func(ctx context.Context, value gjson.Result) (gjson.Result, error) {
		if err := ctx.Err(); err != nil {
			return gjson.Result{}, err
		}
		if value.Get("object").Str != object {
			return value, nil
		}
		setting := value.Get("type")
		if setting.Type != gjson.String {
			return value, nil
		}
		configured := setting.Str
		switch configured {
		case "organization_default":
			if object != "project.data_retention" {
				return value, nil
			}
			configured = "inherit organization default (organization_default)"
		case "none":
			if object != "project.data_retention" {
				return value, nil
			}
		case "zero_data_retention", "modified_abuse_monitoring", "enhanced_zero_data_retention", "enhanced_modified_abuse_monitoring":
		default:
			return value, nil
		}
		// Effective-policy fields are not part of this response contract.
		// Future retention fields require a new interpretation, not a guessed policy.
		known := true
		value.ForEach(func(key, _ gjson.Result) bool {
			known = key.Str == "object" || key.Str == "type"
			return known && ctx.Err() == nil
		})
		if !known {
			return value, ctx.Err()
		}
		return relabelDataControl(ctx, value, "type", "configured_retention", strconv.Quote(configured),
			"effective_retention", "not resolved by this response")
	}
}

// Preserve field order and every residual JSON value. Reserved names and
// duplicate keys fall back to the original response instead of hiding data.
func relabelDataControl(ctx context.Context, value gjson.Result, from, to, replacement, noteKey, note string) (gjson.Result, error) {
	valid := value.IsObject() && gjson.Valid(value.Raw)
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if !valid {
		return value, nil
	}
	seen := make(map[string]bool)
	var out strings.Builder
	out.WriteByte('{')
	value.ForEach(func(key, child gjson.Result) bool {
		if ctx.Err() != nil {
			return false
		}
		if seen[key.Str] || key.Str == to || key.Str == noteKey {
			valid = false
			return false
		}
		if len(seen) > 0 {
			out.WriteByte(',')
		}
		seen[key.Str] = true
		if key.Str == from {
			out.WriteString(strconv.Quote(to))
			out.WriteByte(':')
			out.WriteString(replacement)
		} else {
			out.WriteString(key.Raw)
			out.WriteByte(':')
			out.WriteString(child.Raw)
		}
		return true
	})
	if err := ctx.Err(); err != nil {
		return gjson.Result{}, err
	}
	if !valid || !seen[from] {
		return value, nil
	}
	out.WriteByte(',')
	out.WriteString(strconv.Quote(noteKey))
	out.WriteByte(':')
	out.WriteString(strconv.Quote(note))
	out.WriteByte('}')
	return gjson.Parse(out.String()), ctx.Err()
}
