package requestflag

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestInputPolicyRepeatedNullableText(t *testing.T) {
	for _, test := range []struct {
		name       string
		structured bool
		values     []string
		want       *string
	}{
		{"literal replacement", false, []string{"first", "null"}, Ptr("null")},
		{"empty replacement", false, []string{"first", ""}, Ptr("")},
		{"structured null", true, []string{`"first"`, "null"}, nil},
		{"structured string", true, []string{"null", `"second"`}, Ptr("second")},
	} {
		t.Run(test.name, func(t *testing.T) {
			flag := &Flag[*string]{Name: "instructions", BodyPath: "instructions", Default: Ptr("default"), InputPolicy: InputPolicyTextStructured}
			for _, value := range test.values {
				if test.structured {
					require.NoError(t, flag.setStructuredRaw(value))
				} else {
					require.NoError(t, flag.Set("instructions", value))
				}
			}
			require.True(t, flag.IsSet())
			require.Equal(t, test.want, unwrappedInput(flag.Get()))
		})
	}
}

func TestInputPolicyRepeatedTextOperandsPreserveDefaultsAndTypes(t *testing.T) {
	flag := &Flag[[]string]{Name: "tag", BodyPath: "tags", Default: []string{"default"}, InputPolicy: InputPolicyText}
	require.NoError(t, flag.Set("tag", "null"))
	require.NoError(t, flag.Set("tag", "42"))
	require.Equal(t, []string{"null", "42"}, flag.Get())

	union := &Flag[[]any]{Name: "item", BodyPath: "items", InputPolicy: InputPolicyTextStructured}
	require.NoError(t, union.setStructuredRaw(`"first"`))
	require.NoError(t, union.setStructuredRaw("null"))
	require.NoError(t, union.setStructuredRaw(`{"key":"value"}`))
	require.Equal(t, []any{"first", nil, map[string]any{"key": "value"}}, unwrappedInput(union.Get()))
}

func TestInputPolicyNullableReplacementDiscardsDeferredSource(t *testing.T) {
	for _, value := range []string{"null", `"replacement"`} {
		t.Run(value, func(t *testing.T) {
			flag := &Flag[*string]{Name: "instructions", BodyPath: "instructions", InputPolicy: InputPolicyTextStructured}
			require.NoError(t, flag.setStructuredRaw("@-"))
			require.ErrorContains(t, flag.setStructuredRaw("{}"), "incompatible value type")
			require.NoError(t, flag.setStructuredRaw(value))
			input, ok := flag.Get().(InputValue)
			require.True(t, ok)
			resolved, err := input.Resolve(func(string) (string, error) {
				t.Fatal("a replaced scalar must not read its previous source")
				return "", nil
			}, nil)
			require.NoError(t, err)
			if value == "null" {
				require.Equal(t, (*string)(nil), resolved)
			} else {
				require.Equal(t, Ptr("replacement"), resolved)
			}
		})
	}
}

func TestInputPolicyArrayRetainsDeferredPredecessor(t *testing.T) {
	for _, flag := range []structuredTarget{
		&Flag[any]{Name: "input", BodyPath: "input", InputPolicy: InputPolicyTextStructured},
		&Flag[[]any]{Name: "input", BodyPath: "input", InputPolicy: InputPolicyTextStructured},
	} {
		require.NoError(t, flag.setStructuredRaw("@-"))
		require.NoError(t, flag.setStructuredRaw(`"next"`))
		input := flag.Get().(InputValue)
		var reads []string
		_, err := input.Resolve(func(raw string) (string, error) {
			reads = append(reads, raw)
			return `"first"`, nil
		}, nil)
		require.NoError(t, err)
		require.Equal(t, []string{"@-"}, reads)
	}
}

func TestInputPolicyRepeatedStructuredArrays(t *testing.T) {
	for _, test := range []struct {
		name   string
		values []string
		want   any
	}{
		{"append object", []string{`["first"]`, `{"key":"value"}`}, []any{"first", map[string]any{"key": "value"}}},
		{"append null", []string{`["first"]`, "null"}, []any{"first", nil}},
		{"preserve nested array", []string{`["first"]`, `["second"]`}, []any{"first", []any{"second"}}},
		{"replace null", []string{"null", `["second"]`}, []any{"second"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			flag := &Flag[any]{Name: "input", BodyPath: "input", InputPolicy: InputPolicyTextStructured}
			for _, value := range test.values {
				require.NoError(t, flag.setStructuredRaw(value))
			}
			require.Equal(t, test.want, unwrappedInput(flag.Get()))
		})
	}
}

func TestDeferredInputValidationOrder(t *testing.T) {
	var validations []int
	flag := &Flag[[]any]{Name: "item", Validator: func(items []any) error {
		validations = append(validations, len(items))
		if len(items) == 3 {
			return errors.New("rejected aggregate")
		}
		return nil
	}}
	for _, operand := range []string{`"first"`, "@second", `"third"`, "@unused"} {
		require.NoError(t, flag.setStructuredRaw(operand))
	}
	require.Equal(t, []int{1}, validations)
	var reads []string
	_, err := flag.Get().(InputValue).Resolve(func(source string) (string, error) {
		reads = append(reads, source)
		return `"from source"`, nil
	}, nil)
	require.ErrorContains(t, err, "value failed validation")
	require.Equal(t, []int{1, 2, 3}, validations)
	require.Equal(t, []string{"@second"}, reads)
}

func TestDeferredInputRepeatedResolutionPreservesEarlierResult(t *testing.T) {
	flag := &Flag[[]string]{Name: "item"}
	for _, operand := range []string{"one", "two", "three", "@last"} {
		require.NoError(t, flag.setStructuredRaw(operand))
	}
	input := flag.Get().(InputValue)
	first, err := input.Resolve(func(string) (string, error) { return "four", nil }, nil)
	require.NoError(t, err)
	second, err := input.Resolve(func(string) (string, error) { return "other", nil }, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two", "three", "four"}, first)
	require.Equal(t, []string{"one", "two", "three", "other"}, second)
}

func TestInputPolicyRepeatedNestedOperands(t *testing.T) {
	for _, structured := range []bool{false, true} {
		name := "literal"
		option, first, second := "--entry.content", "null", "key: value"
		if structured {
			name = "structured"
			option, first, second = "--entry.content-structured", "null", `{"key":"value"}`
		}
		t.Run(name, func(t *testing.T) {
			outer := &Flag[[]map[string]any]{Name: "entry", BodyPath: "entries", InputPolicy: InputPolicyTyped}
			inner := &InnerFlag[any]{Name: "entry.content", InnerField: "content", OuterFlag: outer, OuterIsArrayOfObjects: true, InputPolicy: InputPolicyTextStructured}
			command := &cli.Command{Name: "test", Flags: []cli.Flag{outer, inner}, Writer: io.Discard, ErrWriter: io.Discard}
			AddStructuredFlags(command)
			require.NoError(t, ConfigureInputMode(command, "literal"))
			require.NoError(t, command.Run(context.Background(), []string{"test", option, first, option, second}))
			entries := outer.Get().([]map[string]any)
			require.Len(t, entries, 2)
			if structured {
				require.Nil(t, unwrappedInput(entries[0]["content"]))
				require.Equal(t, map[string]any{"key": "value"}, unwrappedInput(entries[1]["content"]))
			} else {
				require.Equal(t, "null", entries[0]["content"])
				require.Equal(t, "key: value", entries[1]["content"])
			}
		})
	}
}

func TestInputPolicyNestedDeferredSourcesRejectMixedModes(t *testing.T) {
	for _, structuredFirst := range []bool{false, true} {
		outer := &Flag[[]map[string]any]{Name: "entry", BodyPath: "entries", InputPolicy: InputPolicyTyped}
		inner := &InnerFlag[any]{Name: "entry.content", InnerField: "content", OuterFlag: outer, OuterIsArrayOfObjects: true, InputPolicy: InputPolicyTextStructured}
		if structuredFirst {
			require.NoError(t, inner.setStructuredRaw("@synthetic-missing.json"))
			err := inner.Set("entry.content", "@-")
			require.ErrorContains(t, err, "cannot combine --entry.content with --entry.content-structured")
		} else {
			require.NoError(t, inner.Set("entry.content", "@-"))
			err := inner.setStructuredRaw("@synthetic-missing.json")
			require.ErrorContains(t, err, "cannot combine --entry.content with --entry.content-structured")
		}
		require.Len(t, outer.Get(), 1, "a rejected operand must not append an entry")
	}
}
