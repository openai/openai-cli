package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// validateHelperSchema compiles the unchanged artifact as JSON Schema Draft
// 2020-12. Compilation does not establish Structured Outputs API acceptance.
func validateHelperSchema(ctx context.Context, data []byte) error {
	_, err := compileHelperSchema(ctx, data)
	return err
}

func compileHelperSchema(ctx context.Context, data []byte) (*jsonschema.Schema, error) {
	// encoding/json replaces malformed UTF-8. Reject it before compilation so
	// the compiler never validates a silently repaired artifact.
	for offset, checkpoint := 0, 0; offset < len(data); {
		if offset >= checkpoint {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			checkpoint = offset + 32*1024
		}
		r, size := utf8.DecodeRune(data[offset:])
		if r == utf8.RuneError && size == 1 {
			return nil, errors.New("schema artifact contains invalid UTF-8")
		}
		offset += size
	}
	decoder := json.NewDecoder(helperSchemaReader{ctx: ctx, reader: bytes.NewReader(data)})
	decoder.UseNumber()
	doc, err := readHelperSchemaValue(ctx, decoder)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("schema artifact must contain exactly one JSON object")
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return nil, errors.New("schema artifact must be a JSON object")
	}
	if err := checkHelperSchemaKeywords(ctx, obj); err != nil {
		return nil, err
	}

	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	// The default loader reads files. Disable it explicitly. The compiler's
	// embedded metaschemas remain available without network or filesystem IO.
	compiler.UseLoader(nil)
	compiler.RegisterVocabulary(&jsonschema.Vocabulary{
		URL: "urn:openai-cli:helpers-schema:offline",
		Compile: func(c *jsonschema.CompilerContext, obj map[string]any) (jsonschema.SchemaExt, error) {
			if err := checkHelperSchemaKeywords(ctx, obj); err != nil {
				return nil, err
			}
			// Compile unused definitions too. Otherwise an unresolved reference
			// in an unused definition can survive root compilation.
			for _, keyword := range []string{"$defs", "definitions"} {
				definitions, _ := obj[keyword].(map[string]any)
				for name := range definitions {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					c.Enqueue([]string{keyword, name})
				}
			}
			return nil, nil
		},
	})
	compiler.AssertVocabs()
	const resource = "urn:openai-cli:helpers-schema:artifact"
	if err := compiler.AddResource(resource, doc); err != nil {
		return nil, errors.New("cannot prepare schema artifact for local compilation")
	}
	// Compile has no context parameter. Callbacks check cancellation, but its
	// resource collection and metaschema validation cannot be interrupted.
	schema, err := compiler.Compile(resource)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		// Compiler diagnostics can include artifact values and private URLs.
		return nil, errors.New("schema artifact failed offline JSON Schema Draft 2020-12 compilation")
	}
	return schema, nil
}

func checkHelperSchemaKeywords(ctx context.Context, obj map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if dialect, present := obj["$schema"]; present {
		if dialect != "https://json-schema.org/draft/2020-12/schema" && dialect != "https://json-schema.org/draft/2020-12/schema#" {
			return errors.New("schema artifact must use JSON Schema Draft 2020-12")
		}
	}
	for _, keyword := range []string{"$ref", "$dynamicRef"} {
		if value, present := obj[keyword]; present {
			ref, ok := value.(string)
			if !ok || !strings.HasPrefix(ref, "#") {
				return errors.New("schema artifact references must use local fragments")
			}
		}
	}
	return nil
}

// Read tokens once to preserve numbers and reject duplicate keys. The existing
// request parser also accepts YAML and cannot check cancellation while parsing.
func readHelperSchemaValue(ctx context.Context, decoder *json.Decoder) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	token, err := decoder.Token()
	if err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		return nil, errors.New("schema artifact contains invalid JSON")
	}
	switch token {
	case json.Delim('{'):
		obj := make(map[string]any)
		for decoder.More() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok {
				return nil, errors.New("schema artifact contains invalid JSON")
			}
			if _, exists := obj[key]; exists {
				return nil, errors.New("schema artifact contains duplicate JSON keys")
			}
			value, err := readHelperSchemaValue(ctx, decoder)
			if err != nil {
				return nil, err
			}
			obj[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, errors.New("schema artifact contains invalid JSON")
		}
		return obj, ctx.Err()
	case json.Delim('['):
		var values []any
		for decoder.More() {
			value, err := readHelperSchemaValue(ctx, decoder)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, errors.New("schema artifact contains invalid JSON")
		}
		return values, ctx.Err()
	default:
		return token, ctx.Err()
	}
}

type helperSchemaReader struct {
	ctx    context.Context
	reader *bytes.Reader
}

func (r helperSchemaReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	// Bound each parsing read for cancellation, without limiting artifact size.
	if len(p) > 32*1024 {
		p = p[:32*1024]
	}
	return r.reader.Read(p)
}
