package admin

import (
	"bytes"
	"context"
	"io"
	"testing"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestExternalKeyCreatePresencePolicy(t *testing.T) {
	for _, test := range []struct {
		name, resource, action, body, want string
	}{
		{"null AWS field", "admin:organization:external-keys", "create", `{"type":"aws","external_id":null,"kms_arn":"private-kms","role_arn":"private-role"}`, "Required external-key fields not set: --external-id"},
		{"null Azure field", "admin:organization:external-keys", "create", `{"type":"azure","key_name":"private-key","tenant_id":"private-tenant","vault_uri":null}`, "Required external-key fields not set: --vault-uri"},
		{"deterministic missing fields", "admin:organization:external-keys", "create", `{"type":"aws","name":"private-name"}`, "Required external-key fields not set: --external-id, --kms-arn, --role-arn"},
		{"present empty string", "admin:organization:external-keys", "create", `{"type":"aws","external_id":"","kms_arn":"private-kms","role_arn":"private-role"}`, ""},
		{"future provider", "admin:organization:external-keys", "create", `{"type":"future-provider"}`, ""},
		{"different operation", "admin:organization:external-keys", "update", `{"type":"aws"}`, ""},
		{"different resource", "other", "create", `{"type":"aws"}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(test.body)
			original := bytes.Clone(body)
			called := false
			command := &cli.Command{Name: test.action, Action: func(_ context.Context, command *cli.Command) error {
				called = true
				return ValidateExternalKeyCreate(command, body)
			}}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Commands: []*cli.Command{{Name: test.resource, Commands: []*cli.Command{command}}}}
			err := root.Run(t.Context(), []string{"openai", test.resource, test.action})
			if test.want == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, test.want)
				var safeError *localcommands.LocalError
				require.ErrorAs(t, err, &safeError)
				require.NotContains(t, err.Error(), "private-")
			}
			require.True(t, called)
			require.Equal(t, original, body)
		})
	}
}
