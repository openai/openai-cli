package app

import (
	"context"
	"errors"
	"io"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestModelsListExternalInterruptPreservesStatusAndFailures(t *testing.T) {
	for _, operation := range []string{"(resource) models > (method) list", "(resource) files > (method) list"} {
		for _, outputErr := range []error{nil, io.ErrClosedPipe} {
			for _, runErr := range []error{tea.ErrInterrupted, errors.Join(tea.ErrProgramKilled, tea.ErrInterrupted)} {
				request, cancel := context.WithCancel(t.Context())
				model := listview.New(listViewerOptions(ShowJSONOpts{Context: t.Context(), Operation: operation}), nil, cancel, 80, 1)
				err := model.Finish(outputErr, runErr)
				require.ErrorIs(t, request.Err(), context.Canceled)
				require.ErrorIs(t, err, tea.ErrInterrupted)
				if outputErr != nil {
					require.ErrorIs(t, err, outputErr)
				}
				var exit cli.ExitCoder
				if operation == "(resource) models > (method) list" {
					require.ErrorIs(t, err, context.Canceled)
					require.True(t, errors.As(err, &exit))
					require.Equal(t, 130, exit.ExitCode())
				} else {
					require.False(t, errors.As(err, &exit), "preserve other list exit status")
				}
			}
		}
	}
}

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
				model := listview.New(listViewerOptions(ShowJSONOpts{Context: t.Context(), Operation: test.operation}), func() (listview.Page, error) {
					return listview.Page{Items: []gjson.Result{gjson.Parse(`{"id":"model-a","object":"model","created":123,"owned_by":"openai"}`)}}, nil
				}, cancel, 80, 1)
				if loaded {
					model.Update(model.Init()())
				}
				_, quit := model.Update(test.key)
				require.NotNil(t, quit)
				require.ErrorIs(t, ctx.Err(), context.Canceled)
				err := model.Finish(nil, tea.ErrProgramKilled)
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
