package custom

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/urfave/cli/v3"
)

type workflowExample struct {
	Topic  string `json:"topic"`
	Shell  string `json:"shell"`
	Script string `json:"script"`
}

func registerExamplesCommands(root *cli.Command) {
	help := `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}EXAMPLE
  {{$bin}} examples files

` + cli.SubcommandHelpTemplate + localUtilityGlobalHelp
	command := &cli.Command{
		Name: "examples", Usage: "Print runnable Files, audio, and model workflows",
		Description: "Prints POSIX shell recipes without credentials or network access. Nothing runs or saves automatically. " +
			"Use openai on PATH when copying recipes. Comments identify required local files. " +
			"Copied API commands require credentials and can incur charges. " +
			"Supports --format auto, text, and json. Does not support --transform or --raw-output.",
		HideHelpCommand:    true,
		CustomHelpTemplate: help,
		Metadata: map[string]any{
			localUtilityMetadata: true, "local-help-full": help, "help-command-section": "Local tools",
			"completion-root-flag-values": map[string][]string{"format": {"auto", "text", "json"}},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			format, err := examplesOutputFormat(command)
			if err != nil {
				return err
			}
			if format == "json" {
				return examplesRecoveryError(command, "Examples JSON requires a topic.", "examples files --format json")
			}
			invocation := errorHelpInvocation(command.Root())
			index := strings.Join([]string{
				"# Choose a recipe to print. These commands stay offline.",
				invocation + " examples files   # upload, metadata, download",
				invocation + " examples audio   # transcription, translation, subtitles",
				invocation + " examples models  # model IDs for scripts",
				"# Show complete help and output options.",
				invocation + " examples --help",
			}, "\n") + "\n"
			_, err = io.WriteString(outputWriter{ctx: ctx, out: command.Root().Writer}, index)
			return examplesOutputFailure(err)
		},
	}
	for _, recipe := range []struct{ topic, usage, script string }{
		{"files", "Upload a local file, inspect metadata, and download its contents", `# POSIX shell. Requires openai on PATH and API credentials.
# Required local input: replace "./upload sample.txt" with your existing file.
# The download replaces "./downloaded copy.txt" after a successful transfer.
file_id=$(openai --format json --transform id --raw-output files upload "./upload sample.txt" --purpose user_data) &&
openai files get "$file_id" &&
openai files download "$file_id" --output "./downloaded copy.txt"
`},
		{"audio", "Transcribe audio, translate into English, and produce subtitles", `# POSIX shell. Requires openai on PATH and API credentials.
# Required local input: replace "./speech sample.wav" with your existing audio.
# These three commands make separate API requests and print their results.
openai --format raw audio transcribe --file "./speech sample.wav" --model whisper-1 --response-format text
openai --format raw audio translate --file "./speech sample.wav" --model whisper-1 --response-format text
openai --format raw audio transcribe --file "./speech sample.wav" --model whisper-1 --response-format srt
`},
		{"models", "Extract available model IDs for scripts", `# POSIX shell. Requires openai on PATH and API credentials.
# Print every available model ID, one per line, without JSON quotes.
openai --format json --transform id --raw-output models list --max-items -1
`},
	} {
		help := `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}EXAMPLE
  {{$bin}} examples {{.Name}}

` + cli.CommandHelpTemplate + localUtilityGlobalHelp
		example := workflowExample{Topic: recipe.topic, Shell: "sh", Script: recipe.script}
		command.Commands = append(command.Commands, &cli.Command{
			Name: recipe.topic, Usage: recipe.usage, Description: command.Description,
			HideHelpCommand: true, CustomHelpTemplate: help,
			Metadata: map[string]any{
				"local-help-full":             help,
				"completion-root-flag-values": map[string][]string{"format": {"auto", "text", "json"}},
			},
			Action: func(ctx context.Context, command *cli.Command) error {
				format, err := examplesOutputFormat(command)
				if err != nil {
					return err
				}
				out := outputWriter{ctx: ctx, out: command.Root().Writer}
				if format == "json" {
					err = json.NewEncoder(out).Encode(example)
				} else {
					_, err = io.WriteString(out, example.Script)
				}
				return examplesOutputFailure(err)
			},
		})
	}
	root.Commands = append(root.Commands, command)
}

func examplesOutputFailure(err error) error {
	if err == nil {
		return nil
	}
	return &localUtilityError{message: "Could not write examples. Output may be incomplete. Check the output file or pipe before rerunning.", cause: err}
}

func examplesOutputFormat(command *cli.Command) (string, error) {
	if command.Args().Present() {
		return "", examplesRecoveryError(command, "Choose one examples topic with no extra arguments.", "examples")
	}
	root := command.Root()
	path := "examples"
	switch command.Name {
	case "files", "audio", "models":
		path += " " + command.Name
	}
	if root.IsSet("transform") || root.IsSet("raw-output") {
		return "", examplesRecoveryError(command, "Examples cannot apply --transform or --raw-output.", path+" --format text")
	}
	format := strings.ToLower(root.String("format"))
	switch format {
	case "auto", "text", "json":
		return format, nil
	default:
		return "", examplesRecoveryError(command, "Examples support --format auto, text, or json.", path+" --format text")
	}
}

// Recovery only prints local guidance; it never replays arguments or API work.
func examplesRecoveryError(command *cli.Command, message, path string) error {
	return &localUtilityError{message: message + "\nTry: " + errorHelpInvocation(command.Root()) + " " + path}
}
