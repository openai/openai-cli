package output

import (
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestReadableParameterFlagUsesExactCommandPaths(t *testing.T) {
	messages := &requestflag.Flag[[]map[string]any]{Name: "message", BodyPath: "messages"}
	command := &cli.Command{Flags: []cli.Flag{
		messages,
		&requestflag.InnerFlag[string]{Name: "message.content", OuterFlag: messages, InnerField: "content"},
		&requestflag.Flag[string]{Name: "model", PathParam: "model"},
		&requestflag.Flag[int64]{Name: "page-size", QueryPath: "limit"},
		&cli.StringFlag{Name: "format"},
	}}
	for _, test := range []struct{ param, flag, path string }{
		{"messages[0].content", "--message.content", "messages.content"},
		{"messages.1.content", "--message.content", "messages.content"},
		{"messages.1.0.content", "--message.content", "messages.content"},
		{"messages[2].future_field", "--message", "messages"},
		{"messages[2]", "--message", "messages"},
		{"model", "--model", "model"},
		{"limit", "--page-size", "limit"},
		{"format", "", ""},
		{"messages_evil", "", ""},
		{"messages.0.content\x1b[2J", "", ""},
		{"messages['synthetic-secret']", "", ""},
		{"https://synthetic.invalid/?token=secret", "", ""},
	} {
		t.Run(test.param, func(t *testing.T) {
			name, path := ParameterFlag(command, test.param)
			require.Equal(t, test.flag, name)
			require.Equal(t, test.path, path)
		})
	}
}
