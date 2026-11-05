// Package auth owns offline authentication inspection commands.
package auth

import (
	"context"
	"encoding/json"
	"fmt"

	"net/url"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/request"
	"github.com/urfave/cli/v3"
)

// Register adds the offline status command alongside any existing auth commands.
func Register(root *cli.Command) {
	group := root.Command("auth")
	if group == nil {
		group = &cli.Command{
			Name: "auth", Usage: "Inspect authentication configuration locally", HideHelpCommand: true,
			Metadata: map[string]any{commands.LocalUtilityMetadata: true, "help-command-section": "Local tools"},
		}
		root.Commands = append(root.Commands, group)
	}
	if group.Command("status") != nil {
		return
	}
	const description = "Reports API and Admin credential sources, organization, project, and a sanitized base URL. " +
		"Does not verify credentials, identity, permissions, or membership. " +
		"Does not read stdin, refresh credentials, or configure your shell. Supports --format auto, text, or json."
	group.Commands = append(group.Commands, &cli.Command{
		Name: "status", Usage: "Show effective authentication configuration without network access",
		Description: description, HideHelpCommand: true, Action: handleAuthStatus,
		Metadata: map[string]any{
			commands.LocalUtilityMetadata: true,
			"help-content": clihelp.Content{
				Description: description,
				Examples:    []clihelp.Example{{Description: "Show the local configuration.", Command: "auth status"}},
				Notes:       "Credential values are never displayed.",
			},
		},
	})
}

type authStatusCredential struct {
	Configured bool   `json:"configured"`
	Type       string `json:"type"`
	Source     string `json:"source"`
}

type authStatusValue struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

type authStatusEndpoint struct {
	authStatusValue
	Redacted bool `json:"redacted"`
}

// Only sanitized configuration reaches either renderer. Credentials never enter this report.
type authStatusReport struct {
	Offline      bool                 `json:"offline"`
	Verified     bool                 `json:"verified"`
	API          authStatusCredential `json:"api_credential"`
	Admin        authStatusCredential `json:"admin_credential"`
	Organization authStatusValue      `json:"organization"`
	Project      authStatusValue      `json:"project"`
	BaseURL      authStatusEndpoint   `json:"base_url"`
}

func handleAuthStatus(ctx context.Context, command *cli.Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root := command.Root()
	if command.Args().Present() {
		return &commands.LocalError{Message: "Auth status takes no positional arguments. Use " + clihelp.DisplayInvocation(root) + " auth status --help."}
	}
	if root.IsSet("transform") || root.IsSet("raw-output") {
		return &commands.LocalError{Message: "Auth status does not support --transform or --raw-output. Use --format text or json."}
	}
	format := strings.ToLower(root.String("format"))
	if format != "auto" && format != "text" && format != "json" {
		return &commands.LocalError{Message: "Auth status supports --format auto, text, or json."}
	}
	report, err := resolveAuthStatus(ctx, root, os.LookupEnv)
	if err != nil {
		return err
	}
	out := output.Writer{Context: ctx, Sink: root.Writer}
	if format == "json" {
		err = json.NewEncoder(out).Encode(report)
	} else {
		endpoint := authStatusTextValue(report.BaseURL.authStatusValue)
		if report.BaseURL.Redacted {
			endpoint += " (URL components omitted)"
		}
		_, err = fmt.Fprintf(out, "Authentication configuration (offline; not verified)\n"+
			"API credential: %s [%s]\nAdmin credential: %s [%s]\n"+
			"Organization: %s\nProject: %s\nBase URL: %s [%s]\n",
			strings.ReplaceAll(report.API.Type, "_", " "), report.API.Source,
			strings.ReplaceAll(report.Admin.Type, "_", " "), report.Admin.Source,
			authStatusContextText(report.Organization), authStatusContextText(report.Project),
			endpoint, report.BaseURL.Source)
	}
	if err != nil {
		return &commands.LocalError{Message: "Could not write auth status. Check the output file or pipe before rerunning.", Cause: err}
	}
	return nil
}

func authStatusContextText(value authStatusValue) string {
	if value.Source == "unset" {
		return "not set in CLI"
	}
	return authStatusTextValue(value) + " [" + readable.Text(value.Source) + "]"
}

func authStatusTextValue(value authStatusValue) string {
	if value.Value == "" {
		return "(empty)"
	}
	// Report fields occupy one row; preserve line breaks and tabs as visible escapes.
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(readable.Text(value.Value))
}

// Match the CLI and SDK option layers without constructing an SDK client or request.
// Snapshot the relevant environment inputs once; never enumerate unrelated secrets.
func resolveAuthStatus(ctx context.Context, root *cli.Command, lookup func(string) (string, bool)) (authStatusReport, error) {
	environment := make(map[string]authStatusValue, 8)
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_WEBHOOK_SECRET", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID", "OPENAI_BASE_URL", "OPENAI_CUSTOM_HEADERS", "OPENAI_PROFILE"} {
		if value, present := lookup(name); present {
			environment[name] = authStatusValue{Value: value, Source: "environment:" + name}
		}
	}
	literal, err := request.Headers(root)
	if err != nil {
		return authStatusReport{}, &commands.LocalError{Message: "Invalid --" + err.Error() + ".", Cause: err}
	}
	inherited := request.EnvironmentHeaders(environment["OPENAI_CUSTOM_HEADERS"].Value)
	setting := func(flag, variable string) authStatusValue {
		if root.IsSet(flag) {
			value := root.String(flag)
			if request.ExplicitRootFlag(root, flag) {
				return authStatusValue{Value: value, Source: "flag:--" + flag}
			}
			if value != "" || environment[variable].Source != "" {
				return authStatusValue{Value: value, Source: "environment:" + variable}
			}
		}
		if env, present := environment[variable]; present {
			return env
		}
		return authStatusValue{Source: "unset"}
	}
	base := environment["OPENAI_BASE_URL"]
	baseSource := "OPENAI_BASE_URL"
	if base.Source == "" {
		base = authStatusValue{Value: "https://api.openai.com/v1/", Source: "default"}
	}
	if value := root.String("base-url"); value != "" {
		base = authStatusValue{Value: value, Source: "flag:--base-url"}
		baseSource = "--base-url"
	}
	endpoint, userinfo, err := authStatusBaseURL(base, baseSource)
	if err != nil {
		return authStatusReport{}, err
	}
	credential := func(flag, variable, kind string) authStatusCredential {
		key := setting(flag, variable)
		result := authStatusCredential{Type: "none", Source: key.Source}
		switch {
		case literal.Values("Authorization") != nil:
			result.Source = "flag:--header"
			if literal.Get("Authorization") != "" {
				result.Type, result.Configured = "authorization_header", true
			}
		case key.Value != "":
			result.Type, result.Configured = kind, true
		case inherited.Values("Authorization") != nil:
			result.Source = "environment:OPENAI_CUSTOM_HEADERS"
			if inherited.Get("Authorization") != "" {
				result.Type, result.Configured = "authorization_header", true
			}
		}
		// net/http supplies Basic authorization from URL user information only when Authorization is empty.
		if !result.Configured && userinfo != nil {
			return authStatusCredential{Configured: true, Type: "url_basic_auth", Source: base.Source}
		}
		return result
	}
	env := func(key string) (string, bool) { value, present := environment[key]; return value.Value, present }
	name, profile, err := request.SelectedProfile(ctx, root, env)
	if err != nil {
		return authStatusReport{}, err
	}
	selection := request.ResolveProjectContext(root, env, literal, inherited, name, profile)
	report := authStatusReport{
		Offline:      true,
		API:          credential("api-key", "OPENAI_API_KEY", "api_key"),
		Admin:        credential("admin-api-key", "OPENAI_ADMIN_KEY", "admin_api_key"),
		Organization: authStatusValue(selection.Organization),
		Project:      authStatusValue(selection.Project),
		BaseURL:      endpoint,
	}
	secrets := request.ConfiguredCredentials(root, env)
	report.Organization.redact(secrets)
	report.Project.redact(secrets)
	if report.BaseURL.authStatusValue.redact(secrets) {
		report.BaseURL.Redacted = true
	}
	return report, nil
}

func (field *authStatusValue) redact(secrets []string) bool {
	if name, ok := strings.CutPrefix(field.Source, "profile:"); ok && request.ContainsCredential(name, secrets) {
		field.Source = "profile:[redacted]"
	}
	if request.ContainsCredential(field.Value, secrets) {
		field.Value = "[redacted]"
		return true
	}
	return false
}

func authStatusBaseURL(base authStatusValue, source string) (authStatusEndpoint, *url.Userinfo, error) {
	if err := request.ValidateBaseURL(base.Value, source); err != nil {
		// The missing-scheme error contains the supplied value. Expose only known, value-free diagnostics.
		message := source + " must start with http:// or https://."
		switch err.Error() {
		case source + request.BaseURLHTTPSRequired, source + request.BaseURLHostRequired:
			message = err.Error() + "."
		}
		return authStatusEndpoint{}, nil, &commands.LocalError{Message: message}
	}
	if base.Value == "" {
		return authStatusEndpoint{authStatusValue: base}, nil, nil
	}
	parsed, _ := url.Parse(base.Value) // ValidateBaseURL has already parsed the same string.
	redacted := parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != ""
	// Custom paths can contain credentials. Display only the origin and known public API paths.
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if path != "/" && path != "/v1" && path != "/v1/" {
		path, redacted = "/", true
	}
	safe := (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: path}).String()
	return authStatusEndpoint{authStatusValue: authStatusValue{Value: safe, Source: base.Source}, Redacted: redacted}, parsed.User, nil
}
