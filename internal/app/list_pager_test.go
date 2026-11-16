package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/commands/models"
	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestListPagerAdvanceAtVisibleRecordBoundary(t *testing.T) {
	const height = 27
	const capacity = height - 2 // The heading and controls each use one row.
	for _, count := range []int{capacity - 1, capacity, capacity + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var out bytes.Buffer
			var requests atomic.Int32
			items := make([]gjson.Result, count)
			for i := range items {
				items[i] = gjson.Parse(fmt.Sprintf(`{"id":"file-%03d","object":"file","filename":"dataset.jsonl","purpose":"batch","bytes":1024,"status":"processed"}`, i+1))
			}
			last := fmt.Sprintf("file-%03d", count)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			opts := listViewerOptions(ShowJSONOpts{Context: ctx, Stdout: &out,
				Operation: "(resource) files > (method) list", OutputKind: OutputPageItem})
			model := listview.New(opts, func() (listview.Page, error) {
				if requests.Add(1) == 1 {
					return listview.Page{Items: items, More: true}, nil
				}
				return listview.Page{Items: []gjson.Result{gjson.Parse(`{"id":"file-next","object":"file","filename":"next.jsonl","purpose":"batch","bytes":1024,"status":"processed"}`)}}, nil
			}, cancel, 101, height)
			model.Update(model.Init()())
			require.EqualValues(t, 1, requests.Load())
			require.NotContains(t, model.View().Content, "Full data:")
			if count <= capacity {
				require.Contains(t, model.View().Content, last)
			} else {
				require.NotContains(t, model.View().Content, last)
			}
			_, advance := model.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
			require.NotNil(t, advance)
			require.IsType(t, tea.QuitMsg{}, advance(), "the inline renderer releases the shell first")
			fetch := model.Init()
			previousID := "file-001"
			if count > capacity {
				require.Nil(t, fetch, "the next file was already loaded")
				require.EqualValues(t, 1, requests.Load())
				require.Contains(t, model.View().Content, last)
				previousID = last
				_, fetch = model.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
			}
			require.NotNil(t, fetch)
			model.Update(fetch())
			require.EqualValues(t, 2, requests.Load())
			nextScreen := model.View().Content
			require.Contains(t, nextScreen, "file-next")
			require.Contains(t, nextScreen, "End")
			require.NotContains(t, nextScreen, last)
			require.NotContains(t, nextScreen, "file-001")
			model.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
			require.Contains(t, model.View().Content, previousID)
			require.NotContains(t, model.View().Content, "file-next")
			_, cached := model.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
			require.Nil(t, cached)
			require.Equal(t, nextScreen, model.View().Content)
			require.EqualValues(t, 2, requests.Load())
			model.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
			require.NoError(t, model.Finish(nil, nil))
			require.Empty(t, out.String())
		})
	}
}

var listPagerRoutes = []string{
	"files", "batches", "models", "projects", "admin projects",
	"admin organization projects", "admin:organization:projects",
}

func listPagerCommand(action cli.ActionFunc) *cli.Command {
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{&cli.StringFlag{Name: "format"}, &cli.StringFlag{Name: "transform"}, &cli.BoolFlag{Name: "raw-output"}},
	}
	for _, name := range []string{"files", "batches", "models", "admin:organization:projects"} {
		root.Commands = append(root.Commands, &cli.Command{Name: name, Category: "API RESOURCE", Commands: []*cli.Command{
			{Name: "list", Action: action, Flags: []cli.Flag{&requestflag.Flag[int64]{Name: "max-items"}}},
		}})
	}
	configureModelsList(root)
	localcommands.ConfigureSubgroups(root)
	localcommands.ConfigureTasks(root)
	configureRegisteredLists(root)
	return root
}

func TestListPagerCommandRoutes(t *testing.T) {
	t.Setenv("CI", "true")
	for _, route := range listPagerRoutes {
		t.Run(route, func(t *testing.T) {
			called := 0
			root := listPagerCommand(func(_ context.Context, command *cli.Command) error {
				called++
				require.True(t, command.IsSet("max-items"))
				require.Equal(t, int64(0), command.Value("max-items"))
				return nil
			})
			args := append([]string{"openai"}, strings.Fields(route)...)
			require.NoError(t, root.Run(t.Context(), append(args, "list", "--max-items", "0")))
			require.Equal(t, 1, called)
		})
	}
}

func TestLateListCommandsBrowseByDefault(t *testing.T) {
	t.Setenv("CI", "1")
	for _, tc := range []struct {
		name, alias string
	}{{"list", ""}, {"ls", ""}, {"list-changes", ""}, {"history", "list-revisions"}} {
		t.Run(tc.name, func(t *testing.T) {
			resource := &cli.Command{Name: "widgets"}
			root := &cli.Command{Name: "openai", Commands: []*cli.Command{resource}}
			configureRegisteredLists(root)
			called := false
			command := &cli.Command{Name: tc.name, Action: func(ctx context.Context, cmd *cli.Command) error {
				called = true
				require.True(t, cmd.Bool("browse"))
				require.False(t, cmd.IsSet("browse"))
				require.Nil(t, printedListFrom(ctx))
				return nil
			}}
			verb := tc.name
			if tc.alias != "" {
				command.Aliases, verb = []string{tc.alias}, tc.alias
			}
			resource.Commands = append(resource.Commands, command)
			configureRegisteredLists(root)
			require.NoError(t, root.Run(t.Context(), []string{"openai", "widgets", verb}))
			require.True(t, called)
		})
	}
}

func TestListPagerHelp(t *testing.T) {
	for _, route := range listPagerRoutes {
		t.Run(route, func(t *testing.T) {
			root := listPagerCommand(func(context.Context, *cli.Command) error { return nil })
			var help bytes.Buffer
			root.Writer = &help
			args := append([]string{"openai"}, strings.Fields(route)...)
			require.NoError(t, root.Run(t.Context(), append(args, "list", "--help")))
			require.Contains(t, help.String(), "Terminal lists start with one screen of table rows or labeled fields.")
			require.Contains(t, help.String(), "Complete results that fit return to the shell.")
			require.Contains(t, help.String(), "--pager")
			require.Contains(t, help.String(), "Press i to continue paging in your terminal.")
		})
	}
}

func TestListPagerOpensDirectlyOnRegisteredRoutes(t *testing.T) {
	for _, route := range listPagerRoutes {
		t.Run(route, func(t *testing.T) {
			called := false
			root := listPagerCommand(func(ctx context.Context, _ *cli.Command) error {
				called = true
				require.True(t, listViewerOptions(ShowJSONOpts{Context: ctx}).Pager)
				return nil
			})
			args := append([]string{"openai"}, strings.Fields(route)...)
			require.NoError(t, root.Run(t.Context(), append(args, "list", "--pager")))
			require.True(t, called)
		})
	}
}

func TestListPagerConflictingPrintFlag(t *testing.T) {
	called := false
	root := listPagerCommand(func(context.Context, *cli.Command) error {
		called = true
		return nil
	})
	root.ExitErrHandler = func(context.Context, *cli.Command, error) {}
	err := root.Run(t.Context(), []string{"openai", "files", "list", "--pager", "--browse=false"})
	require.ErrorContains(t, err, "--pager and --browse=false cannot be combined")
	require.False(t, called)
}

func TestListPagerPreservesAdmission(t *testing.T) {
	for _, mode := range []string{"text", "json", "jsonl", "pretty", "yaml", "raw", "extraction", "raw-output", "CI", "dumb", "redirected", "resource"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CI", "")
			t.Setenv("TERM", "xterm-256color")
			opts := ShowJSONOpts{Context: t.Context(), Operation: "(resource) files > (method) list", OutputKind: OutputPageItem,
				Stdout: os.Stdout}
			switch mode {
			case "text", "json", "jsonl", "pretty", "yaml", "raw":
				opts.Format = mode
			case "extraction":
				opts.Transform = "id"
			case "raw-output":
				opts.RawOutput = true
			case "CI":
				t.Setenv("CI", "true")
			case "dumb":
				t.Setenv("TERM", "dumb")
			case "redirected":
				opts.Stdout = io.Discard
			case "resource":
				opts.OutputKind = OutputResponse
			}
			require.False(t, InteractiveListOutput(opts))
			opts.Operation = "(resource) models > (method) list"
			source, iter := staticModelIterator(t, `[{"id":"model-a","object":"model"}]`, -1, nil)
			handled, err := showModelsListViewer(source, iter, opts)
			require.NoError(t, err)
			require.False(t, handled)
			require.Zero(t, source.Index(), "bypassed presentation must not consume records")
		})
	}
}

func TestModelsListPagerIncludesShortAndEmptyResults(t *testing.T) {
	for _, records := range []string{`[]`, `[{"id":"model-a","object":"model","owned_by":"synthetic"}]`} {
		t.Run(records, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "output")
			require.NoError(t, err)
			defer file.Close()
			_, iter := staticModelIterator(t, records, -1, nil)
			opts := listViewerOptions(ShowJSONOpts{Context: t.Context(), Stdout: file,
				Operation: "(resource) models > (method) list", OutputKind: OutputPageItem})
			// A regular file cannot start the terminal runtime. Both successful
			// responses must reach that runtime instead of printing and returning.
			require.Error(t, models.WriteList(iter, opts, 80, 24, true))
			content, err := os.ReadFile(file.Name())
			require.NoError(t, err)
			require.Empty(t, content)
		})
	}
}
