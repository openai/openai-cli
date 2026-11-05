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

func TestWebhookCreateCategoryArrowsKeepSelectionsAndEnterMovesToSearch(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {36, 10}} {
		m := newWebhookCreatePicker([]string{"agent.session.created", "batch.completed", "response.completed", "future.event"})
		m.width, m.height, m.stage = size[0], size[1], 2
		m.selected["agent.session.created"] = true
		webhookCreateKey(m, tea.KeyLeft)
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, "‹ Other ›")
		require.NotContains(t, view, "▾")
		require.Contains(t, view, "←→ category")
		require.Equal(t, []string{"future.event"}, m.filteredEvents())
		require.True(t, m.selected["agent.session.created"])
		webhookCreateKey(m, tea.KeyEnter)
		require.Equal(t, webhookCreateSearch, m.eventFocus)
		require.NotNil(t, m.View().Cursor)
		m.search.insert("retained query")
		webhookCreateKey(m, tea.KeyLeft)
		require.Equal(t, "Other", m.categories()[m.category], "search arrows edit text")
		webhookCreateKey(m, tea.KeyUp)
		webhookCreateKey(m, tea.KeyRight)
		require.Equal(t, "All event types", m.categories()[m.category])
		require.Empty(t, m.search.value)
		require.True(t, m.selected["agent.session.created"])
		require.False(t, m.confirmed)
		webhookCreateKey(m, tea.KeyEscape)
		require.True(t, m.canceled, "Escape cancels without a nested menu")
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

func TestWebhookCreateEndpointDetailsScrollCompleteValuesWithVisibleControls(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {36, 10}} {
		m := newWebhookCreatePicker([]string{"response.completed"})
		m.width, m.height = size[0], size[1]
		url := "https://example.invalid/" + strings.Repeat("long-path/", 80) + "end-marker"
		events := []string{"response.completed", "response.failed", "future.event"}
		m.browser = &webhookEndpointBrowser{active: true, details: true, items: []webhookEndpoint{{name: "Full values", id: "whe_full", url: url, events: events}}}
		rows := map[int]string{}
		for range 100 {
			view := ansi.Strip(m.View().Content)
			require.Contains(t, view, "↑↓ scroll")
			require.Contains(t, view, "Tab actions")
			require.Contains(t, view, "Esc back")
			lines := strings.Split(view, "\n")
			start, column := 2, 11
			if size[1] < 15 {
				start, column = 1, 9
			}
			for i := start; i < len(lines); i++ {
				line := lines[i]
				if line == "" || strings.HasPrefix(line, "├") {
					break
				}
				if strings.HasPrefix(line, "│ ") {
					line = strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │")
				}
				rows[m.browser.offset+i-start] = strings.TrimRight(ansi.Cut(line, column, ansi.StringWidth(line)), " ")
			}
			before := m.browser.offset
			webhookCreateKey(m, tea.KeyDown)
			m.View()
			if before == m.browser.offset {
				break
			}
		}
		var complete strings.Builder
		for i := range len(rows) {
			complete.WriteString(rows[i])
		}
		for _, value := range append([]string{"Full values", "whe_full", url}, events...) {
			require.Contains(t, complete.String(), value)
		}
		webhookCreateKey(m, tea.KeyTab)
		require.Contains(t, ansi.Strip(m.View().Content), "Tab details")
		webhookCreateKey(m, tea.KeyEscape)
		require.False(t, m.browser.details)
		require.False(t, m.confirmed)
	}
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
