package custom

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/webhooks"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func webhookCreateTestCommand(action cli.ActionFunc) *cli.Command {
	create := &cli.Command{Name: "create", Action: action, Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "name", BodyPath: "name", Required: true},
		&requestflag.Flag[string]{Name: "url", BodyPath: "url", Required: true},
		&requestflag.Flag[[]string]{Name: "event-type", BodyPath: "event_types", Required: true},
	}}
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "format-error"},
			&cli.StringFlag{Name: "transform"}, &cli.StringFlag{Name: "transform-error"},
			&cli.BoolFlag{Name: "raw-output"}, &cli.BoolFlag{Name: "quiet"}, &cli.BoolFlag{Name: "debug"},
			&cli.StringFlag{Name: "base-url"}, NewRequestHeaderFlag(),
			&requestflag.Flag[string]{Name: "api-key"}, &requestflag.Flag[string]{Name: "admin-api-key"},
			&requestflag.Flag[string]{Name: "organization"}, &requestflag.Flag[string]{Name: "project"},
			&requestflag.Flag[string]{Name: "webhook-secret"},
		}, Commands: []*cli.Command{{Name: "webhooks", Commands: []*cli.Command{create}}},
	}
	configureRootRequestFlags(root)
	return root
}

func TestWebhookCreateRequestedKeepsExplicitModes(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want bool
	}{
		{"bare", nil, true},
		{"name", []string{"--name", "example"}, false},
		{"empty name", []string{"--name="}, false},
		{"event", []string{"--event-type", "response.completed"}, false},
		{"url", []string{"--url", "https://example.invalid/hook"}, false},
		{"argument", []string{"extra"}, false},
		{"json", []string{"--format", "json"}, false},
		{"text", []string{"--format", "text"}, false},
		{"auto", []string{"--format", "auto"}, false},
		{"error json", []string{"--format-error", "json"}, false},
		{"extraction", []string{"--transform", "id"}, false},
		{"empty extraction", []string{"--transform="}, false},
		{"error extraction", []string{"--transform-error", "message"}, false},
		{"raw", []string{"--raw-output"}, false},
		{"explicit false raw", []string{"--raw-output=false"}, false},
		{"quiet", []string{"--quiet"}, false},
		{"debug", []string{"--debug"}, false},
		{"project", []string{"--project", "proj_synthetic"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			app := webhookCreateTestCommand(func(_ context.Context, command *cli.Command) error {
				called = true
				require.Equal(t, test.want, webhookCreateRequested(command, func(string) string { return "" }))
				for _, env := range []map[string]string{{"CI": "true"}, {"TERM": "dumb"}, {"TERM": "DUMB"}} {
					require.False(t, webhookCreateRequested(command, func(key string) string { return env[key] }))
				}
				return nil
			})
			require.NoError(t, app.Run(t.Context(), append([]string{"openai", "webhooks", "create"}, test.args...)))
			require.True(t, called)
		})
	}
}

func TestWebhookCreateNonterminalDelegates(t *testing.T) {
	want := errors.New("generated behavior retained")
	called := 0
	app := webhookCreateTestCommand(webhookCreateWorkflow(func(context.Context, *cli.Command) error { called++; return want }))
	require.ErrorIs(t, app.Run(t.Context(), []string{"openai", "webhooks", "create"}), want)
	require.Equal(t, 1, called)
}

func TestWebhookCreateCatalogPreservesExactNames(t *testing.T) {
	events, err := parseWebhookCreateEvents([]byte(`{"object":"list","data":["response.completed","future.event","response.completed","@literal","\\@literal","escape\u001b[31m.event"],"future":true}`))
	require.NoError(t, err)
	require.Equal(t, []string{"response.completed", "future.event", "@literal", `\@literal`, "escape\x1b[31m.event"}, events)
	for _, raw := range []string{`null`, `[]`, `{}`, `{"object":"list"}`, `{"object":"other","data":["x"]}`,
		`{"object":"list","data":null}`, `{"object":"list","data":[]}`, `{"object":"list","data":[""]}`,
		`{"object":"list","data":[" "]}`, `{"object":"list","data":[4]}`, `{"object":"list","data":["x",null]}`,
		`{"object":"list","data":["x"]} trailing`,
		`{"object":"list","object":"list","data":["x"]}`,
		`{"object":"list","data":["x"],"data":["y"]}`,
		`{"object":"list","data":["x"],"\u0064ata":["y"]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			got, err := parseWebhookCreateEvents([]byte(raw))
			require.Error(t, err)
			require.Nil(t, got)
			var workflow *webhookWorkflowError
			require.ErrorAs(t, err, &workflow)
			require.Contains(t, workflow.message, "No endpoint was created")
			require.Contains(t, workflow.message, "webhooks event-types list --format json")
			require.Contains(t, workflow.message, "webhooks create --help")
		})
	}
}

func TestWebhookCreateCatalogKeepsUnknownOrdinaryFields(t *testing.T) {
	events, err := parseWebhookCreateEvents([]byte(`{"object":"list","data":["new.event"],"future":{"object":"a","data":"b"},"another":[1,2]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"new.event"}, events)
}

func TestWebhookCreateDiscoveryUsesProjectAndHeaderOptions(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-webhook-synthetic")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/webhook_event_types", r.URL.Path)
		require.Equal(t, "proj_synthetic", r.Header.Get("OpenAI-Project"))
		require.Equal(t, "org_synthetic", r.Header.Get("OpenAI-Organization"))
		require.Equal(t, "yes", r.Header.Get("X-Synthetic-Header"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":["response.completed","future.event"]}`)
	}))
	defer server.Close()
	app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
		events, err := discoverWebhookCreateEvents(ctx, command)
		require.NoError(t, err)
		require.Equal(t, []string{"response.completed", "future.event"}, events)
		require.False(t, command.IsSet("name"))
		require.False(t, command.IsSet("event-type"))
		return err
	})
	require.NoError(t, app.Run(t.Context(), []string{"openai", "--base-url", server.URL, "--project", "proj_synthetic",
		"--organization", "org_synthetic", "--header", "X-Synthetic-Header: yes", "webhooks", "create"}))
	require.Equal(t, 1, requests)
}

func TestWebhookCreateDiscoveryErrorsKeepSafeGuidanceAndAPIErrors(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-webhook-synthetic")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if status == http.StatusUnauthorized {
				_, _ = io.WriteString(w, `{"error":{"message":"synthetic authentication failure","type":"invalid_request_error"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"object":"list","data":42,"private":"synthetic-private-body"}`)
		}))
		app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
			events, err := discoverWebhookCreateEvents(ctx, command)
			require.Error(t, err)
			require.Nil(t, events)
			var workflow *webhookWorkflowError
			if status == http.StatusUnauthorized {
				var apiError *openai.Error
				require.ErrorAs(t, err, &apiError)
				require.False(t, errors.As(err, &workflow))
				require.Equal(t, status, apiError.StatusCode)
			} else {
				require.ErrorAs(t, err, &workflow)
				require.Contains(t, err.Error(), "No endpoint was created")
				require.Contains(t, err.Error(), "webhooks event-types list --format json")
				require.NotContains(t, err.Error(), "synthetic-private-body")
			}
			return nil
		})
		err := app.Run(t.Context(), []string{"openai", "--base-url", server.URL, "webhooks", "create"})
		server.Close()
		require.NoError(t, err)
	}
}

func TestWebhookCreateFlowNeverSubmitsWithoutConfirmation(t *testing.T) {
	for _, mode := range []string{"discovery error", "canceled before", "canceled during discovery", "picker error", "no", "canceled during picker", "missing events", "unknown event", "yes", "action error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "canceled before" {
				cancel()
			}
			failure := errors.New("synthetic failure")
			discoveries, picks, creates := 0, 0, 0
			app := webhookCreateTestCommand(func(_ context.Context, command *cli.Command) error {
				return runWebhookCreateFlow(ctx, command, func(context.Context, *cli.Command) error {
					creates++
					if mode == "action error" {
						return failure
					}
					return nil
				}, func(context.Context, *cli.Command) ([]string, error) {
					discoveries++
					if mode == "discovery error" {
						return nil, failure
					}
					if mode == "canceled during discovery" {
						cancel()
					}
					return []string{"response.completed"}, nil
				}, func(context.Context, []string) (webhookCreateSettings, bool, error) {
					picks++
					if mode == "picker error" {
						return webhookCreateSettings{}, false, failure
					}
					settings := webhookCreateSettings{"Webhook", "https://example.invalid/hook", []string{"response.completed"}}
					if mode == "canceled during picker" {
						cancel()
					}
					if mode == "missing events" {
						settings.events = nil
					}
					if mode == "unknown event" {
						settings.events = []string{"not.returned"}
					}
					return settings, mode != "no", nil
				})
			})
			err := app.Run(t.Context(), []string{"openai", "webhooks", "create"})
			if mode == "yes" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if mode == "yes" || mode == "action error" {
				require.Equal(t, 1, creates)
			} else {
				require.Zero(t, creates)
			}
			if mode == "no" {
				var exit cli.ExitCoder
				require.ErrorAs(t, err, &exit)
				require.Equal(t, 130, exit.ExitCode())
			}
			if mode == "canceled before" {
				require.Zero(t, discoveries)
			}
			if mode == "canceled before" || mode == "canceled during discovery" || mode == "discovery error" {
				require.Zero(t, picks)
			}
			if mode == "canceled before" || mode == "canceled during discovery" || mode == "canceled during picker" {
				var submitted *webhookCreateCanceledError
				require.False(t, errors.As(err, &submitted), "pre-submit cancellation must retain its ordinary behavior")
			}
		})
	}
}

func TestWebhookCreateLiteralBodyAndFlagRestoration(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-webhook-synthetic")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	for _, name := range []string{"@literal", `\@literal`, "@data://-", "synthetic name", "日本語"} {
		t.Run(name, func(t *testing.T) {
			settings := webhookCreateSettings{name, "https://example.invalid/hook?x=synthetic", []string{"@literal", `\@literal`, "future.event"}}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				require.Equal(t, "POST", r.Method)
				require.Equal(t, "/webhook_endpoints", r.URL.Path)
				var body struct {
					Name   string   `json:"name"`
					URL    string   `json:"url"`
					Events []string `json:"event_types"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, settings.name, body.Name)
				require.Equal(t, settings.url, body.URL)
				require.Equal(t, settings.events, body.Events)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"object":"webhook_endpoint","id":"whe_synthetic"}`)
			}))
			defer server.Close()
			app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
				original := slices.Clone(command.Flags)
				before := requestflag.ExtractRequestContents(command)
				failure := errors.New("synthetic output failure after create")
				for i := range 2 {
					err := runWebhookCreateAction(ctx, command, settings, func(ctx context.Context, command *cli.Command) error {
						options, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, ApplicationJSON, true)
						require.NoError(t, err)
						client := openai.NewClient(GetDefaultRequestOptions(command)...)
						_, err = client.Webhooks.New(ctx, webhooks.WebhookNewParams{}, options...)
						require.NoError(t, err)
						if i == 1 {
							return failure
						}
						return err
					})
					if i == 1 {
						require.ErrorIs(t, err, failure)
					} else {
						require.NoError(t, err)
					}
					for index, flag := range original {
						require.Same(t, flag, command.Flags[index])
					}
					require.Equal(t, before, requestflag.ExtractRequestContents(command))
					require.Equal(t, []string{"original.event"}, command.Value("event-type"))
					require.Equal(t, "original", command.String("name"))
					require.False(t, command.IsSet("url"))
				}
				return nil
			})
			require.NoError(t, app.Run(t.Context(), []string{"openai", "--base-url", server.URL,
				"webhooks", "create", "--name", "original", "--event-type", "original.event"}))
			require.Equal(t, 2, requests)
		})
	}
}

func TestWebhookCreatePreparationFailurePreservesFlags(t *testing.T) {
	want := errors.New("synthetic event validation failed")
	app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
		original := slices.Clone(command.Flags)
		before := requestflag.ExtractRequestContents(command)
		for _, flag := range command.Flags {
			if events, ok := flag.(*requestflag.Flag[[]string]); ok {
				events.Validator = func(values []string) error {
					require.Equal(t, []string{"response.completed"}, values)
					return want
				}
			}
		}
		err := runWebhookCreateAction(ctx, command,
			webhookCreateSettings{"Webhook", "https://example.invalid/hook", []string{"response.completed"}},
			func(context.Context, *cli.Command) error { t.Fatal("validation must prevent dispatch"); return nil })
		require.ErrorIs(t, err, want)
		require.Equal(t, before, requestflag.ExtractRequestContents(command))
		for index, flag := range original {
			require.Same(t, flag, command.Flags[index])
		}
		return nil
	})
	require.NoError(t, app.Run(t.Context(), []string{"openai", "webhooks", "create"}))
}

func TestWebhookCreateDispatchFailureRetainsCauseWithoutRetry(t *testing.T) {
	apiFailure := &openai.Error{StatusCode: http.StatusBadRequest}
	for _, failure := range []error{apiFailure, context.Canceled, io.ErrClosedPipe,
		&outputWriteError{io.ErrClosedPipe}, errors.New("sensitive URL or response body"),
	} {
		app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
			calls := 0
			err := runWebhookCreateAction(ctx, command,
				webhookCreateSettings{"Webhook", "https://example.invalid/hook", []string{"response.completed"}},
				func(context.Context, *cli.Command) error { calls++; return failure })
			require.Equal(t, 1, calls, "the UI must not retry after an uncertain result")
			require.ErrorIs(t, err, failure)
			require.Equal(t, isOutputBrokenPipe(failure), isOutputBrokenPipe(err))
			if failure == apiFailure {
				require.Same(t, failure, err)
			} else if failure != context.Canceled {
				var workflow *webhookWorkflowError
				require.ErrorAs(t, err, &workflow)
				require.Contains(t, err.Error(), "endpoint may already exist")
				require.Contains(t, err.Error(), "webhooks list with your original authentication, project, organization, and API settings before repeating create")
				require.NotContains(t, err.Error(), "sensitive")
			}
			return nil
		})
		require.NoError(t, app.Run(t.Context(), []string{"openai", "webhooks", "create"}))
	}
}

func TestWebhookCreateCanceledDispatchKeepsCauseAndSafeRecovery(t *testing.T) {
	cause := errors.Join(context.Canceled, io.ErrClosedPipe, errors.New("synthetic-private-response-and-url"))
	app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
		calls := 0
		err := runWebhookCreateAction(ctx, command,
			webhookCreateSettings{"Webhook", "https://example.invalid/hook", []string{"response.completed"}},
			func(context.Context, *cli.Command) error { calls++; return cause })
		require.Equal(t, 1, calls)
		var submitted *webhookCreateCanceledError
		require.ErrorAs(t, err, &submitted)
		require.Same(t, cause, submitted.Unwrap())
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, io.ErrClosedPipe)
		require.Contains(t, err.Error(), "Request canceled. The create request may have reached the API.")
		require.Contains(t, err.Error(), "original authentication, project, organization, and API settings")
		require.NotContains(t, err.Error(), "synthetic-private")
		require.False(t, isOutputBrokenPipe(err))
		return nil
	})
	require.NoError(t, app.Run(t.Context(), []string{"openai", "webhooks", "create"}))
}
