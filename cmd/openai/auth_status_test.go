package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
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
	WaitBody    bool   `json:"wait_body"`
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
		var body io.ReadCloser = io.NopCloser(strings.NewReader(fixture.Body))
		if fixture.WaitBody {
			reader, writer := io.Pipe()
			body = reader
			go func() {
				// Pipe.Write returns only after the decoder consumes this incomplete JSON.
				_, err := io.WriteString(writer, `{"object":"user","id":"synthetic-private-response-body`)
				if err == nil {
					err = os.WriteFile(os.Getenv("OPENAI_CLI_AUTH_IDENTITY_BODY_READY"), []byte("body-decoding\n"), 0600)
				}
				if err == nil {
					<-request.Context().Done()
					err = request.Context().Err()
				}
				_ = writer.CloseWithError(err)
			}()
		}
		return &http.Response{StatusCode: fixture.Status, Header: headers, Body: body, Request: request}, nil
	})
	separator := slices.Index(os.Args, "--")
	require.GreaterOrEqual(t, separator, 0)
	os.Args = os.Args[separator+1:]
	main()
	os.Exit(0)
}

type authStatusIdentityProcess struct {
	command   *exec.Cmd
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	context   context.Context
	done      chan error
	requests  string
	bodyReady string
}

func startAuthStatusIdentity(t *testing.T, fixture authStatusIdentityFixture, env []string, args ...string) *authStatusIdentityProcess {
	t.Helper()
	return startAuthStatusIdentityWithTimeout(t, 25*time.Second, fixture, env, args...)
}

func startAuthStatusIdentityWithTimeout(t *testing.T, timeout time.Duration, fixture authStatusIdentityFixture, env []string, args ...string) *authStatusIdentityProcess {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	directory := t.TempDir()
	fixturePath := filepath.Join(directory, "fixture.json")
	data, err := json.Marshal(fixture)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fixturePath, data, 0600))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	process := &authStatusIdentityProcess{context: ctx, done: make(chan error, 1), requests: filepath.Join(directory, "requests.jsonl"),
		bodyReady: filepath.Join(directory, "body-ready")}
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
		"OPENAI_CLI_AUTH_IDENTITY_REQUESTS="+process.requests, "OPENAI_CLI_AUTH_IDENTITY_BODY_READY="+process.bodyReady, "COMPLETION_STYLE=bash",
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

func TestMainAuthStatusIdentitySpacedBearerRemainsPrivate(t *testing.T) {
	const secret = "synthetic-private-spaced-bearer"
	const authorization = "Bearer   " + secret
	for _, source := range []string{"flag", "environment", "key"} {
		for _, kind := range []string{"api", "admin"} {
			for _, format := range []string{"text", "json"} {
				t.Run(source+"_"+kind+"_"+format, func(t *testing.T) {
					args := []string{"--verify", "--credential-kind=" + kind, "--format=" + format, "--format-error=json", "--debug"}
					var env []string
					switch source {
					case "flag":
						args = append(args, "--header=Authorization: "+authorization)
					case "environment":
						env = append(env, "OPENAI_CUSTOM_HEADERS=Authorization: "+authorization)
					case "key":
						variable := "OPENAI_API_KEY"
						if kind == "admin" {
							variable = "OPENAI_ADMIN_KEY"
						}
						env = append(env, variable+"=  "+secret)
					}
					fixture := authStatusIdentityFixture{Status: http.StatusOK, ContentType: "application/json",
						Body: `{"object":"user","id":"user-synthetic","name":"prefix ` + secret + ` suffix"}`}
					got, requests := runAuthStatusIdentity(t, fixture, env, args...)
					require.NotZero(t, got.code)
					require.Len(t, requests, 1)
					require.Equal(t, authorization, requests[0].Headers.Get("Authorization"))
					if format == "json" {
						status := decodeAuthStatusJSON(t, got.stdout)
						require.False(t, status.Verified)
						identity := status.APIIdentity
						if kind == "admin" {
							identity = status.AdminIdentity
						}
						require.Equal(t, "unknown", identity.Status)
						require.Equal(t, "invalid_response", identity.Reason)
					} else {
						require.Contains(t, got.stdout, "unknown (invalid_response)")
					}
					decodeMainStructuredError(t, "json", got.stderr)
					assertAuthStatusPrivate(t, got, secret)
				})
			}
		}
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
			require.Contains(t, got.stderr, "Options and examples: openai auth status --help")
			if tc.name == "unauthorized" || tc.name == "malformed JSON" {
				assertAuthStatusIdentityRecovery(t, got, "json")
				text, textRequests := runAuthStatusIdentity(t, fixture, []string{"OPENAI_API_KEY=" + secret},
					"--verify", "--credential-kind=api", "--format=text", "--format-error=text", "--debug")
				require.Equal(t, got.code, text.code)
				require.Len(t, textRequests, 1)
				require.Contains(t, text.stdout, "API identity: unknown ("+tc.reason+")")
				assertAuthStatusPrivate(t, text, secret, "synthetic-private-response-body", "synthetic-private-transport-details", "synthetic-private-redirect")
				assertAuthStatusIdentityRecovery(t, text, "text")
			}
		})
	}
}

func assertAuthStatusIdentityRecovery(t *testing.T, got mainDispatchResult, format string) {
	t.Helper()
	message := strings.TrimSpace(got.stderr)
	if format == "json" {
		var value map[string]string
		require.NoError(t, json.Unmarshal([]byte(got.stderr), &value))
		require.Len(t, value, 1)
		message = value["message"]
	}
	_, recovery, found := strings.Cut(message, "\nOptions and examples: ")
	require.True(t, found, "%s", message)
	require.Equal(t, "openai auth status --help", recovery)
	// Execute the printed arguments through main, with the same synthetic transport.
	// The fixed prefix matches this helper's invocation and status route.
	args := strings.Fields(recovery)
	copied, requests := runAuthStatusIdentity(t, authStatusIdentitySuccessFixture(),
		[]string{"OPENAI_API_KEY=synthetic-private-recovery-token"}, args[3:]...)
	require.Zero(t, copied.code, "%s", copied.stderr)
	require.Empty(t, copied.stderr)
	require.Empty(t, requests, "help recovery must not verify credentials")
	require.Contains(t, copied.stdout, "Shows configured authentication offline by default.")
	require.Contains(t, copied.stdout, "openai auth status")
	assertAuthStatusPrivate(t, copied, "synthetic-private-recovery-token")
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
	assertAuthStatusIdentityRecovery(t, got, "json")
}

func TestMainAuthStatusIdentitySignals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows process signals require native console control events")
	}
	for _, tc := range []struct {
		name   string
		signal os.Signal
		code   int
		body   bool
	}{
		{"SIGINT", os.Interrupt, 130, false},
		{"SIGTERM", syscall.Signal(15), 143, false},
		{"SIGINT body", os.Interrupt, 130, true},
		{"SIGTERM body", syscall.Signal(15), 143, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			process := startAuthStatusIdentity(t, authStatusIdentityFixture{Wait: !tc.body, WaitBody: tc.body, Status: http.StatusOK, ContentType: "application/json"}, []string{"OPENAI_API_KEY=synthetic-private-api-token"},
				"--verify", "--credential-kind=api", "--format=json", "--format-error=json", "--debug")
			require.Eventually(t, func() bool {
				data, err := os.ReadFile(process.requests)
				return err == nil && json.Valid(bytes.TrimSpace(data))
			}, 5*time.Second, time.Millisecond, "request must start before the signal")
			if tc.body {
				require.Eventually(t, func() bool {
					data, err := os.ReadFile(process.bodyReady)
					return err == nil && string(data) == "body-decoding\n"
				}, 5*time.Second, time.Millisecond, "decoder must consume the partial body before the signal")
			}
			started := time.Now()
			require.NoError(t, process.command.Process.Signal(tc.signal))
			got, requests := finishAuthStatusIdentity(t, process)
			require.Less(t, time.Since(started), 5*time.Second)
			require.Equal(t, tc.code, got.code, "%s", got.stderr)
			require.Empty(t, got.stdout)
			require.Len(t, requests, 1)
			diagnostic := decodeMainStructuredError(t, "json", got.stderr)
			require.Equal(t, "Identity lookup canceled.", diagnostic["message"])
			assertAuthStatusPrivate(t, got, "synthetic-private-api-token", "synthetic-private-response-body")
		})
	}
}

func TestMainAuthStatusIdentityMTLSFIFOs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix FIFO paths require a Unix native runner")
	}
	mkfifo, err := exec.LookPath("mkfifo")
	require.NoError(t, err, "supported Unix runners must provide mkfifo")
	for _, kind := range []string{"certificate", "key"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			certFile := filepath.Join(directory, "synthetic-private-certificate.pem")
			keyFile := filepath.Join(directory, "synthetic-private-key.pem")
			fifo, regular := certFile, keyFile
			if kind == "key" {
				fifo, regular = keyFile, certFile
			}
			const placeholder = "synthetic-private-PEM-placeholder"
			require.NoError(t, os.WriteFile(regular, []byte(placeholder), 0600))
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			require.NoError(t, exec.CommandContext(ctx, mkfifo, fifo).Run())
			require.NoError(t, ctx.Err())
			require.NoError(t, os.Chmod(fifo, 0600))
			process := startAuthStatusIdentityWithTimeout(t, 3*time.Second, authStatusIdentitySuccessFixture(),
				[]string{"OPENAI_API_KEY=synthetic-private-api-token"},
				"--verify", "--credential-kind=api", "--format=json", "--format-error=json", "--debug",
				"--mtls-client-cert-file="+certFile, "--mtls-client-key-file="+keyFile)
			got, requests := finishAuthStatusIdentity(t, process)
			require.NotZero(t, got.code)
			require.Empty(t, requests, "special files must fail before the provider request")
			status := decodeAuthStatusJSON(t, got.stdout)
			require.False(t, status.Offline)
			require.False(t, status.Verified)
			require.Equal(t, "unknown", status.APIIdentity.Status)
			require.Equal(t, "invalid_configuration", status.APIIdentity.Reason)
			require.Equal(t, "not_configured", status.AdminIdentity.Reason)
			decodeMainStructuredError(t, "json", got.stderr)
			assertAuthStatusPrivate(t, got, "synthetic-private-api-token", placeholder, certFile, keyFile)
		})
	}
}

// Linux reads SSL_CERT_FILE and SSL_CERT_DIR without changing system trust.
// Exercise the standalone CLI: no test transport is installed in its process.
func TestBuiltCLIAuthStatusIdentityTransportLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("isolated certificate roots require the Linux trust loader")
	}
	binary, err := shellTestCLI()
	require.NoError(t, err)
	certificate, roots := authStatusProxyCertificate(t)
	caFile := filepath.Join(t.TempDir(), "synthetic-ca.pem")
	require.NoError(t, os.WriteFile(caFile, roots, 0o600))
	emptyCA := filepath.Join(t.TempDir(), "empty-ca.pem")
	require.NoError(t, os.WriteFile(emptyCA, nil, 0o600))
	const api = "synthetic-private-built-api"
	const admin = "synthetic-private-built-admin"
	const private = "synthetic-private-provider-body"
	const success = `{"object":"user","id":"user-built-synthetic","name":null,"email":null}`
	for _, tc := range []struct {
		name, kind, reason, body, location string
		status                             int
	}{
		{name: "offline"},
		{name: "api", kind: "api", status: 200, body: success},
		{name: "admin", kind: "admin", status: 200, body: success},
		{name: "untrusted_tls", kind: "api", reason: "request_failed", status: 200, body: success},
		{name: "invalid_json", kind: "api", reason: "invalid_response", status: 200, body: `{"object":"user","id":7}`},
		{name: "unauthorized", kind: "api", reason: "unauthorized", status: 401, body: private + api + admin},
		{name: "same_origin_redirect", kind: "api", reason: "redirect", status: 302, body: private,
			location: "https://api.openai.com/v1/synthetic-private-redirect"},
		{name: "cross_origin_redirect", kind: "api", reason: "redirect", status: 307, body: private,
			location: "https://example.invalid/synthetic-private-redirect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy, finish := authStatusLoopbackProxy(t, certificate, tc.status, tc.body, tc.location)
			trust := caFile
			if tc.name == "untrusted_tls" {
				trust = emptyCA
			}
			env := append(authStatusEnvironment(t), "OPENAI_API_KEY="+api, "OPENAI_ADMIN_KEY="+admin,
				"OPENAI_BASE_URL=https://api.openai.com/v1/", "HTTPS_PROXY="+proxy, "NO_PROXY=",
				"SSL_CERT_FILE="+trust, "SSL_CERT_DIR="+t.TempDir(), "GODEBUG=",
				"OPENAI_ORG_ID=org_synthetic", "OPENAI_PROJECT_ID=proj_synthetic",
				"OPENAI_CUSTOM_HEADERS=X-Private: synthetic-private-custom-header")
			args := []string{"auth", "status", "--format=json", "--format-error=json", "--debug"}
			if tc.kind != "" {
				args = append(args, "--verify", "--credential-kind="+tc.kind)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, args...)
			child.Env = env
			child.WaitDelay = time.Second
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			err := child.Run()
			observed := finish()
			require.NoError(t, ctx.Err(), "standalone CLI exceeded its bound")
			code := 0
			if err != nil {
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit)
				code = exit.ExitCode()
			}
			got := mainDispatchResult{code, stdout.String(), stderr.String()}
			assertAuthStatusPrivate(t, got, api, admin, private, "synthetic-private-custom-header", "synthetic-private-redirect")
			if tc.kind == "" {
				decodeAuthStatus(t, got)
				require.Empty(t, observed, "offline status must not connect to the proxy")
				return
			}
			require.Len(t, observed, 1, "lookup must not retry or follow any redirect")
			require.Equal(t, "api.openai.com:443", observed[0].authority)
			require.Empty(t, observed[0].connectHeaders.Get("Authorization"))
			for _, values := range observed[0].connectHeaders {
				for _, value := range values {
					require.NotContains(t, value, api)
					require.NotContains(t, value, admin)
				}
			}
			require.Equal(t, "api.openai.com", observed[0].serverName)
			if tc.name == "untrusted_tls" {
				require.True(t, observed[0].tlsRejected)
				require.Nil(t, observed[0].request, "untrusted TLS must receive no credential-bearing HTTP request")
			} else {
				require.Empty(t, observed[0].failure)
				request := observed[0].request
				require.NotNil(t, request)
				require.Equal(t, http.MethodGet, request.Method)
				require.Equal(t, "api.openai.com", request.Host)
				require.Equal(t, "/v1/me", request.URL.String())
				require.Zero(t, request.ContentLength)
				require.Empty(t, request.TransferEncoding)
				credential, unselected := api, admin
				if tc.kind == "admin" {
					credential, unselected = admin, api
				}
				require.Equal(t, []string{"Bearer " + credential}, request.Header.Values("Authorization"))
				for _, values := range request.Header {
					for _, value := range values {
						require.NotContains(t, value, unselected)
					}
				}
				require.Equal(t, "application/json", request.Header.Get("Accept"))
				require.Equal(t, "org_synthetic", request.Header.Get("OpenAI-Organization"))
				require.Equal(t, "proj_synthetic", request.Header.Get("OpenAI-Project"))
				require.Empty(t, request.Header.Get("X-Private"))
			}
			status := decodeAuthStatusJSON(t, got.stdout)
			require.False(t, status.Offline)
			selected, other := status.APIIdentity, status.AdminIdentity
			if tc.kind == "admin" {
				selected, other = other, selected
			}
			require.Equal(t, "unknown", other.Status)
			require.Equal(t, "not_requested", other.Reason)
			if tc.reason == "" {
				require.Zero(t, got.code, "%s", got.stderr)
				require.Empty(t, got.stderr)
				require.True(t, status.Verified)
				require.Equal(t, tc.kind, status.VerifiedKind)
				require.Equal(t, "resolved", selected.Status)
				require.Equal(t, "openai_me_v1", selected.Provider)
				require.Equal(t, "user-built-synthetic", selected.ID)
				require.Nil(t, selected.Name)
				require.Nil(t, selected.Email)
			} else {
				require.Equal(t, 1, got.code)
				require.False(t, status.Verified)
				require.Equal(t, "unknown", selected.Status)
				require.Equal(t, tc.reason, selected.Reason)
				decodeMainStructuredError(t, "json", got.stderr)
			}
		})
	}
}

func authStatusProxyCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caPublic, caPrivate)
	require.NoError(t, err)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"api.openai.com"},
		NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, public, caPrivate)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: private}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}

type authStatusProxyObservation struct {
	authority, serverName, failure string
	connectHeaders                 http.Header
	request                        *http.Request
	tlsRejected                    bool
}

// Terminate CONNECT locally. This proxy never resolves or dials its target.
func authStatusLoopbackProxy(t *testing.T, certificate tls.Certificate, status int, body, location string) (string, func() []authStatusProxyObservation) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	var mutex sync.Mutex
	var active net.Conn
	var closing bool
	var observed []authStatusProxyObservation
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			mutex.Lock()
			if closing {
				mutex.Unlock()
				connection.Close()
				return
			}
			active = connection
			mutex.Unlock()
			observation := authStatusProxyObservation{}
			func() {
				defer connection.Close()
				if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					observation.failure = "set connection deadline"
					return
				}
				request, err := http.ReadRequest(bufio.NewReader(connection))
				if err != nil {
					observation.failure = "read CONNECT"
					return
				}
				observation.authority, observation.connectHeaders = request.Host, request.Header.Clone()
				if request.Method != http.MethodConnect || request.Host != "api.openai.com:443" || request.RequestURI != request.Host {
					observation.failure = "rejected CONNECT target"
					return
				}
				if _, err := io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
					observation.failure = "write CONNECT response"
					return
				}
				secure := tls.Server(connection, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}})
				err = secure.Handshake()
				observation.serverName = secure.ConnectionState().ServerName
				if err != nil {
					observation.tlsRejected = true
					return
				}
				request, err = http.ReadRequest(bufio.NewReader(secure))
				if err != nil {
					observation.failure = "read identity request"
					return
				}
				observation.request = request
				response := &http.Response{StatusCode: status, ProtoMajor: 1, ProtoMinor: 1,
					Header: http.Header{"Content-Type": {"application/json"}},
					Body:   io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Close: true}
				if location != "" {
					response.Header.Set("Location", location)
				}
				if err := response.Write(secure); err != nil {
					observation.failure = "write identity response"
				}
			}()
			observed = append(observed, observation)
			mutex.Lock()
			active = nil
			mutex.Unlock()
		}
	}()
	var once sync.Once
	finish := func() []authStatusProxyObservation {
		once.Do(func() {
			listener.Close()
			mutex.Lock()
			closing = true
			if active != nil {
				active.Close()
			}
			mutex.Unlock()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("local identity proxy did not stop")
			}
		})
		return observed
	}
	t.Cleanup(func() { finish() })
	return "http://" + listener.Addr().String(), finish
}
