package custom

import (
	"strconv"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Align generated dispatch with the final JSON without rewriting request bytes.
func resolveStreamingInput(command *cli.Command, body []byte) error {
	value := gjson.GetBytes(body, "stream")
	if value.Type != gjson.True && value.Type != gjson.False {
		return nil
	}
	return resolveStreamingValue(command, value.Type == gjson.True)
}

func resolveStreamingValue(command *cli.Command, streaming bool) error {
	for _, flag := range command.Flags {
		var name, path string
		switch flag := flag.(type) {
		case *requestflag.Flag[bool]:
			name, path = flag.Name, flag.BodyPath
		case *requestflag.Flag[*bool]:
			name, path = flag.Name, flag.BodyPath
		default:
			continue
		}
		if name != "stream" || path != "stream" || flag.IsSet() {
			continue
		}
		return flag.Set(name, strconv.FormatBool(streaming))
	}
	return nil
}
