package custom

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/urfave/cli/v3"
)

const welcomeHelp = `{{$run := index .Root.Metadata "help-invocation"}}OpenAI CLI
Create images, ask models, and manage files from your terminal.

FIRST TIME? SET UP YOUR KEY
  {{$run}} help setup
  Learn how to enter your API key. Already set it? Skip this step.

MAKE AN IMAGE
  {{$run}} images generate --prompt "A tiny orange robot"
  Replace the words in quotes with a description of your image.
  Saves a PNG to ~/Downloads/gpt-images/ and prints where to find it.
  Previews appear in your terminal when enabled and supported.

WANT TO CHANGE SOMETHING?
  {{$run}} images generate --help
  Quick start and defaults. All settings: {{$run}} images options

EXPLORE MORE
  {{$run}} images models                Find image models and check visibility
  {{$run}} responses create --help      Learn how to ask a model
  {{$run}} help --all                   Browse every command
  {{$run}} help                         Show this guide again

Readable by default. Scripts: --format json. Any command: --help (no API call).
`

const setupHelp = `{{$run := index .Root.Metadata "help-invocation"}}Set up your API key

1. CREATE A KEY
   https://platform.openai.com/settings/organization/api-keys

2. ENTER IT IN YOUR SHELL
   Choose the instructions for the shell you use.

   Bash or zsh (macOS / Linux)
     Run this line, paste your key (hidden), then press Enter:
       read -rs OPENAI_API_KEY
     Then run this line so the CLI can read the key:
       export OPENAI_API_KEY

   PowerShell (Windows)
     Run this line, paste your key (hidden), then press Enter:
       $openaiKey = Read-Host "API key" -AsSecureString
     Then run these lines:
       $env:OPENAI_API_KEY = [System.Net.NetworkCredential]::new("", $openaiKey).Password
       Remove-Variable openaiKey

   These steps set the key for this shell session. A new window needs it again.

3. MAKE YOUR FIRST IMAGE
   {{$run}} images generate --prompt "A tiny orange robot"
   Images save automatically to ~/Downloads/gpt-images/.

This is a guide only. No key has been entered or checked by showing this page.
`

// ConfigureHelp keeps onboarding in CLI help, never in a shell startup file or
// an API command's output. Normalize help before Run so the framework still
// owns parsing, parent links and rendering, while help skips request setup.
func ConfigureHelp(root *cli.Command, args []string) ([]string, bool, error) {
	root.CustomRootCommandHelpTemplate = welcomeHelp
	if root.Metadata == nil {
		root.Metadata = map[string]any{}
	}
	root.Metadata["help-invocation"] = helpInvocation(root.Name, args)
	configureImageHelp(root, root.Metadata["help-invocation"].(string))
	if root.Command("help") == nil {
		root.Commands = append(root.Commands, &cli.Command{
			Name: "help", Usage: "Get help: help [--all] [command...]", HideHelpCommand: true,
			CustomHelpTemplate: welcomeHelp,
			Flags:              []cli.Flag{&cli.BoolFlag{Name: "all", Usage: "Show every command option", HideDefault: true}},
			Action:             showHelpTopics,
			Commands: []*cli.Command{{
				Name: "setup", Usage: "Learn how to enter your API key", HideHelpCommand: true,
				CustomHelpTemplate: setupHelp,
				Action: func(_ context.Context, command *cli.Command) error {
					if command.Args().Present() {
						return cli.Exit("Setup help takes no additional arguments.", 3)
					}
					cli.HelpPrinter(command.Root().Writer, setupHelp, command)
					return nil
				},
			}},
		})
		requestSetup := root.Before
		root.Before = func(ctx context.Context, command *cli.Command) (context.Context, error) {
			// Inspect parsed command selection, never arbitrary token values.
			if command.Args().First() == "help" || requestSetup == nil {
				return ctx, nil
			}
			return requestSetup(ctx, command)
		}
	}
	if len(args) <= 1 {
		return []string{root.Name, "--help"}, true, nil
	}
	if args[1] == "__complete" {
		return args, false, nil
	}
	current := root
	for i := 1; i < len(args); i++ {
		arg := args[i]
		// On a leaf, "help" can be a filename or another positional operand.
		// Only resource groups accept the help-command shorthand.
		if arg == "help" && len(current.Commands) > 0 {
			if current == root {
				// Let the framework dispatch root help and its setup guide, including
				// options before or after the topic. Normalize only nested shorthand.
				return args, true, nil
			}
			path, all := []string{}, false
			for _, topic := range args[i+1:] {
				switch topic {
				case "--all":
					all = true
				case "--help", "-h":
				default:
					next := current.Command(topic)
					if next == nil || next.Hidden || topic == "help" {
						return nil, true, cli.Exit(fmt.Sprintf("Unknown help topic %q. Run %s help --all to see commands.", topic, root.Metadata["help-invocation"]), 3)
					}
					current = next
					path = append(path, topic)
				}
			}
			if all {
				useFullHelp(root, current)
			}
			out := append(append([]string(nil), args[:i]...), path...)
			return append(out, "--help"), true, nil
		}
		// Flags and values remain entirely under the framework parser. In
		// particular, --prompt "help" must never turn a request into help.
		if arg == "--help" || arg == "-h" {
			return args, true, nil
		}
		if local, _ := current.Metadata["local-help"].(bool); local && arg == "--all" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return args, false, nil
		}
		if arg == "" {
			continue
		}
		next := current.Command(arg)
		if next == nil {
			return args, false, nil
		}
		current = next
	}
	if local, _ := current.Metadata["local-help"].(bool); local {
		// These commands only display guidance. Let the framework's help path
		// skip request configuration, even when that configuration is broken.
		return append(append([]string(nil), args...), "--help"), true, nil
	}
	return args, false, nil
}

func showHelpTopics(ctx context.Context, command *cli.Command) error {
	root := command.Root()
	parent, target := root, root
	for _, topic := range command.Args().Slice() {
		next := target.Command(topic)
		if next == nil || next.Hidden || topic == "help" {
			return cli.Exit(fmt.Sprintf("Unknown help topic %q. Run %s help --all to see commands.", topic, root.Metadata["help-invocation"]), 3)
		}
		parent, target = target, next
	}
	if command.Bool("all") {
		useFullHelp(root, target)
	}
	if target == root {
		return cli.ShowRootCommandHelp(root)
	}
	return cli.ShowCommandHelp(ctx, parent, target.Name)
}

func useFullHelp(root, target *cli.Command) {
	if full, _ := target.Metadata["local-help-full"].(string); full != "" {
		target.CustomHelpTemplate = full
	} else if target == root {
		root.CustomRootCommandHelpTemplate = cli.RootCommandHelpTemplate
	} else if isImageGenerateCommand(target) {
		target.CustomHelpTemplate = imageGenerateFullHelp
	} else if len(target.Commands) > 0 {
		target.CustomHelpTemplate = cli.SubcommandHelpTemplate
	} else {
		target.CustomHelpTemplate = cli.CommandHelpTemplate
	}
}

func helpInvocation(fallback string, args []string) string {
	if len(args) == 0 || args[0] == "" || filepath.Base(args[0]) != fallback {
		return fallback
	}
	name := args[0]
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return fallback
	}
	if strings.ContainsAny(name, " \t\r\n'\"$`;&|()<>") {
		return "'" + strings.ReplaceAll(name, "'", "'\\''") + "'"
	}
	return name
}
