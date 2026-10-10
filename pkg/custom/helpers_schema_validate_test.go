package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const helperInvoiceSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "invoice_id": {"type": "string"},
    "line_items": {"type": "array", "items": {"$ref": "#/$defs/line_item"}}
  },
  "required": ["invoice_id", "line_items"],
  "additionalProperties": false,
  "$defs": {
    "line_item": {
      "type": "object",
      "properties": {
        "description": {"type": "string"},
        "quantity": {"type": "integer", "minimum": 1},
        "unit_price": {"type": "number", "minimum": 0}
      },
      "required": ["description", "quantity", "unit_price"],
      "additionalProperties": false
    }
  }
}`

func TestHelperSchemaValidatorCompilesInvoiceArtifact(t *testing.T) {
	data := []byte(helperInvoiceSchema)
	original := bytes.Clone(data)
	schema, err := compileHelperSchema(context.Background(), data)
	require.NoError(t, err)
	require.Equal(t, original, data)
	require.Equal(t, 2020, schema.DraftVersion)

	for _, tc := range []struct {
		name     string
		invoice  string
		accepted bool
	}{
		{"valid invoice", `{"invoice_id":"INV-001","line_items":[{"description":"Setup","quantity":2,"unit_price":125.5}]}`, true},
		{"missing invoice ID", `{"line_items":[]}`, false},
		{"invalid line item", `{"invoice_id":"INV-001","line_items":[{"description":"Setup","quantity":0,"unit_price":125.5}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var instance any
			decoder := json.NewDecoder(strings.NewReader(tc.invoice))
			decoder.UseNumber()
			require.NoError(t, decoder.Decode(&instance))
			if tc.accepted {
				require.NoError(t, schema.Validate(instance))
			} else {
				require.Error(t, schema.Validate(instance))
			}
		})
	}
}

func TestHelperSchemaValidatorRejectsInvalidArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{"empty", ``},
		{"boolean root", `true`},
		{"null root", `null`},
		{"array root", `[]`},
		{"string root", `"schema"`},
		{"malformed", `{"type":`},
		{"invalid UTF-8", "{\"description\":\"\xff\"}"},
		{"trailing object", `{} {}`},
		{"trailing scalar", `{} true`},
		{"trailing comma", `{"type":"object",}`},
		{"duplicate keys", `{"type":"object","type":"string"}`},
		{"escaped duplicate keys", `{"type":"object","\u0074ype":"string"}`},
		{"nested duplicate keys", `{"properties":{"id":{"type":"number","type":"string"}}}`},
		{"invalid type", `{"type":"invoice"}`},
		{"invalid keyword shape", `{"required":"id"}`},
		{"invalid pattern", `{"type":"string","pattern":"["}`},
		{"old dialect", `{"$schema":"http://json-schema.org/draft-07/schema#"}`},
		{"custom dialect", `{"$schema":"https://example.invalid/schema"}`},
		{"nonstring dialect", `{"$schema":{}}`},
		{"remote reference", `{"$ref":"https://example.invalid/schema"}`},
		{"embedded metaschema reference", `{"$ref":"https://json-schema.org/draft/2020-12/schema"}`},
		{"file reference", `{"$ref":"file:///private/schema.json"}`},
		{"relative reference", `{"$ref":"schema.json"}`},
		{"remote dynamic reference", `{"$dynamicRef":"https://example.invalid/schema#node"}`},
		{"unresolved reference", `{"$ref":"#/$defs/missing"}`},
		{"unused unresolved reference", `{"$defs":{"unused":{"$ref":"#/$defs/missing"}}}`},
		{"unused remote reference", `{"$defs":{"unused":{"$ref":"https://example.invalid/schema"}}}`},
		{"nested old dialect", `{"$defs":{"unused":{"$id":"urn:example:unused","$schema":"http://json-schema.org/draft-07/schema#"}}}`},
		{"referenced extra old dialect", `{"$ref":"#/extra","extra":{"$id":"urn:example:extra","$schema":"http://json-schema.org/draft-07/schema#"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, validateHelperSchema(context.Background(), []byte(tc.data)))
		})
	}
}

func TestHelperSchemaValidatorPreservesGenericSchemaSupport(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{"empty schema", `{}`},
		{"explicit dialect fragment", `{"$schema":"https://json-schema.org/draft/2020-12/schema#"}`},
		{"root anyOf", `{"anyOf":[{"type":"string"},{"type":"number"}]}`},
		{"recursive local reference", `{"type":"object","properties":{"child":{"$ref":"#"}}}`},
		{"local anchor", `{"$ref":"#invoice","$defs":{"invoice":{"$anchor":"invoice","type":"object"}}}`},
		{"local dynamic reference", `{"$dynamicAnchor":"node","type":"object","properties":{"child":{"$dynamicRef":"#node"}}}`},
		{"large precise number", `{"type":"integer","minimum":9007199254740993}`},
		{"valid Unicode", `{"description":"Invoice € 日本語"}`},
		{"annotation reference data", `{"examples":[{"$ref":"file:///example.json","$schema":"example"}],"default":{"$ref":"remote"}}`},
		{"const reference data", `{"const":{"$ref":"remote","$schema":"example"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, validateHelperSchema(context.Background(), []byte(tc.data)))
		})
	}
}

func TestHelperSchemaValidatorDoesNotExposeArtifactValues(t *testing.T) {
	const secret = "synthetic-private-value"
	for _, data := range []string{
		`{"type":"` + secret + `"}`,
		`{"$ref":"https://example.invalid/?key=` + secret + `"}`,
		`{"` + secret + `":1,"` + secret + `":2}`,
	} {
		err := validateHelperSchema(context.Background(), []byte(data))
		require.Error(t, err)
		require.NotContains(t, err.Error(), secret)
	}
}

func TestHelperSchemaValidatorCancellation(t *testing.T) {
	t.Run("before parsing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, validateHelperSchema(ctx, []byte(helperInvoiceSchema)), context.Canceled)
	})
	t.Run("during large token parsing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		controlled := &helperSchemaCancelContext{Context: ctx, cancel: cancel, cancelAfter: 48}
		data := []byte(`{"description":"` + strings.Repeat("x", 1024*1024) + `"}`)
		require.ErrorIs(t, validateHelperSchema(controlled, data), context.Canceled)
		require.GreaterOrEqual(t, controlled.checks, controlled.cancelAfter)
	})
}

// Cancel at a deterministic parsing checkpoint without sleeps or worker leaks.
type helperSchemaCancelContext struct {
	context.Context
	cancel      context.CancelFunc
	checks      int
	cancelAfter int
}

func (ctx *helperSchemaCancelContext) Err() error {
	ctx.checks++
	if ctx.checks == ctx.cancelAfter {
		ctx.cancel()
	}
	return ctx.Context.Err()
}
