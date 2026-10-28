package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"

	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type spendPresentationWriter func([]byte) (int, error)

func (write spendPresentationWriter) Write(value []byte) (int, error) { return write(value) }

func TestSpendErrorGuidancePresentationProvenance(t *testing.T) {
	for _, scope := range []string{"admin.organization", "admin.organization.projects"} {
		for _, operation := range []struct{ resource, method, command, read string }{
			{"spend_limit", "update", "update", "spend-limit retrieve"},
			{"spend_limit", "delete", "delete", "spend-limit retrieve"},
			{"spend_alerts", "create", "create", "spend-alerts list"},
			{"spend_alerts", "update", "update", "spend-alerts retrieve"},
			{"spend_alerts", "delete", "delete", "spend-alerts retrieve"},
			{"rate_limits", "update_rate_limit", "update-rate-limit", "rate-limits list-rate-limits"},
		} {
			if scope == "admin.organization" && operation.resource == "rate_limits" {
				continue
			}
			t.Run(scope+"/"+operation.resource+"/"+operation.method, func(t *testing.T) {
				cause := &os.PathError{Op: "write", Path: "synthetic-private-output", Err: os.ErrPermission}
				command := spendGuidanceTestCommand(strings.ReplaceAll(scope, ".", ":")+":"+strings.ReplaceAll(operation.resource, "_", "-"), operation.command)
				for _, format := range []string{"text", "json", "raw", "yaml"} {
					options := ShowJSONOpts{Context: t.Context(), Operation: "(resource) " + scope + "." + operation.resource + " > (method) " + operation.method,
						OutputKind: OutputResponse, Format: format, ExplicitFormat: true, Stdout: failOutputWriter{cause}, Stderr: io.Discard}
					failure := ShowJSON(gjson.Parse(`{"id":"synthetic","value":9007199254740993}`), options)
					var marker *spendMutationPresentationError
					var writeFailure *clioutput.WriteError
					require.ErrorAs(t, failure, &marker)
					require.ErrorAs(t, failure, &writeFailure)
					require.ErrorIs(t, failure, cause)
					require.Equal(t, cause.Error(), failure.Error())
					root := readableErrorTestCommand(t)
					contextual := withCommandError(command, failure)
					message := readableLocalErrorMessage(root, contextual)
					require.Contains(t, message, "The API returned a response, but the CLI could not display it.")
					require.Contains(t, message, "The change may have been applied.")
					require.Contains(t, message, "Check its current state before repeating the command.")
					require.Contains(t, message, "help "+strings.ReplaceAll(scope, ".", " ")+" "+operation.read)
					require.Contains(t, message, spendRecoveryContext)
					require.NotContains(t, message, "synthetic-private")
					require.NotContains(t, message, "confirmed")
					require.Equal(t, localErrorMessage(root, withCommandError(command, cause)), localErrorMessage(root, contextual))
					for _, interrupt := range []error{context.Canceled, context.DeadlineExceeded} {
						joined := withCommandError(command, errors.Join(failure, interrupt))
						require.ErrorIs(t, joined, cause)
						require.ErrorIs(t, joined, interrupt)
						require.Equal(t, localErrorMessage(root, joined), readableLocalErrorMessage(root, joined))
					}
				}
			})
		}
	}
}

func TestSpendErrorGuidancePresentationModesAndControls(t *testing.T) {
	command := spendGuidanceTestCommand("admin:organization:spend-alerts", "create")
	cause := errors.New("synthetic-private-output \x1b]52;c;secret\a")
	opts := ShowJSONOpts{Context: t.Context(), Operation: "(resource) admin.organization.spend_alerts > (method) create",
		OutputKind: OutputResponse, Format: "text", Stdout: failOutputWriter{cause}, Stderr: io.Discard}
	failure := withCommandError(command, ShowJSON(gjson.Parse(`{"id":"alert_synthetic"}`), opts))
	const machineMessage = "The command could not be completed. Check your arguments with --help."
	for _, flags := range [][]string{nil, {"--quiet"}, {"--format-error", "json"}, {"--format-error", "jsonl"}, {"--format-error", "text", "--transform-error", "message"}} {
		var out bytes.Buffer
		require.NoError(t, ShowCommandError(readableErrorTestCommand(t, flags...), failure, &out))
		if len(flags) == 0 || flags[0] == "--quiet" {
			require.Contains(t, out.String(), "Check its current state before repeating")
		} else if flags[1] == "text" {
			require.Equal(t, machineMessage+"\n", out.String())
		} else {
			var fields map[string]string
			require.NoError(t, json.Unmarshal(out.Bytes(), &fields))
			require.Equal(t, map[string]string{"message": machineMessage}, fields)
		}
		require.NotContains(t, out.String(), "synthetic-private")
		require.NotContains(t, out.String(), "\x1b")
	}
	for _, control := range []string{"read", "page", "error"} {
		other := opts
		switch control {
		case "read":
			other.Operation = "(resource) admin.organization.spend_alerts > (method) retrieve"
		case "page":
			other.OutputKind = OutputPageItem
		case "error":
			other.Operation = ""
		}
		var marker *spendMutationPresentationError
		err := ShowJSON(gjson.Parse(`{"id":"alert_synthetic"}`), other)
		require.ErrorIs(t, err, cause)
		require.False(t, errors.As(err, &marker), control)
	}
	opts.Stdout = failOutputWriter{syscall.EPIPE}
	err := ShowJSON(gjson.Parse(`{"id":"alert_synthetic"}`), opts)
	require.ErrorIs(t, err, syscall.EPIPE)
	require.True(t, clioutput.IsBrokenPipe(err), "preserve the existing closed-consumer policy")
	var partial bytes.Buffer
	opts.Stdout = spendPresentationWriter(func(value []byte) (int, error) {
		partial.Write(value[:1])
		return 1, cause
	})
	err = ShowJSON(gjson.Parse(`{"id":"alert_synthetic"}`), opts)
	require.ErrorIs(t, err, cause)
	require.Len(t, partial.Bytes(), 1, "preserve the accepted output prefix")
}
