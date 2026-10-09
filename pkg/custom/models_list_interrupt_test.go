package custom

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestModelsListViewerInterruptStatus(t *testing.T) {
	for _, test := range []struct {
		name, operation string
		key             tea.KeyPressMsg
		interrupted     bool
	}{
		{"models interrupt", "(resource) models > (method) list", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, true},
		{"models quit", "(resource) models > (method) list", tea.KeyPressMsg{Code: 'q', Text: "q"}, false},
		{"files interrupt unchanged", "(resource) files > (method) list", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, loaded := range []bool{false, true} {
				ctx, cancel := context.WithCancel(t.Context())
				model := &listNavigation{opts: ShowJSONOpts{Context: t.Context(), Operation: test.operation}, cancel: cancel}
				if loaded {
					model.pages = []listNavigationPage{{items: []gjson.Result{gjson.Parse(`{"id":"model-a","object":"model","created":123,"owned_by":"openai"}`)}}}
				}
				_, quit := model.Update(test.key)
				require.NotNil(t, quit)
				require.True(t, model.quitting)
				require.False(t, model.printPage)
				require.ErrorIs(t, ctx.Err(), context.Canceled)
				err := model.finish(&listNavigationOutput{}, tea.ErrProgramKilled)
				if test.interrupted {
					require.ErrorIs(t, err, context.Canceled)
					var exit cli.ExitCoder
					require.True(t, errors.As(err, &exit), "interrupt must retain its process status")
					require.Equal(t, 130, exit.ExitCode())
				} else {
					require.NoError(t, err)
				}
			}
		})
	}
}
