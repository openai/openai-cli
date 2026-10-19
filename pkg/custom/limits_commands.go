package custom

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/commands/admin"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/output/transform"
	"github.com/urfave/cli/v3"
)

// Aggregate workflows retain their causes without presenting private source errors.
type limitsWorkflowFailure struct {
	message string
	// Only the text error presenter includes this additional recovery guidance.
	humanDetail string
	cause       error
}

func (failure *limitsWorkflowFailure) Error() string { return failure.message }
func (failure *limitsWorkflowFailure) Unwrap() error { return failure.cause }

// Register before task promotion so the canonical and task routes share behavior.
func registerLimitsCommands(root *cli.Command) {
	admin := root.Command("admin")
	if admin == nil {
		return
	}
	organization := admin.Command("organization")
	if organization == nil {
		return
	}
	organization.Commands = append(organization.Commands, newLimitsCommand(false))
	if projects := organization.Command("projects"); projects != nil {
		projects.Commands = append(projects.Commands, newLimitsCommand(true))
	}
}

func newLimitsCommand(project bool) *cli.Command {
	example := "admin limits"
	if project {
		example = "projects limits --project-id proj_example"
	}
	flags := func() []cli.Flag {
		result := []cli.Flag{&cli.Int64Flag{Name: "max-items", Value: 100, Usage: "Maximum records per list; -1 fetches all records."}}
		if project {
			result = append(result, &cli.StringFlag{Name: "project-id", Required: true, Usage: "Project to inspect or manage."})
		}
		return result
	}
	return &cli.Command{
		Name: "limits", Usage: "Inspect and manage hard spend limits and spend alerts.",
		Metadata: map[string]any{admin.CredentialsRequiredMetadata: true, "help-content": clihelp.Content{Examples: []clihelp.Example{{Description: "Open the limits workspace:", Command: example}}}},
		Description: "Open the limits workspace in an interactive terminal. Every change requires review.\n" +
			"Pipes, CI, explicit output formats, and show print a read-only overview.\n" +
			"Project workspaces also manage project rate limits. Requires an admin key.\n" +
			"Soft budgets, usage tiers, and organization rate catalogs require the Platform website.",
		Flags: flags(), Action: limitsCommandAction(project, false),
		Commands: []*cli.Command{{Name: "show", Usage: "Print a read-only limits overview.", Metadata: map[string]any{admin.CredentialsRequiredMetadata: true}, Action: limitsCommandAction(project, true)}},
	}
}

func limitsCommandAction(project, show bool) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		if cmd.Args().Present() {
			return &localcommands.LocalError{Message: "Limits does not accept positional arguments. Use --help to inspect its options."}
		}
		backend, err := newLimitsBackend(cmd, project)
		if err != nil {
			return err
		}
		root := cmd.Root()
		opts := ShowJSONOpts{Context: ctx, Format: root.String("format"), ExplicitFormat: root.IsSet("format"),
			RawOutput: root.Bool("raw-output"), Transform: root.String("transform"), Stdout: root.Writer, Stderr: root.ErrWriter}
		opts.setDefaults()
		if !show && limitsInteractive(cmd, opts) {
			return runLimitsWorkspace(ctx, cmd, backend, opts)
		}
		snapshot, loadErr := backend.Load(ctx)
		var sourceErr error
		if loadErr != nil || len(snapshot.Errors) != 0 {
			causes := []error{loadErr}
			for _, failure := range snapshot.Errors {
				causes = append(causes, failure.Err)
			}
			route := "admin limits show"
			if project {
				route = "projects limits show"
			}
			message := "The limits overview is incomplete.\nThe source_errors field identifies failed sections when output is available.\n" +
				"Overview options: " + clihelp.DisplayInvocation(root) + " help " + route + "\n" +
				"Keep the original connection, organization, and project settings when refreshing."
			causes = append(causes, &localcommands.LocalError{Message: message})
			sourceErr = &limitsWorkflowFailure{message: message, cause: errors.Join(causes...)}
		}
		value, err := snapshot.JSON()
		if err != nil {
			return errors.Join(sourceErr, ctx.Err(), err)
		}
		if resolvedOutputFormat(opts) == "text" && opts.Transform == "" && !opts.RawOutput {
			value, err = transform.ProjectLimitsOverview(ctx, value)
			if err != nil {
				return errors.Join(sourceErr, ctx.Err(), err)
			}
		}
		write := func() error {
			outputErr := ShowJSON(value, opts)
			return errors.Join(sourceErr, ctx.Err(), outputErr)
		}
		if file, ok := opts.Stdout.(*os.File); ok && file == os.Stdout {
			// Join source failures before classifying a closed stdout pipe.
			return clioutput.StreamToStdout(func(*os.File) error { return write() })
		}
		return write()
	}
}

func limitsInteractive(cmd *cli.Command, opts ShowJSONOpts) bool {
	format := strings.ToLower(opts.Format)
	input, ok := cmd.Root().Reader.(*os.File)
	return ok && term.IsTerminal(input.Fd()) && isTerminal(opts.Stdout) &&
		(format == "" || format == "auto") && !opts.ExplicitFormat && !opts.RawOutput && opts.Transform == "" &&
		!cmd.Root().Bool("quiet") && os.Getenv("CI") == "" && os.Getenv("TERM") != "dumb"
}
