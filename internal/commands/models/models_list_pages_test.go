package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
)

func requestModelsFixture(t *testing.T, handler http.HandlerFunc) *modelRequestPages {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := openai.NewClient(option.WithBaseURL(server.URL+"/v1/"), option.WithAPIKey("synthetic-models"),
		option.WithMaxRetries(0), option.WithOrganization("synthetic-org"), option.WithProject("synthetic-project"))
	return &modelRequestPages{ctx: t.Context(), client: client, options: []option.RequestOption{
		option.WithHeader("X-Synthetic-Fixture", "models"),
	}}
}

func TestModelRequestsUseOpaqueCursorsOnTheOriginalRoute(t *testing.T) {
	const token = "https://untrusted.invalid/a?x=1&y=%+/界"
	var calls atomic.Int32
	pages := requestModelsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		want := url.Values{"limit": {"100"}}
		call := calls.Add(1)
		if call == 2 {
			want.Set("page", token)
		}
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.URL.Query().Encode() != want.Encode() ||
			r.Header.Get("X-Synthetic-Fixture") != "models" || r.Header.Get("Authorization") != "Bearer synthetic-models" {
			t.Error("Models did not preserve the original route, request options, or encoded cursor")
		}
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			encoded, _ := json.Marshal(token)
			fmt.Fprintf(w, `{"object":"page","data":[{"id":"a","object":"model","extra":"kept"}],"has_more":true,"next":%s}`, encoded)
		} else {
			fmt.Fprint(w, `{"object":"page","data":[{"id":"b","object":"model"}],"has_more":false,"next":null}`)
		}
	})
	first, err := pages.next()
	require.NoError(t, err)
	require.True(t, first.More)
	require.False(t, first.Local)
	require.Equal(t, "kept", first.Items[0].Get("extra").Str)
	require.EqualValues(t, 1, calls.Load())
	last, err := pages.next()
	require.NoError(t, err)
	require.False(t, last.More)
	require.Equal(t, "b", last.Items[0].Get("id").Str)
	empty, err := pages.next()
	require.NoError(t, err)
	require.Empty(t, empty.Items)
	require.EqualValues(t, 2, calls.Load())
}

func TestModelRequestsPreserveInvalidBodyErrors(t *testing.T) {
	for _, test := range []struct{ name, contentType, body string }{
		{"malformed_json", "application/json", `{"object":"page","data":[`},
		{"unsupported_media", "text/plain", `{"object":"list","data":[]}`},
		{"null", "application/json", "null"},
	} {
		t.Run(test.name, func(t *testing.T) {
			pages := requestModelsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				fmt.Fprint(w, test.body)
			})
			page, err := pages.next()
			require.Error(t, err)
			require.Empty(t, page.Items)
		})
	}
}

func TestModelRequestsReuseTheLegacyResponse(t *testing.T) {
	for _, metadata := range []string{"", `,"next":null`, `,"has_more":false,"next":null`} {
		t.Run(metadata, func(t *testing.T) {
			var calls atomic.Int32
			pages := requestModelsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) != 1 || r.URL.RawQuery != "limit=100" {
					t.Error("Legacy response triggered another request")
				}
				w.Header().Set("Content-Type", "application/json")
				var records []string
				for i := modelListPageSize; i >= 0; i-- {
					records = append(records, fmt.Sprintf(`{"id":"model-%03d","object":"model"}`, i))
				}
				fmt.Fprintf(w, `{"object":"list","data":[%s]%s}`, strings.Join(records, ","), metadata)
			})
			first, err := pages.next()
			require.NoError(t, err)
			require.True(t, first.Local && first.More)
			require.Len(t, first.Items, modelListPageSize)
			require.Equal(t, "model-000", first.Items[0].Get("id").Str)
			last, err := pages.next()
			require.NoError(t, err)
			require.True(t, last.Local)
			require.False(t, last.More)
			require.Equal(t, "model-100", last.Items[0].Get("id").Str)
			require.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestModelRequestsRejectBrokenContinuations(t *testing.T) {
	for _, test := range []struct{ name, data, fields string }{
		{"missing cursor", `[{"id":"b","object":"model"}]`, `,"has_more":true`},
		{"empty cursor", `[{"id":"b","object":"model"}]`, `,"has_more":true,"next":""`},
		{"repeated cursor", `[{"id":"b","object":"model"}]`, `,"has_more":true,"next":"synthetic-private"`},
		{"empty continuation", `[]`, `,"has_more":true,"next":"another"`},
		{"metadata disappeared", `[{"id":"b","object":"model"}]`, ``},
		{"wrong has_more type", `[]`, `,"has_more":"false"`},
		{"missing final cursor", `[{"id":"b","object":"model"}]`, `,"has_more":false`},
		{"unexpected final cursor", `[]`, `,"has_more":false,"next":"synthetic-private"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			pages := requestModelsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("page") == "" {
					fmt.Fprint(w, `{"object":"page","data":[{"id":"a","object":"model"}],"has_more":true,"next":"synthetic-private"}`)
				} else {
					fmt.Fprintf(w, `{"object":"page","data":%s%s}`, test.data, test.fields)
				}
			})
			first, err := pages.next()
			require.NoError(t, err)
			require.True(t, first.More)
			last, err := pages.next()
			require.Error(t, err)
			require.NotContains(t, err.Error(), "synthetic-private")
			require.False(t, last.More)
			if test.data != "[]" {
				require.Equal(t, "b", last.Items[0].Get("id").Str)
			}
		})
	}
}

func TestModelRequestsDiscardUnrelatedContinuation(t *testing.T) {
	for _, object := range []string{"list", "not-a-page", ""} {
		t.Run(object, func(t *testing.T) {
			pages := requestModelsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("page") == "" {
					fmt.Fprint(w, `{"object":"page","data":[{"id":"a"}],"has_more":true,"next":"second"}`)
				} else {
					fmt.Fprintf(w, `{"object":%q,"data":[{"id":"a"},{"id":"unrelated"}]}`, object)
				}
			})
			first, err := pages.next()
			require.NoError(t, err)
			require.True(t, first.More)
			last, err := pages.next()
			require.Error(t, err)
			require.Empty(t, last.Items)
			require.False(t, last.More)
		})
	}
}

func TestModelRequestsRetryOnlyUnsupportedInitialLimit(t *testing.T) {
	for _, test := range []struct {
		status      int
		param, code string
		retry       bool
	}{
		{400, "limit", "unknown_parameter", true}, {400, "limit", "unsupported_parameter", true},
		{400, "limit", "invalid_value", false}, {400, "other", "unsupported_parameter", false},
		{401, "limit", "unsupported_parameter", false}, {403, "limit", "unsupported_parameter", false},
		{500, "limit", "unknown_parameter", false},
	} {
		t.Run(strconv.Itoa(test.status)+"/"+test.param+"/"+test.code, func(t *testing.T) {
			var calls atomic.Int32
			pages := requestModelsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if calls.Add(1) == 1 {
					w.WriteHeader(test.status)
					fmt.Fprintf(w, `{"error":{"type":"invalid_request_error","code":%q,"param":%q,"message":"synthetic error"}}`, test.code, test.param)
				} else {
					if !test.retry || r.URL.RawQuery != "" {
						t.Error("unexpected retry")
					}
					fmt.Fprint(w, `{"object":"list","data":[{"id":"legacy","object":"model"}]}`)
				}
			})
			page, err := pages.next()
			if test.retry {
				require.NoError(t, err)
				require.True(t, page.Local)
				require.EqualValues(t, 2, calls.Load())
			} else {
				var apiErr *openai.Error
				require.True(t, errors.As(err, &apiErr))
				require.Equal(t, test.status, apiErr.StatusCode)
				require.EqualValues(t, 1, calls.Load())
			}
		})
	}
}

func TestModelRequestsCancelPendingPage(t *testing.T) {
	for _, later := range []bool{false, true} {
		t.Run(fmt.Sprintf("continuation=%t", later), func(t *testing.T) {
			started, ended := make(chan struct{}), make(chan struct{})
			pages := requestModelsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if later && r.URL.Query().Get("page") == "" {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"object":"page","data":[{"id":"a","object":"model"}],"has_more":true,"next":"second"}`)
					return
				}
				close(started)
				<-r.Context().Done()
				close(ended)
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			pages.ctx = ctx
			if later {
				first, err := pages.next()
				require.NoError(t, err)
				require.True(t, first.More)
			}
			result := make(chan error, 1)
			go func() { _, err := pages.next(); result <- err }()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("request did not start")
			}
			cancel()
			require.ErrorIs(t, <-result, context.Canceled)
			select {
			case <-ended:
			case <-time.After(5 * time.Second):
				t.Fatal("server did not observe cancellation")
			}
		})
	}
}
