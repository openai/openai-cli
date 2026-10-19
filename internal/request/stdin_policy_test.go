package request

import (
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestNewStdinSecurityRejectsInvalidBoolean(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "definitely")

	_, err := NewStdinSecurity()

	require.ErrorContains(t, err, UntrustedStdinEnv)
	require.ErrorContains(t, err, "expected a boolean")
}

func TestStdinSecurityProtectsOnlyPipedFlagValues(t *testing.T) {
	t.Parallel()

	query := &requestflag.Flag[string]{Name: "query", QueryPath: "query", DataAliases: []string{"query_alias"}}
	header := &requestflag.Flag[string]{Name: "header", HeaderPath: "X-Test", DataAliases: []string{"header_alias"}}
	explicit := &requestflag.Flag[string]{Name: "explicit", QueryPath: "explicit"}
	outer := &requestflag.Flag[map[string]any]{Name: "nested", BodyPath: "nested"}
	inner := &requestflag.InnerFlag[string]{
		Name:        "nested.untrusted",
		InnerField:  "untrusted",
		DataAliases: []string{"nested_alias"},
		OuterFlag:   outer,
	}
	for _, flag := range []*requestflag.Flag[string]{query, header, explicit} {
		require.NoError(t, flag.PreParse())
	}
	require.NoError(t, outer.PreParse())
	require.NoError(t, explicit.Set("explicit", "@trusted-query"))
	require.NoError(t, outer.Set("nested", `{trusted: "@trusted-inner"}`))
	command := &cli.Command{Flags: []cli.Flag{query, header, explicit, outer, inner}}
	security := &StdinSecurity{enabled: true}

	require.NoError(t, requestflag.ApplyStdinDataToFlagsWithProvenance(command, map[string]any{
		"query_alias":  "@piped-query",
		"header_alias": "@piped-header",
		"explicit":     "@piped-override",
		"nested":       map[string]any{"nested_alias": "@piped-inner"},
	}, security.RecordFlag))
	contents := requestflag.ExtractRequestContents(command)
	security.ProtectFlagValues(&contents)

	require.Equal(t, UntrustedStdinValue("@piped-query"), contents.Queries["query"])
	require.Equal(t, UntrustedStdinValue("@piped-header"), contents.Headers["X-Test"])
	require.Equal(t, "@trusted-query", contents.Queries["explicit"])
	nested := contents.Body.(map[string]any)["nested"].(map[string]any)
	require.Equal(t, "@trusted-inner", nested["trusted"])
	require.Equal(t, UntrustedStdinValue("@piped-inner"), nested["untrusted"])
}

func TestStdinSecurityPreservesExplicitInnerArrayFields(t *testing.T) {
	t.Parallel()

	outer := &requestflag.Flag[[]map[string]any]{Name: "entries", BodyPath: "entries"}
	require.NoError(t, outer.PreParse())
	require.NoError(t, outer.Set("entries", `{value: "@trusted-reference"}`))
	inner := &requestflag.InnerFlag[string]{
		Name:                  "entries.value",
		InnerField:            "value",
		OuterFlag:             outer,
		OuterIsArrayOfObjects: true,
	}
	command := &cli.Command{Flags: []cli.Flag{outer, inner}}
	security := &StdinSecurity{enabled: true}

	err := requestflag.ApplyStdinDataToFlagsWithProvenance(command, map[string]any{
		"entries": map[string]any{"value": "@piped-reference"},
	}, security.RecordFlag)

	require.NoError(t, err)
	entries := outer.Get().([]map[string]any)
	require.Len(t, entries, 1)
	require.Equal(t, "@trusted-reference", entries[0]["value"])
}

func TestWrapFileInputValuesRejectsUntrustedLocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		flag  *requestflag.Flag[string]
		value any
	}{
		{name: "body", flag: &requestflag.Flag[string]{Name: "file", BodyPath: "file", FileInput: true}, value: UntrustedStdinValue("synthetic.txt")},
		{name: "empty body", flag: &requestflag.Flag[string]{Name: "file", BodyPath: "file", FileInput: true}, value: UntrustedStdinValue("")},
		{name: "null body", flag: &requestflag.Flag[string]{Name: "file", BodyPath: "file", FileInput: true}, value: nil},
		{name: "number body", flag: &requestflag.Flag[string]{Name: "file", BodyPath: "file", FileInput: true}, value: 123},
		{name: "body array", flag: &requestflag.Flag[string]{Name: "file", BodyPath: "file", FileInput: true}, value: []any{UntrustedStdinValue("synthetic.txt")}},
		{name: "query", flag: &requestflag.Flag[string]{Name: "file", QueryPath: "file", FileInput: true}, value: UntrustedStdinValue("synthetic.txt")},
		{name: "header", flag: &requestflag.Flag[string]{Name: "file", HeaderPath: "X-File", FileInput: true}, value: UntrustedStdinValue("synthetic.txt")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			contents := requestflag.RequestContents{
				Body:    map[string]any{test.flag.BodyPath: test.value},
				Queries: map[string]any{test.flag.QueryPath: test.value},
				Headers: map[string]any{test.flag.HeaderPath: test.value},
			}

			command := &cli.Command{Flags: []cli.Flag{test.flag}}
			security := &StdinSecurity{enabled: true}
			if test.flag.BodyPath != "" {
				security.RecordBodyFlags(command, contents.Body.(map[string]any))
			} else {
				security.RecordFlag(test.flag)
			}
			err := WrapFileInputValues(command, &contents, security)

			require.ErrorContains(t, err, UntrustedStdinEnv)
			require.ErrorContains(t, err, "provide --file explicitly")
		})
	}

	t.Run("explicit CLI path remains trusted", func(t *testing.T) {
		t.Parallel()

		flag := &requestflag.Flag[string]{Name: "file", BodyPath: "file", FileInput: true}
		require.NoError(t, flag.PreParse())
		require.NoError(t, flag.Set("file", "trusted.txt"))
		contents := requestflag.RequestContents{Body: map[string]any{"file": "trusted.txt"}}
		security := &StdinSecurity{enabled: true}
		security.RecordBodyFlags(&cli.Command{Flags: []cli.Flag{flag}}, contents.Body.(map[string]any))

		require.NoError(t, WrapFileInputValues(&cli.Command{Flags: []cli.Flag{flag}}, &contents, security))
		require.Equal(t, FilePathValue("trusted.txt"), contents.Body.(map[string]any)["file"])
	})
}

func TestUntrustedStdinFileInputAliasCannotBypassProtection(t *testing.T) {
	t.Parallel()

	flag := &requestflag.Flag[string]{
		Name:        "upload",
		BodyPath:    "file",
		DataAliases: []string{"upload_alias"},
		FileInput:   true,
	}
	command := &cli.Command{Flags: []cli.Flag{flag}}
	stdin := map[string]any{"upload_alias": "synthetic-private.txt"}
	ApplyDataAliases(command, stdin)
	contents := requestflag.RequestContents{Body: ProtectStdinValue(stdin)}
	security := &StdinSecurity{enabled: true}
	security.RecordBodyFlags(command, stdin)

	err := WrapFileInputValues(command, &contents, security)

	require.ErrorContains(t, err, `file input "file"`)
	require.ErrorContains(t, err, "provide --upload explicitly")
}
