package output

import (
	"regexp"
	"strings"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// APIRequestID accepts only bounded, single-line returned identifiers and rejects caller reflection.
func APIRequestID(apierr *openai.Error) string {
	if apierr == nil {
		return ""
	}
	// A caller-supplied header can be reflected by the server. Inspect keys only.
	if apierr.Request != nil {
		for name := range apierr.Request.Header {
			if strings.EqualFold(name, "X-Request-ID") {
				return ""
			}
		}
	}
	if apierr.Response == nil {
		return ""
	}
	requestID := apierr.Response.Header.Get("X-Request-ID")
	if len(requestID) == 0 || len(requestID) > 256 {
		return ""
	}
	for _, c := range requestID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return ""
		}
	}
	return requestID
}

var readableParameterPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:(?:\.[A-Za-z0-9_]+)|(?:\[[0-9]+\]))*$`)
var readableParameterIndex = regexp.MustCompile(`\[[0-9]+\]`)

// MissingInputCode recognizes structured API omission codes without inspecting prose.
func MissingInputCode(code string) bool {
	return code == "missing_required_parameter" || code == "missing_required_argument"
}

// MissingStreamInput reads structured codes from a stream failure event.
// The returned parameter is untrusted and must be mapped through ParameterFlag.
func MissingStreamInput(event gjson.Result) (string, bool) {
	var detail gjson.Result
	switch event.Get("type").String() {
	case "":
		detail = event.Get("error")
	case "error":
		detail = event.Get("error")
		if !detail.Exists() {
			detail = event
		}
	case "response.failed":
		detail = event.Get("response.error")
	case "response.completed", "response.done":
		if event.Get("response.status").String() == "failed" {
			detail = event.Get("response.error")
		}
	}
	if !MissingInputCode(detail.Get("code").String()) {
		return "", false
	}
	parameter := detail.Get("param")
	if parameter.Type != gjson.String {
		return "", true
	}
	return parameter.String(), true
}

// ParameterFlag maps untrusted API parameter paths to locally declared flags.
func ParameterFlag(command *cli.Command, parameter string) (name, path string) {
	if command == nil || !readableParameterPattern.MatchString(parameter) {
		return "", ""
	}
	// Normalizing array positions makes messages[0].content and
	// messages.0.content resolve to the same declared request path.
	parts := strings.Split(readableParameterIndex.ReplaceAllString(parameter, ""), ".")
	named := parts[:0]
	for _, part := range parts {
		if strings.Trim(part, "0123456789") != "" {
			named = append(named, part)
		}
	}
	parameter = strings.Join(named, ".")
	for _, flag := range command.Flags {
		for _, candidate := range readableFlagPaths(flag) {
			if candidate == "" || len(candidate) <= len(path) || parameter != candidate && !strings.HasPrefix(parameter, candidate+".") {
				continue
			}
			if len(flag.Names()) == 0 {
				continue
			}
			name, path = "--"+flag.Names()[0], candidate
			if len(flag.Names()[0]) == 1 {
				name = "-" + flag.Names()[0]
			}
		}
	}
	return name, path
}

func readableFlagPaths(flag cli.Flag) []string {
	if inner, ok := flag.(requestflag.HasOuterFlag); ok {
		paths := readableFlagPaths(inner.GetOuterFlag())
		for i := range paths {
			if paths[i] != "" {
				paths[i] += "." + inner.GetInnerField()
			}
		}
		return paths
	}
	if parameter, ok := flag.(requestflag.InRequest); ok {
		return []string{parameter.GetBodyPath(), parameter.GetQueryPath(), parameter.GetPathParam()}
	}
	return nil
}
