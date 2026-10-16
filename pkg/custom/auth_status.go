package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

func registerAuthCommands(root *cli.Command) {
	if root.Command("auth") != nil {
		return
	}
	help := `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}EXAMPLE
  {{$bin}} auth status

Shows configured authentication offline by default.
Use --verify --credential-kind api or admin to look up one authenticated principal.

` + cli.CommandHelpTemplate + localUtilityGlobalHelp
	root.Commands = append(root.Commands, &cli.Command{
		Name: "auth", Usage: "Inspect authentication configuration and identity", HideHelpCommand: true,
		Metadata: map[string]any{localUtilityMetadata: true, "help-command-section": "Local tools"},
		Commands: []*cli.Command{{
			Name: "status", Usage: "Show authentication configuration; optionally resolve one identity",
			Description: "Reports API and Admin credentials separately, plus organization, project, and a sanitized base URL. " +
				"Identity stays unknown unless --verify selects an API or Admin credential. Identity lookup does not verify permissions or membership. " +
				"Does not read stdin, refresh credentials, or configure your shell. " +
				"Supports --format auto, text, or json. Credential values are never displayed.",
			Flags: []cli.Flag{
				&cli.BoolFlag{Name: "verify", Usage: "Resolve one authenticated principal using the OpenAI identity provider"},
				&authStatusCredentialKindFlag{StringFlag: &cli.StringFlag{Name: "credential-kind", Usage: "Credential to resolve with --verify: api or admin"}},
			},
			HideHelpCommand: true, CustomHelpTemplate: help,
			Metadata: map[string]any{"local-help-full": help}, Action: handleAuthStatus,
		}},
	})
}

// urfave keeps presence across command reuse; verification must be explicit each time.
type authStatusCredentialKindFlag struct{ *cli.StringFlag }

func (f *authStatusCredentialKindFlag) PreParse() error {
	f.StringFlag = &cli.StringFlag{Name: f.Name, Usage: f.Usage}
	return f.StringFlag.PreParse()
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

type authStatusIdentity struct {
	Status   string  `json:"status"`
	Reason   string  `json:"reason,omitempty"`
	Provider string  `json:"provider,omitempty"`
	ID       string  `json:"id,omitempty"`
	Name     *string `json:"name"`
	Email    *string `json:"email"`
}

// Only sanitized configuration reaches either renderer. Credentials never enter this report.
type authStatusReport struct {
	Offline                bool                 `json:"offline"`
	Verified               bool                 `json:"verified"`
	VerifiedCredentialKind string               `json:"verified_credential_kind,omitempty"`
	APIIdentity            authStatusIdentity   `json:"api_identity"`
	AdminIdentity          authStatusIdentity   `json:"admin_identity"`
	API                    authStatusCredential `json:"api_credential"`
	Admin                  authStatusCredential `json:"admin_credential"`
	Organization           authStatusValue      `json:"organization"`
	Project                authStatusValue      `json:"project"`
	BaseURL                authStatusEndpoint   `json:"base_url"`
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
	verify, kind := command.Bool("verify"), command.String("credential-kind")
	if verify && kind != "api" && kind != "admin" || !verify && command.IsSet("credential-kind") {
		return &localUtilityError{message: "Use --verify with --credential-kind api or --credential-kind admin."}
	}
	snapshot, err := resolveAuthStatusSnapshot(root, os.LookupEnv)
	if err != nil {
		return err
	}
	report := snapshot.report
	var lookupErr error
	if verify {
		var stop func()
		ctx, stop = adminSetupSignalContext(ctx)
		defer stop()
		report.Offline = false
		identity, failure := authStatusLookup(ctx, root, snapshot, kind)
		lookupErr = failure
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return &localUtilityError{message: "Identity lookup timed out. Identity remains unknown.", cause: context.DeadlineExceeded}
			}
			code := 130
			if errors.Is(context.Cause(ctx), errAdminSetupTerminated) {
				code = 143
			}
			return &authStatusLookupError{localUtilityError: &localUtilityError{message: "Identity lookup canceled.", cause: context.Canceled, preserveOnCancel: true}, code: code}
		}
		if kind == "api" {
			report.APIIdentity = identity
		} else {
			report.AdminIdentity = identity
		}
		if failure == nil {
			report.Verified, report.VerifiedCredentialKind = true, kind
		}
	}
	out := outputWriter{ctx: ctx, out: root.Writer}
	if format == "json" {
		err = json.NewEncoder(out).Encode(report)
	} else {
		endpoint := authStatusTextValue(report.BaseURL.authStatusValue)
		if report.BaseURL.Redacted {
			endpoint += " (URL components omitted)"
		}
		title := "Authentication configuration (offline; not verified)"
		if verify {
			title = "Authentication configuration (identity lookup requested: " + kind + ")"
		}
		_, err = fmt.Fprintf(out, "%s\n"+
			"API credential: %s [%s]\nAdmin credential: %s [%s]\n"+
			"API identity: %s\nAdmin identity: %s\n"+
			"Organization: %s [%s]\nProject: %s [%s]\nBase URL: %s [%s]\n",
			title,
			strings.ReplaceAll(report.API.Type, "_", " "), readable.Text(report.API.Source),
			strings.ReplaceAll(report.Admin.Type, "_", " "), readable.Text(report.Admin.Source),
			authStatusIdentityText(report.APIIdentity), authStatusIdentityText(report.AdminIdentity),
			authStatusTextValue(report.Organization), readable.Text(report.Organization.Source),
			authStatusTextValue(report.Project), readable.Text(report.Project.Source),
			endpoint, readable.Text(report.BaseURL.Source))
	}
	if err != nil {
		return &localUtilityError{message: "Could not write auth status. Check the output file or pipe before rerunning.", cause: errors.Join(err, lookupErr)}
	}
	return lookupErr
}

func authStatusIdentityText(identity authStatusIdentity) string {
	if identity.Status != "resolved" {
		return "unknown (" + identity.Reason + ")"
	}
	value := identity.ID
	if identity.Name != nil {
		value += " | name: " + *identity.Name
	}
	if identity.Email != nil {
		value += " | email: " + *identity.Email
	}
	return "resolved | " + authStatusTextValue(authStatusValue{Value: value})
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
	snapshot, err := resolveAuthStatusSnapshot(root, lookup)
	return snapshot.report, err
}

// Secrets stay in this invocation-local snapshot and never enter a renderer.
type authStatusSnapshot struct {
	report                               authStatusReport
	baseURL, explicitBaseURL             string
	apiAuthorization, adminAuthorization string
	secrets                              []string
}

func resolveAuthStatusSnapshot(root *cli.Command, lookup func(string) (string, bool)) (authStatusSnapshot, error) {
	environment := snapshotContextEnvironment(root, lookup)
	literal, err := requestHeaders(root)
	if err != nil {
		return authStatusSnapshot{}, &localUtilityError{message: "Invalid --header. Use 'Name: Value' without control characters."}
	}
	inherited := inheritedContextHeaders(environment)
	setting := func(flag, variable string) authStatusValue {
		return requestContextSetting(root, environment, flag, variable)
	}
	resolved, err := resolveRequestContext(requestResolutionContext(root), root, environment, literal)
	if err != nil {
		return authStatusSnapshot{}, err
	}
	base := resolved.BaseURL
	endpoint, basic, err := authStatusBaseURL(base)
	if err != nil {
		return authStatusSnapshot{}, err
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
	snapshot := authStatusSnapshot{baseURL: base.Value}
	snapshot.secrets = append(snapshot.secrets, environment["OPENAI_API_KEY"].Value, environment["OPENAI_ADMIN_KEY"].Value)
	if base.Source == "flag:--base-url" || base.Source == "environment:OPENAI_BASE_URL" {
		snapshot.explicitBaseURL = base.Value
	}
	authorization := func(flag, variable string) string {
		key := setting(flag, variable).Value
		snapshot.secrets = append(snapshot.secrets, key)
		if literal.Values("Authorization") != nil {
			return literal.Get("Authorization")
		}
		if key != "" {
			return "Bearer " + key
		}
		return inherited.Get("Authorization")
	}
	snapshot.apiAuthorization = authorization("api-key", "OPENAI_API_KEY")
	snapshot.adminAuthorization = authorization("admin-api-key", "OPENAI_ADMIN_KEY")
	for _, header := range []string{literal.Get("Authorization"), inherited.Get("Authorization")} {
		snapshot.secrets = append(snapshot.secrets, header)
		if _, token, ok := strings.Cut(header, " "); ok {
			snapshot.secrets = append(snapshot.secrets, token)
		}
	}
	snapshot.report = authStatusReport{
		Offline:      true,
		API:          credential("api-key", "OPENAI_API_KEY", "api_key"),
		Admin:        credential("admin-api-key", "OPENAI_ADMIN_KEY", "admin_api_key"),
		Organization: resolved.Organization,
		Project:      resolved.Project, BaseURL: endpoint,
	}
	unknown := func(credential authStatusCredential) authStatusIdentity {
		reason := "not_requested"
		if !credential.Configured {
			reason = "not_configured"
		}
		return authStatusIdentity{Status: "unknown", Reason: reason}
	}
	snapshot.report.APIIdentity, snapshot.report.AdminIdentity = unknown(snapshot.report.API), unknown(snapshot.report.Admin)
	return snapshot, nil
}

type authStatusLookupError struct {
	*localUtilityError
	code int
}

func (e *authStatusLookupError) ExitCode() int { return e.code }

// The provider returns an authenticated principal, not a key ID or membership assertion.
// Keep its request independent of SDK API/Admin fallback and API-body error rendering.
func authStatusLookup(ctx context.Context, root *cli.Command, snapshot authStatusSnapshot, kind string) (authStatusIdentity, error) {
	failure := func(reason, message string) (authStatusIdentity, error) {
		return authStatusIdentity{Status: "unknown", Reason: reason}, &localUtilityError{message: message}
	}
	endpoint, err := url.Parse(snapshot.baseURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() != "api.openai.com" ||
		(endpoint.Port() != "" && endpoint.Port() != "443") || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.RawFragment != "" ||
		endpoint.RawPath != "" || (endpoint.Path != "/v1" && endpoint.Path != "/v1/") {
		return failure("unsupported_provider", "Identity lookup requires the supported https://api.openai.com/v1/ endpoint. No request was sent.")
	}
	authorization := snapshot.apiAuthorization
	if kind == "admin" {
		authorization = snapshot.adminAuthorization
	}
	if authorization == "" {
		return failure("not_configured", "The selected credential is not configured. No request was sent.")
	}
	scheme, token, bearer := strings.Cut(authorization, " ")
	if !bearer || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return failure("invalid_configuration", "Identity lookup requires a Bearer credential. No request was sent.")
	}
	cert, _ := root.Value(mtlsClientCertFileFlag).(string)
	key, _ := root.Value(mtlsClientKeyFileFlag).(string)
	client, err := newMTLSHTTPClient(cert, key, snapshot.explicitBaseURL)
	if err != nil {
		return failure("invalid_configuration", "Could not configure identity lookup TLS. Check the certificate, key, and explicit base URL.")
	}
	if client == nil {
		client = &http.Client{Transport: http.DefaultTransport}
		if transport, ok := http.DefaultTransport.(*http.Transport); ok {
			client.Transport = transport.Clone()
		}
	}
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	endpoint.Path = "/v1/me"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return failure("invalid_configuration", "Could not configure identity lookup. No request was sent.")
	}
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("OpenAI-Organization", snapshot.report.Organization.Value)
	request.Header.Set("OpenAI-Project", snapshot.report.Project.Value)
	response, err := client.Do(request)
	requestFailure := func() (authStatusIdentity, error) {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return failure("timed_out", "Identity lookup timed out. Identity remains unknown.")
		}
		if ctx.Err() != nil {
			return failure("canceled", "Identity lookup canceled.")
		}
		return failure("request_failed", "Could not resolve identity. Check your connection and retry.")
	}
	if err != nil {
		return requestFailure()
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		reason := "unavailable"
		switch {
		case response.StatusCode == http.StatusUnauthorized:
			reason = "unauthorized"
		case response.StatusCode == http.StatusForbidden:
			reason = "forbidden"
		case response.StatusCode == http.StatusTooManyRequests:
			reason = "rate_limited"
		case response.StatusCode >= 300 && response.StatusCode < 400:
			reason = "redirect"
		}
		return failure(reason, "The identity provider did not resolve identity. Identity remains unknown.")
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return failure("invalid_response", "The identity provider returned an invalid response. Identity remains unknown.")
	}
	identity, err := decodeAuthStatusIdentity(response.Body)
	if ctx.Err() != nil {
		return requestFailure()
	}
	if err != nil {
		return failure("invalid_response", "The identity provider returned an invalid response. Identity remains unknown.")
	}
	values := []string{identity.ID}
	if identity.Name != nil {
		values = append(values, *identity.Name)
	}
	if identity.Email != nil {
		values = append(values, *identity.Email)
	}
	for _, value := range values {
		for _, secret := range snapshot.secrets {
			if secret != "" && strings.Contains(value, secret) {
				return failure("invalid_response", "The identity provider returned an invalid response. Identity remains unknown.")
			}
		}
	}
	return identity, nil
}

func decodeAuthStatusIdentity(body io.Reader) (authStatusIdentity, error) {
	decoder := json.NewDecoder(body)
	invalid := errors.New("invalid identity response")
	identity := authStatusIdentity{Status: "resolved", Provider: "openai_me_v1"}
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return authStatusIdentity{}, invalid
	}
	seen := map[string]bool{}
	object := ""
	for decoder.More() {
		token, err = decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return authStatusIdentity{}, invalid
		}
		seen[name] = true
		switch name {
		case "object":
			err = decoder.Decode(&object)
		case "id":
			err = decoder.Decode(&identity.ID)
		case "name":
			err = decoder.Decode(&identity.Name)
		case "email":
			err = decoder.Decode(&identity.Email)
		default:
			var ignored json.RawMessage
			err = decoder.Decode(&ignored)
		}
		if err != nil {
			return authStatusIdentity{}, invalid
		}
	}
	if _, err = decoder.Token(); err != nil {
		return authStatusIdentity{}, invalid
	}
	if _, err = decoder.Token(); err != io.EOF || object != "user" || strings.TrimSpace(identity.ID) == "" {
		return authStatusIdentity{}, invalid
	}
	return identity, nil
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
