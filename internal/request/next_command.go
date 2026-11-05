package request

import (
	"context"
	"net/url"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/terminal"
	"github.com/urfave/cli/v3"
)

// NextCommandOptions reproduces request selection without copying credentials into terminal output.
func NextCommandOptions(root *cli.Command) (args []string, omit bool) {
	if root.IsSet("header") {
		return nil, true
	}
	for _, option := range [][2]string{
		{"api-key", "OPENAI_API_KEY"},
		{"admin-api-key", "OPENAI_ADMIN_KEY"},
		{"webhook-secret", "OPENAI_WEBHOOK_SECRET"},
		{MTLSClientCertFileFlag, MTLSClientCertFileEnv},
		{MTLSClientKeyFileFlag, MTLSClientKeyFileEnv},
	} {
		// The copied command can reuse inherited environment values. Any effective
		// override requires a secret or private path that the receipt must not print.
		if root.IsSet(option[0]) && root.String(option[0]) != os.Getenv(option[1]) {
			return nil, true
		}
	}
	secrets := ConfiguredCredentials(root, os.LookupEnv)
	selection, configured := ConfiguredProjectContext(root)
	usesSavedValues := strings.HasPrefix(selection.Project.Source, "profile:") || strings.HasPrefix(selection.Organization.Source, "profile:")
	// An initially unset field must stay unset if a profile becomes active before paste.
	unsetField := selection.Project.Source == "unset" || selection.Organization.Source == "unset"
	if root.IsSet("profile") || usesSavedValues || unsetField {
		name := root.String("profile")
		if !utf8.ValidString(name) || strings.ContainsRune(name, 0) || ContainsCredential(name, secrets) {
			return nil, true
		}
		if configured {
			// The request already resolved this profile. Pasting the receipt must still
			// select the same values after a profile is edited, activated, or deleted.
			name = ""
		}
		args = append(args, "--profile="+name)
	}
	for _, setting := range []struct {
		name  string
		field ContextValue
	}{
		{"project", selection.Project}, {"organization", selection.Organization}, {"base-url", ContextValue{}},
	} {
		name := setting.name
		value := root.String(name)
		if configured && setting.field.Source != "" && setting.field.Source != "unset" {
			value = setting.field.Value
		} else if !root.IsSet(name) {
			continue
		}
		if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || ContainsCredential(value, secrets) {
			return nil, true
		}
		if name == "base-url" && value != "" {
			endpoint, err := url.Parse(value)
			if err != nil || strings.IndexFunc(value, unicode.IsControl) >= 0 || endpoint.Host == "" || endpoint.User != nil ||
				ContainsCredential(endpoint.Path, secrets) || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" ||
				(endpoint.Scheme != "http" && endpoint.Scheme != "https") {
				return nil, true
			}
		}
		args = append(args, "--"+name+"="+value)
	}
	return args, false
}

// NextCommandSuffix quotes non-secret request settings for a runnable suggestion.
func NextCommandSuffix(ctx context.Context, root *cli.Command) (string, bool) {
	options, omit := NextCommandOptions(root)
	if omit {
		return "", true
	}
	for i, arg := range options {
		// Typical IDs and URLs have the same literal spelling in all supported shells.
		if plain, _ := terminal.QuoteProperties(strings.ReplaceAll(arg, "=", "")); plain {
			continue
		}
		quote := terminal.ShellQuoter(terminal.ParentShell(ctx))
		if quote == nil {
			return "", true
		}
		options[i] = quote(arg)
	}
	if len(options) == 0 {
		return "", false
	}
	return " " + strings.Join(options, " "), false
}
