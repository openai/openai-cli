package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestSpeechAuthenticationGuidanceConfigurationSources(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		code, message string
		env           map[string]string
		flags         []string
		want, omit    []string
	}{
		{name: "missing", status: 401, want: []string{"No API key is configured", "In your main terminal, run: openai help setup", "Reopen this form"}},
		{name: "environment key", status: 401, code: "invalid_api_key", env: map[string]string{"OPENAI_API_KEY": "synthetic-env"}, want: []string{"Replace the rejected API key", "Reopen this form"}},
		{name: "explicit key", status: 401, flags: []string{"--api-key", "synthetic-flag"}, want: []string{"Check your API key and selected project", "Reopen this form"}},
		{name: "empty override", status: 401, flags: []string{"--api-key="}, env: map[string]string{"OPENAI_API_KEY": "synthetic-env"}, want: []string{"override is empty", "Close this form", "Reopen without the empty --api-key option"}, omit: []string{"openai help setup"}},
		{name: "empty override without key", status: 401, flags: []string{"--api-key="}, want: []string{"Close this form", "In your main terminal, run: openai help setup", "Reopen without the empty --api-key option"}},
		{name: "header override", status: 401, flags: []string{"--header", "Authorization: Bearer synthetic-header"}, want: []string{"Correct or remove the Authorization override"}, omit: []string{apiAuthenticationKeyURL}},
		{name: "environment header", status: 401, env: map[string]string{"OPENAI_CUSTOM_HEADERS": "Authorization: Bearer synthetic-header"}, want: []string{"Correct or remove the Authorization override"}},
		{name: "permission", status: 403, message: "You have insufficient permissions for this operation.", want: []string{"Required permissions are missing", "organization and project roles"}},
		{name: "generic access denied", status: 403, flags: []string{"--api-key", "synthetic-flag"}, want: []string{"Check your key's permissions and selected project"}, omit: []string{"required permissions are missing", "Replace"}},
		{name: "IP restriction", status: 401, code: "ip_not_authorized", want: []string{"outbound IP in the allowlist"}, omit: []string{apiAuthenticationKeyURL}},
		{name: "custom endpoint", status: 401, flags: []string{"--base-url", "https://synthetic-private.invalid/v1"}, want: []string{"Authentication failed for this API endpoint", "credential required by this API endpoint"}, omit: []string{apiAuthenticationKeyURL}},
		{name: "unrelated status", status: 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanAPIAuthGuidanceEnvironment(t)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			called := false
			root := apiAuthGuidanceTestCommand(func(_ context.Context, command *cli.Command) error {
				called = true
				apierr := &openai.Error{StatusCode: tc.status, Code: tc.code, Message: tc.message}
				guidance := speechPresentation(ShowJSONOpts{}).AuthenticationGuidance(command, apierr)
				text := guidance.Summary + "\n" + guidance.Action
				for _, want := range tc.want {
					require.Contains(t, text, want)
				}
				for _, omit := range append(tc.omit, "synthetic-env", "synthetic-flag", "synthetic-header", "synthetic-private", "\x1b") {
					require.NotContains(t, text, omit)
				}
				for _, omit := range []string{"Credential source:", "Credential settings:", "https://", "HTTP status:", "Request ID:"} {
					require.NotContains(t, guidance.Summary+guidance.Action, omit)
				}
				if tc.status == http.StatusTooManyRequests {
					require.Empty(t, guidance)
				}
				return nil
			})
			args := append([]string{"openai"}, tc.flags...)
			require.NoError(t, root.Run(t.Context(), append(args, "models", "list")))
			require.True(t, called)
		})
	}
}
