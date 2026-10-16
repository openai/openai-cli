package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type authStatusCredentialResult struct {
	Configured bool   `json:"configured"`
	Type       string `json:"type"`
	Source     string `json:"source"`
}

type authStatusValueResult struct {
	Value    string `json:"value"`
	Source   string `json:"source"`
	Redacted bool   `json:"redacted"`
}

type authStatusResult struct {
	Offline         bool                       `json:"offline"`
	Verified        bool                       `json:"verified"`
	APICredential   authStatusCredentialResult `json:"api_credential"`
	AdminCredential authStatusCredentialResult `json:"admin_credential"`
	Organization    authStatusValueResult      `json:"organization"`
	Project         authStatusValueResult      `json:"project"`
	BaseURL         authStatusValueResult      `json:"base_url"`
}

func authStatusEnvironment(t *testing.T) []string {
	t.Helper()
	env := localUtilitiesEnvironment(t)
	for _, entry := range env {
		if home, ok := strings.CutPrefix(entry, "HOME="); ok {
			return append(env, "APPDATA="+home, "LOCALAPPDATA="+home, "ZDOTDIR="+home,
				"XDG_DATA_HOME="+home, "XDG_STATE_HOME="+home)
		}
	}
	t.Fatal("local utility environment must provide an isolated HOME")
	return nil
}

func decodeAuthStatus(t *testing.T, got mainDispatchResult) authStatusResult {
	t.Helper()
	require.Zero(t, got.code, "%s", got.stderr)
	require.Empty(t, got.stderr)
	decoder := json.NewDecoder(strings.NewReader(got.stdout))
	decoder.DisallowUnknownFields()
	var status authStatusResult
	require.NoError(t, decoder.Decode(&status), "%s", got.stdout)
	require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &fields))
	for _, name := range []string{"offline", "verified", "api_credential", "admin_credential", "organization", "project", "base_url"} {
		require.Contains(t, fields, name)
	}
	require.True(t, status.Offline)
	require.False(t, status.Verified)
	return status
}

func assertAuthStatusPrivate(t *testing.T, got mainDispatchResult, secrets ...string) {
	t.Helper()
	output := got.stdout + got.stderr
	require.NotContains(t, output, "\x1b")
	for _, secret := range secrets {
		require.NotContains(t, output, secret)
	}
}

func TestMainAuthStatusOfflineOutputAndDiagnostics(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	const apiKey = "synthetic-private-api-credential"
	const adminKey = "synthetic-private-admin-credential"
	const webhook = "synthetic-private-webhook-credential"
	for _, format := range []string{"auto", "text", "json"} {
		t.Run(format, func(t *testing.T) {
			env := append(authStatusEnvironment(t),
				"OPENAI_BASE_URL="+server.URL, "OPENAI_API_KEY="+apiKey,
				"OPENAI_ADMIN_KEY="+adminKey, "OPENAI_WEBHOOK_SECRET="+webhook,
				"FORCE_COLOR=1", "CLICOLOR_FORCE=1")
			args := []string{"openai", "auth", "status", "--format", format, "--format-error", "text"}
			ordinary := runMainDispatchWithEnv(t, "bash", env, args...)
			require.Zero(t, ordinary.code, "%s", ordinary.stderr)
			require.Empty(t, ordinary.stderr)
			assertAuthStatusPrivate(t, ordinary, apiKey, adminKey, webhook)
			if format == "json" {
				status := decodeAuthStatus(t, ordinary)
				require.Equal(t, authStatusCredentialResult{true, "api_key", "environment:OPENAI_API_KEY"}, status.APICredential)
				require.Equal(t, authStatusCredentialResult{true, "admin_api_key", "environment:OPENAI_ADMIN_KEY"}, status.AdminCredential)
			} else {
				require.Contains(t, strings.ToLower(ordinary.stdout), "offline")
				require.Contains(t, strings.ToLower(ordinary.stdout), "not verified")
			}
			for _, policy := range [][]string{{"--debug"}, {"--quiet"}, {"--verbose"}, {"--quiet", "--verbose", "--debug"}} {
				got := runMainDispatchWithEnv(t, "bash", env, append(append([]string{}, args...), policy...)...)
				require.Equal(t, ordinary.code, got.code)
				require.Equal(t, ordinary.stdout, got.stdout)
				assertAuthStatusPrivate(t, got, apiKey, adminKey, webhook)
				if len(policy) == 1 && policy[0] == "--verbose" && format != "json" {
					assertLocalUtilityVerbose(t, got.stderr, "auth status", format, "completed", "")
				} else {
					require.Empty(t, got.stderr)
				}
			}
		})
	}
	require.Zero(t, requests.Load(), "offline status must never contact the API")
}

func TestMainAuthStatusCredentialAndContextPrecedence(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	const secret = "synthetic-private-precedence-credential"
	for _, tc := range []struct {
		name       string
		env        []string
		args       []string
		credential authStatusCredentialResult
	}{
		{"missing", nil, nil, authStatusCredentialResult{false, "none", "unset"}},
		{"environment", []string{"OPENAI_API_KEY=" + secret}, nil,
			authStatusCredentialResult{true, "api_key", "environment:OPENAI_API_KEY"}},
		{"equal flag value", []string{"OPENAI_API_KEY=" + secret}, []string{"--api-key", secret},
			authStatusCredentialResult{true, "api_key", "flag:--api-key"}},
		{"empty flag", []string{"OPENAI_API_KEY=" + secret}, []string{"--api-key="},
			authStatusCredentialResult{false, "none", "flag:--api-key"}},
		{"last empty flag", nil, []string{"--api-key", secret, "--api-key="},
			authStatusCredentialResult{false, "none", "flag:--api-key"}},
		{"last nonempty flag", nil, []string{"--api-key=", "--api-key", secret},
			authStatusCredentialResult{true, "api_key", "flag:--api-key"}},
		{"environment key overrides authorization", []string{"OPENAI_API_KEY=" + secret, "OPENAI_CUSTOM_HEADERS=Authorization: Bearer " + secret}, nil,
			authStatusCredentialResult{true, "api_key", "environment:OPENAI_API_KEY"}},
		{"environment authorization only", []string{"OPENAI_CUSTOM_HEADERS=Authorization: Bearer " + secret}, nil,
			authStatusCredentialResult{true, "authorization_header", "environment:OPENAI_CUSTOM_HEADERS"}},
		{"flag authorization", []string{"OPENAI_API_KEY=" + secret}, []string{"--header", "Authorization: Bearer " + secret},
			authStatusCredentialResult{true, "authorization_header", "flag:--header"}},
		{"empty authorization", []string{"OPENAI_API_KEY=" + secret}, []string{"--header", "Authorization:"},
			authStatusCredentialResult{false, "none", "flag:--header"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := append(authStatusEnvironment(t), "OPENAI_BASE_URL="+server.URL)
			args := append([]string{"openai", "auth", "status", "--format", "json"}, tc.args...)
			got := runMainDispatchWithEnv(t, "bash", append(env, tc.env...), args...)
			assertAuthStatusPrivate(t, got, secret)
			status := decodeAuthStatus(t, got)
			require.Equal(t, tc.credential, status.APICredential)
		})
	}
	for _, position := range []string{"before command", "between commands", "after command"} {
		t.Run(position, func(t *testing.T) {
			env := append(authStatusEnvironment(t), "OPENAI_BASE_URL="+server.URL,
				"OPENAI_ORG_ID=org_environment", "OPENAI_PROJECT_ID=proj_environment")
			flags := []string{"--organization", "org_first", "--organization=", "--project", "proj_first", "--project", "proj_last"}
			args := []string{"openai", "--format", "json"}
			switch position {
			case "before command":
				args = append(append(args, flags...), "auth", "status")
			case "between commands":
				args = append(append(append(args, "auth"), flags...), "status")
			default:
				args = append(append(args, "auth", "status"), flags...)
			}
			status := decodeAuthStatus(t, runMainDispatchWithEnv(t, "bash", env, args...))
			require.Equal(t, authStatusValueResult{Value: "", Source: "flag:--organization"}, status.Organization)
			require.Equal(t, authStatusValueResult{Value: "proj_last", Source: "flag:--project"}, status.Project)
		})
	}
	require.Zero(t, requests.Load())
}

func TestMainAuthStatusPreservesStdin(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	const input = "synthetic-private-piped-input\n"
	for _, args := range [][]string{
		{"auth", "status", "--format", "json"},
		{"auth", "status", "--help"},
		{"auth", "status", "--format", "yaml", "--format-error", "json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env := append(authStatusEnvironment(t), "OPENAI_BASE_URL="+server.URL)
			argv := append([]string{"openai"}, args...)
			file := shellFileInput(t, []byte("prefix:"+input))
			const offset = int64(len("prefix:"))
			_, err := file.Seek(offset, io.SeekStart)
			require.NoError(t, err)
			got := runMainDispatchWithStdin(t, "bash", env, file, argv...)
			position, err := file.Seek(0, io.SeekCurrent)
			require.NoError(t, err)
			require.Equal(t, offset, position, "status must preserve the shared stdin file position")
			assertAuthStatusPrivate(t, got, input)

			reader, writer, err := os.Pipe()
			require.NoError(t, err)
			defer reader.Close()
			defer writer.Close()
			_, err = io.WriteString(writer, input)
			require.NoError(t, err)
			// Keep the writer open until the command exits. Reading to EOF would hang.
			piped := runMainDispatchWithStdin(t, "bash", env, reader, argv...)
			require.Equal(t, got, piped)
			require.NoError(t, writer.Close())
			remaining, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Equal(t, input, string(remaining))
		})
	}
	require.Zero(t, requests.Load())
}

func TestMainAuthStatusDoesNotLoadMTLS(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	missing := filepath.Join(t.TempDir(), "synthetic-private-missing.pem")
	for _, tc := range []struct {
		name string
		env  []string
		args []string
	}{
		{"incomplete environment", []string{"OPENAI_MTLS_CLIENT_CERT_FILE=" + missing}, nil},
		{"missing environment files", []string{"OPENAI_MTLS_CLIENT_CERT_FILE=" + missing, "OPENAI_MTLS_CLIENT_KEY_FILE=" + missing}, nil},
		{"incomplete flag", nil, []string{"--mtls-client-cert-file", missing}},
		{"missing flag files", nil, []string{"--mtls-client-cert-file", missing, "--mtls-client-key-file", missing}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := append(authStatusEnvironment(t), "OPENAI_BASE_URL="+server.URL)
			args := append([]string{"openai", "auth", "status", "--format", "json"}, tc.args...)
			got := runMainDispatchWithEnv(t, "bash", append(env, tc.env...), args...)
			decodeAuthStatus(t, got)
			assertAuthStatusPrivate(t, got, missing)
		})
	}
	require.Zero(t, requests.Load())
}

func TestMainAuthStatusSanitizesURLsAndTerminalControls(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	const user = "synthetic-private-url-user"
	const password = "synthetic-private-url-password"
	const query = "synthetic-private-url-query"
	const fragment = "synthetic-private-url-fragment"
	url := strings.Replace(server.URL, "://", "://"+user+":"+password+"@", 1) + "/v1?token=" + query + "#" + fragment
	organization := "org_synthetic\x1b[31m\rspoofed"
	project := "proj_synthetic\x1b]0;spoofed\a\nforged-row\tcolumn"
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			env := append(authStatusEnvironment(t), "OPENAI_BASE_URL="+url,
				"OPENAI_ORG_ID="+organization, "OPENAI_PROJECT_ID="+project)
			got := runMainDispatchWithEnv(t, "bash", env, "openai", "auth", "status", "--format", format)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			assertAuthStatusPrivate(t, got, user, password, query, fragment)
			require.NotContains(t, got.stdout, "\r")
			require.NotContains(t, got.stdout, "\a")
			require.NotContains(t, got.stdout, "\nforged-row")
			require.NotContains(t, got.stdout, "\t")
			if format == "json" {
				status := decodeAuthStatus(t, got)
				require.True(t, status.BaseURL.Redacted)
				require.True(t, strings.HasPrefix(status.BaseURL.Value, server.URL))
				require.Equal(t, "url_basic_auth", status.APICredential.Type)
				require.Equal(t, "url_basic_auth", status.AdminCredential.Type)
				require.Equal(t, organization, status.Organization.Value)
				require.Equal(t, project, status.Project.Value)
			}
		})
	}
	t.Run("explicit empty endpoint", func(t *testing.T) {
		env := append(authStatusEnvironment(t), "OPENAI_BASE_URL=")
		status := decodeAuthStatus(t, runMainDispatchWithEnv(t, "bash", env, "openai", "auth", "status", "--format", "json"))
		require.Equal(t, authStatusValueResult{Value: "", Source: "environment:OPENAI_BASE_URL"}, status.BaseURL)
	})
	require.Zero(t, requests.Load())
}

func TestMainAuthStatusErrorsRemainPrivate(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	const secret = "synthetic-private-invalid-value"
	for _, tc := range []struct {
		name string
		env  []string
		args []string
	}{
		{"unsupported format", nil, []string{"--format", "yaml"}},
		{"unknown format", nil, []string{"--format", secret}},
		{"transform", nil, []string{"--transform", secret}},
		{"raw output false", nil, []string{"--raw-output=false"}},
		{"extra argument", nil, []string{secret}},
		{"malformed header", nil, []string{"--header", secret}},
		{"header control", nil, []string{"--header", "Authorization: " + secret + "\r\nInjected: value"}},
		{"invalid environment URL", []string{"OPENAI_BASE_URL=://" + secret}, nil},
		{"invalid flag URL", nil, []string{"--base-url", "://" + secret}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := append(authStatusEnvironment(t), "OPENAI_BASE_URL="+server.URL,
				"OPENAI_API_KEY="+secret, "OPENAI_ADMIN_KEY="+secret)
			args := append([]string{"openai", "auth", "status", "--format-error", "json"}, tc.args...)
			got := runMainDispatchWithEnv(t, "bash", append(env, tc.env...), args...)
			require.NotZero(t, got.code)
			require.Empty(t, got.stdout)
			decodeMainStructuredError(t, "json", got.stderr)
			assertAuthStatusPrivate(t, got, secret)
			verbose := runMainDispatchWithEnv(t, "bash", append(env, tc.env...), append(args, "--verbose", "--debug")...)
			require.Equal(t, got, verbose, "machine-readable errors must not include optional diagnostics")
		})
	}
	require.Zero(t, requests.Load())
}

func TestBuiltCLIAuthStatusAndHelpAreOffline(t *testing.T) {
	binary, err := shellTestCLI()
	require.NoError(t, err)
	server, requests := localUtilitiesRequestTrap(t)
	const secret = "synthetic-private-built-cli-credential"
	for _, args := range [][]string{
		{"auth", "status", "--format", "json"},
		{"auth", "status", "--help"},
		{"auth", "--help"},
		{"help", "auth", "status"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, args...)
			child.Env = append(authStatusEnvironment(t), "OPENAI_BASE_URL="+server.URL,
				"OPENAI_API_KEY="+secret, "OPENAI_ADMIN_KEY="+secret,
				"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert",
				"SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			require.NoError(t, child.Run(), "%s", stderr.String())
			require.NoError(t, ctx.Err())
			got := mainDispatchResult{0, stdout.String(), stderr.String()}
			require.Empty(t, got.stderr)
			assertAuthStatusPrivate(t, got, secret)
			if args[len(args)-1] == "json" {
				decodeAuthStatus(t, got)
			} else {
				require.Contains(t, got.stdout, "status")
			}
		})
	}
	require.Zero(t, requests.Load())
}
