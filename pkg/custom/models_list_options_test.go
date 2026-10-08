package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestModelsListLimitHelpDefaultKeepsPresence(t *testing.T) {
	for _, test := range []struct {
		args  []string
		set   bool
		value int64
	}{
		{nil, false, 0},
		{[]string{"--max-items", "0"}, true, 0},
		{[]string{"--max-items", "-1"}, true, -1},
	} {
		limit := &requestflag.Flag[int64]{Name: "max-items"}
		unrelated := &requestflag.Flag[int64]{Name: "max-items"}
		called := false
		command := modelsOptionsCommand(func(_ context.Context, command *cli.Command) error {
			called = true
			require.Equal(t, test.set, command.IsSet("max-items"))
			require.Equal(t, test.value, command.Value("max-items"))
			return nil
		})
		command.Command("models").Command("list").Flags = []cli.Flag{limit}
		command.Command("files").Command("list").Flags = []cli.Flag{unrelated}
		configureModelsList(command)
		require.Equal(t, "unlimited", limit.GetDefaultText())
		require.Zero(t, limit.Default)
		require.Empty(t, unrelated.DefaultText)
		require.NoError(t, command.Run(t.Context(), append([]string{"openai", "models", "list"}, test.args...)))
		require.True(t, called)
		require.Equal(t, "unlimited", limit.GetDefaultText())
	}
}

func TestModelsListOptionsValidateBeforeAction(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"filter syntax", []string{"models", "list", "--filter", "id:synthetic-private"}, "--filter accepts"},
		{"empty filter", []string{"models", "list", "--filter="}, "--filter accepts"},
		{"missing operand", []string{"models", "list", "--filter", "id=   "}, "requires an operand"},
		{"unclosed quote", []string{"models", "list", "--filter", `id="synthetic-private`}, "unterminated quote"},
		{"trailing expression", []string{"models", "list", "--filter", `id="synthetic-private" OR id=other`}, "accepts one operand"},
		{"regex syntax", []string{"models", "list", "--filter", "id~[synthetic-private"}, "invalid regular expression"},
		{"sort syntax", []string{"models", "list", "--sort-by", "synthetic-private"}, "--sort-by accepts"},
		{"raw sort", []string{"--format", "raw", "models", "list", "--sort-by", "id"}, "do not support --format raw"},
		{"raw filter", []string{"--format", "RAW", "models", "list", "--filter", "id=synthetic-private"}, "do not support --format raw"},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			command := modelsOptionsCommand(func(context.Context, *cli.Command) error { called = true; return nil })
			configureModelsList(command)
			err := command.Run(t.Context(), append([]string{"openai"}, test.args...))
			require.ErrorContains(t, err, test.want)
			require.False(t, called)
			require.NotContains(t, err.Error(), "synthetic-private")
			require.Contains(t, localErrorMessage(command, err), test.want)
		})
	}
}

func TestModelsListOptionsAreScopedAndPreserveContext(t *testing.T) {
	for _, test := range []struct {
		args                 []string
		selected, descending bool
	}{
		{[]string{"models", "list"}, false, false},
		{[]string{"models", "list", "--sort-by", "~id"}, true, true},
		{[]string{"models", "list", "--filter", ` id = "synthetic value" `}, true, false},
	} {
		called := false
		ctx, cancel := context.WithCancel(t.Context())
		command := modelsOptionsCommand(func(got context.Context, _ *cli.Command) error {
			called = true
			options, selected := got.Value(modelsListOptionsKey{}).(modelsListOptions)
			require.Equal(t, test.selected, selected)
			require.Equal(t, test.descending, options.selection.Descending)
			cancel()
			require.ErrorIs(t, got.Err(), context.Canceled)
			return nil
		})
		configureModelsList(command)
		require.NoError(t, command.Run(ctx, append([]string{"openai"}, test.args...)))
		require.True(t, called)
		for _, flag := range command.Command("files").Command("list").Flags {
			require.NotContains(t, flag.Names(), "filter")
			require.NotContains(t, flag.Names(), "sort-by")
		}
	}
}

func modelsOptionsCommand(action cli.ActionFunc) *cli.Command {
	return &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{&cli.StringFlag{Name: "format"}},
		Commands: []*cli.Command{
			{Name: "models", Commands: []*cli.Command{{Name: "list", Action: action}}},
			{Name: "files", Commands: []*cli.Command{{Name: "list"}}},
		},
	}
}

func TestModelsListFilterOperandRemainsExact(t *testing.T) {
	for _, value := range []string{" leading and trailing ", "value=with=equals", `"literal quotes"`, "model AND other", `single' and "double" quotes`, `model\path`, "model\nline", "\x00\xff"} {
		quoted := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
		options, err := parseModelsListOptions("id="+quoted, true, "id")
		require.NoError(t, err)
		require.NotNil(t, options.selection.ExactID)
		require.Equal(t, value, *options.selection.ExactID)
	}
	options, err := parseModelsListOptions("id~(?i)^model$", true, "id")
	require.NoError(t, err)
	require.True(t, options.selection.Pattern.MatchString("MODEL"))
	require.False(t, options.selection.Pattern.MatchString("prefix-MODEL"))
}

func TestModelsListSelectionUsesExistingFormats(t *testing.T) {
	for _, test := range []struct {
		format, transform string
		raw               bool
		want              string
	}{
		{"jsonl", "", false, "\"id\":\"model-c\""},
		{"text", "", false, "ID: model-c"},
		{"auto", "id", true, "model-c\n"},
		{"json", "id", false, "\"model-c\"\n"},
	} {
		t.Run(test.format+test.transform, func(t *testing.T) {
			source, _ := staticModelIterator(t, `[{"id":"skip","object":"model"},{"id":"model-a","object":"model"},{"id":"model-c","object":"model","detail":"preserved"}]`, -1, nil)
			options, err := parseModelsListOptions("id~^model-", true, "~id")
			require.NoError(t, err)
			var out bytes.Buffer
			opts := ShowJSONOpts{Context: context.WithValue(t.Context(), modelsListOptionsKey{}, options),
				Operation: "(resource) models > (method) list", OutputKind: OutputPageItem,
				Format: test.format, ExplicitFormat: true, Transform: test.transform, RawOutput: test.raw, Stdout: &out}
			require.NoError(t, ShowJSONIterator(source, 1, opts))
			require.Contains(t, out.String(), test.want)
			require.NotContains(t, out.String(), "model-a")
			require.NotContains(t, out.String(), "skip")
			if test.transform == "" {
				require.Contains(t, out.String(), "preserved")
			}
			require.Equal(t, 3, source.Index())
		})
	}
}

func TestModelsListSelectionPreservesFailures(t *testing.T) {
	upstream := errors.New("synthetic upstream")
	for _, test := range []struct {
		maximum  int64
		data     string
		upstream error
		want     error
		message  string
	}{
		{0, `[]`, upstream, upstream, ""},
		{-1, `[]`, upstream, upstream, ""},
		{-1, `[{"object":"model","id":null,"private":"synthetic-private"}]`, nil, nil, "invalid, or ambiguous IDs"},
	} {
		source, _ := staticModelIterator(t, test.data, -1, test.upstream)
		var out bytes.Buffer
		opts := ShowJSONOpts{Context: context.WithValue(t.Context(), modelsListOptionsKey{}, modelsListOptions{}),
			Operation: "(resource) models > (method) list", OutputKind: OutputPageItem, Stdout: &out}
		err := ShowJSONIterator(source, test.maximum, opts)
		if test.want != nil {
			require.ErrorIs(t, err, test.want)
		} else {
			require.ErrorContains(t, err, test.message)
		}
		require.NotContains(t, err.Error(), "synthetic-private")
		require.Empty(t, out.String())
	}
}

func TestModelsListSelectionRejectsCursorAndPreservesCancellation(t *testing.T) {
	opts := ShowJSONOpts{Context: context.WithValue(t.Context(), modelsListOptionsKey{}, modelsListOptions{}),
		Operation: "(resource) models > (method) list", OutputKind: OutputPageItem, Stdout: io.Discard}
	cursor := pagination.NewCursorPageAutoPager(&pagination.CursorPage[openai.Model]{Data: []openai.Model{{ID: "unread"}}, HasMore: true}, nil)
	handled, err := showModelsListSelection(cursor, -1, opts)
	require.True(t, handled)
	require.ErrorContains(t, err, "single models response")
	require.Zero(t, cursor.Index())
	ctx, cancel := context.WithCancel(opts.Context)
	cancel()
	opts.Context = ctx
	source, _ := staticModelIterator(t, `[{"object":"model","id":"unread"}]`, -1, nil)
	require.ErrorIs(t, ShowJSONIterator(source, -1, opts), context.Canceled)
	require.Zero(t, source.Index())
	options, err := parseModelsListOptions("id=missing", true, "id")
	require.NoError(t, err)
	opts.Context = context.WithValue(t.Context(), modelsListOptionsKey{}, options)
	var out bytes.Buffer
	opts.Stdout = &out
	require.NoError(t, ShowJSONIterator(source, -1, opts))
	require.Equal(t, "No results.\n", out.String())
}
