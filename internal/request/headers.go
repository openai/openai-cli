package request

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// NewRequestHeaderFlag returns the repeatable, global flag for literal HTTP headers.
func NewRequestHeaderFlag() cli.Flag {
	return &HeaderFlag{Flag: requestflag.Flag[[]string]{
		Name:        "header",
		Aliases:     []string{"H"},
		Usage:       "Add a request header as 'Name: Value' (repeatable; last value wins; use OPENAI_CUSTOM_HEADERS for secrets)",
		HideDefault: true,
	}}
}

// HeaderFlag is the root-owned repeatable flag for literal request headers.
type HeaderFlag struct {
	requestflag.Flag[[]string]
}

func (*HeaderFlag) IsLocal() bool { return false }

// Headers parses literal header flags without echoing their values in errors.
func Headers(cmd *cli.Command) (http.Header, error) {
	headers := make(http.Header)
	for i, raw := range cmd.Root().StringSlice("header") {
		name, value, ok := strings.Cut(raw, ":")
		if !ok {
			return nil, fmt.Errorf("header %d: expected 'Name: Value'", i+1)
		}
		if name == "" {
			return nil, fmt.Errorf("header %d: name must not be empty", i+1)
		}
		for _, c := range name {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
				return nil, fmt.Errorf("header %d: invalid character in name", i+1)
			}
		}
		for _, c := range []byte(value) {
			if c < ' ' && c != '\t' || c == 0x7f {
				return nil, fmt.Errorf("header %d: invalid control character in value", i+1)
			}
		}
		headers.Set(name, strings.Trim(value, " \t"))
	}
	return headers, nil
}
