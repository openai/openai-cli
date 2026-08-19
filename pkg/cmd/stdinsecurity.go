package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

const untrustedStdinEnv = "OPENAI_UNTRUSTED_STDIN"

// untrustedStdinValue preserves a string's origin until the shared file
// expansion boundary, where its contents are always interpreted literally.
type untrustedStdinValue string

type stdinSecurity struct {
	enabled bool
	flags   map[cli.Flag]struct{}
}

func newStdinSecurity() (*stdinSecurity, error) {
	value, exists := os.LookupEnv(untrustedStdinEnv)
	if !exists {
		return &stdinSecurity{}, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s value %q: expected a boolean", untrustedStdinEnv, value)
	}
	return &stdinSecurity{enabled: enabled}, nil
}

func (s *stdinSecurity) recordFlag(flag cli.Flag) {
	if !s.enabled {
		return
	}
	if inner, ok := flag.(requestflag.HasOuterFlag); ok {
		protectInnerFlagValue(inner)
		return
	}
	if s.flags == nil {
		s.flags = make(map[cli.Flag]struct{})
	}
	s.flags[flag] = struct{}{}
}

func (s *stdinSecurity) protectFlagValues(contents *requestflag.RequestContents) {
	if !s.enabled {
		return
	}
	body, _ := contents.Body.(map[string]any)
	for flag := range s.flags {
		inRequest, ok := flag.(requestflag.InRequest)
		if !ok {
			continue
		}
		protectRequestValue(contents.Queries, inRequest.GetQueryPath())
		protectRequestValue(contents.Headers, inRequest.GetHeaderPath())
		if inRequest.IsBodyRoot() {
			contents.Body = protectStdinValue(contents.Body)
		} else {
			protectRequestValue(body, inRequest.GetBodyPath())
		}
	}
}

func protectInnerFlagValue(inner requestflag.HasOuterFlag) {
	switch outer := inner.GetOuterFlag().Get().(type) {
	case map[string]any:
		protectRequestValue(outer, inner.GetInnerField())
	case []map[string]any:
		if len(outer) > 0 {
			protectRequestValue(outer[len(outer)-1], inner.GetInnerField())
		}
	case []any:
		if len(outer) > 0 {
			if nested, ok := outer[len(outer)-1].(map[string]any); ok {
				protectRequestValue(nested, inner.GetInnerField())
			}
		}
	}
}

func protectRequestValue(values map[string]any, path string) {
	if path == "" {
		return
	}
	if value, exists := values[path]; exists {
		values[path] = protectStdinValue(value)
	}
}

func protectStdinValue(value any) any {
	switch value := value.(type) {
	case string:
		return untrustedStdinValue(value)
	case *string:
		if value != nil {
			return untrustedStdinValue(*value)
		}
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, element := range value {
			result[key] = protectStdinValue(element)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, element := range value {
			result[index] = protectStdinValue(element)
		}
		return result
	case []string:
		result := make([]any, len(value))
		for index, element := range value {
			result[index] = untrustedStdinValue(element)
		}
		return result
	case []map[string]any:
		result := make([]any, len(value))
		for index, element := range value {
			result[index] = protectStdinValue(element)
		}
		return result
	}
	return value
}

func containsUntrustedStdinValue(value any) bool {
	switch value := value.(type) {
	case untrustedStdinValue:
		return value != ""
	case []any:
		for _, element := range value {
			if containsUntrustedStdinValue(element) {
				return true
			}
		}
	case map[string]any:
		for _, element := range value {
			if containsUntrustedStdinValue(element) {
				return true
			}
		}
	}
	return false
}
