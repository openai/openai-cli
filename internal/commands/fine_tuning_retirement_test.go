package commands_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestFineTuningRetirementPreservesVisionHelp(t *testing.T) {
	for _, route := range [][2]string{{"files", "create"}, {"files", "upload"}, {"uploads", "create"}} {
		for _, flat := range []bool{false, true} {
			name := strings.Join(route[:], "/") + map[bool]string{false: "/nested", true: "/flat"}[flat]
			t.Run(name, func(t *testing.T) {
				purpose := &requestflag.Flag[string]{
					Name:     "purpose",
					Usage:    "One of: assistants, batch, fine-tune, vision, user_data, or evals. Vision images support fine-tuning.",
					BodyPath: "purpose",
					Required: true,
				}
				const mimeUsage = "Use MIME types supported by assistants and vision."
				mime := &requestflag.Flag[string]{Name: "mime-type", Usage: mimeUsage, BodyPath: "mime_type"}
				command := &cli.Command{Name: route[1], Flags: []cli.Flag{purpose}}
				if route[0] == "uploads" {
					command.Flags = append(command.Flags, mime)
				}
				var output bytes.Buffer
				root := &cli.Command{Name: "openai", Writer: &output, ErrWriter: &output}
				args := []string{"openai", route[0], route[1], "--help"}
				if flat {
					command.Name = strings.Join(route[:], ":")
					root.Commands = []*cli.Command{command}
					args = []string{"openai", command.Name, "--help"}
				} else {
					root.Commands = []*cli.Command{{Name: route[0], Commands: []*cli.Command{command}}}
				}
				commands.RemoveFineTuningCommands(root)
				require.NoError(t, root.Run(t.Context(), args))
				for _, allowed := range []string{"assistants", "batch", "vision", "user_data", "evals"} {
					require.Contains(t, output.String(), allowed)
				}
				require.NotContains(t, output.String(), "fine-tun")
				require.Equal(t, "purpose", purpose.BodyPath)
				require.True(t, purpose.Required)
				if route[0] == "uploads" {
					require.Equal(t, mimeUsage, mime.Usage)
					require.Contains(t, strings.Join(strings.Fields(output.String()), " "), mimeUsage)
				}
			})
		}
	}
}
