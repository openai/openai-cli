package apiquery

import (
	"net/url"
	"testing"
)

func TestEncode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		val      any
		settings QuerySettings
		enc      string
	}{
		"null": {
			val: nil,
			enc: "query=",
		},
		"string": {
			val: "hello world",
			enc: "query=hello world",
		},
		"int": {
			val: 42,
			enc: "query=42",
		},
		"float": {
			val: 3.14,
			enc: "query=3.14",
		},
		"bool": {
			val: true,
			enc: "query=true",
		},
		"empty_slice": {
			val:      []any{},
			settings: QuerySettings{ArrayFormat: ArrayQueryFormatComma},
			enc:      "query=",
		},
		"nil_slice": {
			val:      []any(nil),
			settings: QuerySettings{ArrayFormat: ArrayQueryFormatComma},
			enc:      "query=",
		},
		"slice_of_ints": {
			val:      []any{10, 20, 30},
			settings: QuerySettings{ArrayFormat: ArrayQueryFormatComma},
			enc:      "query=10,20,30",
		},
		"slice_of_ints_repeat": {
			val:      []any{10, 20, 30},
			settings: QuerySettings{ArrayFormat: ArrayQueryFormatRepeat},
			enc:      "query=10&query=20&query=30",
		},
		"slice_of_ints_indices": {
			val:      []any{10, 20, 30},
			settings: QuerySettings{ArrayFormat: ArrayQueryFormatIndices},
			enc:      "query[0]=10&query[1]=20&query[2]=30",
		},
		"slice_of_ints_brackets": {
			val:      []any{10, 20, 30},
			settings: QuerySettings{ArrayFormat: ArrayQueryFormatBrackets},
			enc:      "query[]=10&query[]=20&query[]=30",
		},
		"slice_of_strings": {
			val:      []any{"a", "b", "c"},
			settings: QuerySettings{},
			enc:      "query=a,b,c",
		},
		"empty_map": {
			val:      map[string]any{},
			settings: QuerySettings{NestedFormat: NestedQueryFormatBrackets},
			enc:      "",
		},
		"nil_map": {
			val:      map[string]any(nil),
			settings: QuerySettings{NestedFormat: NestedQueryFormatBrackets},
			enc:      "",
		},
		"map_string_to_int_brackets": {
			val:      map[string]any{"one": 1, "two": 2},
			settings: QuerySettings{NestedFormat: NestedQueryFormatBrackets},
			enc:      "query[one]=1&query[two]=2",
		},
		"map_string_to_int_dots": {
			val:      map[string]any{"one": 1, "two": 2},
			settings: QuerySettings{NestedFormat: NestedQueryFormatDots},
			enc:      "query.one=1&query.two=2",
		},
		"map_string_to_slice": {
			val:      map[string][]any{"nums": {10, 20, 30}},
			settings: QuerySettings{},
			enc:      "query[nums]=10,20,30",
		},
		"map_string_to_slice_repeat_dots": {
			val:      map[string][]any{"nums": {10, 20, 30}},
			settings: QuerySettings{ArrayFormat: ArrayQueryFormatRepeat, NestedFormat: NestedQueryFormatDots},
			enc:      "query.nums=10&query.nums=20&query.nums=30",
		},
		"map_with_empties": {
			val: map[string]any{
				"empty-array": []any{},
				"nil-array":   []any(nil),
				"null":        nil,
			},
			settings: QuerySettings{ArrayFormat: ArrayQueryFormatComma, NestedFormat: NestedQueryFormatDots},
			enc:      "query.empty-array=&query.nil-array=&query.null=",
		},
		"nested_map": {
			val:      map[string]map[string]any{"outer": {"inner": 42}},
			settings: QuerySettings{},
			enc:      "query[outer][inner]=42",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			query := map[string]any{"query": test.val}
			values, err := MarshalWithSettings(query, test.settings)
			if err != nil {
				t.Fatalf("failed to marshal url %s", err)
			}
			str, _ := url.QueryUnescape(values.Encode())
			if str != test.enc {
				t.Fatalf("expected %+#v to serialize to:\n\t%q\nbut got:\n\t%q", test.val, test.enc, str)
			}
		})
	}
}
