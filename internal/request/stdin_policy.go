package request

import (
	"fmt"
	"os"
	"strconv"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// UntrustedStdinEnv selects literal interpretation of piped request values.
const UntrustedStdinEnv = "OPENAI_UNTRUSTED_STDIN"

// UntrustedStdinValue preserves a string's origin until the shared file
// expansion boundary, where its contents are always interpreted literally.
type UntrustedStdinValue string

// StdinSecurity tracks the origin of piped values for one request preparation.
type StdinSecurity struct {
	enabled    bool
	stdinFlags map[cli.Flag]struct{}
}

// Enabled reports whether piped values require literal interpretation.
func (s *StdinSecurity) Enabled() bool {
	return s.enabled
}

// NewStdinSecurity reads the untrusted-stdin setting for this request.
func NewStdinSecurity() (*StdinSecurity, error) {
	value, exists := os.LookupEnv(UntrustedStdinEnv)
	if !exists {
		return &StdinSecurity{}, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s value %q: expected a boolean", UntrustedStdinEnv, value)
	}
	return &StdinSecurity{enabled: enabled}, nil
}

// RecordFlag records a flag populated from piped input.
func (s *StdinSecurity) RecordFlag(flag cli.Flag) {
	if !s.enabled {
		return
	}
	if s.stdinFlags == nil {
		s.stdinFlags = make(map[cli.Flag]struct{})
	}
	s.stdinFlags[flag] = struct{}{}
	if inner, ok := flag.(requestflag.HasOuterFlag); ok {
		protectInnerFlagValue(inner)
	}
}

// RecordBodyFlags records unset body flags represented in piped data.
func (s *StdinSecurity) RecordBodyFlags(cmd *cli.Command, data map[string]any) {
	if !s.enabled {
		return
	}
	for _, flag := range cmd.Flags {
		inRequest, ok := flag.(requestflag.InRequest)
		if !ok || flag.IsSet() {
			continue
		}
		bodyPath := inRequest.GetBodyPath()
		if bodyPath == "" {
			continue
		}
		if _, exists := data[bodyPath]; exists {
			s.RecordFlag(flag)
		}
	}
}

func (s *StdinSecurity) fileInputFromStdin(flag cli.Flag) bool {
	_, recorded := s.stdinFlags[flag]
	return s.enabled && recorded
}

// ProtectFlagValues preserves recorded values as literals during file expansion.
func (s *StdinSecurity) ProtectFlagValues(contents *requestflag.RequestContents) {
	if !s.enabled {
		return
	}
	body, _ := contents.Body.(map[string]any)
	for flag := range s.stdinFlags {
		inRequest, ok := flag.(requestflag.InRequest)
		if !ok {
			continue
		}
		protectRequestValue(contents.Queries, inRequest.GetQueryPath())
		protectRequestValue(contents.Headers, inRequest.GetHeaderPath())
		if inRequest.IsBodyRoot() {
			contents.Body = ProtectStdinValue(contents.Body)
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
		values[path] = ProtectStdinValue(value)
	}
}

// ProtectStdinValue marks ordinary string leaves while preserving other values.
func ProtectStdinValue(value any) any {
	switch value := value.(type) {
	case string:
		return UntrustedStdinValue(value)
	case *string:
		if value != nil {
			return UntrustedStdinValue(*value)
		}
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, element := range value {
			result[key] = ProtectStdinValue(element)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, element := range value {
			result[index] = ProtectStdinValue(element)
		}
		return result
	case []string:
		result := make([]any, len(value))
		for index, element := range value {
			result[index] = UntrustedStdinValue(element)
		}
		return result
	case []map[string]any:
		result := make([]any, len(value))
		for index, element := range value {
			result[index] = ProtectStdinValue(element)
		}
		return result
	}
	return value
}
