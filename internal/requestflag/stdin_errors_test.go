package requestflag

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestStdinValueErrorPreservesDeclaredNameAndCause(t *testing.T) {
	for _, location := range []string{"query", "header", "path"} {
		t.Run(location, func(t *testing.T) {
			flag := &Flag[int64]{Name: "page-size", Aliases: []string{"n"}, DataAliases: []string{"count"}}
			switch location {
			case "query":
				flag.QueryPath = "page_size"
			case "header":
				flag.HeaderPath = "X-Page-Size"
			case "path":
				flag.PathParam = "page_size"
			}
			var reported []cli.Flag
			err := ApplyStdinDataToFlagsWithProvenance(&cli.Command{Flags: []cli.Flag{flag}}, map[string]any{"count": "synthetic-private-invalid"}, func(f cli.Flag) { reported = append(reported, f) })
			var typed *StdinValueError
			require.ErrorAs(t, err, &typed)
			require.Equal(t, "page-size", typed.FlagName())
			var numeric *strconv.NumError
			require.ErrorAs(t, err, &numeric)
			require.ErrorIs(t, err, strconv.ErrSyntax)
			require.Equal(t, `cannot set flag "page-size" from piped data: `+numeric.Error(), err.Error())
			require.Equal(t, typed.Unwrap().Error(), err.Error())
			require.Empty(t, reported)
			require.False(t, flag.IsSet())
		})
	}
}

func TestStdinValueErrorCoversFormattingAndInnerSet(t *testing.T) {
	for _, inner := range []bool{false, true} {
		t.Run(fmt.Sprintf("inner=%t", inner), func(t *testing.T) {
			flag := &Flag[any]{Name: "value", HeaderPath: "value"}
			command := &cli.Command{Flags: []cli.Flag{flag}}
			data := map[string]any{"value": make(chan int)}
			name := "value"
			if inner {
				outer := &Flag[map[string]any]{Name: "settings", BodyPath: "settings"}
				field := &InnerFlag[bool]{Name: "settings.enabled", InnerField: "enabled", DataAliases: []string{"active"}, OuterFlag: outer}
				command.Flags = []cli.Flag{outer, field}
				data = map[string]any{"settings": map[string]any{"active": make(chan int)}}
				name = "settings.enabled"
			}
			observed := false
			err := ApplyStdinDataToFlagsWithProvenance(command, data, func(cli.Flag) { observed = true })
			var typed *StdinValueError
			var unsupported *json.UnsupportedTypeError
			require.ErrorAs(t, err, &typed)
			require.ErrorAs(t, err, &unsupported)
			require.Equal(t, name, typed.FlagName())
			require.Equal(t, fmt.Sprintf("cannot format piped value for flag %q: cannot format value chan int for flag.Set: %s", name, unsupported.Error()), err.Error())
			require.Equal(t, typed.Unwrap().Error(), err.Error())
			require.False(t, observed)
		})
	}
	outer := &Flag[map[string]any]{Name: "settings", BodyPath: "settings"}
	inner := &InnerFlag[bool]{Name: "settings.enabled", InnerField: "enabled", DataAliases: []string{"active"}, OuterFlag: outer}
	observed := false
	err := ApplyStdinDataToFlagsWithProvenance(&cli.Command{Flags: []cli.Flag{outer, inner}}, map[string]any{"settings": map[string]any{"active": "synthetic-private-invalid"}}, func(cli.Flag) { observed = true })
	var typed *StdinValueError
	var numeric *strconv.NumError
	require.ErrorAs(t, err, &typed)
	require.ErrorAs(t, err, &numeric)
	require.Equal(t, "settings.enabled", typed.FlagName())
	require.Equal(t, `cannot set flag "settings.enabled" from piped data: `+numeric.Error(), err.Error())
	require.False(t, observed)
	require.False(t, outer.IsSet())
}

func TestStdinValueErrorKeepsCollectionAtomicityAndCallbacks(t *testing.T) {
	sentinel := errors.New("synthetic-private-validator")
	flag := &Flag[[]int64]{Name: "include", QueryPath: "include", Default: []int64{7}, Validator: func(v []int64) error {
		if len(v) > 1 {
			return sentinel
		}
		return nil
	}}
	command := &cli.Command{Flags: []cli.Flag{flag}}
	var reported []cli.Flag
	onSet := func(f cli.Flag) { reported = append(reported, f) }
	for _, value := range []any{[]any{1, "synthetic-private-invalid"}, []any{1, 2}} {
		err := ApplyStdinDataToFlagsWithProvenance(command, map[string]any{"include": value}, onSet)
		var typed *StdinValueError
		require.ErrorAs(t, err, &typed)
		require.Equal(t, "include", typed.FlagName())
		require.Equal(t, typed.Unwrap().Error(), err.Error())
		require.Equal(t, []int64{7}, flag.Get())
		require.Zero(t, flag.Count())
		require.False(t, flag.IsSet())
		require.Empty(t, reported)
		if values := value.([]any); values[1] == 2 {
			require.ErrorIs(t, err, sentinel)
		}
	}
	require.NoError(t, ApplyStdinDataToFlagsWithProvenance(command, map[string]any{"include": []any{3}}, onSet))
	require.Equal(t, []int64{3}, flag.Get())
	require.Equal(t, 1, flag.Count())
	require.Equal(t, []cli.Flag{flag}, reported)
}

func TestStdinValueErrorPreservesExplicitAndRawBodyBoundaries(t *testing.T) {
	query := &Flag[int64]{Name: "limit", Aliases: []string{"n"}, QueryPath: "limit", DataAliases: []string{"count"}}
	header := &Flag[bool]{Name: "enabled", HeaderPath: "X-Enabled"}
	path := &Flag[*int64]{Name: "identifier", PathParam: "id"}
	body := &Flag[int64]{Name: "raw", BodyPath: "raw"}
	outer := &Flag[map[string]any]{Name: "settings", BodyPath: "settings"}
	inner := &InnerFlag[bool]{Name: "settings.enabled", InnerField: "enabled", OuterFlag: outer}
	require.NoError(t, query.Set("n", "0"))
	require.NoError(t, inner.Set("settings.enabled", "false"))
	command := &cli.Command{Flags: []cli.Flag{query, header, path, body, outer, inner}}
	data := map[string]any{"count": "synthetic-private-invalid", "X-Enabled": false, "id": nil, "raw": "synthetic-private-raw", "settings": map[string]any{"enabled": "synthetic-private-invalid"}}
	var reported []cli.Flag
	require.NoError(t, ApplyStdinDataToFlagsWithProvenance(command, data, func(f cli.Flag) { reported = append(reported, f) }))
	require.Equal(t, int64(0), query.Get())
	require.Equal(t, false, header.Get())
	require.Equal(t, (*int64)(nil), path.Get())
	require.True(t, path.IsSet())
	require.Equal(t, map[string]any{"enabled": false}, outer.Get())
	require.False(t, body.IsSet())
	require.Equal(t, []cli.Flag{header, path}, reported)
	require.Equal(t, "synthetic-private-raw", data["raw"])
}
