package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestVectorStoreWorkspaceAPIErrors(t *testing.T) {
	for _, tc := range []struct {
		name, code, detail, want string
		status                   int
		custom                   bool
	}{
		{name: "invalid key", code: "invalid_api_key", status: 401,
			want: "OpenAI didn't accept this API key.\nReopen with a valid project key."},
		{name: "unknown authentication failure", status: 401,
			want: "Couldn't sign in to OpenAI.\nReopen with your project API key."},
		{name: "custom endpoint", status: 401, custom: true,
			want: "The API endpoint denied access.\nCheck this endpoint's credentials."},
		{name: "IP rule", code: "ip_not_authorized", status: 401,
			want: "The server rejected this request under an IP access rule."},
		{name: "permissions", status: 401, detail: "You have insufficient permissions for this operation.",
			want: "The server denied this operation because required permissions are missing."},
		{name: "rate limit", status: 429, want: "The API is limiting requests.\nWait before trying again."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanAPIAuthGuidanceEnvironment(t)
			t.Setenv("OPENAI_API_KEY", "synthetic-secret")
			if tc.custom {
				t.Setenv("OPENAI_BASE_URL", "https://synthetic.example.invalid/v1")
			}
			apierr := &openai.Error{StatusCode: tc.status, Code: tc.code, Message: tc.detail,
				Response: &http.Response{Header: http.Header{"X-Request-Id": {"req_synthetic"}}}}
			if apierr.Message == "" {
				apierr.Message = "synthetic-private-response"
			}
			got := readableAPIErrorMessageTo(apiAuthGuidanceTestCommand(nil), apierr, apierr, workspaceError)
			require.Contains(t, got, tc.want)
			require.Contains(t, got, "\n\nHTTP ")
			require.Contains(t, got, "\nRequest ID: req_synthetic")
			require.NotContains(t, got, "--format-error")
			require.NotContains(t, got, "synthetic-private-response")
			require.NotContains(t, got, "synthetic-secret")
		})
	}
}

func TestVectorStoreWorkspaceAPIKeyOverrideError(t *testing.T) {
	cleanAPIAuthGuidanceEnvironment(t)
	t.Setenv("OPENAI_API_KEY", "synthetic-env-key")
	root := apiAuthGuidanceTestCommand(func(_ context.Context, cmd *cli.Command) error {
		apierr := &openai.Error{StatusCode: http.StatusUnauthorized, Code: "invalid_api_key"}
		got := readableAPIErrorMessageTo(cmd.Root(), apierr, apierr, workspaceError)
		require.Contains(t, got, "Replace the value of --api-key and reopen.")
		require.NotContains(t, got, "synthetic")
		return nil
	})
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--api-key", "synthetic-override", "models", "list"}))
}
