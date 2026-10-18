package request

import (
	"fmt"
	"io"
	"maps"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// ReadPipedInput reads borrowed request input and merges it with explicit flags.
// The caller decides whether stdin is eligible for whole-request input. The result
// reports whether nonempty input consumed stdin, including input that failed to parse.
// Bodyless requests still apply path, query, and header values without merging a body.
func ReadPipedInput(cmd *cli.Command, stdin io.Reader, contents *requestflag.RequestContents, stdinSecurity *StdinSecurity, acceptBody bool) (bool, error) {
	pipeData, err := io.ReadAll(stdin)
	if err != nil {
		return false, err
	}

	if len(pipeData) > 0 {
		var bodyData any
		if err := requestflag.UnmarshalYAMLOrJSON(pipeData, &bodyData); err != nil {
			return true, fmt.Errorf("Failed to parse piped data as YAML/JSON:\n%w", err)
		}
		if bodyMap, ok := bodyData.(map[string]any); ok {
			ApplyDataAliases(cmd, bodyMap)
			stdinSecurity.RecordBodyFlags(cmd, bodyMap)
			// Apply any matching keys from the piped data to path, query, and header flags
			// that have not already been set via the command line.
			if err := requestflag.ApplyStdinDataToFlagsWithProvenance(cmd, bodyMap, stdinSecurity.RecordFlag); err != nil {
				return true, err
			}
			// Re-extract request contents now that flags may have been updated.
			*contents = requestflag.ExtractRequestContents(cmd)
			stdinSecurity.ProtectFlagValues(contents)
			if stdinSecurity.Enabled() {
				bodyMap = ProtectStdinValue(bodyMap).(map[string]any)
			}
			// Remove keys that were consumed as query, header, or path params so they
			// don't also leak into the request body via the maps.Copy merge below.
			// We delete both the canonical key and any aliases since the user may have
			// piped data using an alias name rather than the canonical API name.
			for _, flag := range cmd.Flags {
				inReq, ok := flag.(requestflag.InRequest)
				if !ok || !flag.IsSet() {
					continue
				}
				if inReq.GetQueryPath() != "" || inReq.GetHeaderPath() != "" || inReq.GetPathParam() != "" {
					delete(bodyMap, inReq.GetQueryPath())
					delete(bodyMap, inReq.GetHeaderPath())
					delete(bodyMap, inReq.GetPathParam())
					for _, alias := range inReq.GetDataAliases() {
						delete(bodyMap, alias)
					}
				}
			}
			if acceptBody {
				if flagMap, ok := contents.Body.(map[string]any); ok {
					maps.Copy(bodyMap, flagMap)
					contents.Body = bodyMap
				} else {
					bodyData = contents.Body
				}
			}
		} else if acceptBody {
			if stdinSecurity.Enabled() {
				bodyData = ProtectStdinValue(bodyData)
			}
			if flagMap, ok := contents.Body.(map[string]any); ok && len(flagMap) > 0 {
				return true, fmt.Errorf("Cannot merge flags with a body that is not a map: %v", bodyData)
			} else {
				contents.Body = bodyData
			}
		}
	}
	return len(pipeData) > 0, nil
}
