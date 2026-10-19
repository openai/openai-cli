package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/openai/openai-cli/internal/commands/models"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

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

func modelsOptionsCommand(action cli.ActionFunc) *cli.Command {
	return &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{&cli.StringFlag{Name: "format"}},
		Commands: []*cli.Command{
			{Name: "models", Commands: []*cli.Command{{Name: "list", Action: action}}},
			{Name: "files", Commands: []*cli.Command{{Name: "list"}}},
		},
	}
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
			selectionContext := modelSelectionContext(t, "id~^model-", true, "~id")
			var out bytes.Buffer
			opts := ShowJSONOpts{Context: selectionContext,
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
		opts := ShowJSONOpts{Context: modelSelectionContext(t, "", false, "id"),
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
	opts := ShowJSONOpts{Context: modelSelectionContext(t, "", false, "id"),
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
	selectionContext := modelSelectionContext(t, "id=missing", true, "id")
	opts.Context = selectionContext
	var out bytes.Buffer
	opts.Stdout = &out
	require.NoError(t, ShowJSONIterator(source, -1, opts))
	require.Equal(t, "No results.\n", out.String())
}

// Exercise selection context through feature registration, not private context keys.
func modelSelectionContext(t *testing.T, filter string, filterSet bool, order string) context.Context {
	t.Helper()
	var selected context.Context
	root := modelsOptionsCommand(func(ctx context.Context, _ *cli.Command) error { selected = ctx; return nil })
	models.Configure(root, models.Options{})
	args := []string{"openai", "models", "list", "--sort-by", order}
	if filterSet {
		args = append(args, "--filter", filter)
	}
	require.NoError(t, root.Run(t.Context(), args))
	require.NotNil(t, selected)
	return selected
}
