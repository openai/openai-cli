//go:build !windows

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/app"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestMainListCommandsOpenPager(t *testing.T) {
	app.ConfigureCommand(commands.Command)
	count := 0
	var walk func(*cli.Command, []string)
	walk = func(parent *cli.Command, path []string) {
		for _, command := range parent.Commands {
			names := command.Names()
			list := slices.ContainsFunc(names, func(name string) bool {
				return name == "list" || name == "ls" || strings.HasPrefix(name, "list-")
			})
			for _, name := range names {
				args := append(slices.Clone(path), name)
				if len(command.Commands) != 0 {
					walk(command, args)
					continue
				}
				if !list {
					continue
				}
				count++
				t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
					for _, flag := range requestflag.GetMissingRequiredFlags(command, nil) {
						args = append(args, "--"+flag.Names()[0], "parent_synthetic")
					}
					testListPresentation(t, args, "discover", 0, 1)
				})
			}
		}
	}
	walk(commands.Command, []string{"openai"})
	require.Positive(t, count, "the configured tree must include list commands")
}

func TestMainBatchWatchRetainsTerminalStream(t *testing.T) {
	base := []string{"openai", "batches", "list"}
	t.Run("watch", func(t *testing.T) {
		testListPresentation(t, append(slices.Clone(base), "--watch", "--watch-count", "2", "--poll-interval", "1ms"), "watch", 0, 2)
	})
	t.Run("watch-only option", func(t *testing.T) {
		testListPresentation(t, append(slices.Clone(base), "--watch-count", "1"), "watch-without-flag", 1, 0)
	})
}

func testListPresentation(t *testing.T, args []string, action string, status, requests int) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("list attempted %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/webhook_event_types" {
			io.WriteString(w, `{"object":"list","data":["batch.failed","response.completed"]}`)
		} else if action == "discover" {
			// An unfamiliar record still belongs in the pager. Focus this check on
			// routing and leave resource-specific projections to their fixtures.
			io.WriteString(w, `{"object":"list","data":[{"id":"item_synthetic","future":"value"}],"has_more":false}`)
		} else {
			io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
		}
	}))
	t.Cleanup(server.Close)
	markers := []string{"item_synthetic"}
	if strings.Contains(strings.Join(args, " "), "event-types") {
		markers = []string{"batch.failed", "response.completed"}
	}
	runResourceListObserver(t, map[string]any{
		"markers": markers, "endpoint": server.URL, "home": t.TempDir(), "args": args,
		"mode": "empty", "action": action, "expected": status,
	})
	require.Equal(t, int32(requests), calls.Load(), "GET requests for %v", args)
}
