package apiquery

import (
	"strings"
	"testing"
)

func TestCommaArrayRejectsComplexElements(t *testing.T) {
	t.Parallel()

	values, err := MarshalWithSettings(
		map[string]any{
			"filter": []map[string]string{{"name": "alice"}},
		},
		QuerySettings{ArrayFormat: ArrayQueryFormatComma},
	)
	if err == nil {
		t.Fatal("expected comma-form array of maps to return an error")
	}
	if !strings.Contains(err.Error(), "comma format does not support complex array elements") {
		t.Fatalf("unexpected error: %v", err)
	}
	if values != nil {
		t.Fatalf("expected no query values on error, got %v", values)
	}
}

func TestCommaArrayStillEncodesPrimitiveElements(t *testing.T) {
	t.Parallel()

	values, err := MarshalWithSettings(
		map[string]any{"name": []string{"alice", "bob"}},
		QuerySettings{ArrayFormat: ArrayQueryFormatComma},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := values.Get("name"); got != "alice,bob" {
		t.Fatalf("comma-form primitive array = %q, want %q", got, "alice,bob")
	}
}
