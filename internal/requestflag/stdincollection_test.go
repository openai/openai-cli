package requestflag

import (
	"fmt"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestStdinCollections(t *testing.T) {
	for _, input := range []string{`{"include":["first","second"]}`, "include:\n  - first\n  - second\n"} {
		t.Run(input, func(t *testing.T) {
			var data map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(input), &data))
			flag := &Flag[[]string]{Name: "include", QueryPath: "include"}
			require.NoError(t, ApplyStdinDataToFlags(&cli.Command{Flags: []cli.Flag{flag}}, data))
			require.Equal(t, []string{"first", "second"}, flag.Get())
		})
	}
}

func TestStdinCollectionContracts(t *testing.T) {
	tests := []struct {
		name      string
		flag      cli.Flag
		input     any
		want      any
		wantError bool
	}{
		{name: "empty", flag: &Flag[[]string]{Name: "value", QueryPath: "value", Default: []string{"default"}}, input: []any{}, want: []string{}},
		{name: "typed nil", flag: &Flag[[]string]{Name: "value", QueryPath: "value"}, input: []string(nil), want: []string(nil)},
		{name: "null scalar compatibility", flag: &Flag[[]string]{Name: "value", QueryPath: "value"}, input: nil, want: []string{"null"}},
		{name: "string scalar compatibility", flag: &Flag[[]string]{Name: "value", QueryPath: "value"}, input: "first", want: []string{"first"}},
		{name: "array looking scalar", flag: &Flag[[]string]{Name: "value", QueryPath: "value"}, input: `["first"]`, want: []string{`["first"]`}},
		{name: "numeric", flag: &Flag[[]int64]{Name: "value", QueryPath: "value"}, input: []any{1, 2}, want: []int64{1, 2}},
		{name: "numeric strings use CLI parsing", flag: &Flag[[]int64]{Name: "value", QueryPath: "value"}, input: []any{"0x10", "2"}, want: []int64{16, 2}},
		{name: "float", flag: &Flag[[]float64]{Name: "value", QueryPath: "value"}, input: []any{1.5, 2.5}, want: []float64{1.5, 2.5}},
		{name: "boolean", flag: &Flag[[]bool]{Name: "value", QueryPath: "value"}, input: []any{true, false}, want: []bool{true, false}},
		{name: "invalid numeric", flag: &Flag[[]int64]{Name: "value", QueryPath: "value", Default: []int64{7}}, input: []any{1, "bad"}, want: []int64{7}, wantError: true},
		{name: "header compatibility", flag: &Flag[[]string]{Name: "value", HeaderPath: "value"}, input: []any{"a", "b"}, want: []string{`["a","b"]`}},
		{name: "path compatibility", flag: &Flag[[]string]{Name: "value", PathParam: "value"}, input: []any{"a", "b"}, want: []string{`["a","b"]`}},
		{name: "body excluded", flag: &Flag[[]string]{Name: "value", BodyPath: "value"}, input: []any{"a", "b"}, want: []string(nil)},
		{name: "body root excluded", flag: &Flag[[]string]{Name: "value", BodyRoot: true}, input: []any{"a", "b"}, want: []string(nil)},
		{name: "any collection unchanged", flag: &Flag[any]{Name: "value", QueryPath: "value"}, input: []any{"a", "b"}, want: []any{"a", "b"}},
		{name: "nullable scalar unchanged", flag: &Flag[*string]{Name: "value", QueryPath: "value"}, input: nil, want: (*string)(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var recorded []cli.Flag
			err := ApplyStdinDataToFlagsWithProvenance(&cli.Command{Flags: []cli.Flag{tt.flag}}, map[string]any{"value": tt.input}, func(f cli.Flag) { recorded = append(recorded, f) })
			if tt.wantError {
				require.ErrorContains(t, err, `cannot set flag "value" from piped data`)
				require.False(t, tt.flag.IsSet())
				require.Empty(t, recorded)
			} else {
				require.NoError(t, err)
				inReq := tt.flag.(InRequest)
				if inReq.GetBodyPath() == "" && !inReq.IsBodyRoot() {
					require.True(t, tt.flag.IsSet())
					require.Equal(t, []cli.Flag{tt.flag}, recorded)
				} else {
					require.Empty(t, recorded)
					require.False(t, tt.flag.IsSet())
				}
			}
			require.Equal(t, tt.want, tt.flag.Get())
		})
	}
}

func TestStdinCollectionAliasesAndExplicitPrecedence(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		flag := &Flag[[]string]{Name: "include", Aliases: []string{"i"}, DataAliases: []string{"include_alias"}, QueryPath: "include"}
		if explicit {
			require.NoError(t, flag.Set("i", "explicit"))
		}
		var recorded []cli.Flag
		command := &cli.Command{Flags: []cli.Flag{flag}}
		require.NoError(t, ApplyStdinDataToFlagsWithProvenance(command, map[string]any{"include_alias": []any{"piped", "second"}}, func(f cli.Flag) { recorded = append(recorded, f) }))
		if explicit {
			require.Equal(t, []string{"explicit"}, flag.Get())
			require.Empty(t, recorded)
		} else {
			require.Equal(t, []string{"piped", "second"}, flag.Get())
			require.Equal(t, []cli.Flag{flag}, recorded)
		}
		require.Equal(t, 1, flag.Count())
	}
	flag := &Flag[[]string]{Name: "include", DataAliases: []string{"alias"}, QueryPath: "include"}
	require.NoError(t, ApplyStdinDataToFlags(&cli.Command{Flags: []cli.Flag{flag}}, map[string]any{"include": []any{"canonical"}, "alias": []any{"alias"}}))
	require.Equal(t, []string{"canonical"}, flag.Get())
}

func TestStdinCollectionUnsetAndValidation(t *testing.T) {
	flag := &Flag[[]string]{Name: "include", QueryPath: "include", Default: []string{"default"}}
	command := &cli.Command{Flags: []cli.Flag{flag}}
	require.NoError(t, ApplyStdinDataToFlags(command, map[string]any{}))
	require.False(t, flag.IsSet())
	require.Empty(t, ExtractRequestContents(command).Queries)
	flag.Validator = func(values []string) error {
		if len(values) > 1 {
			return fmt.Errorf("only one element")
		}
		return nil
	}
	require.ErrorContains(t, ApplyStdinDataToFlags(command, map[string]any{"include": []any{"a", "b"}}), "only one element")
	require.False(t, flag.IsSet())
	require.Equal(t, []string{"default"}, flag.Get())
	require.Zero(t, flag.Count())
	require.NoError(t, ApplyStdinDataToFlags(command, map[string]any{"include": []any{"a"}}))
	require.Equal(t, []string{"a"}, flag.Get())
	flag.Validator = nil
	require.NoError(t, flag.Set("include", "b"))
	require.Equal(t, []string{"a", "b"}, flag.Get())
}
