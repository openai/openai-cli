//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/app"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Exercise normal command assembly with a resource unknown to the CLI.
func TestNewListCommandProcess(t *testing.T) {
	if os.Getenv("OPENAI_CLI_MAIN_DISPATCH_PROCESS") != "1" {
		return
	}
	show := func(ctx context.Context, cmd *cli.Command) error {
		if cmd.Bool("watch") {
			_, err := fmt.Fprintln(os.Stdout, "Widget updated")
			return err
		}
		return app.ShowJSON(gjson.Parse(`[{"id":"widget_one","calibration":42}]`), app.ShowJSONOpts{Context: ctx})
	}
	resource := &cli.Command{
		Name: "widgets", Category: "API RESOURCE", Commands: []*cli.Command{
			{Name: "list", Action: show, Flags: []cli.Flag{&cli.BoolFlag{Name: "watch"}}},
			{Name: "history", Aliases: []string{"list-revisions"}, Action: show},
		},
	}
	commands.Command = &cli.Command{
		Name: "openai", ErrWriter: &commands.CommandErrorBuffer, Commands: []*cli.Command{resource},
	}
	os.Args = os.Args[slices.Index(os.Args, "--")+1:]
	main()
	os.Exit(0)
}

func TestNewListCommandsInheritPager(t *testing.T) {
	for _, verb := range []string{"list", "list-revisions"} {
		t.Run(verb, func(t *testing.T) {
			runResourceListObserver(t, map[string]any{
				"process": "TestNewListCommandProcess", "args": []string{"openai", "widgets", verb},
				"home": t.TempDir(), "endpoint": "http://127.0.0.1:1", "action": "discover",
				"mode": "complete", "expected": 0, "markers": []string{"CALIBRATION", "widget_one", "42"},
			})
		})
	}
	t.Run("explicit watch", func(t *testing.T) {
		runResourceListObserver(t, map[string]any{
			"process": "TestNewListCommandProcess", "args": []string{"openai", "widgets", "list", "--watch"},
			"home": t.TempDir(), "endpoint": "http://127.0.0.1:1", "action": "live",
			"mode": "complete", "expected": 0, "markers": []string{"Widget updated"},
		})
	})
}
