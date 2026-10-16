package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
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
	APIIdentity     authStatusIdentityResult   `json:"api_identity"`
	AdminIdentity   authStatusIdentityResult   `json:"admin_identity"`
	VerifiedKind    string                     `json:"verified_credential_kind,omitempty"`
}

type authStatusIdentityResult struct {
	Status   string  `json:"status"`
	Reason   string  `json:"reason,omitempty"`
	Provider string  `json:"provider,omitempty"`
	ID       string  `json:"id,omitempty"`
	Name     *string `json:"name,omitempty"`
	Email    *string `json:"email,omitempty"`
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
	status := decodeAuthStatusJSON(t, got.stdout)
	require.True(t, status.Offline)
	require.False(t, status.Verified)
	require.Empty(t, status.VerifiedKind)
	for _, credential := range []struct {
		configured bool
		identity   authStatusIdentityResult
	}{{status.APICredential.Configured, status.APIIdentity}, {status.AdminCredential.Configured, status.AdminIdentity}} {
		require.Equal(t, "unknown", credential.identity.Status)
		reason := "not_configured"
		if credential.configured {
			reason = "not_requested"
		}
		require.Equal(t, reason, credential.identity.Reason)
	}
	return status
}

func decodeAuthStatusJSON(t *testing.T, output string) authStatusResult {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	var status authStatusResult
	require.NoError(t, decoder.Decode(&status), "%s", output)
	require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(output), &fields))
	for _, name := range []string{"offline", "verified", "api_credential", "admin_credential", "organization", "project", "base_url", "api_identity", "admin_identity"} {
		require.Contains(t, fields, name)
	}
	for _, name := range []string{"api_identity", "admin_identity"} {
		var identity map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(fields[name], &identity))
		if string(identity["status"]) != `"resolved"` {
			for _, field := range []string{"provider", "id"} {
				require.NotContains(t, identity, field, "unknown identity must not imply an account")
			}
			require.JSONEq(t, `null`, string(identity["name"]))
			require.JSONEq(t, `null`, string(identity["email"]))
		}
	}
	if !status.Verified {
		require.NotContains(t, fields, "verified_credential_kind")
	}
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
				if len(policy) == 1 && policy[0] == "--verbose" {
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

type authStatusIdentityFixture struct {
	Status      int    `json:"status"`
	Body        string `json:"body"`
	ContentType string `json:"content_type"`
	Location    string `json:"location"`
	Error       string `json:"error"`
	Wait        bool   `json:"wait"`
}

type authStatusIdentityRequest struct {
	Method               string      `json:"method"`
	URL                  string      `json:"url"`
	Headers              http.Header `json:"headers"`
	BodyPresent          bool        `json:"body_present"`
	DeadlineMilliseconds int64       `json:"deadline_milliseconds"`
}

type authStatusIdentityTransport func(*http.Request) (*http.Response, error)

func (transport authStatusIdentityTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// Install the synthetic transport only in this test subprocess. The production
// command still resolves its provider, credentials, request, output, and exit.
func TestMainAuthStatusIdentityProcess(t *testing.T) {
	if os.Getenv("OPENAI_CLI_AUTH_IDENTITY_PROCESS") != "1" {
		return
	}
	data, err := os.ReadFile(os.Getenv("OPENAI_CLI_AUTH_IDENTITY_FIXTURE"))
	require.NoError(t, err)
	var fixture authStatusIdentityFixture
	require.NoError(t, json.Unmarshal(data, &fixture))
	http.DefaultTransport = authStatusIdentityTransport(func(request *http.Request) (*http.Response, error) {
		deadline, _ := request.Context().Deadline()
		observed := authStatusIdentityRequest{
			Method: request.Method, URL: request.URL.String(), Headers: request.Header.Clone(),
			BodyPresent:          request.Body != nil && request.Body != http.NoBody,
			DeadlineMilliseconds: time.Until(deadline).Milliseconds(),
		}
		file, err := os.OpenFile(os.Getenv("OPENAI_CLI_AUTH_IDENTITY_REQUESTS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		err = json.NewEncoder(file).Encode(observed)
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if fixture.Wait {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}
		if fixture.Error != "" {
			return nil, errors.New(fixture.Error)
		}
		headers := http.Header{"Content-Type": {fixture.ContentType}}
		if fixture.Location != "" {
			headers.Set("Location", fixture.Location)
		}
		return &http.Response{StatusCode: fixture.Status, Header: headers,
			Body: io.NopCloser(strings.NewReader(fixture.Body)), Request: request}, nil
	})
	separator := slices.Index(os.Args, "--")
	require.GreaterOrEqual(t, separator, 0)
	os.Args = os.Args[separator+1:]
	main()
	os.Exit(0)
}

type authStatusIdentityProcess struct {
	command  *exec.Cmd
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	context  context.Context
	done     chan error
	requests string
}

func startAuthStatusIdentity(t *testing.T, fixture authStatusIdentityFixture, env []string, args ...string) *authStatusIdentityProcess {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	directory := t.TempDir()
	fixturePath := filepath.Join(directory, "fixture.json")
	data, err := json.Marshal(fixture)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fixturePath, data, 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	process := &authStatusIdentityProcess{context: ctx, done: make(chan error, 1), requests: filepath.Join(directory, "requests.jsonl")}
	argv := append([]string{"-test.run=^TestMainAuthStatusIdentityProcess$", "--", "openai", "auth", "status"}, args...)
	process.command = exec.CommandContext(ctx, binary, argv...)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "OPENAI_") && !strings.EqualFold(name, "COMPLETION_STYLE") {
			process.command.Env = append(process.command.Env, entry)
		}
	}
	process.command.Env = append(process.command.Env, authStatusEnvironment(t)...)
	process.command.Env = append(process.command.Env,
		"OPENAI_CLI_AUTH_IDENTITY_PROCESS=1", "OPENAI_CLI_AUTH_IDENTITY_FIXTURE="+fixturePath,
		"OPENAI_CLI_AUTH_IDENTITY_REQUESTS="+process.requests, "COMPLETION_STYLE=bash",
		"OPENAI_BASE_URL=https://api.openai.com/v1/")
	process.command.Env = append(process.command.Env, env...)
	process.command.Stdout, process.command.Stderr = &process.stdout, &process.stderr
	process.command.WaitDelay = time.Second
	require.NoError(t, process.command.Start())
	finished := make(chan struct{})
	go func() {
		process.done <- process.command.Wait()
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("identity subprocess did not stop after cleanup cancellation")
		}
	})
	return process
}

func finishAuthStatusIdentity(t *testing.T, process *authStatusIdentityProcess) (mainDispatchResult, []authStatusIdentityRequest) {
	t.Helper()
	err := <-process.done
	require.NoError(t, process.context.Err(), "identity process exceeded the outer deadline")
	code := 0
	if err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit)
		code = exit.ExitCode()
	}
	var requests []authStatusIdentityRequest
	data, err := os.ReadFile(process.requests)
	if !errors.Is(err, os.ErrNotExist) {
		require.NoError(t, err)
		decoder := json.NewDecoder(bytes.NewReader(data))
		for {
			var request authStatusIdentityRequest
			err := decoder.Decode(&request)
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)
			requests = append(requests, request)
		}
	}
	return mainDispatchResult{code, process.stdout.String(), process.stderr.String()}, requests
}

func runAuthStatusIdentity(t *testing.T, fixture authStatusIdentityFixture, env []string, args ...string) (mainDispatchResult, []authStatusIdentityRequest) {
	t.Helper()
	return finishAuthStatusIdentity(t, startAuthStatusIdentity(t, fixture, env, args...))
}

func authStatusIdentitySuccessFixture() authStatusIdentityFixture {
	return authStatusIdentityFixture{Status: http.StatusOK, ContentType: "application/json",
		Body: `{"object":"user","id":"user-synthetic","name":"Synthetic Name","email":"synthetic@example.invalid","ignored":"synthetic-private-response-field"}`}
}

func TestMainAuthStatusIdentitySelectionAndRequestBoundary(t *testing.T) {
	const api, admin = "synthetic-private-api-token", "synthetic-private-admin-token"
	for _, kind := range []string{"api", "admin"} {
		t.Run(kind, func(t *testing.T) {
			env := []string{"OPENAI_API_KEY=" + api, "OPENAI_ADMIN_KEY=" + admin,
				"OPENAI_ORG_ID=org_environment", "OPENAI_PROJECT_ID=proj_environment"}
			got, requests := runAuthStatusIdentity(t, authStatusIdentitySuccessFixture(), env,
				"--verify", "--credential-kind", kind, "--format=json", "--debug",
				"--organization=org_flag", "--project=proj_flag",
				"--header=OpenAI-Organization: org_header", "--header=OpenAI-Project: proj_header",
				"--header=X-Private: synthetic-private-extra-header", "--header=Cookie: synthetic-private-cookie")
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			assertAuthStatusPrivate(t, got, api, admin, "synthetic-private-extra-header", "synthetic-private-cookie", "synthetic-private-response-field")
			status := decodeAuthStatusJSON(t, got.stdout)
			require.False(t, status.Offline)
			require.True(t, status.Verified)
			require.Equal(t, kind, status.VerifiedKind)
			selected, unselected, credential := status.APIIdentity, status.AdminIdentity, api
			if kind == "admin" {
				selected, unselected, credential = status.AdminIdentity, status.APIIdentity, admin
			}
			require.Equal(t, "resolved", selected.Status)
			require.Equal(t, "openai_me_v1", selected.Provider)
			require.Equal(t, "user-synthetic", selected.ID)
			require.NotNil(t, selected.Name)
			require.NotNil(t, selected.Email)
			require.Equal(t, "Synthetic Name", *selected.Name)
			require.Equal(t, "synthetic@example.invalid", *selected.Email)
			require.Equal(t, "unknown", unselected.Status)
			require.Equal(t, "not_requested", unselected.Reason)
			require.Len(t, requests, 1, "identity lookup must not retry or use SDK fallback")
			require.Equal(t, http.MethodGet, requests[0].Method)
			require.Equal(t, "https://api.openai.com/v1/me", requests[0].URL)
			require.Equal(t, http.Header{"Authorization": {"Bearer " + credential}, "Accept": {"application/json"},
				"Openai-Organization": {"org_header"}, "Openai-Project": {"proj_header"}}, requests[0].Headers)
			require.False(t, requests[0].BodyPresent)
			require.Greater(t, requests[0].DeadlineMilliseconds, int64(0))
			require.LessOrEqual(t, requests[0].DeadlineMilliseconds, int64(15000))
		})
	}
}

func TestMainAuthStatusIdentityProviderAndCredentialRejections(t *testing.T) {
	for _, endpoint := range []string{
		"http://api.openai.com/v1/", "https://example.invalid/v1/", "https://api.openai.com.example.invalid/v1/",
		"https://api.openai.com:444/v1/", "https://api.openai.com/", "https://api.openai.com/v1/other",
		"https://api.openai.com/%76%31/", "https://api.openai.com/v1/?", "https://api.openai.com/v1/?token=synthetic-private-query",
		"https://api.openai.com/v1/#synthetic-private-fragment", "https://synthetic-private-user:synthetic-private-password@api.openai.com/v1/",
	} {
		t.Run(endpoint, func(t *testing.T) {
			got, requests := runAuthStatusIdentity(t, authStatusIdentitySuccessFixture(), []string{
				"OPENAI_BASE_URL=" + endpoint, "OPENAI_API_KEY=synthetic-private-api-token"},
				"--verify", "--credential-kind=api", "--format=json", "--format-error=json", "--debug")
			require.NotZero(t, got.code)
			require.Empty(t, requests, "unsupported providers must not receive credentials")
			status := decodeAuthStatusJSON(t, got.stdout)
			require.False(t, status.Verified)
			require.Equal(t, "unsupported_provider", status.APIIdentity.Reason)
			decodeMainStructuredError(t, "json", got.stderr)
			assertAuthStatusPrivate(t, got, "synthetic-private-api-token", "synthetic-private-query", "synthetic-private-fragment", "synthetic-private-user", "synthetic-private-password")
		})
	}
	for _, tc := range []struct {
		name, reason string
		env, args    []string
	}{
		{"missing selected admin", "not_configured", []string{"OPENAI_API_KEY=synthetic-private-api-token"}, nil},
		{"empty selected admin override", "not_configured", []string{"OPENAI_ADMIN_KEY=synthetic-private-admin-token"}, []string{"--admin-api-key="}},
		{"unsupported authorization", "invalid_configuration", nil, []string{"--header=Authorization: Basic synthetic-private-basic"}},
		{"incomplete TLS", "invalid_configuration", []string{"OPENAI_ADMIN_KEY=synthetic-private-admin-token", "OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--verify", "--credential-kind=admin", "--format=json", "--format-error=json"}, tc.args...)
			got, requests := runAuthStatusIdentity(t, authStatusIdentitySuccessFixture(), tc.env, args...)
			require.NotZero(t, got.code)
			require.Empty(t, requests)
			status := decodeAuthStatusJSON(t, got.stdout)
			require.False(t, status.Verified)
			require.Equal(t, tc.reason, status.AdminIdentity.Reason)
			decodeMainStructuredError(t, "json", got.stderr)
			assertAuthStatusPrivate(t, got, "synthetic-private-api-token", "synthetic-private-admin-token", "synthetic-private-basic", "/synthetic-private/missing-cert")
		})
	}
}

func TestMainAuthStatusIdentitySelectionRequiresExplicitVerification(t *testing.T) {
	for _, args := range [][]string{{"--verify"}, {"--verify", "--credential-kind=other"}, {"--credential-kind=api"}, {"--verify=false", "--credential-kind=admin"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			got, requests := runAuthStatusIdentity(t, authStatusIdentitySuccessFixture(), nil,
				append(args, "--format=json", "--format-error=json")...)
			require.NotZero(t, got.code)
			require.Empty(t, got.stdout)
			require.Empty(t, requests)
			decodeMainStructuredError(t, "json", got.stderr)
		})
	}
}

func TestMainAuthStatusIdentityFailuresRemainUnknownAndPrivate(t *testing.T) {
	const secret = "synthetic-private-api-token"
	for _, tc := range []struct {
		name, reason string
		fixture      authStatusIdentityFixture
	}{
		{"unauthorized", "unauthorized", authStatusIdentityFixture{Status: 401}},
		{"forbidden", "forbidden", authStatusIdentityFixture{Status: 403}},
		{"missing provider", "unavailable", authStatusIdentityFixture{Status: 404}},
		{"rate limited", "rate_limited", authStatusIdentityFixture{Status: 429}},
		{"server failure", "unavailable", authStatusIdentityFixture{Status: 503}},
		{"same-origin redirect", "redirect", authStatusIdentityFixture{Status: 302, Location: "https://api.openai.com/v1/other"}},
		{"cross-origin redirect", "redirect", authStatusIdentityFixture{Status: 307, Location: "https://example.invalid/synthetic-private-redirect"}},
		{"transport failure", "request_failed", authStatusIdentityFixture{Error: "synthetic-private-transport-details " + secret}},
		{"wrong content type", "invalid_response", authStatusIdentityFixture{Status: 200, ContentType: "text/html", Body: "synthetic-private-response-body"}},
		{"malformed JSON", "invalid_response", authStatusIdentityFixture{Status: 200, Body: `{"object":"user","id":`}},
		{"missing ID", "invalid_response", authStatusIdentityFixture{Status: 200, Body: `{"object":"user","email":"synthetic-private-response-body"}`}},
		{"wrong principal", "invalid_response", authStatusIdentityFixture{Status: 200, Body: `{"object":"organization","id":"org-synthetic"}`}},
		{"wrong name type", "invalid_response", authStatusIdentityFixture{Status: 200, Body: `{"object":"user","id":"user-synthetic","name":7}`}},
		{"duplicate ID", "invalid_response", authStatusIdentityFixture{Status: 200, Body: `{"object":"user","id":"first","id":"second"}`}},
		{"duplicate unknown field", "invalid_response", authStatusIdentityFixture{Status: 200, Body: `{"object":"user","id":"user-synthetic","ignored":1,"ignored":2}`}},
		{"trailing JSON", "invalid_response", authStatusIdentityFixture{Status: 200, Body: `{"object":"user","id":"user-synthetic"}{}`}},
		{"credential echoed in ID", "invalid_response", authStatusIdentityFixture{Status: 200, Body: `{"object":"user","id":"` + secret + `"}`}},
		{"credential echoed in name", "invalid_response", authStatusIdentityFixture{Status: 200, Body: `{"object":"user","id":"user-synthetic","name":"prefix ` + secret + ` suffix"}`}},
		{"credential echoed in email", "invalid_response", authStatusIdentityFixture{Status: 200, Body: `{"object":"user","id":"user-synthetic","email":"` + secret + `@example.invalid"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := tc.fixture
			if fixture.ContentType == "" {
				fixture.ContentType = "application/json"
			}
			if fixture.Body == "" {
				fixture.Body = `{"error":{"message":"synthetic-private-response-body ` + secret + `"}}`
			}
			got, requests := runAuthStatusIdentity(t, fixture, []string{"OPENAI_API_KEY=" + secret},
				"--verify", "--credential-kind=api", "--format=json", "--format-error=json", "--verbose", "--debug")
			require.NotZero(t, got.code)
			require.Len(t, requests, 1, "failure must not trigger retries, redirects, or fallback")
			status := decodeAuthStatusJSON(t, got.stdout)
			require.False(t, status.Offline)
			require.False(t, status.Verified)
			require.Equal(t, "unknown", status.APIIdentity.Status)
			require.Equal(t, tc.reason, status.APIIdentity.Reason)
			require.Equal(t, "not_configured", status.AdminIdentity.Reason)
			decodeMainStructuredError(t, "json", got.stderr)
			assertAuthStatusPrivate(t, got, secret, "synthetic-private-response-body", "synthetic-private-transport-details", "synthetic-private-redirect")
		})
	}
}

func TestMainAuthStatusIdentityNullableFieldsAndSupportedEndpoints(t *testing.T) {
	for _, endpoint := range []string{"https://api.openai.com/v1", "https://api.openai.com/v1/", "https://api.openai.com:443/v1", "https://api.openai.com:443/v1/"} {
		t.Run(endpoint, func(t *testing.T) {
			fixture := authStatusIdentitySuccessFixture()
			fixture.Body = `{"object":"user","id":"user-synthetic","name":null,"email":null}`
			got, requests := runAuthStatusIdentity(t, fixture, []string{"OPENAI_API_KEY=synthetic-private-api-token", "OPENAI_BASE_URL=" + endpoint},
				"--verify", "--credential-kind=api", "--format=json")
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			require.Len(t, requests, 1)
			require.True(t, strings.HasSuffix(requests[0].URL, "/v1/me"))
			status := decodeAuthStatusJSON(t, got.stdout)
			require.True(t, status.Verified)
			require.Nil(t, status.APIIdentity.Name)
			require.Nil(t, status.APIIdentity.Email)
			require.NotContains(t, got.stdout, "synthetic-private-api-token")
		})
	}
}

func TestMainAuthStatusIdentityAuthorizationPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, kind, authorization string
		env, args                 []string
	}{
		{"selected API flag", "api", "Bearer synthetic-private-flag", []string{"OPENAI_API_KEY=synthetic-private-environment"}, []string{"--api-key=synthetic-private-flag"}},
		{"selected admin flag", "admin", "Bearer synthetic-private-flag", []string{"OPENAI_ADMIN_KEY=synthetic-private-environment"}, []string{"--admin-api-key=synthetic-private-flag"}},
		{"literal authorization", "admin", "Bearer synthetic-private-header", []string{"OPENAI_API_KEY=synthetic-private-api", "OPENAI_ADMIN_KEY=synthetic-private-admin"}, []string{"--header=Authorization: Bearer synthetic-private-header"}},
		{"inherited authorization", "api", "Bearer synthetic-private-header", []string{"OPENAI_CUSTOM_HEADERS=Authorization: Bearer synthetic-private-header"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--verify", "--credential-kind=" + tc.kind, "--format=json"}, tc.args...)
			got, requests := runAuthStatusIdentity(t, authStatusIdentitySuccessFixture(), tc.env, args...)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			require.Len(t, requests, 1)
			require.Equal(t, []string{tc.authorization}, requests[0].Headers.Values("Authorization"))
			require.True(t, decodeAuthStatusJSON(t, got.stdout).Verified)
			assertAuthStatusPrivate(t, got, "synthetic-private-flag", "synthetic-private-environment", "synthetic-private-header", "synthetic-private-api", "synthetic-private-admin")
		})
	}
}

func TestMainAuthStatusIdentityTextEscapesTerminalControls(t *testing.T) {
	fixture := authStatusIdentitySuccessFixture()
	fixture.Body = `{"object":"user","id":"user-synthetic","name":"Name\u001b[31m\rspoofed\nforged-row\tcolumn","email":"synthetic@example.invalid"}`
	got, requests := runAuthStatusIdentity(t, fixture, []string{"OPENAI_API_KEY=synthetic-private-api-token", "FORCE_COLOR=1"},
		"--verify", "--credential-kind=api", "--format=text", "--debug", "--quiet")
	require.Zero(t, got.code, "%s", got.stderr)
	require.Empty(t, got.stderr)
	require.Len(t, requests, 1)
	require.Contains(t, got.stdout, "API identity: resolved")
	require.Contains(t, got.stdout, "user-synthetic")
	require.Contains(t, got.stdout, "synthetic@example.invalid")
	require.Contains(t, got.stdout, "Admin identity: unknown")
	assertAuthStatusPrivate(t, got, "synthetic-private-api-token")
	for _, control := range []string{"\r", "\t", "\nforged-row"} {
		require.NotContains(t, got.stdout, control)
	}
}

func TestMainAuthStatusIdentityDeadline(t *testing.T) {
	started := time.Now()
	got, requests := runAuthStatusIdentity(t, authStatusIdentityFixture{Wait: true}, []string{"OPENAI_API_KEY=synthetic-private-api-token"},
		"--verify", "--credential-kind=api", "--format=json", "--format-error=json")
	require.NotZero(t, got.code)
	require.Less(t, time.Since(started), 23*time.Second, "provider timeout must precede the outer process limit")
	require.Len(t, requests, 1)
	require.Greater(t, requests[0].DeadlineMilliseconds, int64(14000))
	require.LessOrEqual(t, requests[0].DeadlineMilliseconds, int64(15000))
	status := decodeAuthStatusJSON(t, got.stdout)
	require.False(t, status.Verified)
	require.Equal(t, "timed_out", status.APIIdentity.Reason)
	decodeMainStructuredError(t, "json", got.stderr)
	assertAuthStatusPrivate(t, got, "synthetic-private-api-token")
}

func TestMainAuthStatusIdentitySignals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows process signals require native console control events")
	}
	for _, tc := range []struct {
		name   string
		signal os.Signal
		code   int
	}{{"SIGINT", os.Interrupt, 130}, {"SIGTERM", syscall.Signal(15), 143}} {
		t.Run(tc.name, func(t *testing.T) {
			process := startAuthStatusIdentity(t, authStatusIdentityFixture{Wait: true}, []string{"OPENAI_API_KEY=synthetic-private-api-token"},
				"--verify", "--credential-kind=api", "--format=json", "--format-error=json", "--debug")
			require.Eventually(t, func() bool {
				data, err := os.ReadFile(process.requests)
				return err == nil && json.Valid(bytes.TrimSpace(data))
			}, 5*time.Second, time.Millisecond, "request must start before the signal")
			started := time.Now()
			require.NoError(t, process.command.Process.Signal(tc.signal))
			got, requests := finishAuthStatusIdentity(t, process)
			require.Less(t, time.Since(started), 5*time.Second)
			require.Equal(t, tc.code, got.code, "%s", got.stderr)
			require.Empty(t, got.stdout)
			require.Len(t, requests, 1)
			decodeMainStructuredError(t, "json", got.stderr)
			assertAuthStatusPrivate(t, got, "synthetic-private-api-token")
		})
	}
}
