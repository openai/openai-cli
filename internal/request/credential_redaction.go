package request

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/urfave/cli/v3"
)

// ConfiguredCredentials collects current and overridden inputs for redacting local output.
// Never use this list to select the credential sent on a request.
func ConfiguredCredentials(root *cli.Command, lookup func(string) (string, bool)) []string {
	get := func(key string) string { value, _ := lookup(key); return value }
	secrets := []string{get("OPENAI_API_KEY"), get("OPENAI_ADMIN_KEY"), get("OPENAI_WEBHOOK_SECRET"),
		root.String("api-key"), root.String("admin-api-key"), root.String("webhook-secret")}
	for _, flag := range root.Flags {
		switch f := flag.(type) {
		case *RootRequestFlag:
			secrets = append(secrets, f.credentialInputs...)
		case *baseURLFlag:
			secrets = append(secrets, f.credentialInputs...)
		}
	}
	secrets = appendURLCredentials(secrets, get("OPENAI_BASE_URL"))
	secrets = appendURLCredentials(secrets, root.String("base-url"))
	for _, line := range root.StringSlice("header") {
		secrets = appendHeaderCredentials(secrets, line)
	}
	for line := range strings.SplitSeq(get("OPENAI_CUSTOM_HEADERS"), "\n") {
		secrets = appendHeaderCredentials(secrets, line)
	}
	return secrets
}

func appendURLCredentials(secrets []string, raw string) []string {
	endpoint, err := url.Parse(raw)
	if err != nil {
		return secrets
	}
	if endpoint.User != nil {
		password, _ := endpoint.User.Password()
		secrets = append(secrets, endpoint.User.Username(), password)
	}
	for name, values := range endpoint.Query() {
		switch strings.ToLower(name) {
		case "api_key", "api-key", "access_token", "token", "signature", "sig", "x-amz-signature", "x-goog-signature":
			secrets = append(secrets, values...)
		}
	}
	return secrets
}

// Also protect the individual credentials in composite headers, including overridden lines.
func appendHeaderCredentials(secrets []string, line string) []string {
	name, value, ok := strings.Cut(line, ":")
	if !ok {
		return secrets
	}
	value = strings.TrimSpace(value)
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization":
		secrets = append(secrets, value)
		if i := strings.IndexAny(value, " \t"); i >= 0 {
			token := strings.TrimSpace(value[i:])
			secrets = append(secrets, token)
			if strings.EqualFold(value[:i], "Basic") {
				if decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(token, "=")); err == nil {
					if user, password, ok := strings.Cut(string(decoded), ":"); ok {
						secrets = append(secrets, user, password)
					}
				}
			}
		}
	case "cookie":
		secrets = append(secrets, value)
		req := http.Request{Header: http.Header{"Cookie": {value}}}
		for _, cookie := range req.Cookies() {
			secrets = append(secrets, cookie.Value)
		}
	case "set-cookie":
		secrets = append(secrets, value)
		if cookie, err := http.ParseSetCookie(value); err == nil {
			secrets = append(secrets, cookie.Value)
		}
	case "api-key", "x-api-key":
		secrets = append(secrets, value)
	}
	return secrets
}

// Recognize long OpenAI key forms even when a user pasted one into a setting
// instead of a credential source. Short labels such as "sk-project" remain valid.
// Other credential formats are protected by comparing to the configured inputs.
var openAIKeyPattern = regexp.MustCompile(`(?:^|[^A-Za-z0-9])sk-(?:(?:proj|admin|svcacct)-[A-Za-z0-9_-]{32,}|[A-Za-z0-9]{32,})`)

func ContainsCredential(value string, secrets []string) bool {
	for _, secret := range secrets {
		// HTTP trims surrounding whitespace. Protect the usable value even when
		// a copied key includes a trailing space or tab in its configured source.
		if token := strings.TrimSpace(secret); token != "" && strings.Contains(value, token) {
			return true
		}
	}
	return strings.Contains(value, "sk-") && openAIKeyPattern.MatchString(value)
}
