package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"github.com/openai/openai-cli/internal/commands/admin"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func spendGuidanceTestCommand(resource, operation string) *cli.Command {
	return &cli.Command{Name: operation, Metadata: map[string]any{
		"openai-resource-command": resource, admin.CredentialsRequiredMetadata: true,
	}}
}

func TestSpendErrorGuidanceResponseConversionProvenance(t *testing.T) {
	command := spendGuidanceTestCommand("admin:organization:spend-alerts", "create")
	options := ShowJSONOpts{Context: t.Context(), Format: "yaml", ExplicitFormat: true,
		Operation:  "(resource) admin.organization.spend_alerts > (method) create",
		OutputKind: OutputResponse, Stdout: io.Discard, Stderr: io.Discard}
	failure := ShowJSON(gjson.Parse(`{"synthetic-private-response": broken}`), options)
	var marker *spendMutationResponseError
	var syntaxFailure *json.SyntaxError
	require.ErrorAs(t, failure, &marker)
	require.ErrorAs(t, failure, &syntaxFailure)
	require.ErrorIs(t, failure, syntaxFailure)
	require.Equal(t, syntaxFailure.Error(), failure.Error())
	root := readableErrorTestCommand(t)
	contextual := withCommandError(command, failure)
	require.Contains(t, readableLocalErrorMessage(root, contextual), "The API may have received the change.")
	require.Equal(t, "Could not decode JSON: invalid JSON syntax.", localErrorMessage(root, contextual))
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		joined := withCommandError(command, errors.Join(failure, cause))
		require.ErrorIs(t, joined, syntaxFailure)
		require.ErrorIs(t, joined, cause)
		require.Equal(t, localErrorMessage(root, joined), readableLocalErrorMessage(root, joined))
	}
	writeFailure := &clioutput.WriteError{Err: syscall.EPIPE}
	joined := errors.Join(failure, writeFailure)
	require.ErrorIs(t, joined, syntaxFailure)
	require.ErrorIs(t, joined, syscall.EPIPE)
	require.False(t, clioutput.IsBrokenPipe(joined), "a closed pipe must not discard a response failure")
	inputFailure := withCommandError(command, syntaxFailure)
	require.Equal(t, localErrorMessage(root, inputFailure), readableLocalErrorMessage(root, inputFailure))
	for _, kind := range []string{"read response", "error presentation"} {
		t.Run(kind, func(t *testing.T) {
			opts := options
			if kind == "read response" {
				opts.Operation = "(resource) admin.organization.spend_alerts > (method) retrieve"
			} else {
				opts.OutputKind = OutputUnspecified
			}
			err := ShowJSON(gjson.Parse(`{"synthetic-private-response": broken}`), opts)
			require.ErrorAs(t, err, &syntaxFailure)
			require.False(t, errors.As(err, &marker))
		})
	}
	options.Stdout = failOutputWriter{err: syscall.EPIPE}
	failure = ShowJSON(gjson.Parse(`{"object":"organization.spend_alert","id":"alert_synthetic"}`), options)
	require.ErrorIs(t, failure, syscall.EPIPE)
	require.True(t, clioutput.IsBrokenPipe(failure))
	require.False(t, errors.As(failure, &marker), "sink failures must retain their output classification")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	options.Context = ctx
	failure = ShowJSON(gjson.Parse(`{"synthetic-private-response": broken}`), options)
	require.ErrorIs(t, failure, context.Canceled)
	require.False(t, errors.As(failure, &marker), "pre-conversion cancellation must retain its classification")
}

func TestSpendMutationResponseValidationPreservesCancellation(t *testing.T) {
	for _, body := range []string{"", `{"synthetic-private-response": broken}`, `null`, `[]`, `{"id":"alert_synthetic"}`,
		`{"object":"organization.spend_alert"}`, `{"object":"organization.spend_alert","object":"organization.spend_alert"}`} {
		t.Run(body, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			checking := &cancelListTableContext{Context: ctx, cancel: cancel, after: 2}
			var out bytes.Buffer
			err := ShowJSON(gjson.Parse(body), ShowJSONOpts{Context: checking, Format: "text",
				Operation:  "(resource) admin.organization.spend_alerts > (method) create",
				OutputKind: OutputResponse, Stdout: &out, Stderr: io.Discard})
			require.ErrorIs(t, err, context.Canceled)
			require.Empty(t, out.String())
			if !gjson.Valid(body) {
				var syntaxFailure *json.SyntaxError
				require.ErrorAs(t, err, &syntaxFailure)
			}
		})
	}
}

func TestSpendErrorGuidanceAdminMetadata(t *testing.T) {
	cleanAPIAuthGuidanceEnvironment(t)
	t.Setenv("OPENAI_API_KEY", "sk-fake-project-auth")
	t.Setenv("OPENAI_ADMIN_KEY", "sk-fake-admin-auth")
	root := readableErrorTestCommand(t)
	root.Metadata["help-invocation"] = "'/tmp/synthetic app/openai'"
	for _, resource := range []string{"models", "admin:organization:spend-limit", "admin:organization:projects:spend-limit"} {
		t.Run(resource, func(t *testing.T) {
			command := spendGuidanceTestCommand(resource, "retrieve")
			failure := &openai.Error{StatusCode: http.StatusUnauthorized, Message: "synthetic-private-auth\x1b[2J"}
			contextual := withCommandError(command, failure)
			message := readableAPIErrorMessage(root, contextual, failure)
			require.Contains(t, strings.Split(message, "\n"), "Key setup: '/tmp/synthetic app/openai' help setup")
			if strings.HasPrefix(resource, "admin:") {
				require.Contains(t, message, "Credential source: OPENAI_ADMIN_KEY. An explicit --admin-api-key takes precedence.")
				require.Contains(t, message, "ask an organization owner to check this admin key's access")
				require.NotContains(t, message, "OPENAI_API_KEY")
			} else {
				require.Contains(t, message, "Credential source: OPENAI_API_KEY. An explicit --api-key takes precedence.")
				require.NotContains(t, message, "OPENAI_ADMIN_KEY")
			}
			for _, private := range []string{"synthetic-private", "sk-fake-", "\x1b"} {
				require.NotContains(t, message, private)
			}
			var machine bytes.Buffer
			require.NoError(t, ShowCommandError(readableErrorTestCommand(t, "--format-error", "json"), contextual, &machine))
			require.JSONEq(t, `{"status_code":401,"message":"HTTP 401 Unauthorized: the server returned no usable JSON error details."}`, machine.String())
		})
	}
}

func TestSpendErrorGuidanceNotFoundPreservesUncertainty(t *testing.T) {
	root := readableErrorTestCommand(t)
	for _, test := range []struct{ resource, operation, want string }{
		{"admin:organization:spend-limit", "retrieve", "may not be configured"},
		{"admin:organization:projects:spend-limit", "delete", "Check the project ID and access first"},
		{"admin:organization:spend-alerts", "update", "help admin organization spend-alerts list"},
		{"admin:organization:projects:spend-alerts", "retrieve", "help admin organization projects spend-alerts list"},
	} {
		command := spendGuidanceTestCommand(test.resource, test.operation)
		failure := &openai.Error{StatusCode: http.StatusNotFound, Message: "synthetic-private-secret\x1b[2J"}
		message := readableAPIErrorMessage(root, withCommandError(command, failure), failure)
		require.Contains(t, message, test.want)
		require.Contains(t, message, spendRecoveryContext)
		require.NotContains(t, message, "No hard spend limit configured")
		require.NotContains(t, message, "synthetic-private")
		require.NotContains(t, message, "\x1b")
	}
	for _, command := range []*cli.Command{
		spendGuidanceTestCommand("models", "retrieve"),
		spendGuidanceTestCommand("admin:organization:spend-limit", "update"),
		spendGuidanceTestCommand("admin:organization:spend-alerts", "create"),
	} {
		require.Empty(t, readableSpendNotFoundMessage(root, withCommandError(command, errors.New("synthetic"))))
	}
}

func TestSpendErrorGuidanceTransportPrecedenceAndMachineFallback(t *testing.T) {
	command := spendGuidanceTestCommand("admin:organization:spend-limit", "update")
	network := &url.Error{Op: "Post", URL: "https://synthetic-private.invalid/?token=synthetic-private", Err: io.EOF}
	for _, test := range []struct {
		name    string
		err     error
		want    string
		changed bool
	}{
		{"lost response", network, "The API may have received the change", true},
		{"truncated response", fmt.Errorf("synthetic-private: %w", io.ErrUnexpectedEOF), "The API may have received the change", true},
		{"cancel", errors.Join(network, context.Canceled), "Request canceled.", false},
		{"timeout", errors.Join(network, context.DeadlineExceeded), "The request timed out.", false},
		{"configuration", &url.Error{Op: "Post", URL: "synthetic-private", Err: errors.New("mTLS does not support HTTPS proxies")}, "mTLS does not support HTTPS proxies", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := withCommandError(command, test.err)
			root := readableErrorTestCommand(t)
			baseline := localErrorMessage(root, failure)
			var human bytes.Buffer
			require.NoError(t, ShowCommandError(root, failure, &human))
			require.Contains(t, human.String(), test.want)
			require.NotContains(t, human.String(), "synthetic-private")
			if test.changed {
				require.Contains(t, human.String(), "help admin organization spend-limit retrieve")
				require.NotContains(t, human.String(), "Could not connect")
			} else {
				require.Equal(t, baseline+"\n", human.String())
			}
			root = readableErrorTestCommand(t, "--format-error", "json")
			var machine bytes.Buffer
			require.NoError(t, ShowCommandError(root, failure, &machine))
			var value map[string]any
			require.NoError(t, json.Unmarshal(machine.Bytes(), &value))
			require.Equal(t, map[string]any{"message": baseline}, value)
			root = readableErrorTestCommand(t, "--format-error", "text", "--transform-error", "message")
			machine.Reset()
			require.NoError(t, ShowCommandError(root, failure, &machine))
			require.Equal(t, baseline+"\n", machine.String())
		})
	}
	root := readableErrorTestCommand(t)
	for _, command := range []*cli.Command{
		spendGuidanceTestCommand("models", "delete"),
		spendGuidanceTestCommand("admin:organization:spend-limit", "retrieve"),
	} {
		failure := withCommandError(command, network)
		require.Equal(t, localErrorMessage(root, failure), readableLocalErrorMessage(root, failure))
	}
}

func TestSpendErrorGuidanceMutationReadRoutes(t *testing.T) {
	root := readableErrorTestCommand(t)
	for _, test := range []struct{ resource, operation, read string }{
		{"admin:organization:projects:spend-limit", "delete", "admin organization projects spend-limit retrieve"},
		{"admin:organization:spend-alerts", "create", "admin organization spend-alerts list"},
		{"admin:organization:projects:spend-alerts", "create", "admin organization projects spend-alerts list"},
		{"admin:organization:projects:spend-alerts", "update", "admin organization projects spend-alerts retrieve"},
		{"admin:organization:projects:rate-limits", "update-rate-limit", "admin organization projects rate-limits list-rate-limits"},
	} {
		require.Contains(t, readableSpendMutationFailure(root, spendGuidanceTestCommand(test.resource, test.operation)), "help "+test.read)
	}
	network := &url.Error{Op: "Post", URL: "https://synthetic-private.invalid", Err: io.EOF}
	for _, operation := range []string{"update-rate-limit", "list-rate-limits"} {
		failure := withCommandError(spendGuidanceTestCommand("admin:organization:projects:rate-limits", operation), network)
		var human, machine bytes.Buffer
		require.NoError(t, ShowCommandError(root, failure, &human))
		baseline := localErrorMessage(root, failure)
		if operation == "update-rate-limit" {
			require.Contains(t, human.String(), "The API response did not confirm the rate-limit change.")
			require.Contains(t, human.String(), "help admin organization projects rate-limits list-rate-limits")
			require.Contains(t, human.String(), spendRecoveryContext)
		} else {
			require.Equal(t, baseline+"\n", human.String())
		}
		require.NotContains(t, human.String(), "synthetic-private")
		require.NoError(t, ShowCommandError(readableErrorTestCommand(t, "--format-error", "json"), failure, &machine))
		var value map[string]any
		require.NoError(t, json.Unmarshal(machine.Bytes(), &value))
		require.Equal(t, map[string]any{"message": baseline}, value)
	}
}

func TestSpendErrorGuidanceLimitCodesStayScoped(t *testing.T) {
	root := readableErrorTestCommand(t)
	for _, test := range []struct{ code, want, absent string }{
		{"organization_spend_limit_exceeded", "admin API key", "project's billing"},
		{"organization_usage_limit_exceeded", "organization's billing", "help admin organization spend-limit"},
		{"project_spend_limit_exceeded", "project's billing", "organization spend limit"},
		{"rate_limit_exceeded", "Wait before trying again", "admin API key"},
		{"synthetic-private-code", "Wait before trying again", "spend limit"},
	} {
		failure := &openai.Error{StatusCode: http.StatusTooManyRequests, Code: test.code}
		message := readableAPIErrorMessage(root, failure, failure)
		require.Contains(t, message, test.want)
		require.NotContains(t, message, test.absent)
		require.NotContains(t, message, "synthetic-private")
	}
}

func TestSpendErrorGuidanceAggregateKeepsSafeMessageAndCause(t *testing.T) {
	apierr := &openai.Error{StatusCode: http.StatusForbidden, Message: "synthetic-private"}
	failure := &limitsWorkflowFailure{message: "The overview is incomplete. Retry the failed source.", cause: errors.Join(apierr, io.ErrClosedPipe)}
	for _, args := range [][]string{nil, {"--format-error", "json"}, {"--transform-error", "message"}} {
		root := readableErrorTestCommand(t, args...)
		var out bytes.Buffer
		require.NoError(t, ShowCommandError(root, failure, &out))
		require.Contains(t, out.String(), failure.message)
		require.NotContains(t, out.String(), "synthetic-private")
		require.NotContains(t, out.String(), "HTTP 403")
		require.ErrorIs(t, failure, io.ErrClosedPipe)
		var preserved *openai.Error
		require.ErrorAs(t, failure, &preserved)
		require.Same(t, apierr, preserved)
	}
}
