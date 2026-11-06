//go:build !windows

package main

import (
	"fmt"
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
					testListPresentation(t, append(args, "--browse"), "discover", 0, 1)
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

func TestMainTerminalListPrintsEveryPageAndReturns(t *testing.T) {
	for _, resource := range []string{"webhooks", "skills"} {
		t.Run(resource, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page := calls.Add(1)
				if page > 2 {
					t.Error("list fetched past the last page")
					http.Error(w, "unexpected page", http.StatusBadRequest)
					return
				}
				expectedCursor := ""
				if page == 2 {
					expectedCursor = "item_015"
				}
				if got := r.URL.Query().Get("after"); got != expectedCursor {
					t.Errorf("cursor = %q, want %q", got, expectedCursor)
				}
				var rows []string
				for i := 1; i <= 15; i++ {
					id := (int(page)-1)*15 + i
					if resource == "webhooks" {
						rows = append(rows, fmt.Sprintf(`{"object":"webhook_endpoint","id":"item_%03d","name":"hook","event_types":["batch.failed"]}`, id))
					} else {
						rows = append(rows, fmt.Sprintf(`{"object":"skill","id":"item_%03d","name":"skill","default_version":"1","latest_version":"2"}`, id))
					}
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"object":"list","data":[%s],"has_more":%t}`, strings.Join(rows, ","), page == 1)
			}))
			defer server.Close()
			screen := runResourceListObserver(t, map[string]any{
				"endpoint": server.URL, "home": t.TempDir(), "args": []string{"openai", resource, "list"},
				"mode": "complete", "action": "print-return", "expected": 0,
			})
			require.EqualValues(t, 2, calls.Load())
			require.Contains(t, screen, "NAME")
			for id := 1; id <= 30; id++ {
				require.Equal(t, 1, strings.Count(screen, fmt.Sprintf("item_%03d", id)))
			}
			require.Greater(t, strings.Index(screen, "NEXT_COMMAND"), strings.Index(screen, "item_030"))
		})
	}
}

func TestMainModelsPrintsOneHintAfterAllRows(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		var rows []string
		for i := 1; i <= 205; i++ {
			rows = append(rows, fmt.Sprintf(`{"object":"model","id":"model_%03d","owned_by":"team","created":1}`, i))
		}
		fmt.Fprintf(w, `{"object":"list","data":[%s]}`, strings.Join(rows, ","))
	}))
	defer server.Close()
	output := runResourceListObserver(t, map[string]any{
		"endpoint": server.URL, "home": t.TempDir(), "args": []string{"openai", "models", "list", "--browse=false"},
		"mode": "complete", "action": "print-return", "expected": 0,
	})
	require.EqualValues(t, 1, calls.Load())
	require.Equal(t, 1, strings.Count(output, "Details: --format json"))
	require.NotContains(t, output, "Showing 5 of 205")
	require.Greater(t, strings.Index(output, "Details: --format json"), strings.Index(output, "model_205"))
	require.Contains(t, output, "model_205")
}

func TestMainListPagerSummarizesEncodedMedia(t *testing.T) {
	const records = `[
		{"id":"ig_sample","type":"image_generation_call","status":"completed","result":"AQID"},
		{"id":"other_sample","type":"future_item","status":"completed","result":"BQQD"}
	]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","has_more":false,"data":`+records+`}`)
	}))
	defer server.Close()

	for _, route := range [][]string{
		{"openai", "responses", "input-items", "list", "--response-id", "resp_sample"},
		{"openai", "beta:responses:input-items", "list", "--response-id", "resp_sample"},
		{"openai", "conversations", "items", "list", "--conversation-id", "conv_sample"},
	} {
		t.Run(strings.Join(route[1:len(route)-2], " "), func(t *testing.T) {
			for _, screen := range []struct {
				width int
				field string
			}{{110, "RESULT"}, {30, "Result:"}} {
				runResourceListObserver(t, map[string]any{
					"args": append(slices.Clone(route), "--browse"), "home": t.TempDir(), "endpoint": server.URL,
					"mode": "complete", "action": "inspect", "expected": 0, "width": screen.width,
					"markers": []string{screen.field, "4 base64 characters", "BQQD"}, "omit": []string{"AQID"},
				})
			}
			result := runMainDispatchWithEnv(t, "", []string{"OPENAI_BASE_URL=" + server.URL + "/v1", "OPENAI_API_KEY=synthetic"},
				append(slices.Clone(route), "--format", "json")...)
			require.Equal(t, 0, result.code, result.stderr)
			require.JSONEq(t, records, result.stdout)
		})
	}
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
			io.WriteString(w, `{"object":"list","data":["batch.failed","batch.completed","response.completed","response.failed","response.incomplete","batch.cancelled","batch.expired","response.cancelled"]}`)
		} else if action == "discover" {
			// An unfamiliar record still belongs in the pager. Focus this check on
			// routing and leave resource-specific projections to their fixtures.
			var records []string
			for n := 1; n <= 8; n++ {
				records = append(records, fmt.Sprintf(`{"id":"item_synthetic_%d","future":"value"}`, n))
			}
			fmt.Fprintf(w, `{"object":"list","data":[%s],"has_more":false}`, strings.Join(records, ","))
		} else {
			io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
		}
	}))
	t.Cleanup(server.Close)
	screen := runResourceListObserver(t, map[string]any{
		"endpoint": server.URL, "home": t.TempDir(), "args": args,
		"mode": "empty", "action": action, "expected": status,
	})
	if action == "discover" {
		if calls.Load() == 0 {
			// Handwritten lists may read local state instead of calling the API.
			return
		}
		require.Contains(t, screen, "Space: browse", "the multi-screen fixture must offer browsing")
		if strings.Contains(strings.Join(args, " "), "event-types") {
			require.Contains(t, screen, "batch.failed")
			require.Contains(t, screen, "response.completed")
		} else {
			require.Contains(t, screen, "item_synthetic")
		}
	}
	require.Equal(t, int32(requests), calls.Load(), "GET requests for %v", args)
}
