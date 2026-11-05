package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestListPagerResizeRendersCachedRecords(t *testing.T) {
	for _, resource := range []struct{ name, input string }{
		{"files", `{"id":"file_first","object":"file","filename":"complete-dataset-filename.jsonl","purpose":"batch","bytes":1024,"status":"processed"}`},
		{"batches", `{"id":"batch_first","object":"batch","status":"completed","request_counts":{"total":12,"completed":12,"failed":0}}`},
		{"admin.organization.projects", `{"id":"proj_first","object":"organization.project","name":"Complete synthetic project name","status":"active"}`},
		{"models", `{"id":"model_first","object":"model","owned_by":"synthetic-owner-name"}`},
	} {
		for _, unfamiliar := range []bool{false, true} {
			name := "known"
			input := resource.input
			if unfamiliar {
				name = "unfamiliar"
				input = strings.TrimSuffix(input, "}") + `,"future_field":{"value":"preserved"}}`
			}
			t.Run(resource.name+"/"+name, func(t *testing.T) {
				var out bytes.Buffer
				var requests atomic.Int32
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				opts := listViewerOptions(ShowJSONOpts{Context: ctx, Stdout: &out,
					Operation: "(resource) " + resource.name + " > (method) list", OutputKind: OutputPageItem})
				pages := [][]gjson.Result{
					{gjson.Parse(input)},
					{gjson.Parse(strings.ReplaceAll(input, "_first", "_second"))},
				}
				failure := errors.New("synthetic next-page failure")
				model := listview.New(opts, func() (listview.Page, error) {
					request := int(requests.Add(1))
					if request <= len(pages) {
						return listview.Page{Items: pages[request-1], More: true}, nil
					}
					return listview.Page{}, failure
				}, func() {}, 120, 100)
				check := func(index, width int) string {
					t.Helper()
					want, err := opts.RenderPage(pages[index], width)
					require.NoError(t, err)
					view := ansi.Strip(model.View().Content)
					footer := strings.LastIndex(view, "\n")
					require.Positive(t, footer, "all records and controls must fit this fixture: %q", view)
					require.Contains(t, view[footer+1:], "q")
					body := view[:footer]
					require.Equal(t, strings.Join(strings.Fields(want), ""), strings.Join(strings.Fields(body), ""),
						"cached records must use the current width")
					for _, line := range strings.Split(view, "\n") {
						require.LessOrEqual(t, ansi.StringWidth(line), width)
					}
					return want
				}
				model.Update(model.Init()())
				check(0, 120)
				_, next := model.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
				require.NotNil(t, next)
				model.Update(next())
				check(1, 120)
				model.Update(tea.WindowSizeMsg{Width: 20, Height: 100})
				secondNarrow := check(1, 20)
				_, back := model.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
				require.Nil(t, back)
				check(0, 20)
				model.Update(tea.WindowSizeMsg{Width: 120, Height: 100})
				firstWide := check(0, 120)
				_, cached := model.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
				require.Nil(t, cached)
				secondWide := check(1, 120)
				require.EqualValues(t, 2, requests.Load(), "resize and cached navigation must not fetch")
				if !unfamiliar {
					require.NotEqual(t, secondNarrow, secondWide, "the fixture must change presentation at narrow width")
				}
				_, next = model.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
				require.NotNil(t, next)
				model.Update(next())
				require.ErrorIs(t, model.Finish(nil, nil), failure)
				require.Equal(t, firstWide+secondWide, out.String(), "later failure must preserve each cached record once")
				require.EqualValues(t, 3, requests.Load())
			})
		}
	}
}
