package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainGlobalFlagErrorTransformExample(t *testing.T) {
	help := runMainDispatch(t, "bash", "openai", "help", "--all")
	require.Zero(t, help.code, help.stderr)
	_, description, found := strings.Cut(help.stdout, "\n   --transform-error ")
	require.True(t, found)
	description, _, _ = strings.Cut(description, "\n   --")
	_, example, found := strings.Cut(description, "such as '")
	require.True(t, found, "missing error extraction example")
	path, _, found := strings.Cut(example, "'")
	require.True(t, found)
	result := runMainAPIErrorResponse(t, http.StatusBadRequest, "application/json",
		`{"error":{"message":"synthetic example message","type":"invalid_request_error"}}`,
		"--transform-error", path)
	require.NotZero(t, result.code)
	require.Empty(t, result.stdout)
	require.Equal(t, "\"synthetic example message\"\n", result.stderr)
}

func TestMainGlobalFlagDescriptions(t *testing.T) {
	groups := []struct {
		title string
		flags []string
	}{
		{"Authentication", []string{"--api-key", "--admin-api-key", "--webhook-secret", "--mtls-client-cert-file", "--mtls-client-key-file"}},
		{"Output", []string{"--format", "--format-error", "--transform", "--transform-error", "--raw-output"}},
		{"Request options", []string{"--organization", "--project", "--base-url", "--header"}},
		{"Troubleshooting", []string{"--debug"}},
	}
	for _, path := range [][]string{nil, {"models", "list"}, {"images"}, {"images", "generate"}} {
		t.Run(strings.Join(append([]string{"root"}, path...), "/"), func(t *testing.T) {
			args := append([]string{"openai", "help", "--all"}, path...)
			got := runMainDispatchWithEnv(t, "bash", nil, args...)
			require.Zero(t, got.code, got.stderr)
			require.Empty(t, got.stderr)
			remaining := got.stdout
			for i, group := range groups {
				_, after, found := strings.Cut(remaining, "\n   "+group.title+"\n")
				require.True(t, found, "missing or reordered group %s", group.title)
				nextTitle := "Other options"
				if i+1 < len(groups) {
					nextTitle = groups[i+1].title
				}
				section, _, _ := strings.Cut(after, "\n   "+nextTitle+"\n")
				for _, flag := range group.flags {
					// These root-local request flags become inherited in the separate globals task.
					if len(path) > 0 && (flag == "--api-key" || flag == "--admin-api-key" || flag == "--webhook-secret" || flag == "--organization" || flag == "--project") {
						continue
					}
					require.Contains(t, section, "\n   "+flag, "%s must appear in %s", flag, group.title)
				}
				remaining = after
			}
			text := strings.Join(strings.Fields(got.stdout), " ")
			for _, format := range []string{"auto", "text", "json", "jsonl", "yaml", "explore", "pretty", "raw"} {
				require.Contains(t, got.stdout, "\n      - "+format+":", "format choices must stay on separate lines")
			}
			for _, description := range []string{
				"Format names are case-insensitive.",
				"readable text, including pipes",
				"raw: unformatted JSON.",
				"raw returns one API page envelope.",
				"Does not change error output.",
				"GJSON path syntax.",
				"the path applies to each item, except in the interactive explore viewer.",
				"With --format raw, the path applies to the page.",
				"If the path does not match, keep the original result.",
				"errors are displayed on stderr.",
				"Inherits json, jsonl, raw, or yaml from --format unless explicitly set.",
				"API and local errors use 'message'; streamed errors can use 'error.message'.",
				"Repeat for multiple headers; the last value for each header name wins.",
				"OPENAI_CUSTOM_HEADERS accepts one 'Name: Value' header per line.",
				"Flags override matching environment headers.",
				"https://api.openai.com/v1",
			} {
				require.Contains(t, text, description)
			}
			for _, name := range []string{"OPENAI_BASE_URL", "OPENAI_CUSTOM_HEADERS", "OPENAI_MTLS_CLIENT_CERT_FILE", "OPENAI_MTLS_CLIENT_KEY_FILE"} {
				require.Contains(t, text, name)
			}
			if len(path) == 0 {
				for _, description := range []string{
					"Authenticate API requests. Set OPENAI_API_KEY to keep the key out of shell history.",
					"Authenticate organization administration requests.",
					"a project API key cannot replace an admin key.",
					"Organization ID to send in the OpenAI-Organization request header.",
					"Project ID to send in the OpenAI-Project request header.",
					"The CLI has no webhook verification command.",
					"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_WEBHOOK_SECRET", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID",
				} {
					require.Contains(t, text, description)
				}
			}
		})
	}
}

func TestMainGlobalFlagHelpHidesConfiguredValues(t *testing.T) {
	env := []string{
		"OPENAI_API_KEY=synthetic-env-api-secret", "OPENAI_ADMIN_KEY=synthetic-env-admin-secret",
		"OPENAI_WEBHOOK_SECRET=synthetic-env-webhook-secret", "OPENAI_ORG_ID=synthetic-env-org-secret",
		"OPENAI_PROJECT_ID=synthetic-env-project-secret",
		"OPENAI_BASE_URL=https://synthetic-env-user:synthetic-env-password@example.invalid/v1?key=synthetic-env-url-secret",
		"OPENAI_CUSTOM_HEADERS=X-First: synthetic-env-header-secret\nX-Second: synthetic-env-header-second-secret",
		"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-env-cert-secret.pem",
		"OPENAI_MTLS_CLIENT_KEY_FILE=/synthetic-env-key-secret.pem",
	}
	flags := []string{
		"--api-key", "synthetic-flag-api-secret", "--admin-api-key", "synthetic-flag-admin-secret",
		"--webhook-secret", "synthetic-flag-webhook-secret", "--organization", "synthetic-flag-org-secret",
		"--project", "synthetic-flag-project-secret",
		"--base-url", "https://synthetic-flag-user:synthetic-flag-password@example.invalid/v1?key=synthetic-flag-url-secret",
		"--header", "X-First: synthetic-flag-header-secret", "-H", "X-Second: synthetic-flag-header-second-secret",
		"--mtls-client-cert-file", "/synthetic-flag-cert-secret.pem", "--mtls-client-key-file", "/synthetic-flag-key-secret.pem",
	}
	for _, explicit := range []bool{false, true} {
		for _, route := range [][]string{
			{"--help"}, {"images", "generate", "--help"}, {"models", "list", "--help"},
			{"help", "--all"}, {"help", "--all", "images", "generate"}, {"help", "--all", "models", "list"},
		} {
			name := "environment/"
			if explicit {
				name = "flags/"
			}
			t.Run(name+strings.Join(route, "/"), func(t *testing.T) {
				args := []string{"openai"}
				if explicit {
					args = append(args, flags...)
				}
				got := runMainDispatchWithEnv(t, "bash", env, append(args, route...)...)
				require.Zero(t, got.code, got.stderr)
				require.Empty(t, got.stderr)
				require.NotEmpty(t, got.stdout)
				for _, secret := range []string{"synthetic-env-", "synthetic-flag-", "example.invalid"} {
					require.NotContains(t, got.stdout+got.stderr, secret)
				}
				if route[0] == "help" {
					require.Contains(t, got.stdout, "https://api.openai.com/v1")
					require.Contains(t, got.stdout, "OPENAI_BASE_URL")
				}
			})
		}
	}
}

func TestMainGlobalFlagDescriptionsPreserveRequestProject(t *testing.T) {
	for _, path := range [][]string{{"admin", "organization", "invites", "create"}, {"admin:organization:invites", "create"}} {
		t.Run(strings.Join(path, "/"), func(t *testing.T) {
			args := append([]string{"openai", "help", "--all"}, path...)
			got := runMainDispatchWithEnv(t, "bash", nil, args...)
			require.Zero(t, got.code, got.stderr)
			require.Empty(t, got.stderr)
			_, after, found := strings.Cut(got.stdout, "\n   --project ")
			require.True(t, found, "invite help must retain its request project flag")
			local, _, _ := strings.Cut(after, "\n   --")
			text := strings.Join(strings.Fields(local), " ")
			require.Contains(t, text, "An array of projects to which membership is granted")
			require.NotContains(t, text, "OpenAI-Project request header")
		})
	}
}
