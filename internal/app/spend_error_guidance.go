package app

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/urfave/cli/v3"
)

func commandErrorContext(root *cli.Command, failure error) *cli.Command {
	var contextual *commandError
	if errors.As(failure, &contextual) && contextual.command != nil {
		return contextual.command
	}
	return root
}

const spendRecoveryContext = "Keep the original API address, authentication, organization, and project options for any check."

// Response conversion happens after the mutation returned successfully. Keep
// that provenance separate from JSON errors raised while preparing a request.
type spendMutationResponseError struct{ cause error }

func (e *spendMutationResponseError) Error() string { return e.cause.Error() }
func (e *spendMutationResponseError) Unwrap() error { return e.cause }

func spendMutationResponseFailure(err error, opts ShowJSONOpts) error {
	if !isSpendMutationResponse(opts) {
		return err
	}
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &syntaxError) || errors.As(err, &typeError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return &spendMutationResponseError{cause: err}
	}
	return err
}

func isSpendMutationResponse(opts ShowJSONOpts) bool {
	if opts.OutputKind != OutputResponse {
		return false
	}
	switch opts.Operation {
	case "(resource) admin.organization.spend_limit > (method) update",
		"(resource) admin.organization.spend_limit > (method) delete",
		"(resource) admin.organization.projects.spend_limit > (method) update",
		"(resource) admin.organization.projects.spend_limit > (method) delete",
		"(resource) admin.organization.spend_alerts > (method) create",
		"(resource) admin.organization.spend_alerts > (method) update",
		"(resource) admin.organization.spend_alerts > (method) delete",
		"(resource) admin.organization.projects.spend_alerts > (method) create",
		"(resource) admin.organization.projects.spend_alerts > (method) update",
		"(resource) admin.organization.projects.spend_alerts > (method) delete",
		"(resource) admin.organization.projects.rate_limits > (method) update_rate_limit":
		return true
	default:
		return false
	}
}

// A status alone cannot establish configuration at an arbitrary API address.
// Recovery uses declared command metadata, never request URLs or response prose.
func readableSpendNotFoundMessage(root *cli.Command, failure error) string {
	command := commandErrorContext(root, failure)
	invocation := clihelp.DisplayInvocation(root)
	message := ""
	switch localcommands.ResourceName(command) {
	case "admin:organization:spend-limit":
		if command.Name == "retrieve" || command.Name == "delete" {
			message = "The organization spend limit was not returned.\n" +
				"Check the API address and organization. The limit may not be configured.\n" +
				"Limit options: " + invocation + " help admin organization spend-limit update"
		}
	case "admin:organization:projects:spend-limit":
		if command.Name == "retrieve" || command.Name == "delete" {
			message = "The project or its spend limit could not be found.\n" +
				"Check the project ID and access first.\n" +
				"Project lookup: " + invocation + " help admin organization projects retrieve\n" +
				"If the project exists, the limit may not be configured.\n" +
				"Limit options: " + invocation + " help admin organization projects spend-limit update"
		}
	case "admin:organization:spend-alerts", "admin:organization:projects:spend-alerts":
		if command.Name == "retrieve" || command.Name == "update" || command.Name == "delete" {
			path := "admin organization spend-alerts list"
			message = "The spend alert could not be found at this API address.\nCheck the alert ID against current organization alerts."
			if localcommands.ResourceName(command) == "admin:organization:projects:spend-alerts" {
				path = "admin organization projects spend-alerts list"
				message = "The project spend alert could not be found at this API address.\nCheck the project ID, access, and current project alerts."
			}
			message += "\nList options: " + invocation + " help " + path
		}
	}
	if message != "" {
		message += "\n" + spendRecoveryContext
	}
	return message
}

func readableOrganizationSpendLimitMessage(root *cli.Command) string {
	invocation := clihelp.DisplayInvocation(root)
	return "The API reports an organization spend limit was reached.\n" +
		"Ask an organization owner to inspect that limit.\n" +
		"An admin API key is required for the OpenAI limit lookup.\n" +
		"Limit lookup: " + invocation + " help admin organization spend-limit retrieve\n" +
		"Admin key setup: " + invocation + " help setup admin\n" +
		spendRecoveryContext
}

func readableSpendMutationFailure(root, command *cli.Command) string {
	path := ""
	change := "spend change"
	switch localcommands.ResourceName(command) {
	case "admin:organization:spend-limit":
		if command.Name == "update" || command.Name == "delete" {
			path = "admin organization spend-limit retrieve"
		}
	case "admin:organization:projects:spend-limit":
		if command.Name == "update" || command.Name == "delete" {
			path = "admin organization projects spend-limit retrieve"
		}
	case "admin:organization:spend-alerts", "admin:organization:projects:spend-alerts":
		prefix := "admin organization spend-alerts "
		if localcommands.ResourceName(command) == "admin:organization:projects:spend-alerts" {
			prefix = "admin organization projects spend-alerts "
		}
		switch command.Name {
		case "create":
			path = prefix + "list"
		case "update", "delete":
			path = prefix + "retrieve"
		}
	case "admin:organization:projects:rate-limits":
		if command.Name == "update-rate-limit" {
			path = "admin organization projects rate-limits list-rate-limits"
			change = "rate-limit change"
		}
	}
	if path == "" {
		return ""
	}
	return "The API response did not confirm the " + change + ".\n" +
		"The API may have received the change.\n" +
		"Check its current state before repeating the command.\n" +
		"Read options: " + clihelp.DisplayInvocation(root) + " help " + path + "\n" +
		spendRecoveryContext
}
