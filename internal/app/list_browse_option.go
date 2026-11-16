package app

import (
	"context"
	"io"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/urfave/cli/v3"
)

type printListKey struct{}
type fullScreenListKey struct{}

// A command can produce a follow-up hint before its last page reaches stdout.
// Retain the hint until the complete table has been written successfully.
type printedList struct {
	hint string
	out  io.Writer
}

func printedListFrom(ctx context.Context) *printedList {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(printListKey{}).(*printedList)
	return state
}

func configureModelsBrowsing(command *cli.Command) {
	markListPager(command)
	configureListBrowsing(command)
}

func configureListBrowsing(command *cli.Command) {
	command.Flags = append(command.Flags, &cli.BoolFlag{
		Name: "browse", Value: true,
		Usage: "Browse the list with the keyboard in an interactive terminal. Use --browse=false to print and return.",
	}, &cli.BoolFlag{
		Name: "pager", Usage: "Open the full-screen list viewer. Press i to continue paging in your terminal.",
	})
	next := command.Action
	command.Action = func(ctx context.Context, cmd *cli.Command) error {
		if cmd.Bool("pager") {
			if cmd.IsSet("browse") && !cmd.Bool("browse") {
				return cli.Exit(&localcommands.LocalError{Message: "--pager and --browse=false cannot be combined"}, 2)
			}
			return next(context.WithValue(ctx, fullScreenListKey{}, true), cmd)
		}
		if cmd.Bool("browse") {
			return next(ctx, cmd)
		}
		state := &printedList{}
		if err := next(context.WithValue(ctx, printListKey{}, state), cmd); err != nil {
			return err
		}
		if state.hint != "" {
			return writeOutputHint(ShowJSONOpts{Context: ctx, Stderr: state.out}, state.hint)
		}
		return nil
	}
}
