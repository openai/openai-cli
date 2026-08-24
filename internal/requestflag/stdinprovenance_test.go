package requestflag

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestApplyStdinDataToFlagsWithProvenance(t *testing.T) {
	t.Parallel()

	query := &Flag[string]{Name: "query", QueryPath: "query", DataAliases: []string{"query_alias"}}
	header := &Flag[string]{Name: "header", HeaderPath: "X-Test"}
	explicit := &Flag[string]{Name: "explicit", QueryPath: "explicit"}
	outer := &Flag[map[string]any]{Name: "nested", BodyPath: "nested"}
	inner := &InnerFlag[string]{
		Name:        "nested.value",
		InnerField:  "value",
		DataAliases: []string{"value_alias"},
		OuterFlag:   outer,
	}
	for _, flag := range []*Flag[string]{query, header, explicit} {
		require.NoError(t, flag.PreParse())
	}
	require.NoError(t, outer.PreParse())
	require.NoError(t, explicit.Set("explicit", "trusted"))

	command := &cli.Command{Flags: []cli.Flag{query, header, explicit, outer, inner}}
	var observed []cli.Flag
	err := ApplyStdinDataToFlagsWithProvenance(command, map[string]any{
		"query_alias": "piped query",
		"X-Test":      "piped header",
		"explicit":    "piped override",
		"nested":      map[string]any{"value_alias": "piped nested value"},
	}, func(flag cli.Flag) {
		observed = append(observed, flag)
	})

	require.NoError(t, err)
	require.Equal(t, []cli.Flag{query, header, inner}, observed)
	require.Equal(t, "trusted", explicit.Get())
}

func TestApplyStdinDataToFlagsWithProvenanceDoesNotReportFailedSet(t *testing.T) {
	t.Parallel()

	flag := &Flag[int64]{Name: "count", QueryPath: "count"}
	require.NoError(t, flag.PreParse())
	command := &cli.Command{Flags: []cli.Flag{flag}}
	observed := false

	err := ApplyStdinDataToFlagsWithProvenance(command, map[string]any{"count": "not an integer"}, func(cli.Flag) {
		observed = true
	})

	require.Error(t, err)
	require.False(t, observed)
}

func TestApplyStdinDataToFlagsWithProvenancePreservesExplicitInnerMapField(t *testing.T) {
	t.Parallel()

	outer := &Flag[map[string]any]{Name: "nested", BodyPath: "nested"}
	inner := &InnerFlag[string]{
		Name:       "nested.value",
		InnerField: "value",
		OuterFlag:  outer,
	}
	require.NoError(t, outer.PreParse())
	require.NoError(t, inner.Set("nested.value", "explicit"))
	command := &cli.Command{Flags: []cli.Flag{outer, inner}}
	observed := false

	err := ApplyStdinDataToFlagsWithProvenance(command, map[string]any{
		"nested": map[string]any{"value": "piped"},
	}, func(cli.Flag) {
		observed = true
	})

	require.NoError(t, err)
	require.Equal(t, "explicit", outer.Get().(map[string]any)["value"])
	require.False(t, observed)
}
