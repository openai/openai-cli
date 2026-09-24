package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"
)

// Local diagnostics can contain rejected values, file names, and credentialed
// URLs. Classify them into local prose; terminal escaping is not redaction.
func localErrorMessage(root *cli.Command, failure error) string {
	var timeout net.Error
	var networkError *url.Error
	var connectionError *net.OpError
	var typeError *json.UnmarshalTypeError
	var syntaxError *json.SyntaxError
	var pathError *os.PathError
	switch {
	case errors.Is(failure, context.Canceled):
		return "Request canceled."
	case errors.Is(failure, context.DeadlineExceeded), errors.As(failure, &timeout) && timeout.Timeout():
		return "The request timed out. The API may have received it; check its status before repeating it."
	case errors.As(failure, &typeError):
		return "Could not decode JSON: a value has an unexpected type."
	case errors.As(failure, &syntaxError):
		return "Could not decode JSON: invalid JSON syntax."
	case errors.As(failure, &pathError):
		switch {
		case errors.Is(pathError, os.ErrNotExist):
			return "A local file could not be found. Check your file arguments and @file references."
		case errors.Is(pathError, os.ErrPermission):
			return "A local file could not be accessed. Check its permissions and your file arguments."
		default:
			return "A local file could not be read or written. Check your file arguments and permissions."
		}
	}
	command := root
	var contextual *commandError
	if errors.As(failure, &contextual) && contextual.command != nil {
		command = contextual.command
	}
	for err := failure; err != nil; err = errors.Unwrap(err) {
		if message := knownLocalError(command, err.Error()); message != "" {
			return message
		}
	}
	// Transport wrappers can contain a vetted local configuration error, such
	// as the mTLS proxy restriction. Keep that guidance before this fallback.
	if errors.As(failure, &networkError) || errors.As(failure, &connectionError) {
		return "Could not connect to the API. Check your connection, proxy, and --base-url setting."
	}
	// The usage buffer is deliberately not a fallback: it repeats raw values.
	return "The command could not be completed. Check your arguments with --help."
}

func knownLocalError(command *cli.Command, message string) string {
	switch message {
	case "mTLS client certificate and key files must be configured together",
		"mTLS requires an explicit HTTPS base URL",
		"mTLS client certificate and key must be a valid matching PEM pair",
		"mTLS requires the default HTTP transport to be an *http.Transport",
		"mTLS does not support HTTPS proxies",
		"mTLS client certificate file does not exist", "mTLS client key file does not exist",
		"mTLS client certificate file is not readable", "mTLS client key file is not readable",
		"could not read mTLS client certificate file", "could not read mTLS client key file",
		"cannot follow HTTP 307 redirect: streamed multipart uploads are not replayable",
		"cannot follow HTTP 308 redirect: streamed multipart uploads are not replayable",
		"stdin has already been read by another parameter; it can only be read once",
		"cannot read from stdin: stdin is already being used for the request body",
		"cannot read from stdin: stdin was already consumed by piped YAML/JSON input",
		"Setup help takes no additional arguments.",
		"COMPLETION_STYLE must be set to 'bash', 'zsh', 'pwsh', or 'fish'",
		"COMPLETION_STYLE must be set to 'bash', 'zsh', 'pwsh', 'fish'":
		return message
	}
	for _, source := range []string{"OPENAI_BASE_URL", "--base-url"} {
		if tail, ok := afterQuotedValue(message, source+" "); ok && tail == " is missing a scheme (expected http:// or https://)" {
			return source + " must start with http:// or https://."
		}
	}
	if tail, ok := afterQuotedValue(message, "invalid OPENAI_UNTRUSTED_STDIN value "); ok && tail == ": expected a boolean" {
		return "OPENAI_UNTRUSTED_STDIN: expected a boolean (true or false)."
	}
	if tail, ok := afterQuotedValue(message, "file input "); ok {
		const guidance = " from piped YAML/JSON is disabled when OPENAI_UNTRUSTED_STDIN is enabled; provide --"
		if rest, ok := strings.CutPrefix(tail, guidance); ok {
			if name, ok := strings.CutSuffix(rest, " explicitly"); ok {
				if flag := declaredErrorFlag(command, name); flag != "" {
					return "File input from piped YAML/JSON is disabled when OPENAI_UNTRUSTED_STDIN is enabled; provide " + flag + " explicitly."
				}
			}
		}
	}
	if rest, ok := strings.CutPrefix(message, "header "); ok {
		index, reason, found := strings.Cut(rest, ": ")
		n, err := strconv.Atoi(index)
		if found && err == nil && n > 0 {
			switch reason {
			case "expected 'Name: Value'", "name must not be empty", "invalid character in name", "invalid control character in value":
				return fmt.Sprintf("header %d: %s", n, reason)
			}
		}
	}
	if tail, ok := afterQuotedValue(message, "invalid value "); ok {
		if rest, ok := strings.CutPrefix(tail, " for flag -"); ok {
			name, _, found := strings.Cut(rest, ": ")
			if flag := declaredErrorFlag(command, name); found && flag != "" {
				return "Invalid value for " + flag + ". Check the expected type and supported values with --help."
			}
		}
	}
	if rest, ok := strings.CutPrefix(message, "flag needs an argument: "); ok {
		if flag := declaredErrorFlag(command, strings.TrimLeft(rest, "-")); flag != "" {
			return "Add a value for " + flag + "."
		}
	}
	// Required flag names originate in declarations, but verify them before
	// emitting any part of this otherwise untyped diagnostic.
	firstLine, _, _ := strings.Cut(message, "\n")
	for _, prefix := range []string{"Required flag ", "Required flags "} {
		if rest, ok := strings.CutPrefix(firstLine, prefix); ok {
			quoted, err := strconv.QuotedPrefix(rest)
			if err != nil || rest[len(quoted):] != " not set" {
				continue
			}
			names, _ := strconv.Unquote(quoted)
			var flags []string
			for _, name := range strings.Split(names, ", ") {
				flag := declaredErrorFlag(command, name)
				if flag == "" {
					return ""
				}
				flags = append(flags, flag)
			}
			return "Missing required options: " + strings.Join(flags, ", ") + ". Check --help for usage."
		}
	}
	switch {
	case strings.HasPrefix(message, "flag provided but not defined: -"):
		return "An option is not recognized. Check the command's available options with --help."
	case strings.HasPrefix(message, "No help topic for '"):
		return unknownCommandErrorMessage(command)
	case strings.HasPrefix(message, "Unknown help topic "):
		return "Unknown help topic. Run openai help --all to see commands."
	case strings.HasPrefix(message, "Failed to parse piped data as YAML/JSON:\n"):
		return "Could not parse piped input as YAML or JSON. Check the input's syntax."
	case strings.HasPrefix(message, "Cannot merge flags with a body that is not a map:"),
		strings.HasPrefix(message, "Cannot send a non-map value to a form-encoded endpoint:"):
		return "The request body must be a JSON or YAML object. Check the piped input and request flags."
	case strings.HasPrefix(message, "Unsupported body for application/octet-stream:"):
		return "The request body is not supported for this binary endpoint. Check the command's input options with --help."
	}
	return ""
}

func unknownCommandErrorMessage(command *cli.Command) string {
	// Reuse the matcher with parsed arguments, never a suggestion copied from
	// the error string. It emits only names from the local command declarations.
	if command != nil {
		command = command.Root()
	}
	for command != nil && command.Args() != nil && command.Args().Present() {
		name := command.Args().First()
		if next := command.Command(name); next != nil {
			command = next
			continue
		}
		if command.Suggest {
			if suggestion := suggestCommand(command.Commands, name); suggestion != "" {
				return "Unknown help topic. " + suggestion
			}
		}
		break
	}
	return "Unknown help topic. Run openai help --all to see commands."
}

func afterQuotedValue(message, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(message, prefix)
	if !ok {
		return "", false
	}
	quoted, err := strconv.QuotedPrefix(rest)
	if err != nil {
		return "", false
	}
	return rest[len(quoted):], true
}

func declaredErrorFlag(command *cli.Command, name string) string {
	if command == nil {
		return ""
	}
	for _, ancestor := range command.Lineage() {
		for _, flag := range ancestor.Flags {
			names := flag.Names()
			for _, candidate := range names {
				if candidate == name {
					prefix := "--"
					if len(names[0]) == 1 {
						prefix = "-"
					}
					return prefix + names[0]
				}
			}
		}
	}
	return ""
}
