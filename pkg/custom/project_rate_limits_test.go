package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestProjectRateLimitEmptyResultBoundaries(t *testing.T) {
	ctx := context.WithValue(t.Context(), projectRateLimitScopeKey{}, "proj_\x1b\n\t\u202e")
	base := ShowJSONOpts{Context: ctx, OutputKind: OutputPageItem,
		Operation: "(resource) admin.organization.projects.rate_limits > (method) list_rate_limits"}
	var out bytes.Buffer
	base.Stdout = &out
	require.NoError(t, ShowJSONIterator(&transformTestIterator{}, -1, base))
	require.Equal(t, "No rate limits returned for proj_\\u001b\\n\\t\\u202e.\n", out.String())
	out.Reset()
	failure := errors.New("upstream failure")
	require.ErrorIs(t, ShowJSONIterator(&transformTestIterator{err: failure}, -1, base), failure)
	require.Empty(t, out.String())
	require.NoError(t, ShowJSONIterator(&transformTestIterator{}, 0, base))
	require.Empty(t, out.String())
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	base.Context = ctx
	require.ErrorIs(t, ShowJSONIterator(&transformTestIterator{}, -1, base), context.Canceled)
	require.Empty(t, out.String())
	base.Context = t.Context()
	for _, opts := range []ShowJSONOpts{
		base,
		{Context: context.WithValue(t.Context(), projectRateLimitScopeKey{}, "proj_demo"), Operation: "other", OutputKind: OutputPageItem},
		{Context: ctx, Operation: base.Operation, OutputKind: OutputResponse},
		{Context: ctx, Operation: base.Operation, OutputKind: OutputPageItem, Format: "json"},
		{Context: ctx, Operation: base.Operation, OutputKind: OutputPageItem, Format: "text", Transform: "model"},
		{Context: ctx, Operation: base.Operation, OutputKind: OutputPageItem, Format: "text", RawOutput: true},
	} {
		_, ok := projectRateLimitEmptyText(opts)
		require.False(t, ok)
	}
}

func TestProjectRateLimitRoutesPreserveHandlers(t *testing.T) {
	for _, path := range []string{
		"admin:organization:projects:rate-limits", "admin organization projects rate-limits",
		"admin projects rate-limits", "projects rate-limits",
	} {
		for _, verb := range []string{"list", "list-rate-limits", "update", "update-rate-limit"} {
			t.Run(path+"/"+verb, func(t *testing.T) {
				failure := errors.New("original action result")
				calls, before, after := 0, 0, 0
				root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard}
				resource := &cli.Command{Name: "admin:organization:projects:rate-limits", Category: "API RESOURCE"}
				for _, name := range []string{"list-rate-limits", "update-rate-limit"} {
					resource.Commands = append(resource.Commands, &cli.Command{
						Name:   name,
						Flags:  []cli.Flag{&cli.StringFlag{Name: "future", Aliases: []string{"f"}}},
						Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) { before++; return ctx, nil },
						After:  func(context.Context, *cli.Command) error { after++; return nil },
						Action: func(_ context.Context, command *cli.Command) error {
							calls++
							require.Equal(t, "preserved", command.String("future"))
							return failure
						},
					})
				}
				root.Commands = []*cli.Command{resource}
				configureProjectRateLimits(root)
				configureProjectRateLimits(root)
				require.Len(t, resource.Commands, 4, "configuration must not duplicate aliases")
				require.True(t, resource.Command("list-rate-limits").Hidden)
				require.False(t, resource.Command("list").Hidden)
				configureCommandSubgroups(root)
				configureTaskCommands(root)
				args := append([]string{"openai"}, strings.Fields(path)...)
				err := root.Run(t.Context(), append(args, verb, "-f", "preserved"))
				require.ErrorIs(t, err, failure)
				require.Equal(t, 1, calls)
				require.Equal(t, 1, before)
				require.Equal(t, 1, after)
			})
		}
	}
}

func TestProjectRateLimitHelpAndConflicts(t *testing.T) {
	existing := &cli.Command{Name: "list"}
	legacy := &cli.Command{Name: "list-rate-limits"}
	update := &cli.Command{Name: "update-rate-limit"}
	resource := &cli.Command{Name: "admin:organization:projects:rate-limits", Commands: []*cli.Command{existing, legacy, update}}
	root := &cli.Command{Commands: []*cli.Command{resource}}
	configureProjectRateLimits(root)
	require.Same(t, existing, resource.Command("list"))
	require.False(t, legacy.Hidden, "a collision must not hide the original command")
	for _, verb := range []string{"update", "update-rate-limit"} {
		content := resource.Command(verb).Metadata["help-content"].(clihelp.Content)
		require.Contains(t, content.Description, "--max-tokens-per-1-day is not supported")
		require.Contains(t, content.Description, "batch input tokens per day, not general tokens per day")
		require.Len(t, content.Examples, 1)
	}
	configureProjectRateLimits(&cli.Command{})
}
