package custom

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestFileReceiptRequestContext(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  []string
		omit  bool
	}{
		{"environment credentials", nil, nil, false},
		{"request selection", []string{"--project=project-one", "--organization=org-one", "--base-url=https://example.invalid/proxy/v1"}, []string{"--project=project-one", "--organization=org-one", "--base-url=https://example.invalid/proxy/v1"}, false},
		{"explicit empty project", []string{"--project="}, []string{"--project="}, false},
		{"same inherited key", []string{"--api-key=sk-fake-inherited"}, nil, false},
		{"different key", []string{"--api-key=sk-fake-override"}, nil, true},
		{"empty key override", []string{"--api-key="}, nil, true},
		{"admin key override", []string{"--admin-api-key=sk-fake-admin"}, nil, true},
		{"webhook secret override", []string{"--webhook-secret=fake-secret"}, nil, true},
		{"header override", []string{"-H", "Authorization: Bearer fake-secret"}, nil, true},
		{"certificate override", []string{"--mtls-client-cert-file=/synthetic/private/cert.pem"}, nil, true},
		{"private key override", []string{"--mtls-client-key-file=/synthetic/private/key.pem"}, nil, true},
		{"URL user info", []string{"--base-url=https://fake:secret@example.invalid"}, nil, true},
		{"URL query", []string{"--base-url=https://example.invalid/v1?key=fake-secret"}, nil, true},
		{"URL fragment", []string{"--base-url=https://example.invalid/v1#fake-secret"}, nil, true},
		{"URL empty query", []string{"--base-url=https://example.invalid/v1?"}, nil, true},
		{"invalid project", []string{"--project=proj\x00name"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{"OPENAI_ADMIN_KEY", "OPENAI_WEBHOOK_SECRET", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID", mtlsClientCertFileEnv, mtlsClientKeyFileEnv} {
				t.Setenv(name, "")
				require.NoError(t, os.Unsetenv(name))
			}
			t.Setenv("OPENAI_API_KEY", "sk-fake-inherited")
			root := fileWorkflowCommandTree(func(ctx context.Context, _ *cli.Command) error {
				invocation := ctx.Value(fileInvocationKey{}).(fileInvocation)
				require.Equal(t, tc.want, invocation.requestArgs)
				require.Equal(t, tc.omit, invocation.omitHint)
				var receipt bytes.Buffer
				require.NoError(t, writeFileReceipt(&receipt, gjson.Parse(fileReceiptFixture), "bash", invocation))
				require.Equal(t, !tc.omit, bytes.Contains(receipt.Bytes(), []byte("Inspect it:")))
				for _, secret := range []string{"sk-fake", "fake-secret", "/synthetic/private/"} {
					require.NotContains(t, receipt.String(), secret)
				}
				return nil
			})
			root.Writer, root.ErrWriter = io.Discard, io.Discard
			root.Flags = []cli.Flag{&cli.StringFlag{Name: "base-url"}, NewRequestHeaderFlag()}
			for _, pair := range [][2]string{{"api-key", "OPENAI_API_KEY"}, {"admin-api-key", "OPENAI_ADMIN_KEY"}, {"webhook-secret", "OPENAI_WEBHOOK_SECRET"}, {"organization", "OPENAI_ORG_ID"}, {"project", "OPENAI_PROJECT_ID"}} {
				root.Flags = append(root.Flags, &requestflag.Flag[string]{Name: pair[0], Sources: cli.EnvVars(pair[1])})
			}
			root.Flags = append(root.Flags, mtlsClientFlags()...)
			configureRootRequestFlags(root)
			configureTaskCommands(root)
			configureFileCommands(root)
			args := append([]string{"openai", "files", "upload", "source.txt", "--purpose=user_data"}, tc.flags...)
			require.NoError(t, root.Run(t.Context(), args))
		})
	}
}
