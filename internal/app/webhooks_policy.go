package app

import (
	"context"
	"errors"
	"io"
	"net/url"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/commands/webhooks"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

// Webhooks owns workflow state; assembly retains request and diagnostic policy.
func webhookCommandOptions() webhooks.CommandOptions {
	return webhooks.CommandOptions{
		PrepareDiscovery: func(command *cli.Command) ([]option.RequestOption, error) {
			return FlagOptions(command.Root(), apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, true)
		},
		DefaultRequestOptions:    GetDefaultRequestOptions,
		WriteHint:                writeWebhookHint,
		ReadableTransportFailure: readableWebhookTransportFailure,
	}
}

func writeWebhookHint(ctx context.Context, destination io.Writer, message string) error {
	return writeOutputHint(ShowJSONOpts{Context: ctx, Stderr: destination}, message)
}

func readableWebhookTransportFailure(command *cli.Command, failure error) (string, bool) {
	var transport *url.Error
	if command == nil || !errors.As(failure, &transport) || transport.Op != "Post" ||
		errorOutputFormat(command.Root()) != "text" || command.Root().String("transform-error") != "" {
		return "", false
	}
	// Known configuration failures precede dispatch and cannot imply creation.
	for cause := failure; cause != nil; cause = errors.Unwrap(cause) {
		if knownLocalError(command, cause.Error()) != "" {
			return "", false
		}
	}
	return localErrorMessage(command.Root(), failure), true
}
