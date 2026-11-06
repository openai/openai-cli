package requestflag_test

import (
	"context"
	"encoding/json"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/requestflag"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// An untyped outer flag (requestflag.Flag[any], used for nullable array-of-objects schemas) that is
// given a JSON or YAML array literal holds a []any, not a []map[string]any. Inner flags
// must merge into the trailing element the same way they do for a typed slice flag.
func TestInnerFlagAfterUntypedArrayLiteral(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{
			"merge into the trailing element",
			[]string{"--entry-structured", `[{"name":"earlier"}]`, "--entry.description", "details"},
			`{"entries":[{"name":"earlier","description":"details"}]}`,
		},
		{
			"repeated field starts another element",
			[]string{"--entry-structured", `[{"name":"earlier"}]`, "--entry.name", "demo"},
			`{"entries":[{"name":"earlier"},{"name":"demo"}]}`,
		},
		{
			"two inner fields fill an empty element",
			[]string{"--entry-structured", "[{}]", "--entry.name", "demo", "--entry.description", "details"},
			`{"entries":[{"name":"demo","description":"details"}]}`,
		},
		{
			"yaml flow array",
			[]string{"--entry-structured", "- {name: earlier}", "--entry.description", "details"},
			`{"entries":[{"name":"earlier","description":"details"}]}`,
		},
		{
			"non-object element starts a new one",
			[]string{"--entry-structured", `["plain"]`, "--entry.name", "demo"},
			`{"entries":["plain",{"name":"demo"}]}`,
		},
		{
			"null element starts a new one",
			[]string{"--entry-structured", `[null]`, "--entry.name", "demo"},
			`{"entries":[null,{"name":"demo"}]}`,
		},
		{
			"empty array",
			[]string{"--entry-structured", "[]", "--entry.name", "demo"},
			`{"entries":[{"name":"demo"}]}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			outer := &requestflag.Flag[any]{Name: "entry", BodyPath: "entries", InputPolicy: requestflag.InputPolicyTextStructured}
			var body []byte
			command := requestflag.WithInnerFlags(cli.Command{
				Name:  "test",
				Flags: []cli.Flag{outer},
				Action: func(_ context.Context, cmd *cli.Command) error {
					var err error
					contents, resolveErr := requestconfig.EmbedFiles(requestflag.ExtractRequestContents(cmd).Body, requestconfig.EmbedText, nil)
					if resolveErr != nil {
						return resolveErr
					}
					body, err = json.Marshal(contents)
					return err
				},
			}, map[string][]requestflag.HasOuterFlag{
				"entry": {
					&requestflag.InnerFlag[string]{Name: "entry.name", InnerField: "name", OuterIsArrayOfObjects: true},
					&requestflag.InnerFlag[string]{Name: "entry.description", InnerField: "description", OuterIsArrayOfObjects: true},
				},
			})

			requestflag.AddStructuredFlags(&command)
			require.NoError(t, command.Run(context.Background(), append([]string{"test"}, tt.args...)))
			assert.JSONEq(t, tt.want, string(body))
		})
	}
}

// Same sequence driven directly, mirroring the existing coverage for a `null` element.
func TestInnerFlagAfterUntypedArrayLiteralDirect(t *testing.T) {
	t.Parallel()

	outer := &requestflag.Flag[any]{Name: "entry", BodyPath: "entries", InputPolicy: requestflag.InputPolicyTextStructured}
	command := &cli.Command{Name: "test", Flags: []cli.Flag{outer}}
	requestflag.AddStructuredFlags(command)
	require.NoError(t, command.Run(context.Background(), []string{"test", "--entry-structured", `[{"name":"earlier"}]`}))

	name := &requestflag.InnerFlag[string]{
		Name: "entry.name", InnerField: "name", OuterFlag: outer, OuterIsArrayOfObjects: true,
	}
	description := &requestflag.InnerFlag[string]{
		Name: "entry.description", InnerField: "description", OuterFlag: outer, OuterIsArrayOfObjects: true,
	}
	require.NoError(t, name.Set(name.Name, "demo"))
	require.NoError(t, description.Set(description.Name, "details"))

	resolved, err := requestconfig.EmbedFiles(outer.Get(), requestconfig.EmbedText, nil)
	require.NoError(t, err)
	assert.Equal(t, []any{map[string]any{"name": "earlier"}, map[string]any{"name": "demo", "description": "details"}}, resolved)
}
