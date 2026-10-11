package custom

import (
	"net/url"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/urfave/cli/v3"
)

// Reproduce request selection without copying credentials into terminal output.
func fileReceiptRequestOptions(root *cli.Command) (args []string, omit bool) {
	if root.IsSet("header") {
		return nil, true
	}
	for _, option := range [][2]string{
		{"api-key", "OPENAI_API_KEY"},
		{"admin-api-key", "OPENAI_ADMIN_KEY"},
		{"webhook-secret", "OPENAI_WEBHOOK_SECRET"},
		{mtlsClientCertFileFlag, mtlsClientCertFileEnv},
		{mtlsClientKeyFileFlag, mtlsClientKeyFileEnv},
	} {
		// The copied command can reuse inherited environment values. Any effective
		// override requires a secret or private path that the receipt must not print.
		if root.IsSet(option[0]) && root.String(option[0]) != os.Getenv(option[1]) {
			return nil, true
		}
	}
	for _, name := range []string{"project", "organization", "base-url"} {
		if !root.IsSet(name) {
			continue
		}
		value := root.String(name)
		if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return nil, true
		}
		if name == "base-url" && value != "" {
			endpoint, err := url.Parse(value)
			if err != nil || strings.IndexFunc(value, unicode.IsControl) >= 0 || endpoint.Host == "" || endpoint.User != nil ||
				endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" ||
				(endpoint.Scheme != "http" && endpoint.Scheme != "https") {
				return nil, true
			}
		}
		args = append(args, "--"+name+"="+value)
	}
	return args, false
}
