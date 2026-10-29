package requestflag

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestUntypedStdinKeepsTypesAndProvenance(t *testing.T) {
	values := []any{"a: b", "null", `[{"x":1}]`, "@local-file", nil, true, float64(42), []any{"one", "two"}, map[string]any{"key": "value"}}
	for _, value := range values {
		t.Run(fmtValue(value), func(t *testing.T) {
			flag := &Flag[any]{Name: "query", QueryPath: "query"}
			var recorded []cli.Flag
			require.NoError(t, ApplyStdinDataToFlagsWithProvenance(&cli.Command{Flags: []cli.Flag{flag}}, map[string]any{"query": value}, func(f cli.Flag) { recorded = append(recorded, f) }))
			require.Equal(t, value, flag.Get())
			require.Equal(t, []cli.Flag{flag}, recorded)
		})
	}
}

func TestUntypedInnerStdinKeepsTypesAndPrecedence(t *testing.T) {
	for _, value := range []any{"a: b", "null", nil, true, float64(42), []any{"x"}, map[string]any{"x": "y"}} {
		t.Run(fmtValue(value), func(t *testing.T) {
			outer := &Flag[map[string]any]{Name: "settings", BodyPath: "settings"}
			inner := &InnerFlag[any]{Name: "settings.value", InnerField: "value", OuterFlag: outer}
			var recorded []cli.Flag
			require.NoError(t, ApplyStdinDataToFlagsWithProvenance(&cli.Command{Flags: []cli.Flag{outer, inner}}, map[string]any{"settings": map[string]any{"value": value}}, func(f cli.Flag) { recorded = append(recorded, f) }))
			require.Equal(t, value, outer.Get().(map[string]any)["value"])
			require.Equal(t, []cli.Flag{inner}, recorded)
		})
	}
}

func fmtValue(value any) string { return fmt.Sprintf("%T:%v", value, value) }

func TestPipedInnerFieldDoesNotOverrideExplicitLiteral(t *testing.T) {
	outer := &Flag[any]{Name: "settings", BodyPath: "settings"}
	inner := &InnerFlag[string]{Name: "settings.value", InnerField: "value", OuterFlag: outer}
	require.NoError(t, outer.Set("settings", "literal"))
	var recorded []cli.Flag
	require.NoError(t, ApplyStdinDataToFlagsWithProvenance(&cli.Command{Flags: []cli.Flag{outer, inner}}, map[string]any{"settings": map[string]any{"value": "piped"}}, func(f cli.Flag) { recorded = append(recorded, f) }))
	require.Equal(t, "literal", outer.Get())
	require.Empty(t, recorded)
}
