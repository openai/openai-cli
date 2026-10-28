package requestflag

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// An untyped outer flag (Flag[any], used for nullable array-of-objects schemas) that is
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
			[]string{"--entry", `[{"name":"earlier"}]`, "--entry.description", "details"},
			`{"entries":[{"name":"earlier","description":"details"}]}`,
		},
		{
			"repeated field starts another element",
			[]string{"--entry", `[{"name":"earlier"}]`, "--entry.name", "demo"},
			`{"entries":[{"name":"earlier"},{"name":"demo"}]}`,
		},
		{
			"two inner fields fill an empty element",
			[]string{"--entry", "[{}]", "--entry.name", "demo", "--entry.description", "details"},
			`{"entries":[{"name":"demo","description":"details"}]}`,
		},
		{
			"yaml flow array",
			[]string{"--entry", "- {name: earlier}", "--entry.description", "details"},
			`{"entries":[{"name":"earlier","description":"details"}]}`,
		},
		{
			"non-object element starts a new one",
			[]string{"--entry", `["plain"]`, "--entry.name", "demo"},
			`{"entries":["plain",{"name":"demo"}]}`,
		},
		{
			"null element starts a new one",
			[]string{"--entry", `[null]`, "--entry.name", "demo"},
			`{"entries":[null,{"name":"demo"}]}`,
		},
		{
			"empty array",
			[]string{"--entry", "[]", "--entry.name", "demo"},
			`{"entries":[{"name":"demo"}]}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			outer := &Flag[any]{Name: "entry", BodyPath: "entries"}
			var body []byte
			command := WithInnerFlags(cli.Command{
				Name:  "test",
				Flags: []cli.Flag{outer},
				Action: func(_ context.Context, cmd *cli.Command) error {
					var err error
					body, err = json.Marshal(ExtractRequestContents(cmd).Body)
					return err
				},
			}, map[string][]HasOuterFlag{
				"entry": {
					&InnerFlag[string]{Name: "entry.name", InnerField: "name", OuterIsArrayOfObjects: true},
					&InnerFlag[string]{Name: "entry.description", InnerField: "description", OuterIsArrayOfObjects: true},
				},
			})

			require.NoError(t, command.Run(context.Background(), append([]string{"test"}, tt.args...)))
			assert.JSONEq(t, tt.want, string(body))
		})
	}
}

// Same sequence driven directly, mirroring the existing coverage for a `null` element.
func TestInnerFlagAfterUntypedArrayLiteralDirect(t *testing.T) {
	t.Parallel()

	outer := &Flag[any]{Name: "entry"}
	require.NoError(t, outer.PreParse())
	require.NoError(t, outer.Set(outer.Name, `[{"name":"earlier"}]`))

	name := &InnerFlag[string]{
		Name: "entry.name", InnerField: "name", OuterFlag: outer, OuterIsArrayOfObjects: true,
	}
	description := &InnerFlag[string]{
		Name: "entry.description", InnerField: "description", OuterFlag: outer, OuterIsArrayOfObjects: true,
	}
	require.NoError(t, name.Set(name.Name, "demo"))
	require.NoError(t, description.Set(description.Name, "details"))

	assert.Equal(t,
		[]any{map[string]any{"name": "earlier"}, map[string]any{"name": "demo", "description": "details"}},
		outer.Get(),
	)
}
