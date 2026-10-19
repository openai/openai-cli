package request

import (
	"fmt"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// FilePathValue is a string wrapper that marks a value as a file path whose contents should be read
// and embedded in the request. A FilePathValue is always interpreted as a file
// path without needing the "@" prefix.
type FilePathValue string

// ApplyDataAliases rewrites keys in a body map based on flag `DataAliases` metadata. For top-level flags,
// `{alias: value}` becomes `{canonical: value}`. For inner flags (those registered under an outer flag
// via WithInnerFlags), the alias translation is also applied to the nested map under the outer flag's
// body path, so values like `--parent '{"alias": val}'` resolve to the canonical inner field name.
func ApplyDataAliases(cmd *cli.Command, bodyMap map[string]any) {
	for _, flag := range cmd.Flags {
		// Inner flags: rewrite aliases inside the nested map under the outer flag's body path.
		if inner, ok := flag.(requestflag.HasOuterFlag); ok {
			outer, outerOk := inner.GetOuterFlag().(requestflag.InRequest)
			if !outerOk {
				continue
			}
			if nested, ok := bodyMap[outer.GetBodyPath()].(map[string]any); ok && inner.GetInnerField() != "" {
				rewriteAliases(nested, inner.GetInnerField(), inner.GetDataAliases())
			}
			continue
		}
		// Top-level flags: rewrite aliases in the body map.
		if inReq, ok := flag.(requestflag.InRequest); ok && inReq.GetBodyPath() != "" {
			rewriteAliases(bodyMap, inReq.GetBodyPath(), inReq.GetDataAliases())
		}
	}
}

// rewriteAliases replaces each alias key in m with the canonical key, preserving the value. The
// "canonical" key is the name the API itself expects (the OpenAPI property/field name) — e.g. for
// a top-level flag, the parameter's BodyPath; for an inner flag, the inner field name. Aliases are
// the user-facing alternate names declared via x-stainless-cli-data-alias.
func rewriteAliases(m map[string]any, canonical string, aliases []string) {
	for _, alias := range aliases {
		if alias == "" || alias == canonical {
			continue
		}
		if val, exists := m[alias]; exists {
			m[canonical] = val
			delete(m, alias)
		}
	}
}

// WrapFileInputValues replaces string values for FileInput flags (type: string, format: binary) with
// FilePathValue sentinel values. embedFilesValue recognizes FilePathValue and reads the file contents
// directly, so the user doesn't need to type the "@" prefix. Untrusted piped
// file paths are rejected before any file can be opened.
func WrapFileInputValues(cmd *cli.Command, contents *requestflag.RequestContents, stdinSecurity *StdinSecurity) error {
	bodyMap, _ := contents.Body.(map[string]any)

	for _, flag := range cmd.Flags {
		inReq, ok := flag.(requestflag.InRequest)
		if !ok || !inReq.IsFileInput() || inReq.IsBodyRoot() {
			continue
		}

		var values map[string]any
		var path string
		if path = inReq.GetBodyPath(); path != "" {
			values = bodyMap
		} else if path = inReq.GetQueryPath(); path != "" {
			values = contents.Queries
		} else if path = inReq.GetHeaderPath(); path != "" {
			values = contents.Headers
		}
		value, exists := values[path]
		if !exists {
			continue
		}
		if stdinSecurity.fileInputFromStdin(flag) {
			return fmt.Errorf("file input %q from piped YAML/JSON is disabled when %s is enabled; provide --%s explicitly", path, UntrustedStdinEnv, flag.Names()[0])
		}
		if wrapped, changed := wrapFileInputValue(value); changed {
			values[path] = wrapped
		}
	}
	return nil
}

func wrapFileInputValue(value any) (any, bool) {
	switch v := value.(type) {
	case string:
		if v == "" {
			return value, false
		}
		return FilePathValue(v), true

	case []string:
		result := make([]any, len(v))
		for i, s := range v {
			result[i] = FilePathValue(s)
		}
		return result, true

	case []any:
		result := make([]any, len(v))
		for i, elem := range v {
			if s, ok := elem.(string); ok {
				result[i] = FilePathValue(s)
			} else {
				result[i] = elem
			}
		}
		return result, true

	default:
		return value, false
	}
}

// IsStdinPath recognizes the supported aliases for standard input.
func IsStdinPath(s string) bool {
	switch s {
	case "-", "/dev/fd/0", "/dev/stdin":
		return true
	}
	return false
}
