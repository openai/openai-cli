package request

import (
	"net/url"
	"strings"

	"github.com/urfave/cli/v3"
)

// ConfiguredCredentials collects values to exclude from command receipts and status output.
// The lookup can read the invocation's environment snapshot.
func ConfiguredCredentials(root *cli.Command, lookup func(string) (string, bool)) []string {
	get := func(key string) string { value, _ := lookup(key); return value }
	// A copied key can also occur in an otherwise printable project, organization, or host.
	secrets := []string{get("OPENAI_API_KEY"), get("OPENAI_ADMIN_KEY"), get("OPENAI_WEBHOOK_SECRET"),
		root.String("api-key"), root.String("admin-api-key"), root.String("webhook-secret")}
	for _, raw := range []string{get("OPENAI_BASE_URL"), root.String("base-url")} {
		if endpoint, err := url.Parse(raw); err == nil && endpoint.User != nil {
			password, _ := endpoint.User.Password()
			secrets = append(secrets, endpoint.User.Username(), password)
		}
	}
	for _, line := range root.StringSlice("header") {
		secrets = appendHeaderCredentials(secrets, line)
	}
	for line := range strings.SplitSeq(get("OPENAI_CUSTOM_HEADERS"), "\n") {
		secrets = appendHeaderCredentials(secrets, line)
	}
	return secrets
}

// Include overridden headers since their credentials may still appear in displayed settings.
func appendHeaderCredentials(secrets []string, line string) []string {
	name, value, ok := strings.Cut(line, ":")
	if !ok {
		return secrets
	}
	value = strings.TrimSpace(value)
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization":
		if i := strings.IndexAny(value, " \t"); i >= 0 {
			secrets = append(secrets, strings.TrimSpace(value[i:]))
		}
		return append(secrets, value)
	case "api-key", "x-api-key":
		return append(secrets, value)
	}
	return secrets
}

func ContainsCredential(value string, secrets []string) bool {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(value, secret) {
			return true
		}
	}
	return false
}
