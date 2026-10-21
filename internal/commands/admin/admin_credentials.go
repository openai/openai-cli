package admin

import (
	"errors"
	"net/url"
	"os"
	"strings"

	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/urfave/cli/v3"
)

// CredentialsError provides fixed guidance for missing admin authentication.
type CredentialsError struct{ invocation string }

type baseURLResolver func(*cli.Command) (string, error)

const baseURLResolverMetadata = "openai-admin-base-url-resolver"

// CredentialsRequiredMetadata marks admin operations before command-tree cloning.
const CredentialsRequiredMetadata = "admin-credentials-required"

func (err *CredentialsError) Error() string {
	invocation := err.invocation
	if invocation == "" {
		invocation = "openai"
	}
	return `Admin API key required.
A project API key cannot run organization admin commands.

To enter a key securely and verify access, run:
  ` + invocation + ` setup admin

For key creation and manual setup instructions, run:
  ` + invocation + ` help setup admin`
}

// ConfigureCredentials installs endpoint selection and marks generated operations before subgroup cloning.
// resolveBaseURL is required and must preserve the request snapshot and safe resolution errors.
// Request setup checks credentials after positional and required-input validation.
func ConfigureCredentials(root *cli.Command, resolveBaseURL func(*cli.Command) (string, error)) {
	if root.Metadata == nil {
		root.Metadata = map[string]any{}
	}
	root.Metadata[baseURLResolverMetadata] = baseURLResolver(resolveBaseURL)
	for _, resource := range root.Commands {
		if resource.Category != "API RESOURCE" ||
			(!strings.HasPrefix(resource.Name, "admin:") && resource.Name != "fine-tuning:checkpoints:permissions") {
			continue
		}
		for _, operation := range resource.Commands {
			if operation.Action == nil {
				continue
			}
			if operation.Metadata == nil {
				operation.Metadata = map[string]any{}
			}
			operation.Metadata[CredentialsRequiredMetadata] = true
		}
	}
}

// CheckCredentials validates effective admin authentication after input validation.
func CheckCredentials(command *cli.Command) error {
	if required, _ := command.Metadata[CredentialsRequiredMetadata].(bool); !required {
		return nil
	}
	invocation, _ := command.Root().Metadata["help-invocation"].(string)
	missing := &CredentialsError{invocation: invocation}
	// Custom endpoints own their authentication contracts and server errors.
	// Do not infer whether an arbitrary gateway requires credentials.
	baseURL, err := effectiveBaseURL(command)
	if err != nil {
		return err
	}
	if !usesStandardAdminService(baseURL) {
		return nil
	}
	// Validate headers before considering authentication, matching FlagOptions.
	headers, err := requestconfig.Headers(command)
	if err != nil {
		return err
	}
	// OpenAI mTLS adds client-certificate authentication; it does not replace
	// the admin API key or an explicit Authorization header.
	// Method-level Authorization overrides SDK credentials, even when empty.
	if values, ok := headers["Authorization"]; ok {
		if values[0] != "" {
			return nil
		}
		return missing
	}
	// Match GetDefaultRequestOptions and the SDK's admin-only security. A normal
	// API key cannot authenticate these operations. Empty overrides stay empty.
	key := os.Getenv("OPENAI_ADMIN_KEY")
	if command.IsSet("admin-api-key") {
		key = command.String("admin-api-key")
	}
	if key != "" {
		return nil
	}
	// The SDK retains an environment Authorization header when no applicable
	// nonempty key replaces it. Match its trimming and last-value-wins parsing.
	var authorization string
	for line := range strings.SplitSeq(os.Getenv("OPENAI_CUSTOM_HEADERS"), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Authorization") {
			authorization = strings.TrimSpace(value)
		}
	}
	if authorization != "" {
		return nil
	}
	return missing
}

func usesStandardAdminService(baseURL string) bool {
	if baseURL == "" {
		// Keep the guard without replacing the explicitly empty request endpoint.
		return true
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	// URL userinfo can provide HTTP Basic authentication through Go's client.
	return endpoint.User == nil && strings.EqualFold(endpoint.Scheme, "https") &&
		(strings.EqualFold(endpoint.Hostname(), "api.openai.com") || strings.EqualFold(endpoint.Hostname(), "mtls.api.openai.com")) &&
		(endpoint.Port() == "" || endpoint.Port() == "443")
}

// effectiveBaseURL shares endpoint selection between credential checks and interactive setup.
func effectiveBaseURL(command *cli.Command) (string, error) {
	if value, present := command.Root().Metadata[baseURLResolverMetadata]; present {
		resolve, ok := value.(baseURLResolver)
		if !ok || resolve == nil {
			return "", errors.New("Admin endpoint selection is unavailable.")
		}
		return resolve(command)
	}
	// Standalone Admin helpers retain their pre-profile flag and environment behavior.
	baseURL := command.String("base-url")
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return baseURL, nil
}
