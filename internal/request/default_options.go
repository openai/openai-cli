package request

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

const BaseURLHTTPSRequired = " must use HTTPS, except for localhost or a loopback IP address"
const BaseURLHostRequired = " must be an absolute HTTP(S) URL with a valid host"

// ValidateBaseURL permits cleartext HTTP only for explicit local development endpoints.
// Use the same URL parser as the SDK, and never resolve DNS names to grant an exception.
func ValidateBaseURL(value, source string) error {
	if value == "" {
		return nil
	}
	endpoint, err := url.Parse(value)
	if err != nil {
		return errors.New(source + BaseURLHostRequired)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return fmt.Errorf("%s %q is missing a scheme (expected http:// or https://)", source, value)
	}
	if endpoint.Hostname() == "" || endpoint.Opaque != "" {
		return errors.New(source + BaseURLHostRequired)
	}
	if port := endpoint.Port(); port != "" {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return errors.New(source + BaseURLHostRequired)
		}
	}
	if endpoint.Scheme == "http" {
		host := endpoint.Hostname()
		localhost := strings.EqualFold(host, "localhost") && !strings.HasPrefix(endpoint.Host, "[")
		ip := net.ParseIP(host)
		if !localhost && (ip == nil || !ip.IsLoopback()) {
			return errors.New(source + BaseURLHTTPSRequired)
		}
	}
	return nil
}

// DefaultOptions builds shared client configuration around caller-supplied middleware.
// Middleware order stays between CLI metadata and explicit request overrides.
func DefaultOptions(cmd *cli.Command, version string, middlewares ...option.Middleware) []option.RequestOption {
	opts := []option.RequestOption{
		option.WithHeader("User-Agent", fmt.Sprintf("OpenAI/CLI %s", version)),
		option.WithHeader("X-Stainless-Lang", "cli"),
		option.WithHeader("X-Stainless-Package-Version", version),
		option.WithHeader("X-Stainless-Runtime", "cli"),
		option.WithHeader("X-Stainless-CLI-Command", cmd.FullName()),
	}
	for _, middleware := range middlewares {
		opts = append(opts, option.WithMiddleware(middleware))
	}
	if cmd.IsSet("api-key") {
		opts = append(opts, option.WithAPIKey(cmd.String("api-key")))
	}
	if cmd.IsSet("admin-api-key") {
		opts = append(opts, option.WithAdminAPIKey(cmd.String("admin-api-key")))
	}
	// Endpoint request fields named project do not select the request context.
	root := cmd.Root()
	if selection, configured := ConfiguredProjectContext(root); configured {
		if selection.Organization.Source != "unset" {
			opts = append(opts, option.WithOrganization(selection.Organization.Value))
		}
		if selection.Project.Source != "unset" {
			opts = append(opts, option.WithProject(selection.Project.Value))
		}
	} else {
		if cmd.IsSet("organization") {
			opts = append(opts, option.WithOrganization(cmd.String("organization")))
		}
		if root.IsSet("project") {
			opts = append(opts, option.WithProject(root.String("project")))
		}
	}
	if cmd.IsSet("webhook-secret") {
		opts = append(opts, option.WithWebhookSecret(cmd.String("webhook-secret")))
	}

	// Override base URL if the --base-url flag is provided
	if baseURL := cmd.String("base-url"); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if httpClient, ok := cmd.Root().Metadata[MTLSHTTPClientMetadata].(*http.Client); ok {
		opts = append(opts, option.WithHTTPClient(httpClient))
	}

	return opts
}
