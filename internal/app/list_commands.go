package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/apiquery"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/urfave/cli/v3"
)

// Configure the canonical actions before routing clones their compatibility aliases.
// Generated declarations and request encoding remain owned by their existing paths.
func configureListCommands(root *cli.Command) {
	// Models has its own request, selection and sorting policy.
	if models := root.Command("models"); models != nil && models.Command("list") != nil {
		markListPager(models.Command("list"))
	}
	configureListCommand(root, "files", "(resource) files > (method) list", "",
		func(ctx context.Context, client openai.Client, options []option.RequestOption) (*pagination.CursorPage[openai.FileObject], error) {
			return client.Files.List(ctx, openai.FileListParams{}, options...)
		})
	configureListCommand(root, "batches", "(resource) batches > (method) list", "",
		func(ctx context.Context, client openai.Client, options []option.RequestOption) (*pagination.CursorPage[openai.Batch], error) {
			return client.Batches.List(ctx, openai.BatchListParams{}, options...)
		})
	configureListCommand(root, "admin:organization:projects", "(resource) admin.organization.projects > (method) list", "last_id",
		func(ctx context.Context, client openai.Client, options []option.RequestOption) (*pagination.ConversationCursorPage[openai.Project], error) {
			return client.Admin.Organization.Projects.List(ctx, openai.AdminOrganizationProjectListParams{}, options...)
		})
}

func configureListCommand[P JSONListPage[P]](root *cli.Command, resourceName, operation, cursorField string,
	first func(context.Context, openai.Client, []option.RequestOption) (P, error),
) {
	resource := root.Command(resourceName)
	if resource == nil {
		return
	}
	command := resource.Command("list")
	if command == nil || command.Action == nil {
		return
	}
	markListPager(command)
	command.Description = strings.TrimSpace(command.Description + "\n\n" +
		listview.ControlsHelp + "\n" +
		"Automatic terminal output shows a compact table, with labeled output when a page needs more detail.\n" +
		"--limit controls API page size; --max-items limits total results and defaults to unlimited.\n" +
		"Explicit text or machine formats, field extraction, and redirected input or output retain their existing output behavior.")
	for _, flag := range command.Flags {
		if limit, ok := flag.(*requestflag.Flag[int64]); ok && limit.Name == "max-items" {
			limit.DefaultText = "unlimited"
		}
	}
	previous := command.Action
	command.Action = func(ctx context.Context, cmd *cli.Command) error {
		opts := ShowJSONOpts{
			Context: ctx, Operation: operation, OutputKind: OutputPageItem,
			Format: cmd.Root().String("format"), ExplicitFormat: cmd.Root().IsSet("format"),
			RawOutput: cmd.Root().Bool("raw-output"), Transform: cmd.Root().String("transform"),
			Title: resourceName + " list",
		}
		// Batch watch owns its snapshot stream and validates watch-only options.
		if resourceName == "batches" && usesBatchWatchOptions(cmd) || !InteractiveListOutput(opts) {
			return previous(ctx, cmd)
		}
		client := openai.NewClient(GetDefaultRequestOptions(cmd)...)
		if args := cmd.Args().Slice(); len(args) != 0 {
			return fmt.Errorf("Unexpected extra arguments: %v", args)
		}
		options, err := FlagOptions(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, false)
		if err != nil {
			return err
		}
		maximum := int64(-1)
		if cmd.IsSet("max-items") {
			maximum = cmd.Value("max-items").(int64)
		}
		return runInteractiveList(opts, func(opts ShowJSONOpts) error {
			return ShowJSONPages(func(requestContext context.Context) (P, error) {
				return first(requestContext, client, options)
			}, maximum, opts, cursorField)
		})
	}
}

func usesBatchWatchOptions(cmd *cli.Command) bool {
	return cmd.Bool("watch") || cmd.IsSet("watch-count") || cmd.IsSet("poll-interval")
}

// One signal owner cancels requests before the viewer joins its fetch worker.
func runInteractiveList(opts ShowJSONOpts, show func(ShowJSONOpts) error) error {
	ctx, stop := clioutput.DownloadSignalContext(opts.Context)
	defer stop()
	opts.Context, opts.CallerHandlesSignals = ctx, true
	err := clioutput.StreamToStdout(func(*os.File) error { return show(opts) })
	var interrupted *clioutput.DownloadSignalError
	if errors.As(context.Cause(ctx), &interrupted) {
		err = errors.Join(err, context.Cause(ctx))
		var exit cli.ExitCoder
		if !errors.As(err, &exit) {
			err = &listCommandInterrupt{cause: err, code: interrupted.SignalExitCode()}
		}
	}
	return err
}

// Keep a nonempty cause so the error presenter does not mistake cancellation
// for an intentionally silent shell-completion status.
type listCommandInterrupt struct {
	cause error
	code  int
}

func (e *listCommandInterrupt) Error() string { return e.cause.Error() }
func (e *listCommandInterrupt) Unwrap() error { return e.cause }
func (e *listCommandInterrupt) ExitCode() int { return e.code }
