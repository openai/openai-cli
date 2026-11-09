//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/app"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Exercise normal command assembly with a resource unknown to the CLI.
func TestNewListCommandProcess(t *testing.T) {
	if os.Getenv("OPENAI_CLI_MAIN_DISPATCH_PROCESS") != "1" {
		return
	}
	show := func(ctx context.Context, cmd *cli.Command) error {
		if cmd.IsSet("max-items") && cmd.Int("max-items") == 0 {
			return nil
		}
		if cmd.Bool("watch") {
			_, err := fmt.Fprintln(os.Stdout, "Widget updated")
			return err
		}
		var rows []string
		for n := 1; n <= 30; n++ {
			rows = append(rows, fmt.Sprintf(`{"id":"widget_%02d","calibration":42}`, n))
		}
		return app.ShowJSON(gjson.Parse("["+strings.Join(rows, ",")+"]"), app.ShowJSONOpts{Context: ctx})
	}
	resource := &cli.Command{
		Name: "widgets", Category: "API RESOURCE", Commands: []*cli.Command{
			{Name: "list", Action: show, Flags: []cli.Flag{&cli.BoolFlag{Name: "watch"}, &cli.IntFlag{Name: "max-items"}}},
			{Name: "history", Aliases: []string{"list-revisions"}, Action: show},
		},
	}
	// Use the same SDK autopager, flag binding, and output path as generated actions.
	sdkList := func(ctx context.Context, cmd *cli.Command) error {
		client := openai.NewClient(app.GetDefaultRequestOptions(cmd)...)
		if cmd.Name == "read-limit" {
			catalog := cmd.Root().Command("widgets").Command("list-unpaged")
			lookup, err := app.FlagOptions(catalog, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, app.EmptyBody, true)
			if err != nil {
				return err
			}
			if _, err := client.Webhooks.EventTypes.List(ctx, lookup...); err != nil {
				return err
			}
		}
		options, err := app.FlagOptions(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, app.EmptyBody, false)
		if err != nil {
			return err
		}
		rows := client.Containers.ListAutoPaging(ctx, openai.ContainerListParams{}, options...)
		maximum := int64(-1)
		if cmd.IsSet("max-items") {
			maximum = cmd.Value("max-items").(int64)
		}
		return app.ShowJSONIterator(rows, maximum, app.ShowJSONOpts{Context: ctx, OutputKind: app.OutputPageItem,
			Format: cmd.Root().String("format"), ExplicitFormat: cmd.Root().IsSet("format")})
	}
	for _, spec := range []struct {
		name   string
		size   cli.Flag
		paging cli.Flag
	}{
		{"limit", &requestflag.Flag[int64]{Name: "limit", QueryPath: "limit"}, &requestflag.Flag[int64]{Name: "max-items"}},
		{"nullable", &requestflag.Flag[*int64]{Name: "limit", QueryPath: "limit"}, &requestflag.Flag[int64]{Name: "max-items"}},
		{"page-size", &requestflag.Flag[int64]{Name: "page-size", QueryPath: "page_size", Aliases: []string{"n"}}, &requestflag.Flag[string]{Name: "after", QueryPath: "after"}},
		{"per-page", &requestflag.Flag[int64]{Name: "per-page", QueryPath: "per_page"}, &requestflag.Flag[string]{Name: "after", QueryPath: "after"}},
		{"local-limit", &cli.Int64Flag{Name: "limit"}, &requestflag.Flag[int64]{Name: "max-items"}},
		{"unpaged", &requestflag.Flag[int64]{Name: "limit", QueryPath: "limit"}, nil},
	} {
		flags := []cli.Flag{spec.size}
		if spec.paging != nil {
			flags = append(flags, spec.paging)
		}
		resource.Commands = append(resource.Commands, &cli.Command{
			Name: "read-" + spec.name, Aliases: []string{"list-" + spec.name}, Action: sdkList, Flags: flags,
		})
	}
	nested := &cli.Command{
		Name: "widgets:revisions", Category: "API RESOURCE", Commands: []*cli.Command{{
			Name: "history", Aliases: []string{"ls", "list-snapshots"}, Action: sdkList,
			Flags: []cli.Flag{
				&requestflag.Flag[int64]{Name: "limit", QueryPath: "limit"},
				&requestflag.Flag[string]{Name: "after", QueryPath: "after"},
			},
		}},
	}
	commands.Command = &cli.Command{
		Name: "openai", Flags: []cli.Flag{&cli.StringFlag{Name: "format", Value: "auto"}}, ErrWriter: &commands.CommandErrorBuffer, Commands: []*cli.Command{resource, nested},
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
				"mode": "complete", "expected": 0, "markers": []string{"CALIBRATION", "widget_01", "42", "Space: browse"},
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
	t.Run("zero items", func(t *testing.T) {
		runResourceListObserver(t, map[string]any{
			"process": "TestNewListCommandProcess", "args": []string{"openai", "widgets", "list", "--max-items", "0"},
			"home": t.TempDir(), "endpoint": "http://127.0.0.1:1",
			"action": "live", "mode": "empty", "expected": 0,
		})
	})
}

func TestNewNestedListBrowsesByDefault(t *testing.T) {
	for _, route := range []string{"widgets revisions history", "widgets revisions ls", "widgets:revisions list-snapshots"} {
		t.Run(route, func(t *testing.T) {
			server, calls := listPageSizeFixture(t, "container", 17, "6")
			runResourceListObserver(t, map[string]any{
				"process": "TestNewListCommandProcess", "args": strings.Fields("openai " + route),
				"endpoint": server.URL, "home": t.TempDir(), "width": 110, "height": 8, "mode": "normal",
				"action": "list-range", "rowPrefix": "container_",
				"spans": []string{"Revisions 1–6", "Revisions 7–12", "Revisions 13–17 of 17", "Revisions 7–12 of 17"},
			})
			require.EqualValues(t, 3, calls.Load())
		})
	}
}
