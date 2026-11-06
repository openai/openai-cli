package app

import (
	"context"
	"io"

	"github.com/urfave/cli/v3"
)

type printListKey struct{}

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
	configureListBrowsing(command, true)
}

func configureListBrowsing(command *cli.Command, defaultBrowse bool) {
	command.Flags = append(command.Flags, &cli.BoolFlag{
		Name: "browse", Value: defaultBrowse,
		Usage: "Browse the list with the keyboard in an interactive terminal. Use --browse=false to print and return.",
	})
	next := command.Action
	command.Action = func(ctx context.Context, cmd *cli.Command) error {
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
