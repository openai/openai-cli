package requestflag

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestInputPolicyRejectsMissingRequestMetadata(t *testing.T) {
	for _, test := range []struct {
		name string
		flag cli.Flag
	}{
		{"body", &Flag[any]{Name: "value", BodyPath: "value"}},
		{"body root", &Flag[any]{Name: "value", BodyRoot: true}},
		{"query", &Flag[string]{Name: "value", QueryPath: "value"}},
		{"header", &Flag[string]{Name: "value", HeaderPath: "X-Value"}},
		{"path", &Flag[string]{Name: "value", PathParam: "value"}},
		{"inner", &InnerFlag[any]{Name: "value", InnerField: "value", OuterFlag: &Flag[map[string]any]{Name: "outer", InputPolicy: InputPolicyTyped}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := &cli.Command{Commands: []*cli.Command{{Name: "resource", Commands: []*cli.Command{{Name: "create", Flags: []cli.Flag{test.flag}}}}}}
			for _, mode := range []string{"", "literal", "legacy"} {
				var policyError *InputPolicyError
				err := ConfigureInputMode(root, mode)
				require.ErrorAs(t, err, &policyError)
				require.Contains(t, err.Error(), "--value")
			}
		})
	}
	t.Run("local flags need no request policy", func(t *testing.T) {
		root := &cli.Command{Flags: []cli.Flag{&Flag[int64]{Name: "max-items"}, &cli.StringFlag{Name: "format"}}}
		require.NoError(t, ConfigureInputMode(root, "literal"))
	})
}

func TestUnannotatedCommandsOnlyFillAnEntireOlderSnapshot(t *testing.T) {
	for _, annotation := range []string{"none", "action", "resource", "root"} {
		input := &Flag[any]{Name: "prompt", BodyPath: "input"}
		query := &Flag[any]{Name: "input", QueryPath: "input"}
		action := &cli.Command{Name: "create", Flags: []cli.Flag{input, query}}
		resource := &cli.Command{Name: "responses", Commands: []*cli.Command{action}}
		root := &cli.Command{Commands: []*cli.Command{resource}}
		global := &Flag[string]{Name: "api-key", InputPolicy: InputPolicyText}
		switch annotation {
		case "action":
			query.InputPolicy = InputPolicyTyped
		case "root":
			root.Flags = []cli.Flag{global}
		case "resource":
			resource.Flags = []cli.Flag{global}
		}
		ConfigureUnannotatedCommands(root, map[string][]string{"responses create": {"input"}})
		if annotation != "none" {
			require.Equal(t, InputPolicyUnknown, input.InputPolicy)
			require.ErrorContains(t, ConfigureInputMode(root, ""), "--prompt")
			continue
		}
		require.NoError(t, ConfigureInputMode(root, ""))
		require.Equal(t, InputPolicyTextStructured, input.InputPolicy)
		require.Equal(t, inputPolicyExistingParser, query.InputPolicy)
		require.NoError(t, query.Set("input", "17"))
		require.NotEqual(t, "17", query.Get())
	}
}

func TestInputPolicyModeBelongsToCommandTree(t *testing.T) {
	t.Parallel()
	newTree := func() (*cli.Command, *Flag[any], *Flag[any]) {
		text := &Flag[any]{Name: "input", BodyPath: "input", InputPolicy: InputPolicyTextStructured}
		typed := &Flag[any]{Name: "count", BodyPath: "count", InputPolicy: InputPolicyTyped}
		return &cli.Command{Commands: []*cli.Command{{Name: "create", Flags: []cli.Flag{text, typed}}}}, text, typed
	}
	legacyTree, legacy, legacyTyped := newTree()
	literalTree, literal, literalTyped := newTree()
	require.NoError(t, ConfigureInputMode(legacyTree, "legacy"))
	require.NoError(t, ConfigureInputMode(literalTree, "literal"))
	require.NoError(t, legacy.Set("input", "key: value"))
	require.NoError(t, literal.Set("input", "key: value"))
	require.Equal(t, map[string]any{"key": "value"}, legacy.Get())
	require.Equal(t, "key: value", literal.Get())
	for _, typed := range []*Flag[any]{legacyTyped, literalTyped} {
		require.NoError(t, typed.Set("count", "1024"))
		require.Equal(t, uint64(1024), typed.Get())
	}
	require.NoError(t, ConfigureInputMode(legacyTree, "literal"))
	require.NoError(t, legacy.Set("input", "null"))
	require.Equal(t, "null", legacy.Get())
	require.NoError(t, ConfigureInputMode(literalTree, "legacy"))
	require.NoError(t, literal.Set("input", "null"))
	require.Nil(t, literal.Get())
}

func TestInputPolicyModeValidationDoesNotDiscloseValues(t *testing.T) {
	for _, mode := range []string{"automatic", "LITERAL", "legacy ", "synthetic-private-mode\x1b[31m"} {
		err := ConfigureInputMode(&cli.Command{}, mode)
		var policyError *InputPolicyError
		require.ErrorAs(t, err, &policyError)
		require.Contains(t, err.Error(), InputModeEnv)
		require.Contains(t, err.Error(), "literal or legacy")
		require.NotContains(t, err.Error(), "synthetic-private")
		require.NotContains(t, err.Error(), "\x1b")
	}
}

func TestInputPolicyCompanionsFollowSchemaAndShareAliases(t *testing.T) {
	text := &Flag[any]{Name: "input", Aliases: []string{"q"}, BodyPath: "input", Usage: "Input value.", InputPolicy: InputPolicyTextStructured}
	plain := &Flag[string]{Name: "name", BodyPath: "name", InputPolicy: InputPolicyText}
	typed := &Flag[any]{Name: "count", BodyPath: "count", InputPolicy: InputPolicyTyped}
	local := &Flag[any]{Name: "local", InputPolicy: InputPolicyTextStructured}
	first := &cli.Command{Name: "first", Flags: []cli.Flag{text, plain, typed, local}}
	alias := &cli.Command{Name: "alias", Flags: []cli.Flag{text, plain, typed, local}}
	root := &cli.Command{Commands: []*cli.Command{first, alias}}
	AddStructuredFlags(root)
	AddStructuredFlags(root)
	require.NoError(t, ConfigureInputMode(root, "literal"))
	require.Len(t, first.Flags, 5)
	require.Len(t, alias.Flags, 5)
	require.Same(t, first.Flags[4], alias.Flags[4])
	require.Equal(t, []string{"input-structured", "q-structured"}, first.Flags[4].Names())
	require.Equal(t, 1, strings.Count(text.Usage, "Text is literal by default."))
	require.Contains(t, text.Usage, "--input-structured")
	require.Equal(t, "", plain.Usage)
	require.Equal(t, "", typed.Usage)
	companion, ok := first.Flags[4].(*structuredFlag)
	require.True(t, ok)
	require.Same(t, text, companion.target)
	require.Contains(t, companion.Usage, "@file or @-")
}

func TestInputPolicyRejectsCompanionNameCollisions(t *testing.T) {
	for _, occupant := range []cli.Flag{
		&cli.StringFlag{Name: "input-structured"},
		&cli.StringFlag{Name: "other", Aliases: []string{"input-structured"}},
		&Flag[string]{Name: "input-structured", BodyPath: "other", InputPolicy: InputPolicyText},
		&cli.StringFlag{Name: "q-structured"},
	} {
		t.Run(occupant.Names()[0], func(t *testing.T) {
			input := &Flag[any]{Name: "input", Aliases: []string{"q"}, BodyPath: "input", InputPolicy: InputPolicyTextStructured}
			root := &cli.Command{Flags: []cli.Flag{input, occupant}}
			AddStructuredFlags(root)
			err := ConfigureInputMode(root, "literal")
			var policyError *InputPolicyError
			require.ErrorAs(t, err, &policyError)
			require.Contains(t, err.Error(), "-structured")
		})
	}
}

func TestInputPolicyStructuredOverridesLegacyAndRejectsAliases(t *testing.T) {
	for _, mode := range []string{"literal", "legacy"} {
		for _, args := range [][]string{
			{"--input-structured", `{"key":"value"}`},
			{"--q-structured", `{"key":"value"}`},
			{"--input-structured", "@synthetic-missing.json", "-q", "@-"},
			{"-q", "@-", "--input-structured", "@synthetic-missing.json"},
			{"--q-structured", "@synthetic-missing.json", "--input", "@-"},
			{"--input", "@-", "--q-structured", "@synthetic-missing.json"},
		} {
			t.Run(mode+"/"+strings.Join(args, "/"), func(t *testing.T) {
				flag := &Flag[any]{Name: "input", Aliases: []string{"q"}, BodyPath: "input", InputPolicy: InputPolicyTextStructured}
				called := false
				command := &cli.Command{Name: "test", Flags: []cli.Flag{flag}, Writer: io.Discard, ErrWriter: io.Discard,
					Action: func(context.Context, *cli.Command) error { called = true; return nil }}
				AddStructuredFlags(command)
				require.NoError(t, ConfigureInputMode(command, mode))
				err := command.Run(context.Background(), append([]string{"test"}, args...))
				if len(args) == 2 {
					require.NoError(t, err)
					require.True(t, called)
					require.Equal(t, map[string]any{"key": "value"}, unwrappedInput(flag.Get()))
				} else {
					require.ErrorContains(t, err, "cannot combine --input with --input-structured")
					require.False(t, called)
					// The application presenter sanitizes urfave's wrapped parse error.
				}
			})
		}
	}
}
