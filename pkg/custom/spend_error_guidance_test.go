package custom

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
	"testing"

	"github.com/openai/openai-cli/internal/commands/admin"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func spendGuidanceTestCommand(resource, operation string) *cli.Command {
	return &cli.Command{Name: operation, Metadata: map[string]any{
		"openai-resource-command": resource, admin.CredentialsRequiredMetadata: true,
	}}
}

func TestSpendErrorGuidanceAdminMetadata(t *testing.T) {
	root := readableErrorTestCommand(t)
	root.Metadata["help-invocation"] = "'/tmp/synthetic app/openai'"
	for _, required := range []bool{false, true} {
		command := &cli.Command{Name: "retrieve", Metadata: map[string]any{admin.CredentialsRequiredMetadata: required}}
		failure := &openai.Error{StatusCode: http.StatusUnauthorized}
		message := readableAPIErrorMessage(root, withCommandError(command, failure), failure)
		want := "Key setup: '/tmp/synthetic app/openai' help setup"
		if required {
			want += " admin"
			require.Contains(t, message, "OPENAI_ADMIN_KEY")
			require.Contains(t, message, "Authorization override")
		}
		require.Contains(t, strings.Split(message, "\n"), want)
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
	} {
		require.Contains(t, readableSpendMutationFailure(root, spendGuidanceTestCommand(test.resource, test.operation)), "help "+test.read)
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
