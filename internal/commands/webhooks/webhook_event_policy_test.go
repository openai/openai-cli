package webhooks

import (
	"bytes"
	"context"
	"io"
	"testing"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestWebhookRequestEventsRejectRetiredEventsWithoutMutation(t *testing.T) {
	for _, test := range []struct{ action, body string }{
		{"create", `{"event_types":["fine_tuning.job.succeeded"]}`},
		{"create", `{"event_types":["response.completed","fine_tuning.future.private","batch.completed"],"name":"private-name"}`},
		{"update", `{"event_types":["response.completed","fine_tuning.job.failed"]}`},
		{"update", `{"event_types":["fine_tuning.job.failed"],"event_types":["response.completed"]}`},
		{"update", `{"event_types":["response.completed"],"event_types":["fine_tuning.job.failed"]}`},
		{"create", `{"event_\u0074ypes":["fine_\u0074uning.job.cancelled"]}`},
		{"test", `{"event_type":"fine_tuning.job.cancelled","private":"private-value"}`},
		{"test", `{"event_type":"fine_tuning.job.failed","event_type":"response.completed"}`},
		{"test", `{"event_type":"response.completed","event_type":"fine_tuning.job.failed"}`},
	} {
		t.Run(test.action+test.body, func(t *testing.T) {
			body := []byte(test.body)
			original := bytes.Clone(body)
			accepted := false
			command := &cli.Command{Name: test.action, Action: func(_ context.Context, command *cli.Command) error {
				if err := ValidateRequestEvents(command, body); err != nil {
					return err
				}
				accepted = true
				return nil
			}}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Commands: []*cli.Command{{Name: "webhooks", Commands: []*cli.Command{command}}}}
			err := root.Run(t.Context(), []string{"openai", "webhooks", test.action})
			require.EqualError(t, err, "This webhook request contains events unavailable in the CLI. Use the Platform API directly.")
			var safeError *localcommands.LocalError
			require.ErrorAs(t, err, &safeError)
			require.False(t, accepted, "a mixed request must fail as a whole")
			require.Equal(t, original, body, "validation must not rewrite subscriptions")
		})
	}
}

func TestWebhookRequestEventsPreserveOtherInputsAndCommands(t *testing.T) {
	for _, test := range []struct{ resource, action, body string }{
		{"webhooks", "create", `{"event_types":["response.completed","future.event","custom.fine_tuning.job"]}`},
		{"webhooks", "update", `{"name":"fine_tuning.job.private"}`},
		{"webhooks", "test", `{"event_type":"batch.completed"}`},
		{"webhooks", "create", `{"EVENT_TYPES":["fine_tuning.job.failed"]}`},
		{"webhooks", "create", `{"metadata":{"event_types":["fine_tuning.job.failed"]}}`},
		{"webhooks", "create", `{"event_types":null}`},
		{"webhooks", "create", `{"event_types":[42]}`},
		{"webhooks", "create", `invalid`},
		{"webhooks", "list", `{"event_types":["fine_tuning.job.failed"]}`},
		{"other", "create", `{"event_types":["fine_tuning.job.failed"]}`},
	} {
		t.Run(test.resource+test.action+test.body, func(t *testing.T) {
			body := []byte(test.body)
			original := bytes.Clone(body)
			called := false
			command := &cli.Command{Name: test.action, Action: func(_ context.Context, command *cli.Command) error {
				called = true
				return ValidateRequestEvents(command, body)
			}}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Commands: []*cli.Command{{Name: test.resource, Commands: []*cli.Command{command}}}}
			require.NoError(t, root.Run(t.Context(), []string{"openai", test.resource, test.action}))
			require.True(t, called)
			require.Equal(t, original, body)
		})
	}
}
