package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/apiquery"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/urfave/cli/v3"
)

// Configure the canonical actions before routing clones their compatibility aliases.
// Generated declarations and request encoding remain owned by their existing paths.
func configureListCommands(root *cli.Command) {
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
	command.Description = strings.TrimSpace(command.Description + "\n\n" +
		"Automatic terminal output shows a compact table, with labeled output when a page needs more detail.\n" +
		"Space advances through loaded results, then requests the next API page. b goes back; q quits.\n" +
		"Press p to print the current page after leaving the viewer. Short complete results finish automatically.\n" +
		"--limit controls API page size; --max-items limits total results and defaults to unlimited.\n" +
		"Explicit text or machine formats, field extraction, and redirected input or output retain noninteractive behavior.")
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
		if !InteractiveListOutput(opts) {
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
		// Reuse the existing output signal lifecycle, also used by audio workflows.
		// One owner cancels requests before the viewer joins its fetch worker.
		ctx, stop := clioutput.DownloadSignalContext(ctx)
		defer stop()
		opts.Context, opts.CallerHandlesSignals = ctx, true
		err = clioutput.StreamToStdout(func(*os.File) error {
			return ShowJSONPages(func(requestContext context.Context) (P, error) {
				return first(requestContext, client, options)
			}, maximum, opts, cursorField)
		})
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
