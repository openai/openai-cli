package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// Exercise the three SDK cursor types used by list commands with synthetic responses.
func registeredSDKPages(ctx context.Context, resource string, maximum int64, respond option.Middleware) iter.Seq2[listview.Page, error] {
	client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0),
		option.WithMiddleware(registeredListRequest, respond))
	return new(registeredList).pages(ctx, func(ctx context.Context) error {
		opts := ShowJSONOpts{Context: ctx, OutputKind: OutputPageItem}
		switch resource {
		case "projects":
			return ShowJSONIterator(client.Admin.Organization.Projects.ListAutoPaging(ctx, openai.AdminOrganizationProjectListParams{}), maximum, opts)
		case "groups":
			return ShowJSONIterator(client.Admin.Organization.Groups.ListAutoPaging(ctx, openai.AdminOrganizationGroupListParams{}), maximum, opts)
		default:
			return ShowJSONIterator(client.Files.ListAutoPaging(ctx, openai.FileListParams{Limit: openai.Int(2)}), maximum, opts)
		}
	})
}

func listResponse(request *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Request: request,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestRegisteredListWaitsAndPreservesPageSize(t *testing.T) {
	for _, limit := range []int64{-1, 0, 1, 2, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var cursors []string
			pages := registeredSDKPages(t.Context(), "files", limit, func(request *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
				q := request.URL.Query()
				cursors = append(cursors, q.Get("after"))
				if q.Get("limit") != "2" {
					return nil, fmt.Errorf("unexpected page size: %s", q.Get("limit"))
				}
				if len(cursors) == 1 {
					return listResponse(request, `{"data":[{"id":"a"},{"id":"b"}],"has_more":true}`), nil
				}
				return listResponse(request, `{"data":[{"id":"c"}],"has_more":false}`), nil
			})
			next, stop := iter.Pull2(pages)
			defer stop()
			require.Empty(t, cursors)
			page, err, ok := next()
			require.True(t, ok)
			require.NoError(t, err)
			count := 2
			if limit >= 0 && limit < 2 {
				count = int(limit)
			}
			require.Len(t, page.Items, count)
			require.Equal(t, []string{""}, cursors)
			wantMore := limit == -1 || limit > 2
			require.Equal(t, wantMore, page.More)
			if wantMore {
				page, err, ok = next()
				require.True(t, ok)
				require.NoError(t, err)
				require.Len(t, page.Items, 1)
				require.Equal(t, "c", page.Items[0].Get("id").Str)
				require.False(t, page.More)
				require.Equal(t, []string{"", "b"}, cursors)
			}
			_, _, ok = next()
			require.False(t, ok)
		})
	}
}

func TestDecoratedListIteratorWaitsBeforeTheNextRequest(t *testing.T) {
	for _, tc := range []struct {
		name        string
		bound, more bool
	}{
		{"single page", false, false}, {"two pages", false, true},
		{"bound single page", true, false}, {"bound two pages", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) { checkDecoratedFilesPages(t, tc.bound, tc.more) })
	}
}

func checkDecoratedFilesPages(t *testing.T, bound, more bool) {
	t.Helper()
	var cursors []string
	respond := func(r *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
		cursors = append(cursors, r.URL.Query().Get("after"))
		switch len(cursors) {
		case 1:
			var rows []string
			for i := 1; i <= 12; i++ {
				rows = append(rows, fmt.Sprintf(`{"id":"row_%d"}`, i))
			}
			return listResponse(r, fmt.Sprintf(`{"data":[%s],"has_more":%t}`, strings.Join(rows, ","), more)), nil
		case 2:
			return listResponse(r, `{"data":[{"id":"row_13"}],"has_more":false}`), nil
		default:
			return nil, errors.New("request after the final Files page")
		}
	}
	client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0), option.WithMiddleware(registeredListRequest, respond))
	list := &registeredList{pageSize: 6}
	pages := list.pages(t.Context(), func(ctx context.Context) error {
		prepare := func(ctx context.Context) *pagination.CursorPageAutoPager[openai.FileObject] {
			return client.Files.ListAutoPaging(ctx, openai.FileListParams{Limit: openai.Int(12)})
		}
		var files jsonview.Iterator[openai.FileObject]
		if bound {
			files = PrepareListIterator(ctx, prepare)
		} else {
			files = prepare(ctx)
		}
		decorated := struct {
			jsonview.Iterator[openai.FileObject]
		}{files}
		return ShowJSONIterator(decorated, -1, ShowJSONOpts{Context: ctx, OutputKind: OutputPageItem})
	})
	displayed, row := 0, 0
	for page, err := range pages {
		displayed++
		require.NoError(t, err)
		require.Len(t, cursors, displayed, "another request started before the current page was displayed")
		size := 1
		if displayed == 1 {
			size = 12 // Inline output needs every loaded row, including rows below the screen.
		}
		require.Len(t, page.Items, size)
		for _, item := range page.Items {
			row++
			require.Equal(t, fmt.Sprintf("row_%d", row), item.Get("id").Str)
		}
		require.Equal(t, displayed == 1 && more, page.More)
	}
	wantCursors := []string{""}
	if more {
		wantCursors = append(wantCursors, "row_12")
	}
	require.Equal(t, wantCursors, cursors)
	require.Equal(t, len(wantCursors), displayed)
}

func TestForwardedFilesIteratorValidatesPagination(t *testing.T) {
	const (
		empty = `{"data":[],"has_more":true}`
		row   = `{"data":[{"id":"a"}],"has_more":true}`
	)
	for _, tc := range []struct {
		name    string
		pages   []string
		limit   int64
		rows    string
		invalid bool
	}{
		{"empty incomplete", []string{empty}, -1, "", true},
		{"partial", []string{row, empty}, -1, "{\"id\":\"a\"}\n", true},
		{"missing row ID", []string{`{"data":[{"filename":"partial"}],"has_more":true}`}, -1, "{\"filename\":\"partial\"}\n", true},
		{"cursor cycle", []string{row, row}, -1, "{\"id\":\"a\"}\n{\"id\":\"a\"}\n", true},
		{"exact cap", []string{row}, 1, "{\"id\":\"a\"}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cursors []string
			respond := func(r *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
				cursors = append(cursors, r.URL.Query().Get("after"))
				if len(cursors) > len(tc.pages) {
					return nil, errors.New("unexpected extra request")
				}
				return listResponse(r, tc.pages[len(cursors)-1]), nil
			}
			ctx := context.WithValue(t.Context(), registeredListKey{}, new(registeredList))
			client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0),
				option.WithMiddleware(registeredListRequest, respond))
			files := client.Files.ListAutoPaging(ctx, openai.FileListParams{})
			forwarded := struct {
				jsonview.Iterator[openai.FileObject]
			}{files}
			var out strings.Builder
			err := ShowJSONIterator(forwarded, tc.limit, ShowJSONOpts{Context: ctx, Format: "jsonl", Stdout: &out})
			if tc.invalid {
				require.ErrorAs(t, err, new(*listview.PaginationError))
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.rows, out.String())
			require.Equal(t, []string{"", "a"}[:len(tc.pages)], cursors)
		})
	}
}

func TestForwardedLocalIteratorIgnoresUnrelatedHTTPPage(t *testing.T) {
	const body = `{"data":[],"has_more":true}`
	ctx := context.WithValue(t.Context(), registeredListKey{}, new(registeredList))
	client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMiddleware(registeredListRequest,
		func(r *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
			return listResponse(r, body), nil
		}))
	var raw []byte
	require.NoError(t, client.Get(ctx, "files", nil, &raw))
	require.Equal(t, body, string(raw))

	// Even the item type is identical to a Files pager. The wrapped source is local.
	local := new(sliceIterator[openai.FileObject])
	inner := struct {
		jsonview.Iterator[openai.FileObject]
	}{local}
	outer := &struct {
		jsonview.Iterator[openai.FileObject]
	}{inner}
	var out strings.Builder
	opts := ShowJSONOpts{Context: ctx, Format: "jsonl", Stdout: &out}
	require.NoError(t, ShowJSONIterator(outer, -1, opts))
	require.Empty(t, out.String())

	outer.Iterator = outer
	require.ErrorContains(t, ShowJSONIterator(outer, -1, opts), "cyclic")
}

// Select the second File of each pair and project it to a string.
type projectedFilePairs struct {
	jsonview.Iterator[openai.FileObject]
}

func (p projectedFilePairs) Next() bool      { return p.Iterator.Next() && p.Iterator.Next() }
func (p projectedFilePairs) Current() string { return "selected " + p.Iterator.Current().ID }
func (p projectedFilePairs) Err() error {
	if err := p.Iterator.Err(); err != nil {
		return fmt.Errorf("file projection: %w", err)
	}
	return nil
}

func TestNamedDecoratorRetainsProjectionAndPaginationError(t *testing.T) {
	ctx := context.WithValue(t.Context(), registeredListKey{}, new(registeredList))
	var cursors []string
	respond := func(r *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
		cursor := r.URL.Query().Get("after")
		cursors = append(cursors, cursor)
		if cursor == "" {
			return listResponse(r, `{"data":[{"id":"a"},{"id":"b"}],"has_more":true}`), nil
		}
		return listResponse(r, `{"data":[],"has_more":true}`), nil
	}
	client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0),
		option.WithMiddleware(registeredListRequest, respond))
	files := PrepareListIterator(ctx, func(ctx context.Context) *pagination.CursorPageAutoPager[openai.FileObject] {
		return client.Files.ListAutoPaging(ctx, openai.FileListParams{})
	})
	var out strings.Builder
	err := ShowJSONIterator(projectedFilePairs{files}, -1, ShowJSONOpts{Context: ctx, Format: "jsonl", Stdout: &out})
	require.ErrorAs(t, err, new(*listview.PaginationError))
	require.ErrorContains(t, err, "file projection")
	require.Equal(t, "\"selected b\"\n", out.String())
	require.Equal(t, []string{"", "b"}, cursors)
}

func TestRegisteredListZeroRetainsRequestErrors(t *testing.T) {
	failure := errors.New("synthetic first request failure")
	calls := 0
	for page, err := range registeredSDKPages(t.Context(), "files", 0, func(*http.Request, option.MiddlewareNext) (*http.Response, error) {
		calls++
		return nil, failure
	}) {
		require.ErrorIs(t, err, failure)
		require.Empty(t, page.Items)
	}
	require.Equal(t, 1, calls)
}

func TestRegisteredListStopsEmptyAndStalledCursors(t *testing.T) {
	for _, tc := range []struct {
		name, resource, first, second string
		cursors                       []string
		wantError                     bool
	}{
		{"empty", "files", `{"data":[],"has_more":true}`, "", []string{""}, true},
		{"last", "files", `{"data":[{"id":"a"}],"has_more":false}`, "", []string{""}, false},
		{"missing", "files", `{"data":[{}],"has_more":true}`, "", []string{""}, true},
		{"repeated", "files", `{"data":[{"id":"a"}],"has_more":true}`, `{"data":[{"id":"a"}],"has_more":true}`, []string{"", "a"}, true},
		{"last_id", "projects", `{"data":[{"id":"a"}],"has_more":true,"last_id":"x"}`, `{"data":[{"id":"b"}],"has_more":true,"last_id":"x"}`, []string{"", "x"}, true},
		{"next", "groups", `{"data":[{"id":"a"}],"has_more":true,"next":"x"}`, `{"data":[{"id":"b"}],"has_more":false}`, []string{"", "x"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cursors []string
			var result error
			pages := registeredSDKPages(t.Context(), tc.resource, -1, func(request *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
				cursors = append(cursors, request.URL.Query().Get("after"))
				switch len(cursors) {
				case 1:
					return listResponse(request, tc.first), nil
				case 2:
					return listResponse(request, tc.second), nil
				default:
					return nil, fmt.Errorf("unexpected request: %s", request.URL)
				}
			})
			for _, err := range pages {
				if err != nil {
					result = err
					break
				}
			}
			require.Equal(t, tc.cursors, cursors)
			if tc.wantError {
				var stalled *listview.PaginationError
				require.ErrorAs(t, result, &stalled)
			} else {
				require.NoError(t, result)
			}
		})
	}
}

func TestRegisteredListQuitPreventsNextRequest(t *testing.T) {
	calls := 0
	pages := registeredSDKPages(t.Context(), "files", -1, func(request *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
		calls++
		return listResponse(request, `{"data":[{"id":"a"}],"has_more":true}`), nil
	})
	for page, err := range pages {
		require.NoError(t, err)
		require.True(t, page.More)
		break
	}
	require.Equal(t, 1, calls)
}

func TestRegisteredListCancellationPreventsRequests(t *testing.T) {
	for _, when := range []string{"before_first", "before_next"} {
		t.Run(when, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			pages := registeredSDKPages(ctx, "files", -1, func(request *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
				calls++
				return listResponse(request, `{"data":[{"id":"a"}],"has_more":true}`), nil
			})
			next, stop := iter.Pull2(pages)
			defer stop()
			wantCalls := 0
			if when == "before_next" {
				page, err, ok := next()
				require.True(t, ok)
				require.NoError(t, err)
				require.True(t, page.More)
				wantCalls = 1
			}
			cancel()
			_, err, ok := next()
			require.True(t, ok)
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, wantCalls, calls)
		})
	}
}

func TestListCursorChecksAllowSDKRetries(t *testing.T) {
	for _, retryStatus := range []int{http.StatusServiceUnavailable, http.StatusOK} {
		t.Run(http.StatusText(retryStatus), func(t *testing.T) {
			var cursors []string
			respond := func(request *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
				cursors = append(cursors, request.URL.Query().Get("after"))
				switch len(cursors) {
				case 1:
					return listResponse(request, `{"data":[{"id":"one"}],"has_more":true}`), nil
				case 2:
					// The SDK can also retry a successful status when requested.
					res := listResponse(request, `{"data":[{"id":"discarded"}],"has_more":false}`)
					res.StatusCode = retryStatus
					res.Header.Set("X-Should-Retry", "true")
					res.Header.Set("Retry-After-Ms", "1")
					return res, nil
				case 3:
					return listResponse(request, `{"data":[{"id":"two"}],"has_more":false}`), nil
				default:
					t.Fatalf("unexpected request %d", len(cursors))
					return nil, nil
				}
			}
			client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(1),
				option.WithMiddleware(registeredListRequest, respond))
			ctx := context.WithValue(t.Context(), registeredListKey{}, new(registeredList))
			var out strings.Builder
			err := ShowJSONIterator(client.Files.ListAutoPaging(ctx, openai.FileListParams{}), -1,
				ShowJSONOpts{Context: ctx, OutputKind: OutputPageItem, Format: "json", Stdout: &out})
			require.NoError(t, err)
			require.JSONEq(t, `[{"id":"one"},{"id":"two"}]`, out.String())
			require.Equal(t, []string{"", "one", "one"}, cursors)
		})
	}
}

func TestRegisteredListWithoutLimitFlagsStopsTokenCycle(t *testing.T) {
	t.Setenv("CI", "1")
	var queries []string
	next := []string{"opaque/1 +", "opaque/2 +", "opaque/1 +"}
	respond := func(request *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
		queries = append(queries, request.URL.Query().Get("page"))
		if len(queries) > len(next) {
			t.Fatalf("attempted repeated page %q", queries[len(queries)-1])
		}
		body := fmt.Sprintf(`{"data":[{"id":"row-%d"}],"has_more":true,"next":%q}`, len(queries), next[len(queries)-1])
		return listResponse(request, body), nil
	}
	var out strings.Builder
	// A new list action can use an SDK iterator without declaring either limit flag.
	command := &cli.Command{Name: "list", Action: func(ctx context.Context, cmd *cli.Command) error {
		options := append(GetDefaultRequestOptions(cmd), option.WithAPIKey("synthetic"),
			option.WithMaxRetries(0), option.WithMiddleware(respond))
		client := openai.NewClient(options...)
		pages := client.Beta.Agents.Environments.Files.ListAutoPaging(ctx, "env_fake", openai.BetaAgentEnvironmentFileListParams{})
		return ShowJSONIterator(pages, -1, ShowJSONOpts{Context: ctx, Format: "jsonl", Stdout: &out})
	}}
	root := &cli.Command{Name: "test", Commands: []*cli.Command{command}}
	configureRegisteredLists(root)
	err := root.Run(t.Context(), []string{"test", "list"})
	require.ErrorAs(t, err, new(*listview.PaginationError))
	require.Equal(t, []string{"", "opaque/1 +", "opaque/2 +"}, queries)
	require.Contains(t, out.String(), "row-3")
}

func TestListCursorHistoryEndsWithItsIterator(t *testing.T) {
	for _, firstLimit := range []int64{-1, 1} {
		t.Run(fmt.Sprint(firstLimit), func(t *testing.T) {
			calls := 0
			respond := func(request *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
				calls++
				// The first output can finish naturally or reach its item limit.
				// In both cases the next list starts at the same URL and cursor.
				more := calls == 1 && firstLimit == 1
				return listResponse(request, fmt.Sprintf(`{"data":[{"id":"same"}],"has_more":%t}`, more)), nil
			}
			ctx := context.WithValue(t.Context(), registeredListKey{}, new(registeredList))
			client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0),
				option.WithMiddleware(registeredListRequest, respond))
			var out strings.Builder
			for _, maximum := range []int64{firstLimit, -1} {
				source := client.Files.ListAutoPaging(ctx, openai.FileListParams{})
				err := ShowJSONIterator(source, maximum, ShowJSONOpts{Context: ctx, Format: "jsonl", Stdout: &out})
				require.NoError(t, err)
			}
			require.Equal(t, 2, calls)
			require.Equal(t, 2, strings.Count(out.String(), "same"))
		})
	}
}

func TestSeparatelyBoundPreparedListsCanBeConsumedInReverse(t *testing.T) {
	owner, other := &cli.Command{Name: "containers"}, &cli.Command{Name: "skills"}
	ctx := context.WithValue(t.Context(), registeredListKey{}, &registeredList{command: owner})
	client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithBaseURL("http://127.0.0.1:1/v1/"),
		option.WithMaxRetries(0), option.WithMiddleware(registeredListRequest))
	var requests []string
	respond := func(request *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
		path := request.URL.RequestURI()
		requests = append(requests, path)
		switch path {
		case "/v1/containers":
			return listResponse(request, `{"data":[{"id":"container_a"}],"has_more":true}`), nil
		case "/v1/containers?after=container_a":
			return listResponse(request, `{"data":[{"id":"container_b"}],"has_more":false}`), nil
		case "/v1/skills":
			return listResponse(request, `{"data":[{"id":"skill_other"}],"has_more":false}`), nil
		default:
			return nil, fmt.Errorf("unexpected synthetic request: %s", path)
		}
	}
	containers := client.Containers.ListAutoPaging(ctx, openai.ContainerListParams{}, option.WithMiddleware(boundListRequest(owner), respond))
	skills := client.Skills.ListAutoPaging(ctx, openai.SkillListParams{}, option.WithMiddleware(boundListRequest(other), respond))
	var out strings.Builder
	opts := ShowJSONOpts{Context: ctx, Format: "jsonl", Stdout: &out}
	require.NoError(t, ShowJSONIterator(skills, -1, opts))
	require.NoError(t, ShowJSONIterator(containers, -1, opts))
	require.Equal(t, []string{`{"id":"skill_other"}`, `{"id":"container_a"}`, `{"id":"container_b"}`}, strings.Fields(out.String()))
	require.Equal(t, []string{"/v1/containers", "/v1/skills", "/v1/containers?after=container_a"}, requests)
}

func TestPreparedListReportsOnlyItsOwnContinuation(t *testing.T) {
	for _, page := range []struct{ name, path, data string }{
		{"empty row ID", "files", "[]"},
		{"empty opaque", "organization/groups", "[]"},
		{"same row, missing opaque cursor", "organization/groups", `[{"id":"same"}]`},
	} {
		for _, incomplete := range []int{0, 1} {
			for _, tc := range []struct {
				name    string
				read    []int
				maximum int64
			}{
				{"in order", []int{0, 1}, -1}, {"reversed", []int{1, 0}, -1},
				{"first unused", []int{1}, -1}, {"second unused", []int{0}, -1},
				{"capped", []int{1, 0}, 1},
			} {
				t.Run(fmt.Sprintf("%s/incomplete-%d/%s", page.name, incomplete, tc.name), func(t *testing.T) {
					var requests []string
					respond := func(r *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
						requests = append(requests, r.URL.RequestURI())
						if len(requests) > 2 {
							return nil, errors.New("a page without a cursor triggered another request")
						}
						body := fmt.Sprintf(`{"data":%s,"has_more":%t}`, page.data, len(requests)-1 == incomplete)
						return listResponse(r, body), nil
					}
					ctx := context.WithValue(t.Context(), registeredListKey{}, new(registeredList))
					client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0),
						option.WithBaseURL("http://127.0.0.1:1/v1/"),
						option.WithMiddleware(registeredListRequest, respond))
					var output strings.Builder
					opts := ShowJSONOpts{Context: ctx, Format: "jsonl", Stdout: &output}
					var show [2]func() error
					for i := range show {
						if page.path == "files" {
							source := PrepareListIterator(ctx, func(ctx context.Context) jsonview.Iterator[openai.FileObject] {
								return client.Files.ListAutoPaging(ctx, openai.FileListParams{})
							})
							show[i] = func() error { return ShowJSONIterator(source, tc.maximum, opts) }
						} else {
							source := PrepareListIterator(ctx, func(ctx context.Context) jsonview.Iterator[openai.Group] {
								return client.Admin.Organization.Groups.ListAutoPaging(ctx, openai.AdminOrganizationGroupListParams{})
							})
							show[i] = func() error { return ShowJSONIterator(source, tc.maximum, opts) }
						}
					}
					// Both lists use the identical URL. Only their SDK iterators distinguish them.
					wantRequests := []string{"/v1/" + page.path, "/v1/" + page.path}
					require.Equal(t, wantRequests, requests)
					containsRow := page.data != "[]"
					for _, i := range tc.read {
						err := show[i]()
						if i == incomplete && !(containsRow && tc.maximum == 1) {
							require.ErrorAs(t, err, new(*listview.PaginationError), "source %d", i)
						} else {
							require.NoError(t, err, "source %d", i)
						}
					}
					wantRows := ""
					if containsRow {
						wantRows = strings.Repeat("{\"id\":\"same\"}\n", len(tc.read))
					}
					require.Equal(t, wantRows, output.String())
					require.Equal(t, wantRequests, requests)
				})
			}
		}
	}
}

func TestUnboundPreparedListRequiresOwnershipAtAmbiguousEOF(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		more       bool
		maximum    int64
		ambiguous  bool
	}{
		{"empty incomplete", "[]", true, -1, true},
		{"identical opaque rows", `[{"id":"same"}]`, true, -1, true},
		{"exact row cap", `[{"id":"same"}]`, true, 1, false},
		{"both empty and complete", "[]", false, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			respond := func(r *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
				calls++
				return listResponse(r, fmt.Sprintf(`{"data":%s,"has_more":%t}`, tc.data, tc.more && calls == 1)), nil
			}
			ctx := context.WithValue(t.Context(), registeredListKey{}, new(registeredList))
			client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0), option.WithMiddleware(registeredListRequest, respond))
			first := client.Admin.Organization.Groups.ListAutoPaging(ctx, openai.AdminOrganizationGroupListParams{})
			second := client.Admin.Organization.Groups.ListAutoPaging(ctx, openai.AdminOrganizationGroupListParams{})
			var output strings.Builder
			err := ShowJSONIterator(first, tc.maximum, ShowJSONOpts{Context: ctx, Format: "jsonl", Stdout: &output})
			if tc.ambiguous {
				require.ErrorContains(t, err, "PrepareListIterator")
				require.False(t, errors.As(err, new(*listview.PaginationError)), "unbound sources cannot identify which response failed")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 2, calls)
			require.Equal(t, tc.data != "[]", strings.Contains(output.String(), `"id":"same"`))
			opts := ShowJSONOpts{Context: ctx, Format: "jsonl", Stdout: io.Discard}
			if err := ShowJSONIterator(second, tc.maximum, opts); err != nil {
				require.True(t, tc.ambiguous)
				require.ErrorContains(t, err, "PrepareListIterator")
			}
			// Both previous readers have finished. The next first page has one owner.
			third := client.Admin.Organization.Groups.ListAutoPaging(ctx, openai.AdminOrganizationGroupListParams{})
			require.NoError(t, ShowJSONIterator(third, -1, opts))
			require.Equal(t, 3, calls)
		})
	}
}

func TestConcurrentPreparedListsWaitForTerminalAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	var attempts, calls atomic.Int32
	var admitted, premature atomic.Bool
	bothPrepared, bothDispatched := make(chan struct{}), make(chan struct{})
	firstDispatch := make(chan struct{})
	enter := func(r *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if attempts.Add(1) == 2 {
			close(bothPrepared)
		}
		select {
		case <-bothPrepared:
			return next(r)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	respond := func(r *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
		if !admitted.Load() {
			premature.Store(true)
		}
		switch calls.Add(1) {
		case 1:
			close(firstDispatch)
		case 2:
			close(bothDispatched)
		}
		select {
		case <-bothDispatched:
			body := fmt.Sprintf(`{"data":[{"id":%q}],"has_more":false}`, r.URL.Query().Get("fixture"))
			return listResponse(r, body), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0), option.WithMiddleware(enter, registeredListRequest, respond))
	list := &registeredList{preparing: true}
	next, stop := iter.Pull2(list.pages(ctx, func(ctx context.Context) error {
		var sources [2]jsonview.Iterator[openai.FileObject]
		var wg sync.WaitGroup
		for i, id := range []string{"shown", "unused"} {
			wg.Go(func() {
				sources[i] = PrepareListIterator(ctx, func(ctx context.Context) *pagination.CursorPageAutoPager[openai.FileObject] {
					return client.Files.ListAutoPaging(ctx, openai.FileListParams{}, option.WithQuery("fixture", id))
				})
			})
		}
		wg.Wait()
		return ShowJSONIterator(sources[0], -1, ShowJSONOpts{Context: ctx})
	}))
	defer stop()
	defer cancel()
	page, err, ok := next()
	require.True(t, ok)
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.EqualValues(t, 2, attempts.Load())
	// Both constructors have already reached the observer. Keep the viewer
	// paused while the other constructor tries to dispatch its first request.
	select {
	case <-firstDispatch:
		t.Fatal("a list request reached the transport before terminal admission")
	case <-time.After(25 * time.Millisecond):
	}
	admitted.Store(true)
	page, err, ok = next()
	require.True(t, ok)
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load())
	require.False(t, premature.Load())
	require.Len(t, page.Items, 1)
	require.Equal(t, "shown", page.Items[0].Get("id").Str)
	_, _, ok = next()
	require.False(t, ok)
}

func TestPreparedListReadersKeepConcurrentContinuationsSeparate(t *testing.T) {
	ctx := context.WithValue(t.Context(), registeredListKey{}, new(registeredList))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var first, next atomic.Int32
	both := make(chan struct{})
	respond := func(r *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
		after := r.URL.Query().Get("after")
		if after == "" {
			body := fmt.Sprintf(`{"data":[{"id":"file_%d"}],"has_more":true}`, first.Add(1))
			return listResponse(r, body), nil
		}
		if next.Add(1) == 2 {
			close(both)
		}
		select {
		case <-both:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		switch after {
		case "file_1":
			return listResponse(r, `{"data":[],"has_more":true}`), nil
		case "file_2":
			return listResponse(r, `{"data":[{"id":"file_final"}],"has_more":false}`), nil
		default:
			return nil, fmt.Errorf("unexpected cursor %q", after)
		}
	}
	client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0), option.WithMiddleware(registeredListRequest, respond))
	prepare := func(ctx context.Context) *pagination.CursorPageAutoPager[openai.FileObject] {
		return client.Files.ListAutoPaging(ctx, openai.FileListParams{})
	}
	sources := []jsonview.Iterator[openai.FileObject]{PrepareListIterator(ctx, prepare), PrepareListIterator(ctx, prepare)}
	require.EqualValues(t, 2, first.Load())
	var wg sync.WaitGroup
	var outputs [2]strings.Builder
	var results [2]error
	for i, source := range sources {
		wg.Go(func() {
			results[i] = ShowJSONIterator(source, -1, ShowJSONOpts{Context: ctx, Format: "jsonl", Stdout: &outputs[i]})
		})
	}
	wg.Wait()
	require.ErrorAs(t, results[0], new(*listview.PaginationError))
	require.NoError(t, results[1])
	require.Equal(t, []string{`{"id":"file_1"}`}, strings.Fields(outputs[0].String()))
	require.Equal(t, []string{`{"id":"file_2"}`, `{"id":"file_final"}`}, strings.Fields(outputs[1].String()))
	require.EqualValues(t, 2, next.Load())
}

func TestPreparedListIteratorsDoNotSharePages(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		reverse, omitFiles, missing, parallel bool
		skillLimit                            int64
	}{
		{name: "prepared", skillLimit: -1},
		{name: "reverse", reverse: true, skillLimit: -1},
		{name: "omitted", omitFiles: true, skillLimit: -1},
		{name: "capped", reverse: true, skillLimit: 1},
		{name: "missing cursor", missing: true, skillLimit: -1},
		{name: "parallel preparation", reverse: true, parallel: true, skillLimit: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			bothDispatched := make(chan struct{})
			respond := func(request *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
				call := calls.Add(1)
				if tc.parallel && call <= 2 {
					if call == 2 {
						close(bothDispatched)
					}
					select {
					case <-bothDispatched:
					case <-time.After(3 * time.Second):
						return nil, errors.New("independent list requests did not run concurrently")
					}
				}
				if strings.HasSuffix(request.URL.Path, "/files") {
					return listResponse(request, `{"data":[{"id":"file_final"}],"has_more":false}`), nil
				}
				if tc.missing {
					return listResponse(request, `{"data":[{"name":"missing_id"}],"has_more":true}`), nil
				}
				if request.URL.Query().Get("after") == "skill_first" {
					return listResponse(request, `{"data":[{"id":"skill_final"}],"has_more":false}`), nil
				}
				return listResponse(request, `{"data":[{"id":"skill_first"}],"has_more":true}`), nil
			}
			ctx := context.WithValue(t.Context(), registeredListKey{}, new(registeredList))
			client := openai.NewClient(option.WithAPIKey("synthetic"), option.WithMaxRetries(0),
				option.WithMiddleware(registeredListRequest, respond))
			var files *pagination.CursorPageAutoPager[openai.FileObject]
			var skills *pagination.CursorPageAutoPager[openai.Skill]
			prepareFiles := func() { files = client.Files.ListAutoPaging(ctx, openai.FileListParams{}) }
			prepareSkills := func() { skills = client.Skills.ListAutoPaging(ctx, openai.SkillListParams{}) }
			if tc.parallel {
				var wg sync.WaitGroup
				wg.Go(prepareFiles)
				wg.Go(prepareSkills)
				wg.Wait()
			} else {
				prepareFiles()
				prepareSkills()
			}
			var out strings.Builder
			opts := ShowJSONOpts{Context: ctx, Format: "jsonl", Stdout: &out}
			showFiles := func() {
				if !tc.omitFiles {
					require.NoError(t, ShowJSONIterator(files, -1, opts))
				}
			}
			if !tc.reverse {
				showFiles()
			}
			err := ShowJSONIterator(skills, tc.skillLimit, opts)
			if tc.reverse {
				showFiles()
			}
			if tc.missing {
				require.ErrorAs(t, err, new(*listview.PaginationError))
				require.Contains(t, out.String(), "missing_id")
			} else {
				require.NoError(t, err)
				require.Contains(t, out.String(), "skill_first")
			}
			require.Equal(t, !tc.omitFiles, strings.Contains(out.String(), "file_final"))
			if tc.missing || tc.skillLimit == 1 {
				require.NotContains(t, out.String(), "skill_final")
				require.EqualValues(t, 2, calls.Load())
			} else {
				require.Contains(t, out.String(), "skill_final")
				require.EqualValues(t, 3, calls.Load())
			}
		})
	}
}
