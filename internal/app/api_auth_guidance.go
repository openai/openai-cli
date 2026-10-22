package app

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-go/v3"
	"github.com/urfave/cli/v3"
)

const apiAuthenticationKeyURL = "https://platform.openai.com/settings/organization/api-keys"
const apiAuthenticationIPAllowlistURL = "https://developers.openai.com/api/docs/guides/ip-allowlist"

// Human guidance names configuration sources, never their values. The existing
// presenter retains status, request IDs, structured data and error identity.
func readableAPIAuthenticationMessage(root *cli.Command, failure error, apierr *openai.Error, output ...io.Writer) string {
	if apierr == nil || apierr.StatusCode != http.StatusUnauthorized {
		return ""
	}
	admin := apiAuthenticationAdminCommand(root, failure)
	flag, variable, label := "api-key", "OPENAI_API_KEY", "API key"
	if admin {
		flag, variable, label = "admin-api-key", "OPENAI_ADMIN_KEY", "admin API key"
	}
	custom := apiAuthenticationCustomBase(root)
	if apierr.Code == "ip_not_authorized" {
		lines := []string{"The server rejected this request under an IP access rule.",
			"Next: ask an administrator to check your outbound IP in the allowlist."}
		if !custom {
			lines = append(lines, "IP allowlist: "+apiAuthenticationIPAllowlistURL)
		}
		return strings.Join(lines, "\n")
	}
	if message := readableAPIAccessPermissionMessage(root, failure, apierr); message != "" {
		return message
	}
	message := "The server did not accept authentication."
	if custom {
		message = "Authentication failed for the custom API address."
	}
	credential, customAuthorization := apiAuthenticationCredentialSource(root, flag, variable, label)
	lines := []string{message, credential}
	switch {
	case customAuthorization:
		lines = append(lines, "Next: correct or remove this Authorization override.")
	case root.IsSet(flag) && root.Count(flag) > 0 && root.String(flag) == "":
		lines = append(lines, "Next: remove the empty --"+flag+" override.", "Then set "+variable+" in this shell.")
	case custom:
		lines = append(lines, "Next: set the credential required by this API endpoint.")
	case !apiAuthenticationKeyPresent(root, flag, variable):
		if admin {
			lines = append(lines, "Next: set OPENAI_ADMIN_KEY to an organization admin API key.")
		} else {
			lines = append(lines, "Next: set OPENAI_API_KEY in this shell.")
		}
	case admin:
		lines = append(lines, "Next: ask an organization owner to check this admin key's access.")
	default:
		lines = append(lines, "Next: check this key's status and access in API key settings.")
	}
	if !custom && !customAuthorization && !admin {
		lines = append(lines, "API keys: "+apiAuthenticationKeyURL)
	}
	lines = append(lines, "Key setup: "+clihelp.SuggestedCommand(root, output, clihelp.DisplayInvocation(root)+" help setup"))
	return strings.Join(lines, "\n")
}

// Recognize the API's permission message without echoing its prose or unknown
// scope names. Status alone does not establish why authentication failed.
func readableAPIAccessPermissionMessage(root *cli.Command, failure error, apierr *openai.Error) string {
	if apierr == nil || apierr.StatusCode != http.StatusUnauthorized && apierr.StatusCode != http.StatusForbidden {
		return ""
	}
	const prefix = "You have insufficient permissions for this operation."
	var scopes string
	if apierr.Message != prefix {
		rest, ok := strings.CutPrefix(apierr.Message, prefix+" Missing scopes: ")
		if !ok {
			return ""
		}
		scopes, _, ok = strings.Cut(rest, ". Check that ")
		if !ok {
			scopes, ok = strings.CutSuffix(rest, ".")
			if !ok {
				return ""
			}
		}
	}
	read, write := false, false
	for scope := range strings.SplitSeq(scopes, ", ") {
		switch scope {
		case "api.webhooks.read":
			read = true
		case "api.webhooks.write":
			write = true
		}
	}
	lines := []string{"The server denied this operation because required permissions are missing."}
	if read {
		lines = append(lines, "Missing permission: api.webhooks.read.")
	}
	if write {
		lines = append(lines, "Missing permission: api.webhooks.write.")
	}
	admin := apiAuthenticationAdminCommand(root, failure)
	flag, variable, label := "api-key", "OPENAI_API_KEY", "API key"
	if admin {
		flag, variable, label = "admin-api-key", "OPENAI_ADMIN_KEY", "admin API key"
	}
	credential, override := apiAuthenticationCredentialSource(root, flag, variable, label)
	switch {
	case apiAuthenticationCustomBase(root):
		lines = append(lines, "Next: check this credential's permissions and your role for this API endpoint.")
	case override:
		lines = append(lines, "Next: check the overridden credential's permissions and your organization and project roles.", credential)
	case admin:
		lines = append(lines, "Next: ask an organization owner to check this admin key's permissions and your role.")
	default:
		lines = append(lines, "Next: check this key's access and your organization and project roles.",
			"API keys: "+apiAuthenticationKeyURL,
			"Select the intended project before checking this key.")
		if read || write {
			lines = append(lines, "Read only may omit webhook permissions.",
				"Restricted may not offer a Webhooks permission.",
				"All grants broader permissions within your organization and project roles. Select it only deliberately.")
		}
	}
	return strings.Join(lines, "\n")
}

func apiAuthenticationAdminCommand(root *cli.Command, failure error) bool {
	var contextual *commandError
	var command *cli.Command
	if errors.As(failure, &contextual) {
		command = contextual.command
	}
	if command == nil {
		command, _, _ = parsedOutputCommand(root)
	}
	if command == nil {
		return false
	}
	// Resource metadata follows the generated admin commands into nested groups
	// and the projects shortcut. Credential availability cannot select a route.
	resource := localcommands.ResourceName(command)
	if strings.HasPrefix(resource, "admin:") {
		return true
	}
	for _, ancestor := range command.Lineage() {
		if ancestor != root && (ancestor.Name == "admin" || strings.HasPrefix(ancestor.Name, "admin:")) {
			return true
		}
	}
	return false
}

func apiAuthenticationKeyPresent(root *cli.Command, flag, variable string) bool {
	if root.IsSet(flag) {
		return root.String(flag) != ""
	}
	return os.Getenv(variable) != ""
}

func apiAuthenticationCredentialSource(root *cli.Command, flag, variable, label string) (string, bool) {
	for _, header := range root.StringSlice("header") {
		if colon := strings.IndexByte(header, ':'); colon >= 0 && strings.EqualFold(header[:colon], "Authorization") {
			return "An Authorization override from --header controls authentication.", true
		}
	}
	// Environment headers precede GetDefaultRequestOptions. Its nonempty key
	// for this endpoint replaces their Authorization header; an empty key does
	// not. Inspect names only, matching the SDK's environment header parser.
	if !root.IsSet(flag) || root.String(flag) == "" {
		for line := range strings.SplitSeq(os.Getenv("OPENAI_CUSTOM_HEADERS"), "\n") {
			if colon := strings.IndexByte(line, ':'); colon >= 0 && strings.EqualFold(strings.TrimSpace(line[:colon]), "Authorization") {
				return "An Authorization header from OPENAI_CUSTOM_HEADERS controls authentication.", true
			}
		}
	}
	if root.IsSet(flag) && root.Count(flag) > 0 && root.String(flag) == "" {
		return "The --" + flag + " override is empty; it disables the " + variable + " key.", false
	}
	if !apiAuthenticationKeyPresent(root, flag, variable) {
		return "No " + label + " is configured.\nCredential settings: --" + flag + " / " + variable + ".", false
	}
	if root.IsSet(flag) {
		if os.Getenv(variable) == "" {
			return "Credential source: --" + flag + ".\nIt takes precedence over " + variable + ".", false
		}
		// PostParse calls Set for environment values too. Count cannot prove
		// whether a nonempty value came from a flag or its environment source.
		return "An " + label + " is configured through --" + flag + " or " + variable + ".\n" +
			"An explicit --" + flag + " takes precedence.", false
	}
	return "Credential source: " + variable + ". An explicit --" + flag + " takes precedence.", false
}

func apiAuthenticationCustomBase(root *cli.Command) bool {
	base := root.String("base-url")
	if base == "" {
		base = os.Getenv("OPENAI_BASE_URL")
	}
	if base == "" {
		return false
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Port() != "" && parsed.Port() != "443" || parsed.Path != "/v1" && parsed.Path != "/v1/" {
		return true
	}
	// These API hosts are declared by the current SDK's endpoint options.
	switch strings.ToLower(parsed.Hostname()) {
	case "api.openai.com", "us.api.openai.com", "eu.api.openai.com", "ae.api.openai.com", "mtls.api.openai.com":
		return false
	}
	return true
}
