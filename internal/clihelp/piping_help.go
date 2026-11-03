package clihelp

import (
	"context"

	"github.com/urfave/cli/v3"
)

const pipingHelp = `{{$run := index .Root.Metadata "help-invocation"}}Shell input and output

Examples use Bash or Zsh.
{{if eq $run "go run ./cmd/openai"}}Run these examples from the CLI repository root.
{{end}}
TOKENIZER
   Pipe UTF-8 text into view to open the editor in your terminal.
       {{suggestFor . (printf "printf hello | %s tokenizer view" $run) "bash" "zsh"}}
   Use count -n for the token count or count --transform input_bytes for bytes.
       {{suggestFor . (printf "printf hello | %s tokenizer count -n" $run) "bash" "zsh"}}
   Use inspect --format json for token IDs and byte boundaries.
       {{suggestFor . (printf "printf hello | %s tokenizer inspect --format json" $run) "bash" "zsh"}}

API REQUESTS
   Use @- for one text field, or --file - for an upload that accepts stdin.
   Replace MODEL with your model ID.
       {{suggestFor . (printf "%s responses create --model MODEL --input @- < prompt.txt" $run) "bash" "zsh"}}
       {{suggestFor . (printf "%s files create --purpose user_data --file - < notes.txt" $run) "bash" "zsh"}}
   For responses create and chat completions create, --request-file accepts a JSON or YAML file.
   Use --request-file - to wait for a piped request. Explicit flags override matching fields.
       {{suggestFor . (printf "cat request.json | %s responses create --request-file -" $run) "bash" "zsh"}}
   For other commands accepting whole requests, finish writing the file before redirecting it.
       {{suggestFor . (printf "%s embeddings create < request.json" $run) "bash" "zsh"}}
   Set OPENAI_UNTRUSTED_STDIN=true when the request comes from an untrusted source.

SCRIPT OUTPUT
   Use --format json for structured results. For API lists, use --format jsonl for one item per line.
   Results go to stdout. Errors go to stderr.
       {{suggest . (printf "%s models list --format jsonl" $run)}}
`

func pipingGuide() *cli.Command {
	return &cli.Command{
		Name: "piping", Usage: "Pipe input and choose output for scripts", HideHelpCommand: true,
		Metadata:           map[string]any{"help-guide": true},
		CustomHelpTemplate: pipingHelp,
		Action: func(_ context.Context, command *cli.Command) error {
			if command.Args().Present() {
				return &UnknownTopicError{Parent: command, Topic: command.Args().First()}
			}
			cli.HelpPrinter(command.Root().Writer, pipingHelp, command)
			return nil
		},
	}
}
