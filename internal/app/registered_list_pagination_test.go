package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
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
