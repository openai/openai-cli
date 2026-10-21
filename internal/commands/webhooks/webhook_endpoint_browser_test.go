package webhooks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestWebhookCreateCategoryMenuSelectsDirectlyAndCancelsWithoutChanges(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {36, 10}} {
		m := newWebhookCreatePicker([]string{"agent.session.created", "batch.completed", "response.completed", "future.event"})
		m.width, m.height, m.stage = size[0], size[1], 2
		m.selected["agent.session.created"] = true
		webhookCreateKey(m, tea.KeyEnter)
		require.True(t, m.categoryOpen)
		webhookCreateKey(m, tea.KeyEnd)
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, "> Other")
		require.LessOrEqual(t, strings.Count(view, "\n")+1, size[1]-1)
		webhookCreateKey(m, tea.KeyEnter)
		require.False(t, m.categoryOpen)
		require.Equal(t, []string{"future.event"}, m.filteredEvents())
		require.True(t, m.selected["agent.session.created"])
		require.Equal(t, webhookCreateChoices, m.eventFocus)
		m.eventFocus = webhookCreateCategory
		m.search.insert("retained query")
		webhookCreateKey(m, tea.KeyEnter)
		webhookCreateKey(m, tea.KeyHome)
		webhookCreateKey(m, tea.KeyEscape)
		require.False(t, m.canceled)
		require.False(t, m.categoryOpen)
		require.Equal(t, "Other", m.categories()[m.category])
		require.Equal(t, "retained query", string(m.search.value))
		webhookCreateKey(m, tea.KeyEnter)
		webhookCreateKey(m, tea.KeyHome)
		webhookCreateKey(m, tea.KeyEnter)
		require.Len(t, m.filteredEvents(), 4)
		require.False(t, m.confirmed)
	}
}

func TestWebhookCreateEndpointBrowserRetainsDraftAndIgnoresReplacedLoads(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed"})
	m.name.insert("Retained draft")
	m.url.insert("https://example.invalid/webhook")
	m.selected["response.completed"] = true
	m.browser = &webhookEndpointBrowser{ctx: t.Context(), load: func(context.Context) ([]webhookEndpoint, error) {
		return []webhookEndpoint{{id: "whe_first", name: "First", url: "https://example.invalid/first", events: []string{"response.failed"}}}, nil
	}}
	t.Cleanup(m.browser.close)
	m.stage, m.reviewFocused = 2, true
	before := m.settings()
	webhookCreateKey(m, tea.KeyRight)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Contains(t, m.View().Content, "Loading webhooks")
	m.Update(cmd())
	require.Contains(t, m.View().Content, "First")
	webhookCreateKey(m, tea.KeyEnter)
	view := ansi.Strip(m.View().Content)
	for _, value := range []string{"whe_first", "https://example.invalid/first", "response.failed"} {
		require.Contains(t, view, value)
	}
	webhookCreateKey(m, tea.KeyEscape)
	require.False(t, m.browser.details)
	old := m.browser.generation
	webhookCreateKey(m, tea.KeyEscape)
	require.False(t, m.browser.active)
	m.Update(webhookEndpointLoadMsg{generation: old, items: []webhookEndpoint{{name: "Stale response"}}})
	require.NotContains(t, m.View().Content, "Stale response")
	require.Equal(t, before, m.settings())
	require.False(t, m.confirmed)
	require.False(t, m.canceled)
	webhookCreateKey(m, tea.KeyLeft)
	webhookCreateKey(m, tea.KeyEnter)
	require.Equal(t, 3, m.stage)
	require.False(t, m.confirmYes)
}

func TestWebhookCreateEndpointBrowserCancellationAndLateCommand(t *testing.T) {
	started, done := make(chan struct{}), make(chan struct{})
	b := &webhookEndpointBrowser{ctx: t.Context(), load: func(ctx context.Context) ([]webhookEndpoint, error) {
		close(started)
		<-ctx.Done()
		close(done)
		return nil, ctx.Err()
	}}
	cmd := b.start()
	returned := make(chan tea.Msg, 1)
	go func() { returned <- cmd() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("endpoint loader did not start")
	}
	b.close()
	select {
	case <-done:
	default:
		t.Fatal("browser returned before its loader ended")
	}
	<-returned
	called := false
	late := &webhookEndpointBrowser{ctx: t.Context(), load: func(context.Context) ([]webhookEndpoint, error) {
		called = true
		return nil, nil
	}}
	lateCmd := late.start()
	late.close()
	require.Nil(t, lateCmd())
	require.False(t, called)
}

func TestWebhookCreateEndpointBrowserFramesAndSafeFailure(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {36, 10}, {40, 12}} {
		m := newWebhookCreatePicker([]string{"response.completed"})
		m.width, m.height = size[0], size[1]
		m.browser = &webhookEndpointBrowser{active: true, generation: 1}
		m.browser.receive(webhookEndpointLoadMsg{generation: 1, items: []webhookEndpoint{{name: "future\x1b[31m\nname", id: "whe_safe", url: "https://example.invalid/" + strings.Repeat("long/", 100), events: []string{"new.\x1b[31mvalue"}}}, err: &openai.Error{StatusCode: 403}})
		for _, details := range []bool{false, true} {
			m.browser.details = details
			view := m.View()
			require.Nil(t, view.Cursor)
			require.NotContains(t, view.Content, "\x1b")
			lines := strings.Split(view.Content, "\n")
			require.LessOrEqual(t, len(lines), size[1]-1)
			for _, line := range lines {
				require.LessOrEqual(t, ansi.StringWidth(line), min(76, size[0]-2))
			}
			require.Contains(t, view.Content, "HTTP 403")
			require.Contains(t, view.Content, "[ Back ]")
			require.Contains(t, view.Content, "[ Refresh ]")
		}
		m.browser.note = ""
		m.browser.items = nil
		m.browser.details = false
		require.Contains(t, m.View().Content, "No webhook endpoints")
	}
}

func TestWebhookCreateEndpointBrowserAlignsUnicodeNamesAndKeepsDetails(t *testing.T) {
	m := newWebhookCreatePicker([]string{"response.completed"})
	m.width, m.height = 80, 24
	m.browser = &webhookEndpointBrowser{active: true, items: []webhookEndpoint{
		{name: "短い", id: "whe_short"},
		{name: "Response notifications", id: "whe_response"},
		{name: strings.Repeat("通知", 12), id: "whe_long", url: "https://example.invalid/long"},
	}}
	for _, colored := range []bool{false, true} {
		m.color = colored
		view := ansi.Strip(m.View().Content)
		column := -1
		for _, line := range strings.Split(view, "\n") {
			before, _, found := strings.Cut(line, "whe_")
			if !found {
				continue
			}
			if column < 0 {
				column = ansi.StringWidth(before)
			}
			require.Equal(t, column, ansi.StringWidth(before), "IDs align by display cells")
		}
		require.Greater(t, column, 0)
		require.Contains(t, view, "…")
	}
	m.browser.current, m.browser.details = 2, true
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, strings.Repeat("通知", 12))
	require.Contains(t, view, "https://example.invalid/long")
}

func TestWebhookCreateEndpointDiscoveryKeepsScopeAndPagination(t *testing.T) {
	for _, failure := range []string{"", "HTTP", "repeated", "empty"} {
		t.Run(failure, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/webhook_endpoints", r.URL.Path)
				require.Equal(t, "Bearer sk-fake-browser", r.Header.Get("Authorization"))
				require.Equal(t, "proj_browser", r.Header.Get("OpenAI-Project"))
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Should-Retry", "false")
				if calls > 1 && failure == "HTTP" {
					w.WriteHeader(403)
					_, _ = io.WriteString(w, `{"error":{"message":"synthetic-private-body"}}`)
					return
				}
				if calls == 1 || failure == "repeated" {
					_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"whe_first","name":"First","url":"https://example.invalid/one","event_types":["response.completed"]}],"has_more":true}`)
					return
				}
				require.Equal(t, "whe_first", r.URL.Query().Get("after"))
				if failure == "empty" {
					_, _ = io.WriteString(w, `{"object":"list","data":[],"has_more":true}`)
					return
				}
				_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"whe_second","name":"Second","url":"https://example.invalid/two","event_types":[]}],"has_more":false}`)
			}))
			defer server.Close()
			app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
				items, err := discoverWebhookEndpoints(ctx, command)
				if failure == "" {
					require.NoError(t, err)
					require.Len(t, items, 2)
					require.Equal(t, "whe_second", items[1].id)
				} else {
					require.Error(t, err)
					require.NotEmpty(t, items, "retain loaded records after a later page fails")
				}
				require.Equal(t, 2, calls)
				return nil
			})
			require.NoError(t, app.Run(t.Context(), []string{"openai", "--api-key", "sk-fake-browser", "--project", "proj_browser", "--base-url", server.URL, "webhooks", "create"}))
		})
	}
}

func TestWebhookCreateEndpointBrowserFailureNeverPrintsCause(t *testing.T) {
	b := &webhookEndpointBrowser{active: true, generation: 1}
	b.receive(webhookEndpointLoadMsg{generation: 1, err: fmt.Errorf("synthetic-private-token: %w", errors.New("secret body"))})
	require.NotContains(t, b.note, "synthetic-private")
	require.NotContains(t, b.note, "secret body")
}
