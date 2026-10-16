package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

func registerAuthCommands(root *cli.Command) {
	if root.Command("auth") != nil {
		return
	}
	help := `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}EXAMPLE
  {{$bin}} auth status

Shows configured authentication without sending requests or verifying access.

` + cli.CommandHelpTemplate + localUtilityGlobalHelp
	root.Commands = append(root.Commands, &cli.Command{
		Name: "auth", Usage: "Inspect authentication configuration locally", HideHelpCommand: true,
		Metadata: map[string]any{localUtilityMetadata: true, "help-command-section": "Local tools"},
		Commands: []*cli.Command{{
			Name: "status", Usage: "Show effective authentication configuration without network access",
			Description: "Reports API and Admin credentials separately, plus organization, project, and a sanitized base URL. " +
				"Does not verify credentials, identity, permissions, or membership. Does not read stdin, refresh credentials, or configure your shell. " +
				"Supports --format auto, text, or json. Credential values are never displayed.",
			HideHelpCommand: true, CustomHelpTemplate: help,
			Metadata: map[string]any{"local-help-full": help}, Action: handleAuthStatus,
		}},
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
		return &localUtilityError{message: "Auth status takes no positional arguments. Use openai auth status --help."}
	}
	if root.IsSet("transform") || root.IsSet("raw-output") {
		return &localUtilityError{message: "Auth status does not support --transform or --raw-output. Use --format text or json."}
	}
	format := strings.ToLower(root.String("format"))
	if format != "auto" && format != "text" && format != "json" {
		return &localUtilityError{message: "Auth status supports --format auto, text, or json."}
	}
	report, err := resolveAuthStatus(root, os.LookupEnv)
	if err != nil {
		return err
	}
	out := outputWriter{ctx: ctx, out: root.Writer}
	if format == "json" {
		err = json.NewEncoder(out).Encode(report)
	} else {
		endpoint := authStatusTextValue(report.BaseURL.authStatusValue)
		if report.BaseURL.Redacted {
			endpoint += " (URL components omitted)"
		}
		_, err = fmt.Fprintf(out, "Authentication configuration (offline; not verified)\n"+
			"API credential: %s [%s]\nAdmin credential: %s [%s]\n"+
			"Organization: %s [%s]\nProject: %s [%s]\nBase URL: %s [%s]\n",
			strings.ReplaceAll(report.API.Type, "_", " "), report.API.Source,
			strings.ReplaceAll(report.Admin.Type, "_", " "), report.Admin.Source,
			authStatusTextValue(report.Organization), report.Organization.Source,
			authStatusTextValue(report.Project), report.Project.Source,
			endpoint, report.BaseURL.Source)
	}
	if err != nil {
		return &localUtilityError{message: "Could not write auth status. Check the output file or pipe before rerunning.", cause: err}
	}
	return nil
}

func authStatusTextValue(value authStatusValue) string {
	if value.Source == "unset" {
		return "(unset)"
	}
	if value.Value == "" {
		return "(empty)"
	}
	// Report fields occupy one row; preserve line breaks and tabs as visible escapes.
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(readable.Text(value.Value))
}

// Match the CLI and SDK option layers without constructing an SDK client or request.
// Snapshot relevant environment inputs once; never enumerate unrelated secrets.
func resolveAuthStatus(root *cli.Command, lookup func(string) (string, bool)) (authStatusReport, error) {
	environment := snapshotContextEnvironment(root, lookup)
	literal, err := requestHeaders(root)
	if err != nil {
		return authStatusReport{}, &localUtilityError{message: "Invalid --header. Use 'Name: Value' without control characters."}
	}
	inherited := inheritedContextHeaders(environment)
	setting := func(flag, variable string) authStatusValue {
		return requestContextSetting(root, environment, flag, variable)
	}
	resolved, err := resolveRequestContext(requestResolutionContext(root), root, environment, literal)
	if err != nil {
		return authStatusReport{}, err
	}
	base := resolved.BaseURL
	endpoint, basic, err := authStatusBaseURL(base)
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
		if !result.Configured && basic {
			return authStatusCredential{Configured: true, Type: "url_basic_auth", Source: base.Source}
		}
		return result
	}
	return authStatusReport{
		Offline:      true,
		API:          credential("api-key", "OPENAI_API_KEY", "api_key"),
		Admin:        credential("admin-api-key", "OPENAI_ADMIN_KEY", "admin_api_key"),
		Organization: resolved.Organization,
		Project:      resolved.Project, BaseURL: endpoint,
	}, nil
}

func authStatusBaseURL(base authStatusValue) (authStatusEndpoint, bool, error) {
	if base.Value == "" {
		return authStatusEndpoint{authStatusValue: base}, false, nil
	}
	parsed, err := url.Parse(base.Value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return authStatusEndpoint{}, false, &localUtilityError{message: "Configured base URL is invalid. Use an http:// or https:// URL."}
	}
	redacted := parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != ""
	// Custom paths can contain credentials. Display only the origin and known public API paths.
	path := parsed.Path
	if path == "" {
		path = "/"
	}
	if path != "/" && path != "/v1" && path != "/v1/" {
		path, redacted = "/", true
	}
	safe := (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: path}).String()
	return authStatusEndpoint{authStatusValue: authStatusValue{Value: safe, Source: base.Source}, Redacted: redacted}, parsed.User != nil, nil
}

// Identify local context commands before optional shell setup can spawn a worker.
// Consume root flag values by declaration, so a request value named "auth" does not match.
func skipContextFirstRunSetup(root *cli.Command, args []string) bool {
	if root == nil || len(args) < 2 || root.Command("auth") == nil && root.Command("profile") == nil {
		return false
	}
	lineage := []*cli.Command{root}
	flags, help := audioSetupParsesFlags(root), false
	for index := 1; index < len(args); index++ {
		arg := args[index]
		if flags && strings.TrimSpace(arg) == "--" {
			flags = false
			continue
		}
		if flags && strings.HasPrefix(strings.TrimSpace(arg), "-") {
			token := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(arg), "-"), "-")
			name, _, assigned := strings.Cut(token, "=")
			shape, ok := audioSetupFlag(lineage, name).(interface{ TakesValue() bool })
			if !ok {
				return false
			}
			if shape.TakesValue() && !assigned {
				index++
			}
			continue
		}
		if arg == "help" && !help {
			help = true
			if declared := root.Command("help"); declared != nil {
				lineage = append(lineage, declared)
				flags = audioSetupParsesFlags(declared)
			}
			continue
		}
		selected := root.Command(arg)
		return selected != nil && (selected == root.Command("auth") || selected == root.Command("profile"))
	}
	return false
}
